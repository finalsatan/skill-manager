// Package cli defines skillet's command-line interface.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/finalsatan/skill-manager/internal/config"
	"github.com/finalsatan/skill-manager/internal/deploy"
)

// Version is set at build time with -ldflags "-X github.com/finalsatan/skill-manager/internal/cli.Version=...".
// Without it, the module version recorded by `go install ...@vX.Y.Z` is used.
var Version = "dev"

func version() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "skillet",
		Short:         "Manage agent skills from git repos across Claude Code, Codex, OpenCode and pi",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// Bare `skillet` opens the TUI on a terminal and prints help otherwise.
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !canPrompt(cmd) {
				return cmd.Help()
			}
			if _, err := config.Load(); errors.Is(err, config.ErrNotInitialized) {
				if err := cmd.Help(); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "\nskillet is not initialized yet; run `skillet init` to get started.")
				return nil
			}
			return startTUI()
		},
	}
	root.PersistentFlags().Bool("no-input", false, "never prompt; fail instead (also SKILLET_NO_INPUT=1 or CI)")
	root.AddCommand(
		newInitCmd(),
		newRepoCmd(),
		newListCmd(),
		newEnableCmd(),
		newDisableCmd(),
		newStatusCmd(),
		newSaveCmd(),
		newRestoreCmd(),
		newCheckCmd(),
		newUpdateCmd(),
		newDoctorCmd(),
		newAgentsCmd(),
		newPendingCmd(),
		newScheduleCmd(),
		newHookCmd(),
		newTUICmd(),
	)
	return root
}

func Execute() int {
	root := NewRoot()
	if err := root.Execute(); err != nil {
		if errors.Is(err, errAborted) {
			fmt.Fprintln(os.Stderr, "aborted")
			return 130
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	remind(root, os.Args[1:])
	return 0
}

// withManager opens the store for the duration of fn.
func withManager(fn func(m *deploy.Manager) error) error {
	m, err := deploy.Open()
	if err != nil {
		return err
	}
	defer m.Close()
	return fn(m)
}

func table(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
