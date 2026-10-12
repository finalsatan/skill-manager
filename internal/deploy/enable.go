package deploy

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/link"
	"github.com/finalsatan/skill-manager/internal/skill"
	"github.com/finalsatan/skill-manager/internal/state"
)

// ErrNameTaken is matched (errors.Is) by the refusal to link a skill under a
// name that another skill already uses in the target directory. Linking it
// under another name (`enable --as`) resolves it.
var ErrNameTaken = errors.New("link name is used by another skill")

type nameTakenError struct{ msg string }

func (e nameTakenError) Error() string        { return e.msg }
func (e nameTakenError) Is(target error) bool { return target == ErrNameTaken }

// Action describes one change to the filesystem.
type Action struct {
	Kind   string // "create", "keep", "replace" (same target, new source), "remove"
	Skill  string
	Target string
	Mode   link.Mode
	Agents []string
	Pin    string // commit the target is (or becomes) pinned to
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
	Skills  []string // skill refs
	Agents  []string // agent ids; for Disable empty means all
	Scope   agents.Scope
	Mode    link.Mode // Enable only; empty uses the config default
	Pin     string    // Enable only; a git revision to pin to; empty keeps the current pin
	Unpin   bool      // Enable only; move pinned links back to the main clone
	Alias   string    // Enable only; link name instead of the skill's name; empty keeps the current one
	Unalias bool      // Enable only; go back to the skill's own name (restore)
	DryRun  bool
}

// Enable makes each skill visible to the requested agents in the scope. With
// a pin the links point at a worktree of that revision. Without Pin the
// skill's existing pin in this scope is kept (new links get it too); Unpin
// moves the links back to the main clone.
func (m *Manager) Enable(o ChangeOpts) ([]Result, error) {
	if len(o.Agents) == 0 {
		return nil, errors.New("specify at least one agent with --agent")
	}
	if err := m.checkEnable(o, o.Agents); err != nil {
		return nil, err
	}
	return m.change(o, func(served []string) []string {
		return append(served, o.Agents...)
	}, true)
}

// checkEnable validates the options of an enabling call for the given agents.
func (m *Manager) checkEnable(o ChangeOpts, agentIDs []string) error {
	if _, err := m.Agents.Resolve(agentIDs); err != nil {
		return err
	}
	if o.Pin != "" && o.Unpin {
		return errors.New("--pin and --unpin cannot be combined")
	}
	if o.Alias != "" {
		switch {
		case len(o.Skills) != 1:
			return errors.New("--as renames one link, so it needs exactly one skill")
		case !skill.ValidName(o.Alias):
			return fmt.Errorf("invalid alias %q: use lowercase letters, digits and single hyphens, 1-64 chars", o.Alias)
		case o.Unalias:
			return errors.New("alias and unalias cannot be combined")
		}
	}
	return nil
}

// Set serves each skill to the agents already served in the scope, plus enable,
// minus disable, in one planned operation. Doing both at once differs from
// calling Disable and Enable in turn: the links are planned for the final set
// of agents only, so the dry run shows exactly what applying does, and a
// skill is changed as a unit. o.Agents is ignored. With nothing to enable it
// behaves like Disable (and may name a skill upstream deleted); otherwise like
// Enable, including the Pin and Alias options.
func (m *Manager) Set(o ChangeOpts, enable, disable []string) ([]Result, error) {
	drop := map[string]bool{}
	if len(disable) > 0 {
		as, err := m.Agents.Resolve(disable)
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			drop[a.ID] = true
		}
	}
	if len(enable) > 0 {
		if err := m.checkEnable(o, enable); err != nil {
			return nil, err
		}
		for _, id := range enable {
			if drop[id] {
				return nil, fmt.Errorf("agent %s is to be both enabled and disabled", id)
			}
		}
	} else if len(drop) == 0 {
		return nil, errors.New("nothing to enable or disable")
	}
	return m.change(o, func(served []string) []string {
		var keep []string
		for _, id := range served {
			if !drop[id] {
				keep = append(keep, id)
			}
		}
		return append(keep, enable...)
	}, len(enable) > 0)
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
		// Only disabling may name a skill that upstream deleted but that
		// still has pinned deployments.
		id, err := m.resolveSkill(ref, !reuse)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		p, err := m.planSkill(id, o.Scope, want, reuse, mode, o)
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
	if err := m.St.Save(); err != nil {
		return results, err
	}
	_, err := m.gcPins()
	return results, err
}

type skillPlan struct {
	id      string
	src     string
	name    string
	mode    link.Mode
	pin     string // full sha, or empty to follow the main clone
	alias   string // link name override, empty when the link is named after the skill
	wanted  []string
	actions []Action
}

