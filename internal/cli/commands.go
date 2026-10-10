package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"skillet/internal/agents"
	"skillet/internal/config"
	"skillet/internal/deploy"
	"skillet/internal/link"
	"skillet/internal/skill"
	"skillet/internal/state"
)

func newInitCmd() *cobra.Command {
	var store string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the config file and the store directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			cfg, err := config.Load()
			switch {
			case err == nil:
				if store != "" {
					want, err := config.ExpandHome(store)
					if err != nil {
						return err
					}
					if want != cfg.Store {
						p, _ := config.Path()
						return fmt.Errorf("already initialized with store %s; edit %s to change it", cfg.Store, p)
					}
				}
				fmt.Fprintf(out, "Already initialized. Store: %s\n", cfg.Store)
				return nil
			case !errors.Is(err, config.ErrNotInitialized):
				return err
			}
			if cfg, err = config.Default(); err != nil {
				return err
			}
			if store != "" {
				if cfg.Store, err = config.ExpandHome(store); err != nil {
					return err
				}
			}
			st, err := state.Open(cfg.Store)
			if err != nil {
				return err
			}
			if err := st.Save(); err != nil {
				st.Close()
				return err
			}
			st.Close()
			if err := config.Save(cfg); err != nil {
				return err
			}
			p, _ := config.Path()
			fmt.Fprintf(out, "Store:  %s\nConfig: %s\n", cfg.Store, p)
			return nil
		},
	}
	cmd.Flags().StringVar(&store, "store", "", "directory for cloned skill repos (default ~/.skillet)")
	return cmd
}

func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "repo", Short: "Manage skill repositories"}

	var branch string
	var addDry bool
	add := &cobra.Command{
		Use:   "add <owner/repo | url | path>",
		Short: "Clone a repository into the store and register its skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withManager(func(m *deploy.Manager) error {
				out := cmd.OutOrStdout()
				if addDry {
					plan, err := m.PlanAddRepo(args[0], branch)
					if err != nil {
						return err
					}
					if branch == "" {
						branch = "(the remote's default)"
					}
					fmt.Fprintf(out, "Dry run, nothing cloned.\nWould add %s\n  url:    %s\n  dir:    %s\n  branch: %s\n", plan.ID, plan.URL, plan.Dir, branch)
					return nil
				}
				id, found, err := m.AddRepo(args[0], branch)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Added %s (%d skills)\n", id, len(found))
				w := table(out)
				for _, f := range found {
					fmt.Fprintf(w, "  %s\t%s\n", f.Name, f.RelPath)
					for _, warn := range f.Warnings {
						fmt.Fprintf(w, "    warning: %s\n", warn)
					}
				}
				return w.Flush()
			})
		},
	}
	add.Flags().StringVar(&branch, "branch", "", "branch to track (default: the remote's default branch)")
	add.Flags().BoolVar(&addDry, "dry-run", false, "show where it would be cloned; clone nothing")

	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List repositories in the store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withManager(func(m *deploy.Manager) error {
				if asJSON {
					return printJSON(cmd.OutOrStdout(), m.St.Repos)
				}
				count := map[string]int{}
				for _, sk := range m.St.Skills {
					count[sk.Repo]++
				}
				w := table(cmd.OutOrStdout())
				fmt.Fprintln(w, "REPO\tBRANCH\tHEAD\tSKILLS\tURL")
				for _, id := range m.RepoIDs() {
					r := m.St.Repos[id]
					fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", id, r.Branch, short(r.Head), count[id], r.URL)
				}
				return w.Flush()
			})
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "print JSON")

	var force, rmDry bool
	remove := &cobra.Command{
		Use:     "remove <repo>",
		Aliases: []string{"rm"},
		Short:   "Delete a repository from the store",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withManager(func(m *deploy.Manager) error {
				id, err := m.ResolveRepo(args[0])
				if err != nil {
					return err
				}
				if rmDry {
					plan, perr := m.PlanRemoveRepo(id, force)
					out := cmd.OutOrStdout()
					fmt.Fprintf(out, "Dry run, nothing changed.\nWould remove %s\n", id)
					for _, t := range plan.Links {
						fmt.Fprintf(out, "  unlink %s\n", t)
					}
					for _, p := range plan.Pins {
						fmt.Fprintf(out, "  remove pin checkout %s\n", p)
					}
					return perr
				}
				removed, err := m.RemoveRepo(id, force)
				for _, t := range removed {
					fmt.Fprintf(cmd.OutOrStdout(), "  unlinked %s\n", t)
				}
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", id)
				return nil
			})
		},
	}
	remove.Flags().BoolVar(&force, "force", false, "also remove every deployment of its skills")
	remove.Flags().BoolVar(&rmDry, "dry-run", false, "show what would be removed")

	var showJSON bool
	show := &cobra.Command{
		Use:   "show <repo>",
		Short: "Show a repository's state, pin checkouts and skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withManager(func(m *deploy.Manager) error {
				id, err := m.ResolveRepo(args[0])
				if err != nil {
					return err
				}
				info, err := m.RepoInfo(id)
				if err != nil {
					return err
				}
				if showJSON {
					return printJSON(cmd.OutOrStdout(), info)
				}
				return printRepoInfo(cmd.OutOrStdout(), info)
			})
		},
	}
	show.Flags().BoolVar(&showJSON, "json", false, "print JSON")

	cmd.AddCommand(add, list, remove, show)
	return cmd
}

