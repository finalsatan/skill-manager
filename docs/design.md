# skillet 设计方案

一个轻量 CLI：把 skill 所在的 git 仓库集中 clone 到用户指定目录，按「目录 × agent」用软链接启用 skill，并监控上游更新。

- 语言：Go，单个二进制，Linux / macOS / Windows 三平台。
- 外部依赖：系统里的 `git`（复用用户已有的 SSH key / credential helper，不自己处理认证）。
- 参考实现：xingkongliang/skills-manager（下文简称 SM），取其链接降级、安全替换、适配器表的做法，去掉 GUI、SQLite、市场、备份等。

## 1. 与 SM 的主要差异

| | SM | skillet |
|---|---|---|
| 中央存储 | 把 skill 导入自己的库（`~/.skills-manager`），clone 只是缓存 | 保留**完整 git 工作区**，链接直接指向仓库里的 skill 目录 |
| 项目级启用 | 只有桌面端能做 | CLI 原生支持 `--dir` |
| 部署记录 | SQLite | 一个 JSON 状态文件，含跨项目的反向索引 |
| 更新判断 | 比对整个仓库的 HEAD，仓库任何提交都算"有更新" | `git fetch` 后按 skill 子目录 diff，只报真正变了的 skill |
| 更新前审阅 | 无 | 展示 commit 列表 + diff，确认后再 fast-forward |
| 定时检查 | 桌面应用常驻时运行 | 安装系统定时任务（cron / launchd / 任务计划程序）+ 桌面通知 |

## 2. 目录与文件

```
<config>/skillet/config.toml      # os.UserConfigDir()：~/.config、~/Library/Application Support、%AppData%
<store>/                          # 用户选择，默认 ~/.skillet
├── repos/<host>/<owner>/<repo>/  # 完整 git clone
├── pins/<host>/<owner>/<repo>@<short-sha>/   # 固定版本时用 git worktree 检出（已实现，见 6.4）
├── state.json                    # 仓库、skill、部署记录
└── state.lock                    # 文件锁，防止定时任务和手动命令并发写
```

`config.toml`：

```toml
store = "~/.skillet"
check_interval = "6h"          # 被动检查的最小间隔
link_mode = "auto"             # auto | symlink | junction | copy

[[agents.custom]]              # 可选：自定义 / 覆盖 agent
id = "hermes"
global_dir = "~/.hermes/skills"
project_dir = ".hermes/skills"
reads = [".hermes/skills"]
```

## 3. 数据模型（state.json）

```jsonc
{
  "version": 1,
  "repos": {
    "github.com/anthropics/skills": {
      "url": "https://github.com/anthropics/skills.git",
      "branch": "main",
      "path": "repos/github.com/anthropics/skills",
      "head": "a1b2c3…",            // 本地 HEAD
      "upstream": "d4e5f6…",        // 最近一次 fetch 到的远端
      "last_checked": "2026-10-10T08:00:00Z"
    }
  },
  "skills": {
    // id = <repo>:<skill 目录在仓库内的相对路径>
    "github.com/anthropics/skills:skills/pdf": {
      "name": "pdf",                // frontmatter.name，即链接名
      "description": "…",
      "pending": true               // 上游对该子目录有改动
    }
  },
  "deployments": [
    {
      "skill": "github.com/anthropics/skills:skills/pdf",
      "agents": ["claude", "opencode"], // 一条链接可同时服务多个 agent
      "scope": "project",           // project | global
      "dir": "/home/u/work/projA",  // scope=global 时为空
      "target": "/home/u/work/projA/.claude/skills/pdf",
      "mode": "symlink",            // symlink | junction | copy
      "pin": "",                    // 固定版本时为 sha
      "alias": "",                  // enable --as 指定的链接名；空表示用 skill 自己的名字
      "source_hash": "",            // copy 模式下记录内容哈希
      "created_at": "…"
    }
  ]
}
```

功能 3"更新所有相关目录"就是遍历 `deployments` 里属于该 skill 的条目。

## 4. Agent 适配器

内置表（数据驱动，可被 `config.toml` 覆盖或扩展）：

