<div align="center">

# skillet

**Manage agent skills from git repos across Claude Code, Codex, OpenCode and pi.**

[![CI](https://github.com/finalsatan/skill-manager/actions/workflows/ci.yml/badge.svg)](https://github.com/finalsatan/skill-manager/actions/workflows/ci.yml)
[![Go](https://img.shields.io/github/go-mod/go-version/finalsatan/skill-manager)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

English | [简体中文](README.zh-CN.md)

![skillet TUI: skill × agent matrix with pending changes](docs/images/tui-skills.png)

</div>

## Why skillet?

Agent skills live in git repositories, but every coding agent reads them from a different directory, and copying them by hand means they never get updated. skillet keeps each skill repository as a real git clone in one store, links the skills you want into each project for each agent, and tells you when upstream changes — with the commits and diff to review before you pull them in.

## Features

- **Git repos are the source of truth** — `skillet repo add owner/repo` clones it; links point straight into the clone, so there is nothing to sync.
- **Enable per project × agent** — turn a skill on for Claude Code in one project and for every agent globally, from the CLI or a TUI matrix.
- **Minimal links, honest visibility** — skillet knows which agents read which directories, creates as few links as possible and prints who can actually see each skill.
- **Reviewed updates** — `check` only reports skills whose own directory changed upstream and flags changed scripts; `update` shows the log and diff before fast-forwarding.
- **Team manifests** — `skillet save` writes `skillet.toml`; teammates run `skillet restore` after cloning.
- **Safe and cross-platform** — never overwrites files it didn't create; works on Linux, macOS and Windows (symlink → junction → copy).

## Supported agents

| Agent | Project directory | Global directory |
|---|---|---|
| Claude Code | `.claude/skills` | `~/.claude/skills` |
| Codex | `.agents/skills` | `~/.agents/skills` |
| OpenCode | `.opencode/skills` (also reads `.claude/skills`, `.agents/skills`) | `~/.config/opencode/skills` |
| pi | `.pi/skills` (also reads `.agents/skills`) | `~/.pi/agent/skills` |

Other agents can be added in the config file — see [Configuration](#configuration).

## Installation

skillet needs `git` on your `PATH`.

```bash
go install github.com/finalsatan/skill-manager/cmd/skillet@latest
```

Or build from source:

```bash
git clone https://github.com/finalsatan/skill-manager.git
cd skill-manager
go build -o bin/skillet ./cmd/skillet
```

## Quick start

```bash
skillet init                          # create the config and the store (~/.skillet by default)
skillet repo add anthropics/skills    # clone a skill repo (owner/repo, URL or local path)
skillet list                          # see the skills it provides

cd ~/work/my-project
skillet enable pdf -a claude,codex    # link the skill for Claude Code and Codex in this project
skillet status                        # skill × agent matrix for this project
```

Or just run `skillet` with no arguments to do all of this in the terminal UI.

## Common commands

| Command | What it does |
|---|---|
| `skillet enable <skill> -a <agents>` | Link skills into a project (`-g` for global, `--dry-run` to preview) |
| `skillet disable <skill> [-a <agents>]` | Remove the links |
| `skillet status [--all]` | Show which skills each agent sees |
| `skillet check` | Fetch repos and list skills that changed upstream |
| `skillet update [--diff]` | Review upstream commits, then fast-forward |
| `skillet doctor [--fix]` | Find and repair broken links and name clashes |
| `skillet save` / `skillet restore` | Write / apply the project's `skillet.toml` |
| `skillet schedule install` | Check for updates periodically in the background |
| `skillet hook claude` | Print a Claude Code hook that reports pending updates |

Run `skillet <command> --help` for every flag, or see the full [usage guide](docs/usage.md).

## Terminal UI

Run `skillet` (or `skillet tui`) in a terminal. Four tabs, switched with `1`–`4`:

- **Skills** — the skill × agent matrix: toggle cells with `space`, preview and apply with `enter`.
- **Updates** — upstream changes per repository, with the full diff one key away.
- **Repos** — add and remove skill repositories.
- **Doctor** — problems grouped by severity, with one-key fixes.

![Updates tab: upstream commits to review before updating](docs/images/tui-updates.png)

Every change is previewed as a dry run before it runs. Press `?` for all keys; the [usage guide](docs/usage.md#terminal-ui) lists them too. The UI is available in English and Chinese (`SKILLET_LANG=zh`).

## Configuration

The config file is `~/.config/skillet/config.toml` (`%AppData%\skillet` on Windows); set `SKILLET_CONFIG_DIR` to move it. Add or override agents with `[[agents]]`:

```toml
[[agents]]
id = "hermes"
project_dir = ".hermes/skills"
global_dir = "~/.hermes/skills"
```

## How it works

- **Minimal links**: OpenCode also reads `.claude/skills` and `.agents/skills`, and pi also reads `.agents/skills`, so `-a all` creates just two links: `.claude/skills/<name>` and `.agents/skills/<name>`.
- **Never touches what isn't its own**: if a target already holds a directory or link skillet did not create, the whole operation is refused and nothing changes.
- **Windows**: tries a symlink, then a junction (no admin rights needed), then a copy. Copies carry a `.skillet-source.json` so skillet can recognise them and detect manual edits. Force a mode with `--mode` or `link_mode` in the config.
- **Updates**: symlinks pick up changes automatically; copies are re-copied unless edited by hand; renamed or moved skills are relinked; skills deleted upstream are unlinked. Repositories with local changes or a diverged history are never updated.

See [docs/design.md](docs/design.md) for the full design and [docs/research.md](docs/research.md) for how each agent discovers skills (both in Chinese).

## Status

All planned features are implemented and CI runs on Linux, macOS and Windows. The TUI has not yet been tried on light terminal backgrounds or on real Windows / macOS terminals — bug reports are welcome.

## Contributing

Issues and pull requests are welcome. Before sending a change, run:

```bash
go vet ./...
go test ./...
```

## License

[MIT](LICENSE)
