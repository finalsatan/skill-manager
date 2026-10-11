package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"skillet/internal/deploy"
	"skillet/internal/tui"
)

func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive terminal UI (also: run skillet with no command)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !canPrompt(cmd) {
				return errors.New("the TUI needs a terminal and input enabled (see --no-input, SKILLET_NO_INPUT, CI)")
			}
			return runTUI()
		},
	}
}

func runTUI() error {
	// The project scope is the git root of the working directory, as for the
	// other commands.
	s, err := deploy.ResolveScope(false, "", false)
	if err != nil {
		return err
	}
	return tui.Run(tui.Options{Project: s})
}
