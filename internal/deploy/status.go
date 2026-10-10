package deploy

import (
	"os"
	"path/filepath"
	"sort"

	"skillet/internal/agents"
	"skillet/internal/link"
	"skillet/internal/skill"
	"skillet/internal/state"
)

// Entry is one skill an agent can see in a scope.
type Entry struct {
	Agent  string `json:"agent"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Skill  string `json:"skill,omitempty"` // empty when not managed by skillet
	Mode   string `json:"mode,omitempty"`
	Pin    string `json:"pin,omitempty"`   // commit the link is pinned to
	Alias  string `json:"alias,omitempty"` // set when the link was renamed with `enable --as`
	Health string `json:"health"`          // ok, missing, broken, wrong-target, modified, outdated, unmanaged
	// Pending is set when upstream has changes for this skill not yet applied.
	Pending bool `json:"pending,omitempty"`
}

// Status lists, per agent, every skill visible in the scope, managed or not.
func (m *Manager) Status(s agents.Scope) ([]Entry, error) {
	var out []Entry
	health := map[string]string{}
	for _, a := range m.Agents.All() {
		for _, dir := range a.ReadDirs(s) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				p := filepath.Join(dir, e.Name())
				d := m.St.DeploymentAt(p)
				if d == nil {
					if isSkillDir(p) {
						out = append(out, Entry{Agent: a.ID, Name: e.Name(), Path: p, Health: "unmanaged"})
					}
					continue
				}
				h, ok := health[p]
				if !ok {
					h = m.Health(d)
					health[p] = h
				}
				out = append(out, Entry{Agent: a.ID, Name: e.Name(), Path: p, Skill: d.Skill, Mode: d.Mode, Pin: d.Pin, Alias: d.Alias, Health: h, Pending: d.Pin == "" && m.pending(d.Skill)})
			}
		}
	}
	// Deployments whose link has disappeared do not show up in directory
	// listings; report them for the agents they were meant to serve.
	for _, d := range m.St.Deployments {
		if !inScope(d, s) {
			continue
		}
		if _, err := os.Lstat(d.Target); err == nil {
			continue
		}
		for _, id := range d.Agents {
			out = append(out, Entry{Agent: id, Name: filepath.Base(d.Target), Path: d.Target, Skill: d.Skill, Mode: d.Mode, Pin: d.Pin, Alias: d.Alias, Health: "missing"})
		}
	}
	order := map[string]int{}
	for i, a := range m.Agents.All() {
		order[a.ID] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Agent != out[j].Agent {
			return order[out[i].Agent] < order[out[j].Agent]
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Health checks one deployment against the filesystem.
func (m *Manager) Health(d *state.Deployment) string {
	src, err := m.DeploymentSource(d)
	if err != nil {
		return "broken"
	}
	t, err := link.Inspect(d.Target)
	if err != nil {
		return "broken"
	}
	switch t.Kind {
	case link.Missing:
		return "missing"
	case link.Link:
		if !t.PointsTo(src) {
			return "wrong-target"
		}
		if _, err := os.Stat(filepath.Join(d.Target, skill.FileName)); err != nil {
			return "broken"
		}
		return "ok"
	case link.ManagedCopy:
		if h, err := skill.Hash(d.Target, link.MarkerFile); err == nil && h != t.Marker.Hash {
			return "modified"
		}
		if h, err := skill.Hash(src); err == nil && h != t.Marker.Hash {
			return "outdated"
		}
		return "ok"
	}
	return "broken"
}

func (m *Manager) pending(id string) bool {
	sk, ok := m.St.Skills[id]
	return ok && sk.Pending
}

func isSkillDir(p string) bool {
	_, err := os.Stat(filepath.Join(p, skill.FileName))
	return err == nil
}

// ScopeSet is the scopes that have deployments, plus project directories
// that were deployed to but no longer exist.
type ScopeSet struct {
	Scopes  []agents.Scope // global first, then projects sorted by directory
	Missing []string
}

// DeployedScopes lists every scope with at least one deployment.
func (m *Manager) DeployedScopes() ScopeSet {
	var set ScopeSet
	global := false
	dirs := map[string]bool{}
	for _, d := range m.St.Deployments {
		if d.Scope == state.ScopeGlobal {
			global = true
		} else if d.Scope == state.ScopeProject {
			dirs[d.Dir] = true
		}
	}
	if global {
		set.Scopes = append(set.Scopes, agents.Scope{Global: true})
	}
	var sorted []string
	for dir := range dirs {
		sorted = append(sorted, dir)
	}
	sort.Strings(sorted)
	for _, dir := range sorted {
		if dirExists(dir) {
			set.Scopes = append(set.Scopes, agents.Scope{Dir: dir})
		} else {
			set.Missing = append(set.Missing, dir)
		}
	}
	return set
}

// ServedIn maps each skill deployed in the scope to the agents its links serve
// there (sorted, without duplicates).
func (m *Manager) ServedIn(s agents.Scope) map[string][]string {
	return ServedIn(m.St.State, s)
}

// ServedIn is Manager.ServedIn on a state snapshot (see state.Peek).
func ServedIn(st *state.State, s agents.Scope) map[string][]string {
	set := map[string]map[string]bool{}
	for _, d := range st.Deployments {
		if !inScope(d, s) {
			continue
		}
		if set[d.Skill] == nil {
			set[d.Skill] = map[string]bool{}
		}
		for _, a := range d.Agents {
			set[d.Skill][a] = true
		}
	}
	out := make(map[string][]string, len(set))
	for id, as := range set {
		for a := range as {
			out[id] = append(out[id], a)
		}
		sort.Strings(out[id])
	}
	return out
}
