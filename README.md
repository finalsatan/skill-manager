# skillet

把 skill 所在的 git 仓库集中 clone 到一个目录，再按「项目目录 × agent」用软链接启用。支持 Claude Code、Codex、OpenCode、pi，可在 Linux、macOS、Windows 上运行。

设计见 [docs/design.md](docs/design.md)，调研见 [docs/research.md](docs/research.md)。

## 构建

```bash
go build -o bin/skillet ./cmd/skillet
```

需要系统里装有 `git`。

## 用法

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
