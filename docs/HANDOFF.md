# skillet 交接文档

更新于 2026-10-10，对应提交 `0046a90`（M1 + M2）加上**未提交**的 M3 工作区改动。新会话先读本文，再按需读 `docs/design.md`（设计）和 `docs/research.md`（调研）。

## 1. 项目是什么

一个轻量 Go CLI，帮 Claude Code / Codex / OpenCode / pi 管理 skill：

1. 用户选一个 store 目录，skill 所在的 git 仓库完整 clone 到 `<store>/repos/<host>/<owner>/<repo>`。
2. 按「项目目录 × agent」把 skill 软链接进各 agent 的 skill 目录（也支持用户级 `-g`）。Windows 依次降级为 junction、带标记的复制。
3. 检查上游更新，用户确认后 fast-forward，所有相关目录、所有 agent 随之更新。

为什么自己写而不用现成工具：调研过 xingkongliang/skills-manager（Tauri 桌面 + Rust CLI，功能全，但 CLI 不支持项目级部署，定时检查依赖桌面常驻）和 vercel-labs/skills（`npx skills`，没有中央 git 仓库、不跨项目追踪、只能手动 check）。实现上参考了 skills-manager 的链接降级和"只删自己创建的东西"的做法。

## 2. 用户已确定的决定

- 语言 Go，工具名 **skillet**（`skm` 已被别人占用）。
- 三平台（Linux / macOS / Windows）第一版就支持。
- 状态用 JSON 文件，不用 SQLite；依赖系统 `git`，不用 go-git。
- 用户偏好：中文交流；先汇报再动手做大改动；提交前确认。

## 3. 环境

- Go 1.27.2 装在 `~/go-sdk/go`（不在 PATH）。每条命令前加 `export PATH=$HOME/go-sdk/go/bin:$PATH`。
- 仓库：`/home/ubuntu/github-project-research/skill-manager`，分支 `master`，无远端，未推送。
- 这台机器 git 没配置身份。上次提交用的是 `git -c user.name=zhangyhhust -c user.email=zhangyhhust@gmail.com commit …`，没有写进 config。

常用命令：

```bash
go vet ./... && GOOS=windows go vet ./... && GOOS=darwin go vet ./...   # 三平台编译检查
go test ./...
go build -o bin/skillet ./cmd/skillet
```

手动试跑时**不要碰真实 HOME**，用隔离环境：

```bash
export HOME=<scratch>/home SKILLET_CONFIG_DIR=<scratch>/home/cfg
```

`SKILLET_CONFIG_DIR` 覆盖配置目录；agent 目录跟随 `HOME`（以及 `CLAUDE_CONFIG_DIR`、`XDG_CONFIG_HOME`、`CODEX_HOME`、`PI_CODING_AGENT_DIR`）。

## 4. 代码结构

| 包 | 内容 |
|---|---|
| `cmd/skillet` | 入口 |
| `internal/cli` | cobra 命令：`commands.go`（init/repo/list/enable/disable/status/agents）、`update.go`（check/update/doctor）、`status.go`（矩阵视图） |
| `internal/deploy` | 核心逻辑。`Manager` 持有 config、agent 注册表、加锁的状态文件。`enable.go` 规划与执行链接，`update.go` 检查与更新，`doctor.go` 健康检查与同名冲突，`status.go` 扫描各 agent 实际可见的 skill |
| `internal/agents` | 内置 agent 表、`Plan`（选最少的链接目录）、`Reads`（判断谁会看到） |
| `internal/link` | symlink / junction / copy 的创建、识别、安全删除；`link_windows.go` 用 `nyaosorg/go-windows-junction` |
| `internal/gitx` | 调系统 git：clone、fetch、diff、log、ff-merge，以及仓库地址解析 |
| `internal/skill` | 扫描 `SKILL.md`、解析 frontmatter、名字校验、内容哈希 |
| `internal/state` | `<store>/state.json` 读写，`gofrs/flock` 文件锁 |
| `internal/config` | `config.toml`（store、link_mode、check_interval、自定义 agent） |

## 5. 关键行为（改代码前要知道）

- **Agent 表**（`agents.Builtin`，顺序有意义，见注释）：

  | agent | 项目目录 | 全局目录 | 还会读 |
  |---|---|---|---|
  | claude | `.claude/skills` | `~/.claude/skills` | — |
  | codex | `.agents/skills` | `~/.agents/skills` | `~/.codex/skills`（旧路径） |
  | opencode | `.opencode/skills` | `~/.config/opencode/skills` | `.claude/skills`、`.agents/skills`（项目和全局） |
  | pi | `.pi/skills` | `~/.pi/agent/skills` | `.agents/skills`（项目和全局） |

  pi 的 `.pi/skills` 只有第三方资料佐证，官方文档只写了 `.agents/skills`，**未核实**。

