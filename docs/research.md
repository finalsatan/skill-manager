# Skill 管理 CLI —— 调研报告

调研日期：2026-10-10。Agent 的路径规则变化很快，开发前以官方文档为准再核实一次。

## 1. 各 Agent 的 skill 发现规则

所有 agent 都实现了 [Agent Skills 规范](https://agentskills.io/specification)：一个目录 + `SKILL.md`（frontmatter 含 `name`、`description`）。差异只在扫描路径。

| Agent | 项目级 | 用户级 | 其他 | 软链接 |
|---|---|---|---|---|
| Claude Code | `.claude/skills/<name>/`（从 cwd 向上到仓库根；子目录嵌套懒加载） | `~/.claude/skills/<name>/` | 企业 managed 目录、插件、`--add-dir` | 官方支持；多处链接指向同一目标只加载一次 |
| Codex | `.agents/skills/`（cwd 到仓库根逐级扫描） | `~/.agents/skills/`（`~/.codex/skills` 仍兼容读取，计划废弃） | `/etc/codex/skills`、内置系统 skill | 官方支持，跟随链接目标 |
| OpenCode | `.opencode/skills/`、`.claude/skills/`、`.agents/skills/`（向上到 git worktree 根） | `~/.config/opencode/skills/`、`~/.claude/skills/`、`~/.agents/skills/` | — | 文档未说明，需实测 |
| pi | `.pi/skills/`（约定，官方文档未写明）、`.agents/skills/`（向上到仓库根） | `~/.pi/agent/skills/`（agent 目录可用 `PI_CODING_AGENT_DIR` 改）、`~/.agents/skills/` | settings 里的 `skills` 数组、pi packages | 文档未说明；社区做法普遍是软链接/clone 进目录 |

### 关键发现

1. **`.agents/skills` 已经是事实标准**：Codex、OpenCode、pi 都读它（项目级和用户级）。只有 Claude Code 只读 `.claude/skills`。
   - 所以最省事的投放目标是两个：`.agents/skills` + `.claude/skills`。
2. **"按 agent 精确控制"会和共享目录冲突**：
   - 放进 `.agents/skills` 等于同时对 Codex、OpenCode、pi 生效。
   - Codex **没有自己专属的项目级目录**。要做到"这个项目只对 Codex 生效"，只能放 `.agents/skills`，再用 `~/.codex/config.toml` 的 `[[skills.config]] path=... enabled=false` 对其他 agent 屏蔽——但这个配置只对 Codex 生效，反过来不行。
   - OpenCode 同时读 `.claude/skills`，给 Claude 启用的 skill 会自动被 OpenCode 看到。
   - 结论：工具需要一张"agent → 实际会读哪些目录"的矩阵，启用时计算出**实际生效范围**并告诉用户，而不是假装每个 agent 都能独立开关。
3. **重名处理各不相同**：Claude Code 按 企业 > 个人 > 项目 覆盖；Codex 不合并、两个都显示；pi 保留第一个并警告；OpenCode 要求全局唯一。同一 skill 同时出现在 `~/.codex/skills` 和 `~/.agents/skills` 时 Codex 会出现重复项。工具应在 `doctor` 里检测重复。
4. **名字规范**（OpenCode 最严，按它来）：1–64 字符，`^[a-z0-9]+(-[a-z0-9]+)*$`，且必须与目录名一致。链接名应取 frontmatter 的 `name`，并校验。
5. **热加载**：Claude Code 监听 skills 目录，改 `SKILL.md` 即时生效（目录在会话启动时不存在则需 `/reload-skills`）；Codex 自动检测但不保证，可能要重启。更新后应提示"部分 agent 需重启会话"。

## 2. Windows 兼容性

| 方式 | 权限要求 | 限制 |
|---|---|---|
| 目录符号链接 `mklink /D` | 管理员或开启开发者模式 | 可跨卷、可相对路径 |
| 目录联接 `mklink /J`（junction） | 无 | 只能目录、只能本地卷、绝对路径 |
| 复制 | 无 | 需要自己追踪来源并重新同步 |

- 业界通行降级顺序：symlink → junction → copy（jiweiyeah/Skills-Manager 就是这么做的，复制时写 `.skills-manager-source.json` 记录来源）。skill 都是目录，所以 junction 基本够用。
- **Claude Code 对 junction 有过已知问题**（第三方 issue 镜像，需到官方仓库核实现状）：
  - 2026-03：`~/.claude/skills` 下 junction 链接的 skill 在会话首次调用时报 "Unknown skill"，之后正常（已关闭）。
  - 2026-06：桌面版 `/` 菜单不显示 junction skill，但模型能加载（已关闭）。
  - 2026-07：桌面版在新会话 worktree 里把被 gitignore 的 junction 复制成真实目录（仍开放）。
- WSL：Windows 原生程序无法在 WSL 文件系统里建链接，只能复制；WSL 里的 agent 应该用 Linux 版 CLI。
- 其他细节：Windows 上 git 的 `core.autocrlf` 可能改动 skill 里脚本的换行；删除 junction 时必须删链接本身而不是递归删除内容（Go 的 `os.RemoveAll` 对 junction 的行为要专门测试）。

## 3. 竞品

| 项目 | 形态 | Star | 中央存储 | Git 仓库作为源 | 项目级 | 更新检测 | Windows |
|---|---|---|---|---|---|---|---|
| [xingkongliang/skills-manager](https://github.com/xingkongliang/skills-manager) | Tauri 桌面 + Rust CLI | ~5.9k | `~/.skills-manager`，SQLite 元数据 | ✅ | ✅ Project Workspaces | ✅ `skills check/update --all`（手动触发） | ✅（WSL 只能复制） |
| [vercel-labs/skills](https://github.com/vercel-labs/skills)（`npx skills`） | Node CLI | 很高（skills.sh 生态） | `~/.agents/skills` 规范副本 | ✅ GitHub | ✅（默认） | `check`/`update`，经 Vercel API 比对哈希；**据文档项目级不追踪更新** | junction |
| [jiweiyeah/Skills-Manager](https://github.com/jiweiyeah/Skills-Manager) | Tauri 桌面 + `skm` CLI | ~1.0k | 自选目录 | ❌（只有市场） | ❌ 只按工具 | ❌ | ✅ symlink→junction→copy |
| [803/skills-supply](https://github.com/803/skills-supply)（`sk`） | CLI | ~35 | — | ✅ gh/git/本地 | ✅ `agents.toml` | 每次 `sk sync` 拉最新分支；tag/rev 固定 | Scoop 安装，细节未知 |
| [emanuelet/skill-manager](https://github.com/emanuelet/skill-manager)（`sm`） | TS CLI | 0（fork） | `~/.skill-manager/skills` | ✅ | ✅ `.skills.json` | SHA-256 内容哈希；`doctor` 提示 30 天未同步 | ❌ |

另外注意：
- 你打算用的 `skm` 名字已被 jiweiyeah 的项目占用，需要换名。
- vercel-labs/skills 有公开 issue（#851）：`-a claude-code -g` 只装到 `~/.agents/skills` 而没链到 `~/.claude/skills`，说明"共享目录 + 个别 agent 例外"的映射容易出错。

## 4. 差异化机会

没有一个竞品同时做好这三件事：

1. **以 git clone 为中心**：中央存储是完整的 git 工作区，链接直接指向它。`git pull` 一次全部生效；需要固定版本时用 `git worktree`。vercel 是拷贝副本，xingkongliang 是导入到自己的库。
2. **全局反向索引**："哪个仓库的哪个 skill，被哪些项目的哪些 agent 用着"。现有工具大多只知道全局目录，或者需要每个项目自己的 manifest，没有跨项目视图。功能 3（更新所有相关目录）依赖它。
3. **主动的更新监控**：后台/定时检查 + 通知 + 展示 diff 再确认。现有工具都是用户手动跑 `check`；skill 带可执行脚本，"先看 diff 再更新"也是安全卖点。

可以再加的点：
- agent 读取路径矩阵 + "实际生效范围"提示 + 重复/冲突检测（上文第 1 节）。
- 项目内 `skills.lock` 提交到仓库，队友 `sync` 还原；链接目录自动加 `.gitignore`。
- 单二进制、无 GUI 依赖，适合服务器/SSH 场景。

**风险**：xingkongliang/skills-manager 已经覆盖了很大一部分功能且很活跃。建议动手前实际装一下它和 `npx skills` 试用，确认差异点足够大。

## 5. 对设计的直接影响

- 投放目标默认用 `.agents/skills` + `.claude/skills`；只有用户要求独占时才用 `.opencode/skills`、`.pi/skills`。
- 适配器表作为数据文件维护，字段：`id`、项目目录列表、用户目录列表、还会读取的其他目录、是否支持软链接、是否需要重启。
- 链接名用 frontmatter `name`，按 OpenCode 规则校验。
- Windows：symlink → junction → copy，每条链接记录模式；copy 模式记录内容哈希。

## 来源

- Claude Code skills：https://code.claude.com/docs/en/skills
- Codex skills：https://learn.chatgpt.com/docs/build-skills
- Codex `~/.agents/skills` 迁移讨论：https://community.openai.com/t/codex-desktop-is-the-empty-skills-picker-a-temporary-upgrade-issue/1381921
- OpenCode skills：https://opencode.ai/docs/skills/
- pi skills：https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/skills.md
- pi 路径（第三方）：https://wener.me/notes/ai/agent/pi
- vercel-labs/skills README：https://cdn.jsdelivr.net/gh/vercel-labs/skills@main/README.md
- vercel skills 更新机制（第三方文档）：https://www.mintlify.com/vercel-labs/skills/advanced/update-system
- vercel skills issue #851：https://github.com/vercel-labs/skills/issues/851
- Claude Code junction 问题（第三方镜像）：https://claudeissues.com/issue/41177-skills-in-claude-skills-via-directory-junctions-not-resolved-at-session-start-wi 、https://claudeissues.com/issue/68318-desktop-app-omits-windows-junction-symlink-skills-from-the-command-menu-agent-lo 、https://claudeissues.com/issue/80819-bug-desktop-app-materializes-junctioned-claude-skills-as-real-dir-copies-in-new
- 竞品：https://github.com/xingkongliang/skills-manager 、https://github.com/jiweiyeah/Skills-Manager 、https://github.com/803/skills-supply 、https://github.com/emanuelet/skill-manager
