package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"skillet/internal/agents"
	"skillet/internal/deploy"
	"skillet/internal/manifest"
)

// projectFlags are --dir/--exact for commands that only make sense for a project.
type projectFlags struct {
	dir    string
	exact  bool
	global bool // only to give a clear error
}

func (f *projectFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.dir, "dir", "d", "", "project directory (default: current directory's git root)")
	cmd.Flags().BoolVar(&f.exact, "exact", false, "use --dir as is instead of its git root")
	cmd.Flags().BoolVarP(&f.global, "global", "g", false, "not supported")
	cmd.Flags().MarkHidden("global")
}

func (f *projectFlags) scope() (agents.Scope, error) {
	if f.global {
		return agents.Scope{}, errors.New("skillet.toml is per project; global scope is not supported")
	}
	return deploy.ResolveScope(false, f.dir, f.exact)
}

// syncManifest refreshes an existing skillet.toml after enable/disable.
func syncManifest(cmd *cobra.Command, m *deploy.Manager, s agents.Scope) error {
	ok, err := m.SyncManifest(s)
	if err != nil {
		return fmt.Errorf("update %s: %w", manifest.FileName, err)
	}
	if ok {
		fmt.Fprintf(cmd.OutOrStdout(), "Updated %s\n", manifest.FileName)
	}
	return nil
}

func newSaveCmd() *cobra.Command {
	var pf projectFlags
	cmd := &cobra.Command{
		Use:   "save",
		Short: "Write skillet.toml from the project's current skills",
		Long: `Write skillet.toml in the project root from the skills enabled there, so a
teammate can recreate them with ` + "`skillet restore`" + `. It records repos, skills,
the requested agents and pins, not link modes or paths. Commit the file.
Once it exists, enable and disable keep it up to date.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := pf.scope()
			if err != nil {
				return err
			}
			return withManager(func(m *deploy.Manager) error {
				mf, err := m.SaveManifest(s)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Wrote %s (%d repo(s), %d skill(s))\n", deploy.ManifestPath(s), len(mf.Repos), len(mf.Skills))
				for _, sk := range mf.Skills {
					fmt.Fprintf(out, "  %s  %s%s\n", sk.ID, strings.Join(sk.Agents, ", "), pinSuffix(sk.Pin))
				}
				for _, r := range mf.Repos {
					if manifest.IsLocal(r.ID) {
						fmt.Fprintf(out, "warning: %s is a local path (%s); teammates cannot clone it\n", r.ID, r.URL)
					}
				}
				return nil
			})
		},
	}
	pf.register(cmd)
	return cmd
}

func newRestoreCmd() *cobra.Command {
	var pf projectFlags
	var dryRun, prune bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Recreate the skills listed in skillet.toml",
		Long: `Recreate the project's skills from skillet.toml: clone missing repos, enable
each skill for exactly the listed agents (dropping other agents), and pin or
unpin it as listed. Skills enabled here but not in the manifest are kept and
listed; --prune removes them. A skill that cannot be restored is reported and
the rest carry on, with a non-zero exit at the end.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := pf.scope()
			if err != nil {
				return err
			}
			mf, err := manifest.Load(deploy.ManifestPath(s))
			if err != nil {
				return err
			}
			return withManager(func(m *deploy.Manager) error {
				res, err := m.Restore(deploy.RestoreOpts{Manifest: mf, Scope: s, DryRun: dryRun, Prune: prune})
				if err != nil {
					return err
				}
				printRestore(cmd, m, res, s, dryRun)
				if n := len(res.Problems); n > 0 {
					return fmt.Errorf("%d problem(s) while restoring", n)
				}
				return nil
			})
		},
	}
	pf.register(cmd)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change")
	cmd.Flags().BoolVar(&prune, "prune", false, "also disable skills enabled here but not in skillet.toml")
	return cmd
}

func printRestore(cmd *cobra.Command, m *deploy.Manager, res *deploy.RestoreResult, s agents.Scope, dryRun bool) {
	out := cmd.OutOrStdout()
	for _, r := range res.Repos {
		switch r.Status {
		case "cloned":
			fmt.Fprintf(out, "Cloned %s\n", r.ID)
		case "would clone":
			fmt.Fprintf(out, "Would clone %s\n", r.URL)
		case "failed":
			fmt.Fprintf(out, "Could not clone %s\n", r.ID)
		}
		if r.Warning != "" {
			fmt.Fprintf(out, "warning: %s: %s\n", r.ID, r.Warning)
		}
	}
	printResults(cmd, m, res.Results, s, dryRun)
	for _, n := range res.Skipped {
		fmt.Fprintf(out, "skipped: %s\n", n)
	}
	if len(res.Kept) > 0 {
		fmt.Fprintln(out, "Not in skillet.toml (kept; --prune to remove):")
		for _, id := range res.Kept {
			fmt.Fprintf(out, "  %s\n", id)
		}
	}
	for _, p := range res.Problems {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", p)
	}
}
