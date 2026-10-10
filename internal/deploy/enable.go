package deploy

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"skillet/internal/agents"
	"skillet/internal/link"
	"skillet/internal/skill"
	"skillet/internal/state"
)

// Action describes one change to the filesystem.
type Action struct {
	Kind   string // "create", "keep", "remove"
	Skill  string
	Target string
	Mode   link.Mode
	Agents []string
}

// Result of an enable/disable call for one skill.
type Result struct {
	Skill   string
	Actions []Action
	// Visible maps every agent that will see the skill in this scope to the
	// number of locations it is visible from.
	Visible map[string]int
	// Unwanted lists agents that see the skill without being asked for,
	// because they also read another agent's directory.
	Unwanted []string
}

type ChangeOpts struct {
	Skills []string // skill refs
	Agents []string // agent ids; for Disable empty means all
	Scope  agents.Scope
	Mode   link.Mode // Enable only; empty uses the config default
	DryRun bool
}

// Enable makes each skill visible to the requested agents in the scope.
func (m *Manager) Enable(o ChangeOpts) ([]Result, error) {
	if len(o.Agents) == 0 {
		return nil, errors.New("specify at least one agent with --agent")
	}
	if _, err := m.Agents.Resolve(o.Agents); err != nil {
		return nil, err
	}
	return m.change(o, func(served []string) []string {
		return append(served, o.Agents...)
	}, true)
}

// Disable stops each skill from being served to the given agents (all when
// none are given). Remaining agents are re-planned so that, where possible,
// the disabled agents no longer see the skill.
func (m *Manager) Disable(o ChangeOpts) ([]Result, error) {
	drop := map[string]bool{}
	if len(o.Agents) > 0 {
		as, err := m.Agents.Resolve(o.Agents)
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			drop[a.ID] = true
		}
	}
	return m.change(o, func(served []string) []string {
		var keep []string
		for _, id := range served {
			if len(drop) > 0 && !drop[id] {
				keep = append(keep, id)
			}
		}
		return keep
	}, false)
}

