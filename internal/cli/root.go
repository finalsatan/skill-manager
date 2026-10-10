// Package cli defines skillet's command-line interface.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"skillet/internal/deploy"
)

// Version is set at build time with -ldflags "-X skillet/internal/cli.Version=...".
var Version = "dev"

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "skillet",
		Short:         "Manage agent skills from git repos across Claude Code, Codex, OpenCode and pi",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
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
	)
	return root
}

func Execute() int {
	root := NewRoot()
	if err := root.Execute(); err != nil {
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
