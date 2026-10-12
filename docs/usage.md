# Usage guide

English | [简体中文](usage.zh-CN.md)

- [Terminal UI](#terminal-ui)
- [Command line](#command-line)
- [Prompts and scripting](#prompts-and-scripting)

## Terminal UI

```bash
skillet            # run without a subcommand in a terminal to open the UI
skillet tui        # same
```

With no terminal, with `--no-input`, `SKILLET_NO_INPUT` or `CI` set, bare `skillet` just prints help.

Four tabs, switched with `1`–`4` (or `tab`):

### Skills

A skill × agent matrix.

| Key | Action |
|---|---|
| `↑` / `↓` | Move |
| `←` / `→` | Pick the agent column |
| `space` | Toggle the current cell (`+` pending enable, `−` pending disable); on a repository row, collapse or expand it |
| `a` | Toggle the whole row |
| `enter` | Preview and apply pending changes; on a repository row, collapse or expand it |
| `esc` | Clear pending changes |
| `f` | Show only enabled skills |
| `/` | Search |
| `p` | Pin a version |
| `R` | Rename the link |
| `z` | Collapse / expand all repositories |
| `v` | Switch between grouped and flat view |
| `s` | Cycle the scope: project → global → all ("all" is read-only) |

Skills are grouped by repository and all groups start collapsed.

### Updates

Repositories with upstream changes.

| Key | Action |
|---|---|
| `space` | Select |
| `r` | Check online |
| `D` | Show the full diff |
| `enter` | Update |

### Repos

| Key | Action |
|---|---|
| `n` | Add a repository |
| `x` | Remove (with a preview first) |
| `enter` | Show that repository's skills |
| `u` | Jump to updates |

### Doctor

Problems grouped by severity.

| Key | Action |
|---|---|
| `F` | Fix everything fixable |
| `enter` | Jump to the related skill |

### Everywhere

`?` shows all keys, `q` quits. Every change is previewed as a dry run and only executed after you confirm; the TUI does not hold the state lock while you are thinking.

**UI language**: the `SKILLET_LANG` environment variable (`zh` / `en`) wins, then `lang = "zh"` in `config.toml`, then `LC_ALL` / `LC_MESSAGES` / `LANG` (Chinese if it starts with `zh`); the default is English. Command-line output is always English.

## Command line

### Setup and repositories

```bash
skillet init --store ~/skills-store           # choose where repositories are stored (default ~/.skillet)
skillet repo add anthropics/skills            # also accepts an https/ssh URL or a local path
skillet repo add anthropics/skills --dry-run  # show where it would be cloned
skillet repo list                             # repositories in the store
skillet repo show anthropics/skills           # commits behind, pinned checkouts, skills and where they're deployed (--json)
skillet repo remove anthropics/skills --force # delete a repository and all its links (preview with --dry-run)
skillet list                                  # skills available in the store (--json)
skillet agents                                # directories each agent reads and whether it is installed (--json)
```

### Enabling and disabling

```bash
cd ~/work/projA
skillet enable pdf -a claude,codex            # enable in the current project (git root)
skillet enable pdf -a all --dry-run           # see which links would be created
skillet enable pdf -a claude -g               # enable at user (global) level
skillet enable pdf -a claude --pin v1.2.0     # pin to a sha, tag or branch (git worktree)
skillet enable weird_name -a claude --as weird-name   # use a different link name for invalid or clashing names
skillet disable pdf -a claude                 # disable for claude only
skillet disable pdf                           # disable everywhere
skillet enable                                # in a terminal: pick skills and agents interactively, preview, confirm (--yes skips confirmation)
skillet disable                               # in a terminal: pick from skills deployed in this directory

skillet status                                # skill × agent matrix; -l shows each link path
skillet status --all                          # every scope with deployments (global + each project)
```

Use `-d <dir>` to target another project, and `--exact` to use that directory as is instead of its git root.

When skill names clash, use `owner/repo/name` or the full id (the `id` field in `skillet list --json`).

### Updates

```bash
skillet check                                 # fetch all repositories and list skills that changed upstream
skillet update                                # review commit log and diffstat, then fast-forward after confirming
skillet update --diff                         # show the full diff (recommended when scripts changed)
skillet update -y                             # update without asking
skillet update --dry-run                      # fetch and only show what would be applied, no prompt, no merge
skillet pending [--json]                      # results of the last check (offline)
```

### Background checks and reminders

```bash
skillet schedule install                      # run `skillet check --notify` periodically (systemd timer or crontab, launchd, schtasks)
skillet schedule remove
skillet hook claude                           # print a Claude Code SessionStart hook; merge it into .claude/settings.json
```

After ordinary commands, skillet prints a one-line reminder when upstream updates are waiting, and starts a background `skillet check --quiet` when the last check is older than `check_interval` (6h by default). Set `SKILLET_NO_REMIND=1` to turn this off.

### Team manifest

```bash
skillet save [--dry-run]                      # write the current project's skills to skillet.toml (commit it to git)
skillet restore [--dry-run] [--prune]         # teammates restore the same skills with one command after cloning
```

Later `enable` / `disable` runs keep `skillet.toml` in sync.

### Diagnostics

```bash
skillet doctor [--fix] [--json]               # broken links, deleted projects, unused pin checkouts, name clashes
```

## Prompts and scripting

`enable` and `disable` without arguments, and `update` without `-y`, ask questions in a terminal. `--no-input`, `SKILLET_NO_INPUT=1` or `CI` disables every prompt: commands that would need one fail instead. Most read commands accept `--json`.