| id | 项目级目标 | 全局目标 | 该 agent 还会读取（用于计算实际生效范围） |
|---|---|---|---|
| `claude` | `.claude/skills` | `$CLAUDE_CONFIG_DIR` 或 `~/.claude` 下的 `skills` | — |
| `codex` | `.agents/skills` | `~/.agents/skills` | `~/.codex/skills`（旧路径，只用于查重） |
| `opencode` | `.opencode/skills` | `~/.config/opencode/skills` | `.claude/skills`、`.agents/skills`（项目和全局） |
| `pi` | `.pi/skills` | `$PI_CODING_AGENT_DIR` 或 `~/.pi/agent` 下的 `skills` | `.agents/skills`（项目和全局） |

注：SM 把 Codex 全局目录定为 `~/.codex/skills`，我们按 OpenAI 当前文档用 `~/.agents/skills`。

### 4.1 选择目标目录（避免重复加载）

用户 `enable pdf --agent claude,opencode,codex,pi` 时：

1. 按顺序处理每个 agent。如果某个**已选中的目标目录**在它的读取列表里，就跳过（标记"已由 X 覆盖"）。
2. 否则选它自己的目标目录。
3. 上例结果：`.claude/skills`（claude，同时覆盖 opencode）、`.agents/skills`（codex，同时覆盖 pi）——两条链接，四个 agent 都能看到，没有重复。

### 4.2 告知实际生效范围

链接建好后，反查所有 agent 的读取列表，列出"实际能看到它的 agent"。例如只给 codex 启用，会提示 opencode 和 pi 也会看到（因为它们读 `.agents/skills`）。这是 agent 的行为，工具无法阻止，只能如实告知。

## 5. 链接与安全替换

### 5.1 创建（参考 SM `sync_engine.rs`）

```
mode=auto:
  Unix:    os.Symlink(绝对路径)
  Windows: os.Symlink → 失败则 junction → 失败则 copy
```

- junction：Go 标准库没有，调用 `DeviceIoControl(FSCTL_SET_REPARSE_POINT)`，用小型库（如 `github.com/nyaosorg/go-windows-junction`）或自己写约 80 行；不再 fork `cmd /c mklink /J`。
- copy：复制后在目标里写 `.skillet-source.json`（skill id、源路径、内容哈希），用来识别"这是我们管的副本"，并在更新时判断用户是否手改过。
- 链接目标一律用绝对路径：junction 只支持绝对路径，统一处理更简单。
- 结果里记录实际用的 mode。

### 5.2 只动自己管的东西（参考 SM `authorize_replacement` / `classify_target`）

目标路径已存在时先分类：

| 状态 | 处理 |
|---|---|
| 不存在 | 创建 |
| 链接，且指向 `<store>` 内 | 视为自己管理，可替换 |
| 带 `.skillet-source.json` 的目录 | 自己的副本；哈希与记录一致才替换，否则提示用户有本地修改 |
| 其他（普通目录、指向别处的链接） | **拒绝**，提示用户手动处理 |

删除链接时先 `Lstat` 确认是链接或 junction，再 `os.Remove`；**绝不对链接调用 `RemoveAll`**。

## 6. 更新检查与应用

### 6.1 检查（`skillet check`）

对每个仓库执行：

1. `git fetch --quiet origin <branch>`
2. 对仓库里每个被部署的 skill：`git diff --quiet HEAD origin/<branch> -- <skill 路径>`，非零就标记 `pending`。
3. 写回 `upstream`、`last_checked`。

并发：多个仓库并行 fetch（上限 4）。网络失败只记录，不中断。

### 6.2 提醒的三种途径

1. **被动**：任何 skillet 命令执行完后，如果有 `pending`，打印一行提示；若距 `last_checked` 超过 `check_interval`，在后台子进程里跑一次 check（不阻塞当前命令）。
2. **定时任务**：`skillet schedule install --every 6h` 注册系统任务，运行 `skillet check --notify`：
   - Linux：用户级 crontab 或 systemd user timer；通知用 `notify-send`。
   - macOS：`~/Library/LaunchAgents/*.plist`；通知用 `osascript -e 'display notification …'`。
   - Windows：`schtasks /Create`；通知用 PowerShell toast。
3. **Agent 钩子（可选）**：`skillet hook claude` 输出一段 Claude Code SessionStart hook 配置（`matcher: startup|resume`，命令 `skillet pending --hook-json`）。`pending` 只读上次 check 的结果、不联网；有待更新时输出 JSON（`systemMessage` 给用户看，`hookSpecificOutput.additionalContext` 给 Claude），否则无输出。

