package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"skillet/internal/agents"
	"skillet/internal/link"
	"skillet/internal/state"
)

// Clash is one agent seeing several different skill directories that share
// a name. Links resolving to the same directory are not a clash.
type Clash struct {
	Agent string
	Name  string
	Paths []string
}

// Clashes finds name clashes in Status entries.
func Clashes(entries []Entry) []Clash {
	type key struct{ agent, name string }
	real := map[key]map[string]string{} // resolved path -> shown path
	for _, e := range entries {
		if e.Health == "missing" {
			continue
		}
		k := key{e.Agent, e.Name}
		if real[k] == nil {
			real[k] = map[string]string{}
		}
		r := e.Path
		if p, err := filepath.EvalSymlinks(e.Path); err == nil {
			r = p
		}
		if e.Path == "" {
			r = e.Skill // entries built without paths (tests)
		}
		real[k][r] = e.Path
	}
	var out []Clash
	for k, paths := range real {
		if len(paths) < 2 {
			continue
		}
		c := Clash{Agent: k.agent, Name: k.name}
		for _, p := range paths {
			c.Paths = append(c.Paths, p)
		}
		sort.Strings(c.Paths)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Agent < out[j].Agent
	})
	return out
}

// Issue is one problem found by Doctor.
type Issue struct {
	Kind   string // repo-missing, orphan, project-gone, missing, broken, wrong-target, outdated, modified, clash, pending
	Target string
	Detail string
	Paths  []string // extra paths involved (clashes)
	Fixed  bool
	Err    error // set when a fix was attempted and failed
}

// Doctor checks repos, deployments and name clashes. With fix it repairs
// what can be repaired safely.
func (m *Manager) Doctor(fix bool) ([]Issue, error) {
	var issues []Issue
	for _, id := range m.RepoIDs() {
		dir := m.RepoDir(m.St.Repos[id])
		if !dirExists(filepath.Join(dir, ".git")) {
			issues = append(issues, Issue{Kind: "repo-missing", Target: dir,
				Detail: fmt.Sprintf("clone of %s is gone; run `skillet repo remove --force %s` and add it again", id, id)})
		}
	}

	scopes := []agents.Scope{{Global: true}}
	seenDir := map[string]bool{}
	for _, d := range append([]*state.Deployment(nil), m.St.Deployments...) {
		if d.Scope == state.ScopeProject && !seenDir[d.Dir] && dirExists(d.Dir) {
			seenDir[d.Dir] = true
			scopes = append(scopes, agents.Scope{Dir: d.Dir})
		}
		issues = append(issues, m.checkDeployment(d, fix)...)
	}

	for _, s := range scopes {
		entries, err := m.Status(s)
		if err != nil {
			return issues, err
		}
		for _, c := range Clashes(entries) {
			issues = append(issues, Issue{Kind: "clash", Target: ScopeLabel(s), Paths: c.Paths,
				Detail: fmt.Sprintf("%s sees %d different skills named %q", c.Agent, len(c.Paths), c.Name)})
		}
	}

	if n := m.PendingCount(); n > 0 {
		issues = append(issues, Issue{Kind: "pending", Detail: fmt.Sprintf("%d skill(s) have upstream changes; run `skillet update`", n)})
	}
	if fix {
		return issues, m.St.Save()
	}
	return issues, nil
}

func (m *Manager) checkDeployment(d *state.Deployment, fix bool) []Issue {
	if _, ok := m.St.Skills[d.Skill]; !ok {
		is := Issue{Kind: "orphan", Target: d.Target, Detail: fmt.Sprintf("skill %s no longer exists in the store", d.Skill)}
		if fix {
			if is.Err = m.removeTarget(d, false); is.Err == nil {
				m.St.RemoveDeployment(d.Target)
				is.Fixed = true
			}
		}
		return []Issue{is}
	}
	if d.Scope == state.ScopeProject && !dirExists(d.Dir) {
		is := Issue{Kind: "project-gone", Target: d.Dir, Detail: "project directory no longer exists; forgetting " + d.Target}
		if fix {
			m.St.RemoveDeployment(d.Target)
			is.Fixed = true
		}
		return []Issue{is}
	}

	h := m.Health(d)
	if h == "ok" {
		return nil
	}
	is := Issue{Kind: h, Target: d.Target}
	switch h {
	case "missing":
		is.Detail = "link was deleted"
	case "broken":
		is.Detail = "link no longer resolves to a skill"
	case "wrong-target":
		is.Detail = "link points somewhere else"
	case "outdated":
		is.Detail = "copy is older than the repo"
	case "modified":
		is.Detail = "copy was edited by hand; not touched"
		return []Issue{is}
	}
	if !fix {
		return []Issue{is}
	}
	t, err := link.Inspect(d.Target)
	if err != nil {
		is.Err = err
		return []Issue{is}
	}
	if t.Kind == link.Link && !link.Within(t.Dest, m.Cfg.Store) {
		is.Err = fmt.Errorf("link now points outside the store (%s); fix it by hand", t.Dest)
		return []Issue{is}
	}
	src, _ := m.SkillDir(d.Skill)
	if h == "outdated" {
		_, is.Err = m.recopy(d, src)
		is.Fixed = is.Err == nil
		return []Issue{is}
	}
	if is.Err = link.Remove(d.Target); is.Err != nil {
		return []Issue{is}
	}
	used, hash, err := link.Create(src, d.Target, link.Mode(d.Mode), d.Skill)
	if err != nil {
		is.Err = err
		return []Issue{is}
	}
	d.Mode, d.SourceHash = string(used), hash
	is.Fixed = true
	return []Issue{is}
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
