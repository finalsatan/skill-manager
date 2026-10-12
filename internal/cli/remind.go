package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/finalsatan/skill-manager/internal/config"
	"github.com/finalsatan/skill-manager/internal/state"
)

const (
	defaultInterval = 6 * time.Hour
	attemptFile     = ".last-auto-check"
)

// quietCommands never print the passive reminder or start a background check.
var quietCommands = map[string]bool{
	"check": true, "update": true, "pending": true, "schedule": true, "hook": true,
	"init": true, "tui": true, "help": true, "completion": true, "__complete": true,
}

// checkInterval reads check_interval, falling back to six hours.
func checkInterval(cfg *config.Config) time.Duration {
	if d, err := time.ParseDuration(cfg.CheckInterval); err == nil && d >= time.Minute {
		return d
	}
	return defaultInterval
}

// remind runs after a successful command. It prints a one-line reminder when
// upstream updates are waiting and, if the last check is older than
// check_interval, starts a detached `skillet check --quiet`. It never blocks
// and never fails the command.
func remind(root *cobra.Command, args []string) {
	if os.Getenv("SKILLET_NO_REMIND") != "" {
		return
	}
	cmd, _, err := root.Find(args)
	if err != nil || cmd == root {
		return
	}
	top := cmd
	for top.Parent() != nil && top.Parent() != root {
		top = top.Parent()
	}
	if quietCommands[top.Name()] {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		return
	}
	st, err := state.Peek(cfg.Store)
	if err != nil || len(st.Repos) == 0 {
		return
	}

	pending := 0
	for _, sk := range st.Skills {
		if sk.Pending {
			pending++
		}
	}
	if pending > 0 && isTerminal(os.Stderr) {
		fmt.Fprintf(os.Stderr, "\n%d skill(s) have upstream updates; run `skillet update` to review.\n", pending)
	}
	if checkDue(cfg, st) {
		startBackgroundCheck(cfg)
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// checkDue reports whether the oldest check (or the last automatic attempt,
// which also covers failing fetches such as being offline) is past the interval.
func checkDue(cfg *config.Config, st *state.State) bool {
	interval := checkInterval(cfg)
	now := time.Now()
	for _, r := range st.Repos {
		if now.Sub(r.LastChecked) < interval {
			continue
		}
		fi, err := os.Stat(filepath.Join(cfg.Store, attemptFile))
		return err != nil || now.Sub(fi.ModTime()) >= interval
	}
	return false
}

func startBackgroundCheck(cfg *config.Config) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	marker := filepath.Join(cfg.Store, attemptFile)
	if err := os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return
	}
	cmd := exec.Command(exe, "check", "--quiet")
	cmd.Env = append(os.Environ(), "SKILLET_NO_REMIND=1")
	detach(cmd)
	if cmd.Start() == nil {
		cmd.Process.Release()
	}
}