实现要点：
- 后台检查（被动提醒触发的 `check --quiet`、定时任务的 `check --notify`）用 `TryLock` 拿状态锁，拿不到就跳过这一轮。
- 被动提醒只在 stderr 是终端时打印；后台检查的尝试时间记在 `<store>/.last-auto-check`，离线时不会每条命令都起子进程。
- `--notify` 对每个仓库只在 upstream 提交变化时通知一次（`Repo.Notified`）。
- `SKILLET_NO_REMIND=1` 关闭被动提醒和后台检查。
- `schedule install` 会把 `SKILLET_CONFIG_DIR` 等环境变量写进任务；`--dry-run` 只打印将写入的文件和命令。

### 6.3 应用（`skillet update [repo…]`）

1. 对每个 pending 的仓库，展示 `git log --oneline HEAD..origin/<branch>` 和按 skill 的 `git diff --stat`（`--diff` 显示完整 diff，重点提示脚本文件变更）。
2. 用户确认（`--yes` 跳过）。
3. `git merge --ff-only origin/<branch>`。工作区有本地修改或已分叉时拒绝，提示用户自行处理。
4. 同步：
   - symlink / junction 部署：自动生效，无需处理。
   - copy 部署：检查 `.skillet-source.json` 的哈希，未被手改就重新复制，否则跳过并报告。
   - pin 部署：不动，提示"N pinned deployment(s) not updated (pinned to <sha>)"；`check` 的 `Deployed` 只数未固定的部署，另有 `Pinned` 计数。上游删除或移动 skill 时也不动固定的部署（源在 worktree 里）。
   - skill 目录在新版本中被删除：链接会失效，列出受影响的部署，询问是否 `disable`。
   - 新增的 skill：只提示，不自动启用。
5. 提示：Claude Code 会热加载；Codex、pi 可能需要重启会话。实现：所有仓库更新完后，若有仓库被更新、且它未固定的部署服务于 claude 以外的 agent，打印一行 `Claude Code reloads skills automatically; restart Codex / OpenCode / pi sessions to pick up changes.`。
6. 锁：`update` 只在 fetch（`Check`，或 `--no-fetch` 时的 `Pending`）并读出 log/diff 时、以及应用单个仓库时持有状态锁；等待用户回答期间不持锁，确认后重新 `Open` 再 `Update`（`Update` 在 head/upstream 与检查时不一致时拒绝）。交互式 `enable`/`disable` 同理：选项来自无锁快照（`state.Peek`），规划（dry-run）与应用各自短暂持锁，“Apply?” 在锁外。
7. `update --dry-run`：照常 fetch（`--no-fetch` 则离线），打印每个仓库的 log 与 diffstat，不询问、不合并。

### 6.4 固定版本（`enable --pin <rev>` / `--unpin`，已实现）

- `<rev>` 是主 clone 里能解析的任意 git 版本（sha、tag、分支），用 `rev-parse --verify <rev>^{commit}` 解析成完整 sha；该 commit 里必须存在 `<skill 路径>/SKILL.md`，否则整个命令拒绝、不改任何东西。
- 每个 (repo, sha) 一个 worktree：`git worktree add --detach <store>/pins/<repo id>@<前 7 位> <sha>`，按需创建，所有固定到同一 sha 的部署共用。链接/副本的源是 worktree 里的 skill 目录，链接名仍是 skill 当前的名字；`Deployment.Pin` 记录完整 sha。
- 同一 scope 内一个 skill 的所有链接共用同一个 pin。再次 `enable`：不带 `--pin` 保留该 skill 在此 scope 的现有 pin（为新 agent 添加的链接同样固定）；带另一个 `--pin` 把所有链接换到新 pin；`--unpin` 指回主 clone（`--pin` 与 `--unpin` 互斥）。换源在计划里是 `replace` 动作，`--dry-run` 可见。`disable` 重新规划时保留原 pin。上游删除了 skill 后，仍有固定部署的 skill 可以按名字或完整 id `disable`（链接名取自部署目标）；`enable` 仍要求 skill 当前存在于仓库中。
- Health、doctor、resync、relink 等都通过 `Manager.DeploymentSource(d)` 取源目录；固定部署的路径来自 skill id，所以上游改名/移动/删除 skill 都不影响。
- 清理：没有任何部署引用的 worktree 在 enable/disable 之后用 `git worktree remove --force` + `prune` 删除；`repo remove` 删除该仓库全部 pin worktree。`doctor` 报告并（`--fix`）清理无人使用的 worktree（`orphan-pin`），并报告缺失的 worktree（`pin-missing`，`--fix` 重建）。
- `status --long` / `--json` 显示 pin（`SOURCE` 列的 `@sha`、`Entry.pin`）。

