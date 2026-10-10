package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"skillet/internal/agents"
	"skillet/internal/config"
	"skillet/internal/deploy"
	"skillet/internal/state"
)

// option is one row of a multi-select.
type option struct {
	Value    string
	Label    string
	Selected bool
}

// chooser is how commands ask the user questions. The huh implementation is
// used on a terminal; tests inject a fake.
type chooser interface {
	Skills(title string, opts []option) ([]string, error)
	Agents(title string, opts []option) ([]string, error)
	Confirm(question string) (bool, error)
}

// errAborted means the user cancelled (Ctrl-C, Esc); nothing was changed.
var errAborted = errors.New("aborted")

// Seams for tests.
var (
	newChooser        = func() chooser { return huhChooser{} }
	stdinStdoutAreTTY = func() bool { return isTTY(os.Stdin) && isTTY(os.Stdout) }
	lookupEnv         = os.Getenv
)

func isTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// canPrompt reports whether commands may ask questions: both stdin and stdout
// are terminals and neither --no-input, SKILLET_NO_INPUT nor CI is set.
func canPrompt(cmd *cobra.Command) bool {
	if noInput, _ := cmd.Flags().GetBool("no-input"); noInput {
		return false
	}
	if envTrue(lookupEnv("SKILLET_NO_INPUT")) || lookupEnv("CI") != "" {
		return false
	}
	return stdinStdoutAreTTY()
}

func envTrue(v string) bool {
	switch strings.ToLower(v) {
	case "", "0", "false", "no":
		return false
	}
	return true
}

type huhChooser struct{}

func (huhChooser) Skills(title string, opts []option) ([]string, error) {
	return multiSelect(title, opts)
}

func (huhChooser) Agents(title string, opts []option) ([]string, error) {
	return multiSelect(title, opts)
}

func (huhChooser) Confirm(q string) (bool, error) {
	var yes bool
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(q).Affirmative("Yes").Negative("No").Value(&yes),
	)).Run()
	return yes, wrapAbort(err)
}

func multiSelect(title string, opts []option) ([]string, error) {
	hs := make([]huh.Option[string], len(opts))
	for i, o := range opts {
		hs[i] = huh.NewOption(o.Label, o.Value).Selected(o.Selected)
	}
	var sel []string
	err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title(title).
			Description("space toggles, / filters, enter confirms").
			Options(hs...).
			Height(14).
			Filterable(true).
			Value(&sel),
	)).Run()
	return sel, wrapAbort(err)
}

func wrapAbort(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return errAborted
	}
	return err
}

// skillOptions lists every skill in the store. served maps skill id to the
// agents already served in the scope; those skills are pre-selected when
// preselectServed is set.
func skillOptions(skills map[string]*state.Skill, served map[string][]string, preselectServed bool) []option {
	ids := make([]string, 0, len(skills))
	for id := range skills {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := skills[ids[i]], skills[ids[j]]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return ids[i] < ids[j]
	})
	var opts []option
	for _, id := range ids {
		sk := skills[id]
		as, on := served[id]
		label := fmt.Sprintf("%s  %s", sk.Name, sk.Repo)
		if sk.Description != "" {
			label += " — " + truncate(sk.Description, 60)
		}
		if on {
			label += "  [enabled: " + strings.Join(as, ",") + "]"
		}
		opts = append(opts, option{Value: id, Label: label, Selected: on && preselectServed})
	}
	return opts
}

// deployedOptions lists the skills deployed in a scope with the agents they
// are served to, for `disable`.
func deployedOptions(skills map[string]*state.Skill, served map[string][]string) []option {
	var opts []option
	for _, o := range skillOptions(skills, served, false) {
		if _, ok := served[o.Value]; ok {
			opts = append(opts, o)
		}
	}
	// Skills deleted upstream but still deployed (pinned) are not in the store map.
	var gone []string
	for id := range served {
		if _, ok := skills[id]; !ok {
			gone = append(gone, id)
		}
	}
	sort.Strings(gone)
	for _, id := range gone {
		opts = append(opts, option{Value: id, Label: fmt.Sprintf("%s  [enabled: %s]", id, strings.Join(served[id], ","))})
	}
	return opts
}

// agentOptions lists the registry's agents. An agent is pre-selected when it
// is in want. Detected installs are marked in the label.
func agentOptions(all []agents.Agent, want map[string]bool) []option {
	var opts []option
	for _, a := range all {
		label := a.ID
		if a.Name != "" && a.Name != a.ID {
			label += "  " + a.Name
		}
		if in := a.Detect(); in.Known && in.Found {
			label += "  (installed)"
		}
		opts = append(opts, option{Value: a.ID, Label: label, Selected: want[a.ID]})
	}
	return opts
}

// defaultAgents decides which agents start selected: those already serving
// every chosen skill in the scope when all of them are enabled there, else the
// detected-installed ones.
func defaultAgents(all []agents.Agent, served map[string][]string, chosen []string) map[string]bool {
	want := map[string]bool{}
	allServed := len(chosen) > 0
	for _, id := range chosen {
		if len(served[id]) == 0 {
			allServed = false
			break
		}
		for _, a := range served[id] {
			want[a] = true
		}
	}
	if allServed {
		return want
	}
	want = map[string]bool{}
	for _, a := range all {
		if in := a.Detect(); in.Known && in.Found {
			want[a.ID] = true
		}
	}
	return want
}

// snapshot is a lock-free view of the store for building prompts: the state
// as last saved and the agents registry. Nothing is held while the user thinks.
type snapshot struct {
	St     *state.State
	Agents *agents.Registry
}

func takeSnapshot() (*snapshot, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	st, err := state.Peek(cfg.Store)
	if err != nil {
		return nil, err
	}
	reg, err := agents.NewRegistry(cfg.Agents)
	if err != nil {
		return nil, err
	}
	return &snapshot{St: st, Agents: reg}, nil
}

// confirmChange shows the dry-run plan and applies the change after a "yes".
// run performs the same operation with DryRun on and off. The state lock is
// held while planning and again while applying, never while the user answers;
// applying plans again, so it refuses safely if things changed in between.
func confirmChange(cmd *cobra.Command, ch chooser, s agents.Scope, yes bool,
	run func(m *deploy.Manager, dry bool) ([]deploy.Result, error), dryRun bool) error {
	out := cmd.OutOrStdout()
	err := withManager(func(m *deploy.Manager) error {
		res, err := run(m, true)
		printResults(cmd, m, res, s, true)
		return err
	})
	if err != nil || dryRun {
		return err
	}
	if !yes {
		ok, err := ch.Confirm("Apply?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "Aborted, nothing changed.")
			return nil
		}
	}
	return withManager(func(m *deploy.Manager) error {
		res, err := run(m, false)
		printResults(cmd, m, res, s, false)
		if err == nil {
			err = syncManifest(cmd, m, s)
		}
		return err
	})
}