// change computes the wanted agent set per skill and reconciles the links.
// The whole operation is planned first; nothing is touched if any target is
// refused.
func (m *Manager) change(o ChangeOpts, want func(served []string) []string, reuse bool) ([]Result, error) {
	mode := o.Mode
	if mode == "" {
		var err error
		if mode, err = m.LinkMode(); err != nil {
			return nil, err
		}
	}
	var plans []*skillPlan
	seen := map[string]bool{}
	for _, ref := range o.Skills {
		id, err := m.ResolveSkill(ref)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		p, err := m.planSkill(id, o.Scope, want, reuse, mode)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	if err := checkCollisions(plans); err != nil {
		return nil, err
	}

	var results []Result
	for _, p := range plans {
		res := Result{Skill: p.id, Actions: p.actions}
		if !o.DryRun {
			done, err := m.apply(p, o.Scope)
			res.Actions = done
			if err != nil {
				m.St.Save()
				return append(results, res), err
			}
		}
		res.Visible = m.visibility(p, o.Scope, o.DryRun)
		res.Unwanted = unwanted(res.Visible, p.wanted)
		results = append(results, res)
	}
	if o.DryRun {
		return results, nil
	}
	return results, m.St.Save()
}

type skillPlan struct {
	id      string
	src     string
	name    string
	mode    link.Mode
	wanted  []string
	actions []Action
}

func (m *Manager) planSkill(id string, s agents.Scope, want func([]string) []string, reuse bool, mode link.Mode) (*skillPlan, error) {
	sk := m.St.Skills[id]
	src, err := m.SkillDir(id)
	if err != nil {
		return nil, err
	}
	var current []*state.Deployment
	var served, existingDirs []string
	for _, d := range m.St.DeploymentsOf(id) {
		if inScope(d, s) {
			current = append(current, d)
			served = append(served, d.Agents...)
			existingDirs = append(existingDirs, filepath.Dir(d.Target))
		}
	}
	wanted, err := m.Agents.Resolve(want(served))
	if err != nil {
		return nil, err
	}
	if !reuse {
		existingDirs = nil
	}
	placements, err := agents.Plan(wanted, s, existingDirs)
	if err != nil {
		return nil, err
	}

	p := &skillPlan{id: id, src: src, name: sk.Name, mode: mode}
	for _, a := range wanted {
		p.wanted = append(p.wanted, a.ID)
	}
	keep := map[string]bool{}
	for _, pl := range placements {
		target := filepath.Join(pl.Dir, sk.Name)
		keep[target] = true
		if d := m.St.DeploymentAt(target); d != nil {
			if d.Skill != id {
				return nil, fmt.Errorf("%s is already used by skill %s", target, d.Skill)
			}
			p.actions = append(p.actions, Action{Kind: "keep", Skill: id, Target: target, Mode: link.Mode(d.Mode), Agents: pl.Agents})
			continue
		}
		if err := m.checkFree(target, id, src); err != nil {
			return nil, err
		}
		p.actions = append(p.actions, Action{Kind: "create", Skill: id, Target: target, Mode: mode, Agents: pl.Agents})
	}
	for _, d := range current {
		if !keep[d.Target] {
			if err := m.checkRemovable(d); err != nil {
				return nil, err
			}
			p.actions = append(p.actions, Action{Kind: "remove", Skill: id, Target: d.Target, Mode: link.Mode(d.Mode), Agents: d.Agents})
		}
	}
	return p, nil
}

// checkFree verifies that target is empty, or is a leftover skillet link to
// the same source that can be adopted. Anything else is refused.
func (m *Manager) checkFree(target, id, src string) error {
	t, err := link.Inspect(target)
	if err != nil {
		return err
	}
	switch t.Kind {
	case link.Missing:
		return nil
	case link.Link:
		if t.PointsTo(src) || link.Within(t.Dest, m.Cfg.Store) {
			return nil // stale skillet link; replaced on apply
		}
		return fmt.Errorf("%s already exists (link to %s, not managed by skillet); remove it or pick another agent", target, t.Dest)
	case link.ManagedCopy:
		if t.Marker.Skill == id {
			// The leftover is deleted on apply, so it must be unmodified.
			h, err := skill.Hash(target, link.MarkerFile)
			if err != nil {
				return err
			}
			if h != t.Marker.Hash {
				return fmt.Errorf("%s is a leftover skillet copy that was edited by hand; remove it by hand if you no longer need the changes", target)
			}
			return nil
		}
		return fmt.Errorf("%s holds a skillet copy of a different skill (%s)", target, t.Marker.Skill)
	}
	return fmt.Errorf("%s already exists and is not managed by skillet; remove or rename it first", target)
}

func (m *Manager) checkRemovable(d *state.Deployment) error {
	t, err := link.Inspect(d.Target)
	if err != nil {
		return err
	}
	switch t.Kind {
	case link.Missing:
		return nil
	case link.Link:
		if link.Within(t.Dest, m.Cfg.Store) {
			return nil
		}
		return fmt.Errorf("%s is no longer managed by skillet (link to %s, outside the store); remove it by hand", d.Target, t.Dest)
	case link.ManagedCopy:
		h, err := skill.Hash(d.Target, link.MarkerFile)
		if err != nil {
			return err
		}
		if h != t.Marker.Hash {
			return fmt.Errorf("%s was edited after skillet copied it; remove it by hand if you no longer need the changes", d.Target)
		}
		return nil
	}
	return fmt.Errorf("%s is no longer managed by skillet (replaced by a regular directory); remove it by hand", d.Target)
}

// checkCollisions rejects two skills with the same name landing on one target.
func checkCollisions(plans []*skillPlan) error {
	owner := map[string]string{}
	for _, p := range plans {
		for _, a := range p.actions {
			if a.Kind == "remove" {
				continue
			}
			if other, ok := owner[a.Target]; ok && other != p.id {
				return fmt.Errorf("skills %s and %s both want %s; enable them in separate scopes", other, p.id, a.Target)
			}
			owner[a.Target] = p.id
		}
	}
	return nil
}

func (m *Manager) apply(p *skillPlan, s agents.Scope) ([]Action, error) {
	scope, dir := scopeFields(s)
	var done []Action
	for _, a := range p.actions {
		switch a.Kind {
		case "remove":
			d := m.St.DeploymentAt(a.Target)
			if err := m.removeTarget(d, true); err != nil {
				return done, err
			}
			m.St.RemoveDeployment(a.Target)
		case "keep":
			d := m.St.DeploymentAt(a.Target)
			d.Agents = a.Agents
			// Repair a link that was deleted or broken behind our back.
			if t, err := link.Inspect(a.Target); err == nil && t.Kind == link.Missing {
				used, hash, err := link.Create(p.src, a.Target, link.Mode(d.Mode), p.id)
				if err != nil {
					return done, err
				}
				d.Mode, d.SourceHash = string(used), hash
				a.Kind = "repair"
			}
		case "create":
			if err := link.Remove(a.Target); err != nil { // adoptable leftovers only
				return done, err
			}
			used, hash, err := link.Create(p.src, a.Target, p.mode, p.id)
			if err != nil {
				return done, fmt.Errorf("link %s: %w", a.Target, err)
			}
			a.Mode = used
			m.St.Deployments = append(m.St.Deployments, &state.Deployment{
				Skill: p.id, Agents: a.Agents, Scope: scope, Dir: dir, Target: a.Target,
				Mode: string(used), SourceHash: hash, CreatedAt: time.Now().UTC(),
			})
		}
		done = append(done, a)
	}
	return done, nil
}

// removeTarget deletes a deployment's link or copy. checked means the caller
// already verified a copy is unmodified.
func (m *Manager) removeTarget(d *state.Deployment, checked bool) error {
	if !checked {
		if err := m.checkRemovable(d); err != nil {
			return err
		}
	}
	return link.Remove(d.Target)
}

// visibility reports which agents see the skill in this scope. In dry-run
// mode it is computed from the planned actions instead of the state.
func (m *Manager) visibility(p *skillPlan, s agents.Scope, planned bool) map[string]int {
	var dirs []string
	if planned {
		for _, a := range p.actions {
			if a.Kind != "remove" {
				dirs = append(dirs, filepath.Dir(a.Target))
			}
		}
	} else {
		for _, d := range m.St.DeploymentsOf(p.id) {
			if inScope(d, s) {
				dirs = append(dirs, filepath.Dir(d.Target))
			}
		}
	}
	vis := map[string]int{}
	for _, a := range m.Agents.All() {
		for _, d := range dirs {
			if a.Reads(s, d) {
				vis[a.ID]++
			}
		}
	}
	return vis
}

// FormatVisible renders a visibility map in registry order, e.g.
// "claude, opencode (2 locations)".
func (m *Manager) FormatVisible(v map[string]int) string {
	var parts []string
	for _, a := range m.Agents.All() {
		switch n := v[a.ID]; {
		case n == 1:
			parts = append(parts, a.ID)
		case n > 1:
			parts = append(parts, fmt.Sprintf("%s (%d locations)", a.ID, n))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func unwanted(v map[string]int, wanted []string) []string {
	want := map[string]bool{}
	for _, id := range wanted {
		want[id] = true
	}
	var out []string
	for id := range v {
		if !want[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