### 6.5 项目清单 `skillet.toml`（已实现）

团队把 `skillet.toml` 提交到项目根目录（即 `ResolveScope` 得到的项目目录），队友用 `skillet restore` 一条命令还原同样的 skill。

```toml
# Managed by skillet. Restore with `skillet restore`.

[[repo]]
id = "github.com/anthropics/skills"
url = "https://github.com/anthropics/skills.git"
branch = "main"

[[skill]]
id = "github.com/anthropics/skills:skills/pdf"
agents = ["claude", "codex"]
pin = "<完整 sha>"     # 未固定时省略
as = "<别名>"          # enable --as 指定的链接名；没有别名时省略
```

- `agents` 是该 skill 在本项目里被请求的 agent（本 scope 内它所有部署的 `Agents` 之并集），按注册表顺序。链接模式、路径与机器相关，不记录。repo 按 id、skill 按 id 排序，diff 稳定。本地路径仓库（id 为 `local/…`）照写，`save` 会警告队友无法 clone。
- `internal/manifest`：纯数据层（类型、`Load`/`Save`/`Build`）。`Load` 拒绝未知键、重复项、缺失的 repo、空 agents、非法 skill 路径。`Save` 先写临时文件再 rename。
- `skillet save [--dir d] [--exact] [--dry-run]`：按当前部署生成/覆盖清单（`--dry-run` 打印到 stdout 不写文件）。不支持 `-g`。
- `skillet restore [--dir d] [--exact] [--dry-run] [--prune]`（`deploy/restore.go`）：
  1. 先校验清单里的 agent 名；有误则整个命令报错、不改任何东西。
  2. 清单里的 repo 不在 store 时用 `AddRepo(url, branch)` clone，结果 id 必须等于清单 id，否则撤销刚 clone 的仓库并报错；已存在但分支不同只警告。
  3. 每个 skill 一次计划：agent 集合精确等于清单（多出的 agent 被停用），有 `pin` 就固定，没有就 `--unpin`，有 `as` 就用该链接名，没有就去掉本地的别名，所以还原后状态与清单一致，重复执行无变化。pin 在本地 clone 里找不到时 `git fetch origin <branch>` 后重试一次。
  4. 清单里的 skill 在仓库中不存在、repo clone 失败等不会中断：记入 problems，继续其他 skill，最后以非零退出。
  5. 本项目已启用但不在清单里的 skill：默认只列出（`not in skillet.toml (kept; --prune to remove)`），`--prune` 才停用。
  6. `--dry-run` 什么都不改，也不 clone：打印 `Would clone <url>`，并跳过尚未 clone 的仓库里的 skill。
- 同步：项目根已有 `skillet.toml` 时，项目级 `enable`/`disable` 成功后（非 dry-run）按状态重写它并打印 `Updated skillet.toml`；没有该文件就什么都不做，不会凭空生成。`restore` 本身不回写清单。

## 7. 命令

```
skillet init [--store <dir>]                       # 生成配置，创建 store
skillet repo add <url | owner/repo> [--branch b] [--dry-run]   # clone，扫描 SKILL.md；dry-run 只解析并打印 id/URL/目标目录
skillet repo list | remove <repo> [--force] [--dry-run] | show <repo> [--json]   # show：地址/分支/HEAD/上游落后数/pin 检出/各 skill 及部署位置
skillet list [--repo <repo>]                       # 列出可用 skill
skillet enable [<skill…>] [--agent a,b] [--yes] [--dir <path> | --global] [--pin <rev>] [--as <alias>] [--mode …]
skillet disable [<skill…>] [--agent a,b] [--yes] [--dir <path> | --global]
skillet status [--dir <path> | --global | --all]   # 当前目录各 agent 生效了什么
skillet save [--dir <path>] [--dry-run]                      # 把项目当前的 skill 写成 skillet.toml
skillet restore [--dir <path>] [--dry-run] [--prune]   # 按 skillet.toml 还原
skillet check [--notify] [--quiet] [--json]
skillet pending [--json | --hook-json]               # 上次 check 的结果，不联网；--json 无待更新时输出 []
skillet update [repo…] [--yes] [--diff] [--no-fetch] [--dry-run]
skillet doctor [--fix] [--json]                             # 失效链接、重名、目录已删除的项目、未受管目录
skillet agents [--json]                              # 适配器表及检测到的安装情况（INSTALLED 列）
skillet schedule install [--every 6h] | remove
skillet hook claude
```

