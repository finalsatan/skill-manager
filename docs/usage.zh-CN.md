# 使用指南

[English](usage.md) | 简体中文

- [终端界面](#终端界面)
- [命令行](#命令行)
- [提问与脚本](#提问与脚本)

## 终端界面

```bash
skillet            # 在终端里不带子命令运行，直接进入界面
skillet tui        # 同上
```

非终端、`--no-input`、设置了 `SKILLET_NO_INPUT` 或 `CI` 时，不带子命令的 `skillet` 只打印帮助。

四个标签，按 `1`–`4`（或 `tab`）切换：

### 技能

skill × agent 矩阵。

| 按键 | 作用 |
|---|---|
| `↑` / `↓` | 移动 |
| `←` / `→` | 选 agent 列 |
| `space` | 切换当前格（`+` 待启用、`−` 待停用）；在仓库行上折叠或展开 |
| `a` | 整行切换 |
| `enter` | 预览并应用待应用项；在仓库行上折叠或展开 |
| `esc` | 清除待应用项 |
| `f` | 只看已启用 |
| `/` | 搜索 |
| `p` | 固定版本 |
| `R` | 改链接名 |
| `z` | 全部折叠 / 展开 |
| `v` | 在分组视图和平铺视图间切换 |
| `s` | 在 项目 → 全局 → 全部 间切换范围（「全部」只读） |

默认按仓库分组，且全部折叠。

### 更新

有上游变更的仓库。

| 按键 | 作用 |
|---|---|
| `space` | 勾选 |
| `r` | 联网检查 |
| `D` | 看完整 diff |
| `enter` | 更新 |

### 仓库

| 按键 | 作用 |
|---|---|
| `n` | 添加仓库 |
| `x` | 移除（先预览） |
| `enter` | 看该仓库的技能 |
| `u` | 跳到更新 |

### 体检

问题按严重程度分组。

| 按键 | 作用 |
|---|---|
| `F` | 一键修复可修复的项 |
| `enter` | 跳到相关技能 |

### 通用

`?` 展开全部按键，`q` 退出。所有改动都先 dry-run 预览，确认后才执行；TUI 不会在你思考时占着状态锁。

**界面语言**：环境变量 `SKILLET_LANG`（`zh` / `en`）优先，其次 `config.toml` 的 `lang = "zh"`，再其次 `LC_ALL` / `LC_MESSAGES` / `LANG`（以 `zh` 开头即中文），默认英文。命令行输出始终是英文。

## 命令行

### 初始化与仓库

```bash
skillet init --store ~/skills-store           # 选择存放仓库的目录（默认 ~/.skillet）
skillet repo add anthropics/skills            # 也可以是 https/ssh URL 或本地路径
skillet repo add anthropics/skills --dry-run  # 只显示会 clone 到哪
skillet repo list                             # store 里的仓库
skillet repo show anthropics/skills           # 落后提交数、pin 检出、各 skill 及部署位置（--json）
skillet repo remove anthropics/skills --force # 删除仓库及其所有链接（--dry-run 先看）
skillet list                                  # store 里可用的 skill（--json）
skillet agents                                # 各 agent 读取的目录和是否已安装（--json）
```

### 启用与停用

```bash
cd ~/work/projA
skillet enable pdf -a claude,codex            # 在当前项目（git 根）启用
skillet enable pdf -a all --dry-run           # 先看会建哪些链接
skillet enable pdf -a claude -g               # 用户级（全局）启用
skillet enable pdf -a claude --pin v1.2.0     # 固定到某个 sha、tag 或分支（git worktree）
skillet enable weird_name -a claude --as weird-name   # 名字不合规或重名时，换一个链接名
skillet disable pdf -a claude                 # 只对 claude 停用
skillet disable pdf                           # 全部停用
skillet enable                                # 终端里：交互选择 skill 和 agent，预览后确认（--yes 跳过确认）
skillet disable                               # 终端里：从本目录已部署的 skill 里选

skillet status                                # skill × agent 矩阵；-l 显示每条链接路径
skillet status --all                          # 所有有部署的 scope（全局 + 各项目）
```

用 `-d <dir>` 指定其他项目，加 `--exact` 则直接使用该目录而不是它的 git 根。

skill 重名时用 `owner/repo/name` 或完整 id（`skillet list --json` 里的 `id`）。

### 更新

```bash
skillet check                                 # fetch 所有仓库，列出哪些 skill 在上游变了
skillet update                                # 看提交记录和 diffstat，确认后 fast-forward
skillet update --diff                         # 显示完整 diff（改了脚本时建议看）
skillet update -y                             # 不询问直接更新
skillet update --dry-run                      # fetch 后只展示将应用的内容，不询问不合并
skillet pending [--json]                      # 上次 check 的结果（不联网）
```

### 后台检查与提醒

```bash
skillet schedule install                      # 定期运行 `skillet check --notify`（systemd timer 或 crontab、launchd、schtasks）
skillet schedule remove
skillet hook claude                           # 输出 Claude Code 的 SessionStart hook，合并进 .claude/settings.json
```

执行普通命令后，如果有待更新的 skill，skillet 会打印一行提醒；距上次检查超过 `check_interval`（默认 6h）时，会在后台启动 `skillet check --quiet`。设置 `SKILLET_NO_REMIND=1` 可关闭。

### 团队清单

```bash
skillet save [--dry-run]                      # 把当前项目的 skill 写入 skillet.toml（提交到 git）
skillet restore [--dry-run] [--prune]         # 队友 clone 项目后一条命令还原同样的 skill
```

之后的 `enable` / `disable` 会自动同步 `skillet.toml`。

### 诊断

```bash
skillet doctor [--fix] [--json]               # 失效链接、已删除的项目、未使用的 pin 检出、同名冲突
```

## 提问与脚本

不带参数的 `enable` / `disable`，以及不带 `-y` 的 `update`，会在终端里提问。`--no-input`、`SKILLET_NO_INPUT=1` 或 `CI` 关闭所有提问：需要提问的命令会直接失败。大多数查询类命令支持 `--json`。