- **最少链接**：`-a all` 只建 `.claude/skills/<name>`（服务 claude、opencode）和 `.agents/skills/<name>`（服务 codex、pi）。一条部署记录对应一个链接，`Agents` 字段记录它服务哪些 agent。
- **停用会重新规划**：只停用 claude 时，opencode 会被挪到 `.opencode/skills`，保证 claude 真的看不到。操作后打印"实际可见"，并提示哪些 agent 因为读别人的目录而顺带可见。
- **安全**：所有改动先完整规划，任何目标被拒绝则整个操作不做任何改动。只替换指向 store 内的链接或带 `.skillet-source.json` 的副本；删除链接只用 `os.Remove`，绝不对链接用 `RemoveAll`；手动改过的副本拒绝删除和覆盖。
- **更新**：`check` 用 `git diff --name-only HEAD origin/<branch>` 按 skill 子目录判断变更，标出改了非 Markdown 文件的 skill。`update` 要求工作区干净、可 fast-forward；合并后重新扫描，换了目录的 skill 按名字迁移部署，删除的 skill 删链接，改名的 skill 改链接名，复制的副本重新复制。
- **skill id** = `<repo id>:<仓库内相对路径>`，例如 `github.com/anthropics/skills:skills/pdf`。命令行可写 `pdf`、`owner/repo/pdf` 或完整 id。
- **Windows**：Go 1.23 起 junction 的 `Lstat` 是 `ModeIrregular` 而不是 `ModeSymlink`，`link_windows.go` 的 `readLink` 已处理。

## 6. 已知问题与未验证项

1. **Windows / macOS 从没真正运行过**，只做了 `GOOS=… go vet`。最需要验证的是 junction 降级、junction 删除，以及 store 里 git 只读 pack 文件在 Windows 上能否被 `os.RemoveAll` 删掉（`repo remove` 会用到）。CI 配置在 `.github/workflows/ci.yml`，推到 GitHub 就能跑。
2. **`update` 等待 y/N 期间一直持有状态锁**，其他前台 skillet 命令会被阻塞（后台检查用 `TryLock`，会跳过）。
3. 同名冲突只报告不处理（status 警告、doctor 报 `clash`）。
4. 输出全部英文，用户界面没有中文化（用户没提要求）。
5. 没有交互式选择或 TUI。用户看过方案，选择先做矩阵视图的 `status`，交互式选择（`charmbracelet/huh`）和矩阵 TUI（`bubbletea`）暂缓。
6. 安全修复（已落地）：`skill.Hash` 与 `copyDir` 一致地跟随文件软链接；`checkFree` 拒绝采用被手改过的残留副本；`checkRemovable` 只允许删除指向 store 内的链接，哈希出错也视为错误；`ParseSource` 拒绝含空段、`.`、`..` 的 id（`AddRepo` 另外检查目录在 store 内）；`pending` 用 `OpenTry`，锁被占用时静默退出。

## 7. M3（提醒）：已实现，待提交

新增：`internal/notify`（notify-send / osascript / PowerShell toast）、`internal/schedule`（systemd user timer 或 crontab / launchd / schtasks）、`internal/cli/remind.go`（被动提醒与后台检查）、`internal/cli/schedule.go`（`schedule`、`pending`、`hook claude`）。`state` 增加 `TryOpen`、`Peek`、`Repo.Notified`。行为细节见 `docs/design.md` 6.2。

已验证（Linux，隔离 HOME）：`check --quiet` 无输出；`--notify` 无 notify-send 时报错但不失败；被动提醒在终端打印一行并在过期时起后台 `check --quiet`；锁被占用时后台检查立即跳过；`schedule install --dry-run` 输出的 unit 内容；`pending --hook-json` 输出符合 Claude Code 文档的 SessionStart JSON。
**未验证**：真正的 `schedule install`（systemd/cron/launchd/schtasks）、macOS/Windows 通知、hook 在真实 Claude Code 会话里的显示。

## 7.1 M4：pin 已实现（未提交，分支 `feat/m4`）

`enable --pin <rev>` / `--unpin`（不带 `--pin` 保留现有 pin；上游已删除但仍有固定部署的 skill 可以 disable）：见 `docs/design.md` 6.4。代码：`internal/deploy/pins.go`（worktree 创建/清理、`DeploymentSource`）、`enable.go`（计划里的 `replace` 动作）、`doctor.go`（`orphan-pin`、`pin-missing`）、`gitx`（`ResolveCommit`、`Worktree*`）。测试在 `internal/deploy/pins_test.go`。隔离 HOME 手动跑过 enable/--dry-run/取消固定/doctor。未验证：Windows 上 worktree 与 junction 的组合。

### 下一步：M4 剩余

项目内 `skillet.toml` 供团队还原、交互式选择。第 6 节第 2 条（update 等待 y/N 时持锁）对后台检查已不构成问题（TryLock 跳过），但会阻塞同时运行的前台命令，仍可改进。

## 8. 工作方式备忘

- 每次改动后：`gofmt -l .`、三平台 `go vet`、`go test ./...`，再在隔离 HOME 里用 CLI 实际跑一遍。
- 测试辅助函数在 `internal/deploy/deploy_test.go`（`setup`、`writeSkill`、`git`）和 `update_test.go`（`commit`、`check`、`update`），用本地 git 仓库模拟上游，不访问网络。
- 设计有变化时同步更新 `docs/design.md` 和 README 的"进度"一节。