func newListCmd() *cobra.Command {
	var repo string
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List skills available in the store",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withManager(func(m *deploy.Manager) error {
				repoID := ""
				if repo != "" {
					var err error
					if repoID, err = m.ResolveRepo(repo); err != nil {
						return err
					}
				}
				type row struct {
					ID          string `json:"id"`
					Name        string `json:"name"`
					Repo        string `json:"repo"`
					Path        string `json:"path"`
					Description string `json:"description"`
					Deployments int    `json:"deployments"`
				}
				var rows []row
				for _, id := range m.SkillIDs() {
					sk := m.St.Skills[id]
					if repoID != "" && sk.Repo != repoID {
						continue
					}
					rows = append(rows, row{id, sk.Name, sk.Repo, sk.RelPath, sk.Description, len(m.St.DeploymentsOf(id))})
				}
				if asJSON {
					return printJSON(cmd.OutOrStdout(), rows)
				}
				w := table(cmd.OutOrStdout())
				fmt.Fprintln(w, "NAME\tREPO\tUSED\tDESCRIPTION")
				for _, r := range rows {
					fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", r.Name, r.Repo, r.Deployments, truncate(r.Description, 60))
				}
				for _, r := range rows {
					if !skill.ValidName(r.Name) {
						fmt.Fprintf(w, "warning: name %q is not valid for every agent; enable it with --as <alias>\n", r.Name)
					}
				}
				return w.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "only skills from this repo")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

type scopeFlags struct {
	dir    string
	global bool
	exact  bool
}

func (f *scopeFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.dir, "dir", "d", "", "project directory (default: current directory's git root)")
	cmd.Flags().BoolVarP(&f.global, "global", "g", false, "use the agents' user-level skill directories")
	cmd.Flags().BoolVar(&f.exact, "exact", false, "use --dir as is instead of its git root")
	cmd.MarkFlagsMutuallyExclusive("dir", "global")
}