func (m *Manager) planSkill(id string, s agents.Scope, want func([]string) []string, reuse bool, mode link.Mode, o ChangeOpts) (*skillPlan, error) {
	rev, unpin := o.Pin, o.Unpin
	var src string
	sk := m.St.Skills[id]
	if sk != nil {
		var err error
		if src, err = m.SkillDir(id); err != nil {
			return nil, err
		}
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
	// All links of a skill in a scope share one pin: the existing one unless
	// Enable is asked to re-pin or unpin.
	// The link name is likewise shared: an alias given earlier stays unless
	// Enable names another one, so enabling again without --as keeps it.
	pin, name, alias := "", "", ""
	if len(current) > 0 {
		pin, name, alias = current[0].Pin, filepath.Base(current[0].Target), current[0].Alias
	}
	if sk != nil && alias == "" {
		name = sk.Name
	}
	if reuse && o.Unalias {
		alias = ""
		if sk != nil {
			name = sk.Name
		}
	}
	if reuse && o.Alias != "" {
		alias, name = o.Alias, o.Alias
		if sk != nil && alias == sk.Name {
			alias = "" // the skill's own name needs no override
		}
	}
	switch {
	case reuse && unpin:
		pin = ""
	case reuse && rev != "":
		var err error
		if pin, err = m.resolvePin(id, rev); err != nil {
			return nil, err
		}
	}
	if pin != "" {
		src = m.pinSource(id, pin)
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

	p := &skillPlan{id: id, src: src, name: name, mode: mode, pin: pin, alias: alias}
	for _, a := range wanted {
		p.wanted = append(p.wanted, a.ID)
	}
	keep := map[string]bool{}
	for _, pl := range placements {
		target := filepath.Join(pl.Dir, name)
		keep[target] = true
		if d := m.St.DeploymentAt(target); d != nil {
			if d.Skill != id {
				return nil, nameTakenError{fmt.Sprintf("%s is already used by skill %s", target, d.Skill)}
			}
			kind := "keep"
			if d.Pin != pin {
				if err := m.checkRemovable(d); err != nil {
					return nil, err
				}
				kind = "replace"
			}
			p.actions = append(p.actions, Action{Kind: kind, Skill: id, Target: target, Mode: link.Mode(d.Mode), Agents: pl.Agents, Pin: pin})
			continue
		}
		if err := m.checkFree(target, id, src); err != nil {
			return nil, err
		}
		// A link renamed in place keeps its mode unless --mode says otherwise.
		cm := mode
		if o.Mode == "" {
			for _, d := range current {
				if agents.SamePath(filepath.Dir(d.Target), pl.Dir) {
					cm = link.Mode(d.Mode)
					break
				}
			}
		}
		p.actions = append(p.actions, Action{Kind: "create", Skill: id, Target: target, Mode: cm, Agents: pl.Agents, Pin: pin})
	}
	for _, d := range current {
		if !keep[d.Target] {
			if err := m.checkRemovable(d); err != nil {
				return nil, err
			}
			p.actions = append(p.actions, Action{Kind: "remove", Skill: id, Target: d.Target, Mode: link.Mode(d.Mode), Agents: d.Agents, Pin: d.Pin})
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
		return nameTakenError{fmt.Sprintf("%s holds a skillet copy of a different skill (%s)", target, t.Marker.Skill)}
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
	if p.pin != "" {
		for _, a := range p.actions {
			if a.Kind != "remove" {
				repo, _ := splitSkillID(p.id)
				if err := m.ensurePin(repo, p.pin); err != nil {
					return done, err
				}
				break
			}
		}
	}
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
			d.Agents, d.Alias = a.Agents, p.alias
			// Repair a link that was deleted or broken behind our back.
			if t, err := link.Inspect(a.Target); err == nil && t.Kind == link.Missing {
				used, hash, err := link.Create(p.src, a.Target, link.Mode(d.Mode), p.id)
				if err != nil {
					return done, err
				}
				d.Mode, d.SourceHash = string(used), hash
				a.Kind = "repair"
			}
		case "replace":
			d := m.St.DeploymentAt(a.Target)
			if err := link.Remove(a.Target); err != nil { // checked removable in the plan
				return done, err
			}
			used, hash, err := link.Create(p.src, a.Target, link.Mode(d.Mode), p.id)
			if err != nil {
				return done, fmt.Errorf("link %s: %w", a.Target, err)
			}
			d.Agents, d.Pin, d.Alias, d.Mode, d.SourceHash = a.Agents, p.pin, p.alias, string(used), hash
		case "create":
			if err := link.Remove(a.Target); err != nil { // adoptable leftovers only
				return done, err
			}
			used, hash, err := link.Create(p.src, a.Target, a.Mode, p.id)
			if err != nil {
				return done, fmt.Errorf("link %s: %w", a.Target, err)
			}
			a.Mode = used
			m.St.Deployments = append(m.St.Deployments, &state.Deployment{
				Skill: p.id, Agents: a.Agents, Scope: scope, Dir: dir, Target: a.Target,
				Mode: string(used), Pin: p.pin, Alias: p.alias, SourceHash: hash, CreatedAt: time.Now().UTC(),
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
