package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/deploy"
	"github.com/finalsatan/skill-manager/internal/notify"
	"github.com/finalsatan/skill-manager/internal/state"
)

func newCheckCmd() *cobra.Command {
	var asJSON, quiet, notifyFlag bool
	cmd := &cobra.Command{
		Use:   "check [repo]...",
		Short: "Fetch repos and report which skills changed upstream",
		Long: `Fetch repos and report which skills changed upstream.

--quiet prints nothing and --notify shows a desktop notification once per new
upstream commit. Both are meant for timers: they skip the run instead of
waiting when another skillet command holds the state lock.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			background := quiet || notifyFlag
			open := deploy.Open
			if background {
				open = deploy.OpenTry
			}
			m, err := open()
			if errors.Is(err, state.ErrLocked) {
				return nil
			}
			if err != nil {
				return err
			}
			defer m.Close()
			res, err := m.Check(args)
			if err != nil {
				return err
			}
			if notifyFlag {
				if err := notifyPending(m, res); err != nil && !quiet {
					fmt.Fprintln(cmd.ErrOrStderr(), "notify:", err)
				}
			}
			if quiet {
				return nil
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), res)
			}
			out := cmd.OutOrStdout()
			pending := false
			for _, c := range res {
				printCheck(out, c)
				pending = pending || (c.Err == nil && !c.UpToDate())
			}
			if pending {
				fmt.Fprintln(out, "\nRun `skillet update` to review and apply.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing; never wait for the state lock")
	cmd.Flags().BoolVar(&notifyFlag, "notify", false, "show a desktop notification for new upstream changes")
	return cmd
}

// notifyPending sends one notification covering repos not announced before.
func notifyPending(m *deploy.Manager, res []deploy.RepoCheck) error {
	fresh := m.Unnotified(res)
	if len(fresh) == 0 {
		return nil
	}
	if err := notify.Send("skillet: skill updates available", pendingSummary(fresh)); err != nil {
		return err
	}
	return m.MarkNotified(fresh)
}

// pendingSummary lists, per repo, the changed skills that are in use first.
func pendingSummary(res []deploy.RepoCheck) string {
	var lines []string
	for _, c := range res {
		var names []string
		for _, ch := range c.Changes {
			names = append(names, ch.Name)
		}
		for _, p := range c.NewSkills {
			names = append(names, "+"+path.Base(p))
		}
		line := strings.TrimPrefix(c.Repo, "github.com/") + ": "
		if len(names) == 0 {
			line += fmt.Sprintf("%d new commit(s)", c.Commits)
		} else {
			line += strings.Join(names, ", ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\nRun `skillet update` to review."
}

// printCheck prints a one-repo summary of pending upstream changes.
func printCheck(out io.Writer, c deploy.RepoCheck) {
	name := strings.TrimPrefix(c.Repo, "github.com/")
	switch {
	case c.Err != nil:
		fmt.Fprintf(out, "%s  error: %v\n", name, c.Err)
		return
	case c.UpToDate():
		fmt.Fprintf(out, "%s  up to date\n", name)
		return
	}
	fmt.Fprintf(out, "%s  %d new commit(s)  %s -> %s\n", name, c.Commits, short(c.Head), short(c.Upstream))
	w := table(out)
	for _, ch := range c.Changes {
		used := "unused"
		switch {
		case ch.Deployed > 0 && ch.Pinned > 0:
			used = fmt.Sprintf("used %dx, %d pinned", ch.Deployed, ch.Pinned)
		case ch.Deployed > 0:
			used = fmt.Sprintf("used %dx", ch.Deployed)
		case ch.Pinned > 0:
			used = fmt.Sprintf("%d pinned", ch.Pinned)
		}
		var flags []string
		if ch.Removed {
			flags = append(flags, "deleted upstream")
		}
		if ch.Code {
			flags = append(flags, "code changed")
		}
		note := ""
		if len(flags) > 0 {
			note = "[" + strings.Join(flags, ", ") + "]"
		}
		fmt.Fprintf(w, "  %s\t%s\t%d file(s)\t%s\n", ch.Name, used, len(ch.Files), note)
	}
	for _, p := range c.NewSkills {
		fmt.Fprintf(w, "  + %s\tnew skill\t\t\n", p)
	}
	w.Flush()
	if len(c.Changes) == 0 && len(c.NewSkills) == 0 {
		fmt.Fprintln(out, "  (no skill directories changed)")
	}
}

// pendingRepo is a repo with upstream changes, with its log and diff read
// while the state lock was held so the prompts can run without it.
type pendingRepo struct {
	check     deploy.RepoCheck
	log, diff string
}

func newUpdateCmd() *cobra.Command {
	var yes, full, noFetch, dryRun bool
	cmd := &cobra.Command{
		Use:   "update [repo]...",
		Short: "Review upstream changes and fast-forward repos",
		Long: `Review upstream changes and fast-forward repos.

For each repo with new commits, skillet prints the commit log and a diffstat
(--diff for the full diff) and asks before applying. Symlinked skills update
in place; copies are re-copied unless edited by hand; skills that moved are
re-linked and links to deleted skills are removed.

The state lock is held only while fetching and while applying one repo, not
while waiting for your answer. --dry-run fetches and shows what would be
applied, without asking or changing anything.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var res []deploy.RepoCheck
			var items []pendingRepo
			err := withManager(func(m *deploy.Manager) error {
				var err error
				if noFetch {
					res, err = m.Pending(args)
				} else {
					res, err = m.Check(args)
				}
				if err != nil {
					return err
				}
				for _, c := range res {
					if c.UpToDate() {
						continue
					}
					it := pendingRepo{check: c}
					if c.Err == nil {
						if it.log, it.diff, err = m.RepoDiff(c, full); err != nil {
							return err
						}
					}
					items = append(items, it)
				}
				return nil
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			in := bufio.NewReader(cmd.InOrStdin())
			var ch chooser
			if !yes && !dryRun && canPrompt(cmd) {
				ch = newChooser()
			}
			updated, failed := 0, 0
			restart := false
			for _, it := range items {
				c := it.check
				printCheck(out, c)
				if c.Err != nil {
					failed++
					continue
				}
				fmt.Fprintf(out, "\n%s\n\n%s\n\n", indent(it.log), indent(it.diff))
				if dryRun {
					continue
				}
				if !yes {
					q := fmt.Sprintf("Apply update to %s?", c.Repo)
					var ok bool
					if ch != nil {
						if ok, err = ch.Confirm(q); err != nil {
							return err
						}
					} else {
						ok = confirm(out, in, q)
					}
					if !ok {
						fmt.Fprintln(out, "  skipped")
						continue
					}
				}
				r, serves, err := applyUpdate(c)
				if err != nil {
					fmt.Fprintf(out, "  error: %v\n", err)
					failed++
					continue
				}
				updated++
				fmt.Fprintf(out, "Updated %s %s -> %s\n", c.Repo, short(r.From), short(r.To))
				for _, n := range r.Notes {
					fmt.Fprintf(out, "  %s\n", n)
				}
				for _, a := range serves {
					restart = restart || a != "claude"
				}
			}
			if restart {
				fmt.Fprintln(out, "Claude Code reloads skills automatically; restart Codex / OpenCode / pi sessions to pick up changes.")
			}
			switch {
			case failed > 0:
				return fmt.Errorf("%d repo(s) could not be updated", failed)
			case dryRun && len(items) > 0:
				fmt.Fprintln(out, "Dry run, nothing applied.")
			case updated == 0 && !anyPending(res):
				fmt.Fprintln(out, "Everything is up to date.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without asking")
	cmd.Flags().BoolVar(&full, "diff", false, "show the full diff instead of a diffstat")
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "use the result of the last check instead of fetching")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be applied; apply nothing and ask nothing")
	return cmd
}

// applyUpdate takes the state lock just long enough to update one repo. It
// returns the agents that see the repo's unpinned deployments afterwards.
// Manager.Update refuses if the repo moved since the check.
func applyUpdate(c deploy.RepoCheck) (deploy.UpdateResult, []string, error) {
	m, err := deploy.Open()
	if err != nil {
		return deploy.UpdateResult{}, nil, err
	}
	defer m.Close()
	r, err := m.Update(c)
	if err != nil {
		return r, nil, err
	}
	return r, m.RepoServes(c.Repo), nil
}

func anyPending(res []deploy.RepoCheck) bool {
	for _, c := range res {
		if !c.UpToDate() {
			return true
		}
	}
	return false
}

func confirm(out io.Writer, in *bufio.Reader, q string) bool {
	fmt.Fprintf(out, "%s [y/N] ", q)
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func indent(s string) string {
	if s == "" {
		return "  (empty)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

func newDoctorCmd() *cobra.Command {
	var fix, asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Find broken links, deleted projects, unused pin checkouts and name clashes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withManager(func(m *deploy.Manager) error {
				issues, err := m.Doctor(fix)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				open, fixable := countIssues(issues)
				switch {
				case asJSON:
					if issues == nil {
						issues = []deploy.Issue{}
					}
					if err := printJSON(out, issues); err != nil {
						return err
					}
				case len(issues) == 0:
					fmt.Fprintln(out, "No problems found.")
				default:
					printIssues(out, issues)
					if !fix && fixable > 0 {
						fmt.Fprintf(out, "\nRun `skillet doctor --fix` to repair %d of them.\n", fixable)
					}
				}
				if open > 0 {
					return fmt.Errorf("%d problem(s) need attention", open)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "repair links, re-copy stale copies and forget deleted projects")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

// countIssues returns how many issues are still open (information excluded)
// and how many of those `doctor --fix` can repair.
func countIssues(issues []deploy.Issue) (open, fixable int) {
	for _, is := range issues {
		if is.Fixed || is.Kind == "pending" {
			continue
		}
		open++
		if is.Fixable() {
			fixable++
		}
	}
	return open, fixable
}

func printIssues(out io.Writer, issues []deploy.Issue) {
	for _, is := range issues {
		mark := "!"
		switch {
		case is.Fixed:
			mark = "fixed"
		case is.Err != nil:
			mark = "failed"
		case is.Kind == "pending":
			mark = "i"
		}
		target := ""
		if is.Target != "" {
			target = display(is.Target, agents.Scope{Global: true}) + ": "
		}
		fmt.Fprintf(out, "[%s] %s  %s%s\n", mark, is.Kind, target, is.Detail)
		for _, p := range is.Paths {
			fmt.Fprintf(out, "      %s\n", display(p, agents.Scope{Global: true}))
		}
		if is.Err != nil {
			fmt.Fprintf(out, "      %v\n", is.Err)
		}
	}
}