func newEnableCmd() *cobra.Command {
	var sf scopeFlags
	var agentIDs []string
	var mode, pin, alias string
	var unpin bool
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "enable [<skill>...] [--agent <id,...>]",
		Short: "Link skills into agents' skill directories",
		Long: `Link skills into agents' skill directories.

When several agents are requested, skillet creates as few links as possible:
OpenCode also reads .claude/skills, and pi and OpenCode also read .agents/skills.
The output lists every agent that can see the skill afterwards.

--pin links a fixed revision (sha, tag or branch of the skill's repo) instead
of the repo's current checkout; updates do not touch pinned links. Enabling
again keeps the skill's pin in that scope (links added for more agents get it
too), another --pin re-points all of them and --unpin moves them back to the
repo's current checkout.

--as <alias> names the link <alias> instead of the skill's name, for a name that
some agent rejects or that clashes with another skill. It takes exactly one
skill. Enabling again without --as keeps the alias; another --as renames the
links (shown by --dry-run); --as with the skill's own name drops it. Updates
do not rename an aliased link.

On a terminal, missing skills or agents are chosen interactively, then the
plan is shown and confirmed (--yes skips the confirmation). --no-input or
SKILLET_NO_INPUT=1 turns that off.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := canPrompt(cmd) && (len(args) == 0 || len(agentIDs) == 0)
			if len(args) == 0 && !prompt {
				return errors.New("no skills given; name them, or run on a terminal without --no-input to choose interactively")
			}
			s, err := deploy.ResolveScope(sf.global, sf.dir, sf.exact)
			if err != nil {
				return err
			}
			lm := link.Mode("")
			if mode != "" {
				if lm, err = link.ParseMode(mode); err != nil {
					return err
				}
			}
			if prompt {
				snap, err := takeSnapshot()
				if err != nil {
					return err
				}
				ch := newChooser()
				served := deploy.ServedIn(snap.St, s)
				if len(args) == 0 {
					if args, err = ch.Skills("Skills to enable", skillOptions(snap.St.Skills, served, true)); err != nil {
						return err
					}
					if len(args) == 0 {
						return errors.New("no skills selected")
					}
				}
				if len(agentIDs) == 0 {
					want := defaultAgents(snap.Agents.All(), served, args)
					if agentIDs, err = ch.Agents("Agents to enable them for", agentOptions(snap.Agents.All(), want)); err != nil {
						return err
					}
					if len(agentIDs) == 0 {
						return errors.New("no agents selected")
					}
				}
				return confirmChange(cmd, ch, s, yes, func(m *deploy.Manager, dry bool) ([]deploy.Result, error) {
					return m.Enable(deploy.ChangeOpts{Skills: args, Agents: agentIDs, Scope: s, Mode: lm, Pin: pin, Unpin: unpin, Alias: alias, DryRun: dry || dryRun})
				}, dryRun)
			}
			return withManager(func(m *deploy.Manager) error {
				res, err := m.Enable(deploy.ChangeOpts{Skills: args, Agents: agentIDs, Scope: s, Mode: lm, Pin: pin, Unpin: unpin, Alias: alias, DryRun: dryRun})
				printResults(cmd, m, res, s, dryRun)
				if err == nil && !dryRun {
					err = syncManifest(cmd, m, s)
				}
				return err
			})
		},
	}
	sf.register(cmd)
	cmd.Flags().StringSliceVarP(&agentIDs, "agent", "a", nil, "agents to enable for (claude, codex, opencode, pi, all)")
	cmd.Flags().StringVar(&mode, "mode", "", "link mode: auto, symlink, junction, copy (default from config)")
	cmd.Flags().StringVar(&pin, "pin", "", "pin to a git revision (sha, tag or branch) of the skill's repo")
	cmd.Flags().BoolVar(&unpin, "unpin", false, "move pinned links back to the repo's current checkout")
	cmd.MarkFlagsMutuallyExclusive("pin", "unpin")
	cmd.Flags().StringVar(&alias, "as", "", "link name to use instead of the skill's name (one skill only)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without asking when choosing interactively")
	return cmd
}

func newDisableCmd() *cobra.Command {
	var sf scopeFlags
	var agentIDs []string
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "disable [<skill>...] [--agent <id,...>]",
		Short: "Remove skill links (for all agents unless --agent is given)",
		Long: `Remove skill links (for all agents unless --agent is given).

Without skill arguments, on a terminal, choose among the skills deployed in the
scope, then the plan is shown and confirmed (--yes skips the confirmation).
--no-input or SKILLET_NO_INPUT=1 turns that off.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prompt := len(args) == 0 && canPrompt(cmd)
			if len(args) == 0 && !prompt {
				return errors.New("no skills given; name them, or run on a terminal without --no-input to choose interactively")
			}
			s, err := deploy.ResolveScope(sf.global, sf.dir, sf.exact)
			if err != nil {
				return err
			}
			if prompt {
				snap, err := takeSnapshot()
				if err != nil {
					return err
				}
				ch := newChooser()
				opts := deployedOptions(snap.St.Skills, deploy.ServedIn(snap.St, s))
				if len(opts) == 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "No skills are deployed in %s.\n", display(deploy.ScopeLabel(s), agents.Scope{Global: true}))
					return nil
				}
				if args, err = ch.Skills("Skills to disable", opts); err != nil {
					return err
				}
				if len(args) == 0 {
					return errors.New("no skills selected")
				}
				return confirmChange(cmd, ch, s, yes, func(m *deploy.Manager, dry bool) ([]deploy.Result, error) {
					return m.Disable(deploy.ChangeOpts{Skills: args, Agents: agentIDs, Scope: s, DryRun: dry || dryRun})
				}, dryRun)
			}
			return withManager(func(m *deploy.Manager) error {
				res, err := m.Disable(deploy.ChangeOpts{Skills: args, Agents: agentIDs, Scope: s, DryRun: dryRun})
				printResults(cmd, m, res, s, dryRun)
				if err == nil && !dryRun {
					err = syncManifest(cmd, m, s)
				}
				return err
			})
		},
	}
	sf.register(cmd)
	cmd.Flags().StringSliceVarP(&agentIDs, "agent", "a", nil, "agents to disable for (default: all)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without asking when choosing interactively")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change")
	return cmd
}