- 交互模式：`enable` 缺 skill 参数或缺 `--agent`、`disable` 缺 skill 参数，且 stdin 与 stdout 都是终端时，用 huh 提问（可过滤的多选）：
  - skill 列表为 store 里所有 skill（`name  repo — 描述`，描述截断到 60 字符），本 scope 已启用的预选并标出已服务的 agent；`disable` 只列本 scope 已部署的 skill，不预选。
  - agent 列表为注册表里的 agent；若所选 skill 在本 scope 都已启用，预选已服务它们的 agent，否则预选检测到已安装的 agent（`Agent.Detect`）。
  - 然后以 dry-run 打印计划，再问 “Apply?”；确认后真正执行（并同步 `skillet.toml`）。`--yes` 跳过确认，`--dry-run` 只打印计划。Ctrl-C / Esc 打印 `aborted`、退出码 130、不做任何改动。
  - 全局 `--no-input`、环境变量 `SKILLET_NO_INPUT`（非空且非 0/false）或 `CI` 关闭所有提问；非终端同样不提问，行为同以前：缺 skill 报错（提示需要终端），缺 agent 报 `specify at least one agent with --agent`。
  - `update` 的 y/N 在终端里也用同一个确认框；非终端仍读 stdin 一行（管道 `y` 可用）。
- skill 引用：唯一时直接写 `pdf`；重名时写 `anthropics/skills/pdf` 或完整 id。
- `--dir` 默认当前目录，并向上找 git 根作为项目目录（`--exact` 关闭）。
- 所有会改文件系统的命令支持 `--dry-run`（`repo add` 不联网不 clone；`repo remove` 列出将删除的链接/副本/pin 检出及拒绝原因；`update` 照常 fetch 但不应用；`save` 把清单打印到 stdout 而不写文件），查询类命令（`list`、`repo list/show`、`status`、`check`、`pending`、`doctor`、`agents`）支持 `--json`。
- `status --all`（与 `--dir`/`--global` 互斥）依次打印每个有部署记录的 scope：先全局，再各项目目录（按路径排序）；目录已不存在的项目跳过并提示（`doctor --fix` 会忘掉它）。`--json` 输出 `[{scope, dir, missing?, entries}]`。
- `repo show` 不联网：上游落后数来自上次 `check` 记录的 `upstream`（与 `diffRepo` 同一逻辑），skill 列表和警告来自对 clone 的重新扫描。
- `agents` 的 INSTALLED：内置 agent 的二进制（`claude`、`codex`、`opencode`、`pi`）在 PATH 上，或配置目录存在（`$CLAUDE_CONFIG_DIR`/`~/.claude`、`$CODEX_HOME`/`~/.codex`、`$XDG_CONFIG_HOME/opencode`、`~/.pi` 或 `$PI_CODING_AGENT_DIR`）即为 `yes`，都没有为 `no`；自定义 agent 没有可检测的信息，显示 `-`（JSON 为 `installed: null`）。

## 8. Skill 扫描与校验

- 在仓库内递归查找 `SKILL.md`，跳过 `.git`、`node_modules`；找到后不再向下递归。
- 解析 YAML frontmatter：`name` 必填，作为链接名；`description` 必填。
- 名字按最严的 OpenCode 规则校验：`^[a-z0-9]+(-[a-z0-9]+)*$`，1–64 字符。不合规时警告（`list`、`repo add`、`repo show` 都会提示），并允许 `enable --as <别名>`。
- `enable <skill> --as <alias>`：用别名作链接名，用于 frontmatter 名字不合规，或与别的 skill 重名（`status`/`doctor` 的 clash 提示里也会建议）。只允许一个 skill 参数，别名要通过 `skill.ValidName`。别名存在该 scope 内这个 skill 所有部署的 `Deployment.Alias` 里：
  - 再次 `enable` 不带 `--as` 沿用别名（规划时链接名取现有部署的别名而不是 `sk.Name`），为新 agent 建的链接也用它；`disable` 重新规划同理，`disable` 也可以直接写别名。
  - 另一个 `--as` 改名：旧链接 `remove`、新链接 `create`，在 `--dry-run` 里可见；新名字已被别的 skill 占用则整个命令拒绝。`--as` 取 skill 自己的名字等于去掉别名。
  - `update`：skill 在上游改名时不重命名带别名的链接；skill 目录被移动时按旧名字（rescan 之前记下）找到新目录并保留别名。
  - `skillet.toml` 记 `as`，`restore` 应用它（清单里没有 `as` 则去掉本地别名）。
