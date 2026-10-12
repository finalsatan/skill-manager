# skillet

把 skill 所在的 git 仓库集中 clone 到一个目录，再按「项目目录 × agent」用软链接启用。支持 Claude Code、Codex、OpenCode、pi，可在 Linux、macOS、Windows 上运行。

设计见 [docs/design.md](docs/design.md)，调研见 [docs/research.md](docs/research.md)。

## 构建

```bash
go build -o bin/skillet ./cmd/skillet
```

需要系统里装有 `git`。

## 用法

### 终端界面（TUI）

```bash
skillet            # 在终端里不带子命令运行，直接进入界面
skillet tui        # 同上；非终端、--no-input、SKILLET_NO_INPUT、CI 时 `skillet` 只打印帮助
```

四个标签，按 `1`–`4`（或 `tab`）切换：

- **技能**：skill × agent 矩阵。`↑/↓` 移动，`←/→` 选 agent 列，`space` 切换当前格（`+` 待启用、`−` 待停用），`a` 整行切换，`enter` 预览并应用，`esc` 清除待应用项；`f` 只看已启用，`/` 搜索，`p` 固定版本，`R` 改链接名；默认按仓库分组：`space`/`enter` 折叠或展开仓库，`z` 全部折叠/展开，`v` 切换平铺视图；`s` 在 项目 → 全局 → 全部 间切换范围（「全部」只读）。
- **更新**：有上游变更的仓库，`space` 勾选，`r` 联网检查，`D` 看完整 diff，`enter` 更新。
- **仓库**：`n` 添加，`x` 移除（先预览），`enter` 看该仓库的技能，`u` 跳到更新。
- **体检**：问题按严重程度分组，`F` 一键修复可修复的项，`enter` 跳到相关技能。

`?` 展开全部按键，`q` 退出。所有改动都先 dry-run 预览，确认后才执行；TUI 不会在你思考时占着状态锁。

界面语言：环境变量 `SKILLET_LANG`（`zh` / `en`）优先，其次 `config.toml` 的 `lang = "zh"`，再其次 `LC_ALL` / `LC_MESSAGES` / `LANG`（以 `zh` 开头即中文），默认英文。命令行输出始终是英文。

### 命令行

```bash
skillet init --store ~/skills-store          # 选择存放仓库的目录
skillet repo add anthropics/skills            # 也可以是 https/ssh URL 或本地路径（--dry-run 只显示会 clone 到哪）
skillet list                                  # 列出可用 skill

cd ~/work/projA
skillet enable pdf -a claude,codex            # 在当前项目（git 根）启用
skillet enable pdf -a all --dry-run           # 先看会建哪些链接
skillet enable pdf -a claude -g               # 用户级（全局）启用
skillet status                                # skill × agent 矩阵；-l 显示每条链接路径
skillet enable weird_name -a claude --as weird-name   # 名字不合规或重名时，换一个链接名
skillet status --all                          # 所有有部署的 scope（全局 + 各项目）
skillet disable pdf -a claude                 # 只对 claude 停用
skillet disable pdf                           # 全部停用
skillet enable                                # 终端里：交互选择 skill 和 agent，预览后确认（--yes 跳过确认）
skillet disable                               # 终端里：从本目录已部署的 skill 里选
# --no-input 或环境变量 SKILLET_NO_INPUT=1 / CI 关闭所有提问

skillet check                                 # fetch 所有仓库，列出哪些 skill 在上游变了
skillet update                                # 看提交记录和 diffstat，确认后 fast-forward
skillet update --diff                         # 显示完整 diff（改了脚本时建议看）
skillet update -y                             # 不询问直接更新
skillet update --dry-run                      # fetch 后只展示将应用的内容，不询问不合并
skillet pending [--json]                      # 上次 check 的结果（不联网）
skillet doctor [--fix] [--json]                        # 失效链接、已删除的项目、同名冲突

skillet save [--dry-run]                      # 把当前项目的 skill 写入 skillet.toml（提交到 git）
skillet restore [--dry-run] [--prune]         # 队友 clone 项目后一条命令还原同样的 skill

skillet repo remove anthropics/skills --force # （--dry-run 先看）删除仓库及其所有链接
skillet repo show anthropics/skills           # 仓库详情：落后提交数、pin 检出、各 skill 及部署位置（--json）
skillet agents                                # 各 agent 读取的目录和是否已安装（--json）
```

skill 重名时用 `owner/repo/name` 或完整 id（`skillet list --json` 里的 `id`）。

## 行为说明

- **最少链接**：OpenCode 也读 `.claude/skills` 和 `.agents/skills`，pi 也读 `.agents/skills`。所以 `-a all` 只建两条链接：`.claude/skills/<name>` 和 `.agents/skills/<name>`。每次操作后会打印"实际对哪些 agent 可见"。
- **不碰别人的东西**：目标位置已有不是 skillet 创建的目录或链接时，整个操作都会被拒绝，不做任何改动。
- **Windows**：依次尝试 symlink、junction（不需要管理员权限）、复制。复制出来的目录里有 `.skillet-source.json`，用来识别和检测手动修改。可用 `--mode` 或 config 里的 `link_mode` 强制指定。
- **更新**：`check` 只把真正改了 skill 目录的提交算作更新，并标出改了脚本等非 Markdown 文件的 skill。`update` 之后：软链接自动生效；复制的副本会重新复制，手动改过的跳过；skill 改名或换目录会重新链接；上游删除的 skill 会删掉对应链接。store 里的仓库有本地修改或已分叉时拒绝更新。
- **配置**：`~/.config/skillet/config.toml`（Windows 为 `%AppData%\skillet`），环境变量 `SKILLET_CONFIG_DIR` 可以覆盖。可在里面用 `[[agents]]` 覆盖或新增 agent：

  ```toml
  [[agents]]
  id = "hermes"
  project_dir = ".hermes/skills"
  global_dir = "~/.hermes/skills"
  ```

## 进度

- [x] M1：init、repo add/list/remove、list、enable/disable、status、agents，三平台链接
- [x] M2：check / update（按 skill 子目录检测更新、diff 审阅、copy 重新同步）、doctor
- [x] M3：被动提醒 + 后台检查、`schedule install/remove`、`check --notify/--quiet`、`pending`、`hook claude`
- [x] M4：固定版本（`enable --pin`，git worktree）已完成；团队清单 `skillet.toml`（`save` / `restore`，之后 `enable`/`disable` 自动同步）已完成；交互式选择（`enable`/`disable` 缺参数时用 huh 多选，`--no-input` 关闭）已完成；`repo show`、`status --all`、`enable --as`、agent 检测、`doctor --json` 已完成
- [x] TUI（`skillet` / `skillet tui`，bubbletea v2）：技能矩阵（切换、预览、应用、固定版本、别名）、更新、仓库、体检四个标签，中英文界面。未在真实终端、浅色背景、Windows / macOS 上验证。