func printResults(cmd *cobra.Command, m *deploy.Manager, res []deploy.Result, s agents.Scope, dryRun bool) {
	out := cmd.OutOrStdout()
	if dryRun {
		fmt.Fprintln(out, "Dry run, nothing changed.")
	}
	if len(res) > 0 {
		fmt.Fprintf(out, "Scope: %s\n", display(deploy.ScopeLabel(s), agents.Scope{Global: true}))
	}
	for _, r := range res {
		fmt.Fprintln(out, r.Skill)
		changed := false
		for _, a := range r.Actions {
			switch a.Kind {
			case "keep":
				continue
			case "create":
				fmt.Fprintf(out, "  + %s (%s) for %s%s\n", display(a.Target, s), a.Mode, strings.Join(a.Agents, ", "), pinSuffix(a.Pin))
			case "replace":
				what := "now follows the repo"
				if a.Pin != "" {
					what = "now pinned to " + short(a.Pin)
				}
				fmt.Fprintf(out, "  ~ %s (%s) %s\n", display(a.Target, s), a.Mode, what)
			case "repair":
				fmt.Fprintf(out, "  ~ %s re-created (%s)\n", display(a.Target, s), a.Mode)
			case "remove":
				fmt.Fprintf(out, "  - %s\n", display(a.Target, s))
			}
			changed = true
		}
		if !changed {
			fmt.Fprintln(out, "  no change")
		}
		fmt.Fprintf(out, "  visible to: %s\n", m.FormatVisible(r.Visible))
		if len(r.Unwanted) > 0 {
			fmt.Fprintf(out, "  note: also visible to %s, which read another agent's directory\n", strings.Join(r.Unwanted, ", "))
		}
	}
}

// pinSuffix renders " @ a1b2c3d" for a pinned link, nothing otherwise.
func pinSuffix(pin string) string {
	if pin == "" {
		return ""
	}
	return " @ " + short(pin)
}

