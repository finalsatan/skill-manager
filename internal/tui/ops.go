package tui

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"skillet/internal/agents"
	"skillet/internal/deploy"
)

// step is the dry-run of one Disable or Enable call.
type step struct {
	Agents   []string       // the agents asked for
	Actions  []planAction   // without the "keep" ones
	Visible  map[string]int // agents that see the skill afterwards -> locations
	Unwanted []string       // visible only because they read another agent's directory
}

// skillPlan is the preview of one skill's change.
type skillPlan struct {
	Change change
	Step   *step // nil when planning failed
	Err    error
}

// planAction is a filesystem action with the directory the link points at.
type planAction struct {
	deploy.Action
	Source string // empty for removals and pinned links
}

func toStep(m *deploy.Manager, res []deploy.Result, agentIDs []string) *step {
	st := &step{Agents: agentIDs}
	for _, r := range res {
		for _, a := range r.Actions {
			if a.Kind == "keep" {
				continue
			}
			pa := planAction{Action: a}
			if a.Kind != "remove" && a.Pin == "" {
				pa.Source, _ = m.SkillDir(a.Skill)
			}
			st.Actions = append(st.Actions, pa)
		}
		st.Visible, st.Unwanted = r.Visible, r.Unwanted
	}
	return st
}

// opts are the options of the Set call for one skill.
func (c change) opts(s agents.Scope, dry bool) deploy.ChangeOpts {
	return deploy.ChangeOpts{Skills: []string{c.ID}, Scope: s, DryRun: dry, Pin: c.Pin, Unpin: c.Unpin, Alias: c.Alias, Unalias: c.Unalias}
}

// planChanges dry-runs each skill's change with one Set call, the same call
// applyChanges makes, so the preview is what applying does. Nothing on disk
// changes. A skill that cannot be planned carries its error and the others are
// still planned.
func planChanges(m *deploy.Manager, s agents.Scope, cs []change) []skillPlan {
	plans := make([]skillPlan, 0, len(cs))
	for _, c := range cs {
		p := skillPlan{Change: c}
		res, err := m.Set(c.opts(s, true), c.Enable, c.Disable)
		if err != nil {
			p.Err = err
		} else {
			p.Step = toStep(m, res, append(append([]string(nil), c.Enable...), c.Disable...))
		}
		plans = append(plans, p)
	}
	return plans
}

// applied is one skill that was changed for real.
type applied struct {
	Name            string
	Enable, Disable []string
	Pin, Alias      string
	Unpin, Unalias  bool
}

// applyChanges makes the changes, one Set call per skill, and stops at the
// first error. It returns what was done before that.
func applyChanges(m *deploy.Manager, s agents.Scope, cs []change) ([]applied, error) {
	var done []applied
	for _, c := range cs {
		if _, err := m.Set(c.opts(s, false), c.Enable, c.Disable); err != nil {
			return done, fmt.Errorf("%s: %w", c.Name, err)
		}
		done = append(done, applied{Name: c.Name, Enable: c.Enable, Disable: c.Disable, Pin: c.Pin, Alias: c.Alias, Unpin: c.Unpin, Unalias: c.Unalias})
	}
	return done, nil
}

// previewMsg is the result of previewCmd.
type previewMsg struct {
	kind     scopeKind
	plans    []skillPlan
	manifest bool // skillet.toml exists in the project and will be updated
	err      error
	changes  []change // set when the changes were worked out by the command
}

// previewCmd opens the store, dry-runs the changes and closes it again.
func previewCmd(kind scopeKind, s agents.Scope, cs []change) tea.Cmd {
	return func() tea.Msg {
		msg := previewMsg{kind: kind}
		msg.err = withManager(func(m *deploy.Manager) error {
			msg.plans = planChanges(m, s, cs)
			if !s.Global {
				_, err := os.Stat(deploy.ManifestPath(s))
				msg.manifest = err == nil
			}
			return nil
		})
		return msg
	}
}

// applyMsg is the result of applyCmd.
type applyMsg struct {
	kind scopeKind
	done []applied
	err  error
}

// applyCmd opens the store again (the preview's lock is long gone), makes the
// changes for real and, like the CLI, refreshes skillet.toml when it exists.
// Enable and Disable plan again, so changes made since the preview are
// handled by them.
func applyCmd(kind scopeKind, s agents.Scope, cs []change) tea.Cmd {
	return func() tea.Msg {
		msg := applyMsg{kind: kind}
		msg.err = withManager(func(m *deploy.Manager) error {
			var err error
			msg.done, err = applyChanges(m, s, cs)
			// Links changed even when a later skill failed, so keep the manifest in step.
			if len(msg.done) > 0 && !s.Global {
				if _, serr := m.SyncManifest(s); serr != nil && err == nil {
					err = fmt.Errorf("update skillet.toml: %w", serr)
				}
			}
			return err
		})
		return msg
	}
}

// optionChangeCmd previews a pin or alias change: it works out the agents the
// skill is served to in the scope, as `enable --pin/--as` would be given them,
// and dry-runs re-enabling those with the option set.
func optionChangeCmd(kind scopeKind, s agents.Scope, c change) tea.Cmd {
	return func() tea.Msg {
		msg := previewMsg{kind: kind}
		msg.err = withManager(func(m *deploy.Manager) error {
			c.Enable = m.ServedIn(s)[c.ID]
			if len(c.Enable) == 0 {
				return fmt.Errorf("%s is not enabled in this scope", c.Name)
			}
			msg.changes = []change{c}
			msg.plans = planChanges(m, s, msg.changes)
			if !s.Global {
				_, err := os.Stat(deploy.ManifestPath(s))
				msg.manifest = err == nil
			}
			return nil
		})
		return msg
	}
}
