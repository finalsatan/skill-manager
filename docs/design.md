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
├── pins/<host>/<owner>/<repo>@<short-sha>/   # 固定版本时用 git worktree 检出
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
   - pin 部署：不动，提示"有 N 个固定版本的部署未更新"。
   - skill 目录在新版本中被删除：链接会失效，列出受影响的部署，询问是否 `disable`。
   - 新增的 skill：只提示，不自动启用。
5. 提示：Claude Code 会热加载；Codex、pi 可能需要重启会话。

## 7. 命令

```
skillet init [--store <dir>]                       # 生成配置，创建 store
skillet repo add <url | owner/repo> [--branch b]   # clone，扫描 SKILL.md
skillet repo list | remove <repo> | show <repo>
skillet list [--repo <repo>]                       # 列出可用 skill
skillet enable <skill…> --agent a,b [--dir <path> | --global] [--pin <rev>] [--mode …]
skillet disable <skill…> [--agent a,b] [--dir <path> | --global]
skillet status [--dir <path> | --global | --all]   # 当前目录各 agent 生效了什么
skillet check [--notify] [--quiet]
skillet update [repo…] [--yes] [--diff]
skillet doctor [--fix]                             # 失效链接、重名、目录已删除的项目、未受管目录
skillet agents                                     # 适配器表及检测到的安装情况
skillet schedule install [--every 6h] | remove
skillet hook claude
```

- skill 引用：唯一时直接写 `pdf`；重名时写 `anthropics/skills/pdf` 或完整 id。
- `--dir` 默认当前目录，并向上找 git 根作为项目目录（`--no-git-root` 关闭）。
- 所有会改文件系统的命令支持 `--dry-run`，查询类命令支持 `--json`。

## 8. Skill 扫描与校验

- 在仓库内递归查找 `SKILL.md`，跳过 `.git`、`node_modules`；找到后不再向下递归。
- 解析 YAML frontmatter：`name` 必填，作为链接名；`description` 必填。
- 名字按最严的 OpenCode 规则校验：`^[a-z0-9]+(-[a-z0-9]+)*$`，1–64 字符。不合规时警告，并允许 `enable --as <别名>`。
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
4. **M4 增强**：pin（git worktree）、自定义 agent、项目内 `skillet.toml` 供团队还原。