- frontmatter `name` 与目录名不一致时警告（OpenCode 要求一致，链接名用 `name` 就自然满足）。

## 9. 代码结构

```
cmd/skillet/main.go
internal/
  config/      # config.toml 读写，路径展开
  state/       # state.json 读写、文件锁、迁移
  agents/      # 内置适配器表、目标选择、实际生效范围计算
  gitx/        # 调用 git：clone / fetch / diff / log / merge / worktree
  skill/       # 扫描、frontmatter 解析、名字校验、内容哈希
  link/        # link.go + link_unix.go + link_windows.go（symlink / junction / copy / 分类 / 安全删除）
  deploy/      # enable / disable / resync，组合 agents + link + state
  update/      # check / update 流程
  notify/      # 各平台桌面通知
  schedule/    # 各平台定时任务注册
  cli/         # cobra 命令定义、输出格式
  tui/         # 终端界面（bubbletea v2），见第 12 节
  i18n/        # TUI 的中英文文案表与语言检测
```

依赖：`spf13/cobra`、`BurntSushi/toml`、`gopkg.in/yaml.v3`、`gofrs/flock`、`golang.org/x/sys`。

## 10. 测试

- 单元测试：临时目录 + 本地裸仓库（`git init --bare`）模拟上游，覆盖 add → enable → 上游提交 → check → update 全流程。
- 链接测试：Windows 上通过 `--mode junction` / `copy` 强制走降级路径，验证创建、识别、安全删除。
- CI：GitHub Actions 三平台矩阵（ubuntu / macos / windows）。
- 手动验收：四个 agent 实际加载链接后的 skill，尤其是 Windows 上 Claude Code 对 junction 的支持。

## 11. 里程碑

1. **M1 核心**：init、repo add/list/remove、list、enable/disable/status，三平台链接（symlink / junction / copy）+ 安全替换。
2. **M2 更新**：check、update（diff 审阅、ff-only、copy 重同步、删除检测）、doctor。
3. **M3 提醒**（已完成）：被动检查、schedule、桌面通知、Claude hook。
4. **M4 增强**：pin（git worktree，已完成）、自定义 agent、项目内 `skillet.toml` 供团队还原（`save`/`restore`，已完成，见 6.5）；交互式选择待做。

## 12. 终端界面（TUI）

入口：终端里不带子命令的 `skillet`，或 `skillet tui`（`internal/cli/tui.go`）。未初始化时前者打印帮助和 `skillet init` 提示，后者返回同样的错误。技术栈为 Charm v2（`charm.land/bubbletea/v2`、`bubbles/v2`、`lipgloss/v2`、`huh/v2`）。

### 12.1 结构

- 根模型 `tui.Model`（`model.go`）：标签栏（带更新数、体检问题数）、范围指示（仅「技能」标签）、状态行、页脚按键帮助（`bubbles/help` + `key`）、全局按键（`1`–`4`、`tab`、`s`、`r`、`?`、`q`）、对话框和忙碌状态。
- 每个标签一个子模型：`skills.go`（矩阵 + 详情栏 + 搜索）、`updates.go`、`repos.go`、`doctor.go`。子模型只处理光标/勾选/滚动；启动操作、开对话框由根模型做。
- 纯函数与数据：`matrix.go`（`buildRows` / `filterRows`，把技能、`Status` 条目、agent 变成矩阵行和格子状态）、`pending.go`（待应用集合 → 每个 skill 的 `{enable, disable}`）、`ops.go`（预览 / 应用）、`data.go`（加载）。
- 对话框 `modal.go`：同一个组件用于变更预览、更新确认、添加/移除仓库、体检修复、固定版本和别名输入。

### 12.2 锁规则