func newStatusCmd() *cobra.Command {
	var sf scopeFlags
	var asJSON, long, all bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the skills each agent sees in a project (or globally)",
		Long: `Show the skills each agent sees in a project (or globally).

--all shows every scope that has deployments: the global one and each project
directory skillet has linked into. Project directories that no longer exist are
skipped with a note.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if all {
				return withManager(func(m *deploy.Manager) error { return runStatusAll(cmd, m, long, asJSON) })
			}
			s, err := deploy.ResolveScope(sf.global, sf.dir, sf.exact)
			if err != nil {
				return err
			}
			return withManager(func(m *deploy.Manager) error {
				entries, err := m.Status(s)
				if err != nil {
					return err
				}
				if asJSON {
					return printJSON(cmd.OutOrStdout(), entries)
				}
				return printScopeStatus(cmd.OutOrStdout(), m, s, entries, long)
			})
		},
	}
	sf.register(cmd)
	cmd.Flags().BoolVar(&all, "all", false, "every scope with deployments: global and all known projects")
	cmd.MarkFlagsMutuallyExclusive("all", "dir")
	cmd.MarkFlagsMutuallyExclusive("all", "global")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVarP(&long, "long", "l", false, "one line per agent and link path")
	return cmd
}

func newAgentsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List supported agents, the directories they read and whether they are installed",
		Long: `List supported agents, the directories they read and whether they are installed.

INSTALLED is "yes" when the agent's binary is on PATH or its config directory
exists, "no" when neither is found, and "-" for custom agents, which have
nothing to detect them by.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Works before `skillet init`: only the config's custom agents matter.
			var custom []config.CustomAgent
			cfg, err := config.Load()
			switch {
			case err == nil:
				custom = cfg.Agents
			case !errors.Is(err, config.ErrNotInitialized):
				return err
			}
			reg, err := agents.NewRegistry(custom)
			if err != nil {
				return err
			}
			g := agents.Scope{Global: true}
			type row struct {
				ID           string   `json:"id"`
				Name         string   `json:"name,omitempty"`
				ProjectDir   string   `json:"project_dir"`
				GlobalDir    string   `json:"global_dir"`
				ProjectReads []string `json:"project_reads"`
				GlobalReads  []string `json:"global_reads"`
				Installed    *bool    `json:"installed"`              // null when it cannot be detected
				DetectedVia  string   `json:"detected_via,omitempty"` // binary path or config directory
			}
			var rows []row
			for _, a := range reg.All() {
				r := row{ID: a.ID, Name: a.Name, ProjectDir: a.ProjectDir, GlobalDir: a.GlobalDir, ProjectReads: a.ProjectReads, GlobalReads: a.GlobalReads}
				if in := a.Detect(); in.Known {
					r.Installed, r.DetectedVia = &in.Found, in.Via
				}
				rows = append(rows, r)
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), rows)
			}
			w := table(cmd.OutOrStdout())
			fmt.Fprintln(w, "AGENT\tINSTALLED\tPROJECT DIR\tGLOBAL DIR\tALSO READS")
			for i, a := range reg.All() {
				var also []string
				for _, r := range a.ProjectReads {
					if r != a.ProjectDir {
						also = append(also, r)
					}
				}
				for _, r := range a.GlobalReads {
					if r != a.GlobalDir {
						also = append(also, display(r, g))
					}
				}
				installed := "-"
				if rows[i].Installed != nil {
					installed = "no"
					if *rows[i].Installed {
						installed = "yes"
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.ID, installed, a.ProjectDir, display(a.GlobalDir, g), strings.Join(also, ", "))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// display shortens a path: relative to the project root in project scope,
// with ~ for the home directory otherwise.
func display(p string, s agents.Scope) string {
	if !s.Global {
		if rel, err := filepath.Rel(s.Dir, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}
