package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"skillet/internal/config"
	"skillet/internal/deploy"
	"skillet/internal/schedule"
	"skillet/internal/state"
)

func newScheduleCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "schedule",
		Short: "Run `skillet check --notify` periodically via the system scheduler",
	}
	var every string
	var dry bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Register the periodic check (systemd timer or crontab, launchd, schtasks)",
		RunE: func(cmd *cobra.Command, args []string) error {
			d := defaultInterval
			if cfg, err := config.Load(); err == nil {
				d = checkInterval(cfg)
			}
			if every != "" {
				var err error
				if d, err = time.ParseDuration(every); err != nil {
					return fmt.Errorf("--every: %w", err)
				}
			}
			out := cmd.OutOrStdout()
			what, err := schedule.Install(schedule.Options{
				Every: d, DryRun: dry,
				Out: func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) },
			})
			if err != nil {
				return err
			}
			if dry {
				fmt.Fprintln(out, "(dry run: nothing changed)")
				return nil
			}
			fmt.Fprintf(out, "Installed %s: `skillet check --notify` every %s.\n", what, d)
			return nil
		},
	}
	install.Flags().StringVar(&every, "every", "", "interval, e.g. 6h or 30m (default: check_interval from config)")
	install.Flags().BoolVar(&dry, "dry-run", false, "show what would be written and run, change nothing")

	remove := &cobra.Command{
		Use:   "remove",
		Short: "Unregister the periodic check",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			what, err := schedule.Remove(schedule.Options{
				DryRun: dry,
				Out:    func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) },
			})
			if err != nil {
				return err
			}
			if what == "" {
				fmt.Fprintln(out, "No scheduled check was installed.")
			} else if !dry {
				fmt.Fprintf(out, "Removed %s.\n", what)
			}
			return nil
		},
	}
	remove.Flags().BoolVar(&dry, "dry-run", false, "show what would be done, change nothing")
	root.AddCommand(install, remove)
	return root
}

func newPendingCmd() *cobra.Command {
	var hookJSON bool
	cmd := &cobra.Command{
		Use:   "pending",
		Short: "Show upstream updates found by the last check (no network); silent when none",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Runs from a SessionStart hook: skip instead of waiting on the lock.
			m, err := deploy.OpenTry()
			if errors.Is(err, state.ErrLocked) {
				return nil
			}
			if err != nil {
				return err
			}
			defer m.Close()
			res, err := m.Pending(nil)
			if err != nil {
				return err
			}
			var pend []deploy.RepoCheck
			for _, c := range res {
				if c.Err == nil && !c.UpToDate() {
					pend = append(pend, c)
				}
			}
			if len(pend) == 0 {
				return nil
			}
			msg := pendingSummary(pend)
			out := cmd.OutOrStdout()
			if hookJSON {
				return json.NewEncoder(out).Encode(map[string]any{
					"systemMessage": "skillet: skill updates available\n" + msg,
					"hookSpecificOutput": map[string]string{
						"hookEventName":     "SessionStart",
						"additionalContext": "Upstream updates are available for installed skills (not applied):\n" + msg,
					},
				})
			}
			fmt.Fprintln(out, msg)
			return nil
		},
	}
	cmd.Flags().BoolVar(&hookJSON, "hook-json", false, "emit Claude Code SessionStart hook JSON")
	return cmd
}

func newHookCmd() *cobra.Command {
	root := &cobra.Command{Use: "hook", Short: "Print agent hook configuration"}
	root.AddCommand(&cobra.Command{
		Use:   "claude",
		Short: "Print a Claude Code SessionStart hook that reports pending skill updates",
		Long: `Print a Claude Code SessionStart hook that reports pending skill updates.

Merge the output into ~/.claude/settings.json (or a project's
.claude/settings.json). The hook runs offline: it only reads the result of the
last check, so it never slows a session start down.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				exe = "skillet"
			}
			cfg := map[string]any{"hooks": map[string]any{"SessionStart": []any{map[string]any{
				"matcher": "startup|resume",
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": exe,
					"args":    []string{"pending", "--hook-json"},
					"timeout": 10,
				}},
			}}}}
			return printJSON(cmd.OutOrStdout(), cfg)
		},
	})
	return root
}