TUI 长时间运行，**绝不在用户交互期间持有 `Manager`**。每次加载或操作都在一个 `tea.Cmd` 里：`deploy.OpenTry` → 干活 → `Close` → 返回结果消息（`withManager`）。锁被其他 skillet 进程占用时 `state.ErrLocked` 显示为友好提示。进程内并发的命令（启动时同时加载计数和矩阵）用包级互斥量排队，否则会互相抢锁、误报「正忙」。预览（dry-run）和应用是两次独立的打开/关闭；`Enable`/`Disable`/`Set` 应用时会重新规划，所以预览到应用之间状态变了也安全。长操作（fetch、clone、update、apply）在页脚显示 spinner，期间忽略按键（`ctrl+c` 除外）。

### 12.3 原子的逐 skill 变更：`Manager.Set`

同一个 skill 既要启用某些 agent 又要停用另一些时，分别调用 `Disable` 再 `Enable` 会规划两次，预览与实际不一致，也不是原子的。`Manager.Set(o, enable, disable)`（`deploy/enable.go`）按「当前服务的 agent − disable + enable」一次规划，只用 `change()`；没有要启用的就等同 `Disable`（可以处理上游已删除的 skill），否则等同 `Enable`（支持 `Pin`/`Alias`）。TUI 的预览和应用都是每个 skill 一次 `Set`，所以预览 == 应用。固定版本、别名也是对该 skill 当前服务的 agent 做一次带选项的 `Set`。

### 12.4 体检：`Issue.Fixable`

`deploy.Issue.Fixable()` 说明 `doctor --fix` 能否修复（orphan、orphan-pin、pin-missing、project-gone、missing、broken、wrong-target、outdated 可以；repo-missing、modified、clash 不行）。CLI 的计数和 TUI 的列表/确认框都用它，不各写一份。`Issue.Detail` 是英文句子，TUI 原样显示，类别名、分组和标签是本地化的。

### 12.5 i18n

`internal/i18n`：`en` / `zh` 两张表，稳定的字符串 key，`T(key, args...)` 按 fmt 格式化；测试保证两张表 key 完全一致、占位符一致。语言选择优先级：环境变量 `SKILLET_LANG` → `config.toml` 的 `lang` → `LC_ALL` → `LC_MESSAGES` → `LANG`（`zh` 开头为中文）→ 英文。只有 TUI 使用，命令行输出保持英文。宽度一律用 lipgloss/ansi 的单元格宽度（CJK 占两格），不用 `len`。

### 12.5.1 技能标签：按仓库分组与折叠

技能标签默认按仓库分组（`v` 在分组和平铺间切换，仅内存，不写配置）。每个仓库一行表头（折叠符号、仓库 id、`(N 个 · 已启用 M)`，组内有待应用项或上游更新时追加计数和 `↑`），按仓库 id 排序，其下缩进两格列出该仓库的技能；非托管条目在最后的「未托管」组；上游已删除但仍部署的技能放在各自的仓库 id 下。表头可被光标选中（范围分节标题不可）：`space`/`enter` 折叠，`z` 全部折叠（有展开的）或全部展开，`←/→`、`a`、`p`、`R` 在表头上无效；回车只在技能行上才打开预览。初始状态：所有组（含「未托管」）都折叠，表头上的计数已能看出哪里有已启用的技能；折叠状态按范围保存在内存里，重新加载、`v` 切换后保留，之后出现的新仓库同样先折叠。搜索非空时，有匹配的组一律展开（不改动保存的状态）并显示 `匹配/总数`；`f` 同样隐藏没有匹配的组。「全部」范围保持平铺（`v` 提示后忽略），避免双层标题。

### 12.6 只读与限制

- 「全部」范围按已部署的范围分节显示，**只读**：一次变更只对应一个范围，混在一起无法预览/应用。
- 非托管条目（`■`）和上游已不存在的 skill 不能在矩阵里切换（后者只能 `skillet disable`）。
- 有待应用的矩阵变更时不能固定版本/改别名，避免两种变更混在同一个预览里。
- 更新和移除仓库后不同步 `skillet.toml`（与 CLI 一致）；矩阵的应用会同步。

### 12.7 测试

不使用伪终端。模型测试直接调用 `Update`/`View`（`ansi.Strip` 后断言文本，中英文各一遍）；`ops`/仓库/更新/体检的命令用临时真实 store（本地 git 仓库作上游）验证磁盘结果和「预览不改动」；`program_test.go` 用管道输入、缓冲区输出跑一个真正的 `tea.Program` 走完 切换 → 预览 → 应用 → 退出。
