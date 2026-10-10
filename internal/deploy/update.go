package deploy

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"skillet/internal/gitx"
	"skillet/internal/link"
	"skillet/internal/skill"
	"skillet/internal/state"
)

// SkillChange is one skill touched by upstream commits.
type SkillChange struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Files    []string `json:"files"`            // changed files, relative to the skill directory
	Code     bool     `json:"code"`             // a non-Markdown file changed (scripts deserve a review)
	Deployed int      `json:"deployed"`         // number of unpinned deployments using it
	Pinned   int      `json:"pinned,omitempty"` // deployments pinned to a commit, which an update does not touch
	Removed  bool     `json:"removed"`          // the skill no longer exists upstream
}

// RepoCheck is the result of checking one repo against its upstream.
type RepoCheck struct {
	Repo      string        `json:"repo"`
	Head      string        `json:"head"`
	Upstream  string        `json:"upstream"`
	Commits   int           `json:"commits"`
	Changes   []SkillChange `json:"changes,omitempty"`
	NewSkills []string      `json:"new_skills,omitempty"` // rel paths of skills added upstream
	Err       error         `json:"-"`
}

func (c RepoCheck) UpToDate() bool { return c.Err == nil && c.Head == c.Upstream }

// MarshalJSON adds the error as a string.
func (c RepoCheck) MarshalJSON() ([]byte, error) {
	type plain RepoCheck
	v := struct {
		plain
		Error string `json:"error,omitempty"`
	}{plain: plain(c)}
	if c.Err != nil {
		v.Error = c.Err.Error()
	}
	return json.Marshal(v)
}

// Check fetches each repo (all when ids is empty) and records which skills
// changed upstream. Fetches run in parallel.
func (m *Manager) Check(ids []string) ([]RepoCheck, error) {
	repos, err := m.resolveRepos(ids)
	if err != nil {
		return nil, err
	}
	results := make([]RepoCheck, len(repos))
	upstream := make([]string, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, id := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := m.St.Repos[id]
			upstream[i], results[i].Err = gitx.Fetch(m.RepoDir(r), r.Branch)
		}()
	}
	wg.Wait()

	now := time.Now().UTC()
	for i, id := range repos {
		results[i].Repo = id
		if results[i].Err != nil {
			continue
		}
		r := m.St.Repos[id]
		r.Upstream, r.LastChecked = upstream[i], now
		results[i] = m.diffRepo(id)
	}
	return results, m.St.Save()
}

// diffRepo compares a repo's HEAD with its last fetched upstream and updates
// the skills' pending flags. It does not touch the network.
func (m *Manager) diffRepo(id string) RepoCheck {
	r := m.St.Repos[id]
	res := RepoCheck{Repo: id, Head: r.Head, Upstream: r.Upstream}
	for _, sk := range m.St.Skills {
		if sk.Repo == id {
			sk.Pending = false
		}
	}
	if r.Upstream == "" || r.Upstream == r.Head {
		res.Upstream = r.Head
		return res
	}
	dir := m.RepoDir(r)
	if !gitx.IsAncestor(dir, r.Head, r.Upstream) {
		res.Err = fmt.Errorf("local branch has diverged from origin/%s; resolve it in %s", r.Branch, dir)
		return res
	}
	var err error
	if res.Commits, err = gitx.CountCommits(dir, r.Head, r.Upstream); err != nil {
		res.Err = err
		return res
	}
	files, err := gitx.ChangedFiles(dir, r.Head, r.Upstream)
	if err != nil {
		res.Err = err
		return res
	}

	byID := map[string]*SkillChange{}
	known := map[string]bool{}
	for sid, sk := range m.St.Skills {
		if sk.Repo != id {
			continue
		}
		known[sk.RelPath] = true
		for _, f := range files {
			rel, ok := under(f, sk.RelPath)
			if !ok {
				continue
			}
			c := byID[sid]
			if c == nil {
				c = &SkillChange{ID: sid, Name: sk.Name}
				for _, d := range m.St.DeploymentsOf(sid) {
					if d.Pin != "" {
						c.Pinned++
					} else {
						c.Deployed++
					}
				}
				c.Removed = !gitx.Exists(dir, r.Upstream, path.Join(sk.RelPath, skill.FileName))
				byID[sid] = c
			}
			c.Files = append(c.Files, rel)
			if !strings.EqualFold(path.Ext(f), ".md") {
				c.Code = true
			}
		}
	}
	for sid, c := range byID {
		m.St.Skills[sid].Pending = true
		res.Changes = append(res.Changes, *c)
	}
	sort.Slice(res.Changes, func(i, j int) bool { return res.Changes[i].Name < res.Changes[j].Name })

	for _, f := range files {
		if path.Base(f) != skill.FileName {
			continue
		}
		d := path.Dir(f)
		if !known[d] && gitx.Exists(dir, r.Upstream, f) {
			res.NewSkills = append(res.NewSkills, d)
		}
	}
	sort.Strings(res.NewSkills)
	return res
}

// under returns f relative to dir if f lies inside it ("." matches all).
func under(f, dir string) (string, bool) {
	if dir == "." {
		return f, true
	}
	if strings.HasPrefix(f, dir+"/") {
		return f[len(dir)+1:], true
	}
	return "", false
}

// Pending re-evaluates pending changes from the last fetch, without network.
func (m *Manager) Pending(ids []string) ([]RepoCheck, error) {
	repos, err := m.resolveRepos(ids)
	if err != nil {
		return nil, err
	}
	var out []RepoCheck
	for _, id := range repos {
		out = append(out, m.diffRepo(id))
	}
	return out, nil
}

func (m *Manager) resolveRepos(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return m.RepoIDs(), nil
	}
	var out []string
	for _, ref := range ids {
		id, err := m.ResolveRepo(ref)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// RepoDiff returns the commit log and the diff (or diffstat) of a pending
// update, limited to the given skills when any are named.
func (m *Manager) RepoDiff(c RepoCheck, full bool, skillIDs ...string) (log, diff string, err error) {
	r := m.St.Repos[c.Repo]
	dir := m.RepoDir(r)
	var paths []string
	for _, id := range skillIDs {
		if sk, ok := m.St.Skills[id]; ok && sk.RelPath != "." {
			paths = append(paths, sk.RelPath)
		}
	}
	if log, err = gitx.Log(dir, c.Head, c.Upstream, paths...); err != nil {
		return "", "", err
	}
	diff, err = gitx.Diff(dir, c.Head, c.Upstream, !full, paths...)
	return log, diff, err
}

// RepoServes lists the agents (sorted) that see an unpinned deployment of a
// skill from the repo, i.e. the ones an update of the repo changes.
func (m *Manager) RepoServes(repoID string) []string {
	set := map[string]bool{}
	for _, d := range m.St.Deployments {
		if repo, _ := splitSkillID(d.Skill); repo == repoID && d.Pin == "" {
			for _, a := range d.Agents {
				set[a] = true
			}
		}
	}
	var out []string
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// UpdateResult describes what Update did for one repo.
type UpdateResult struct {
	Repo     string
	From, To string
	Notes    []string // per-deployment outcomes worth showing
}

// Update fast-forwards a repo to the upstream recorded by the last Check and
// brings every deployment of its skills up to date: links follow
// automatically, copies are re-copied unless edited by hand, moved or renamed
// skills are re-linked and links to deleted skills are removed.
func (m *Manager) Update(c RepoCheck) (UpdateResult, error) {
	res := UpdateResult{Repo: c.Repo, From: c.Head, To: c.Upstream}
	if c.Err != nil {
		return res, c.Err
	}
	r := m.St.Repos[c.Repo]
	if r == nil || r.Head != c.Head || r.Upstream != c.Upstream {
		return res, fmt.Errorf("%s changed since it was checked; run the update again", c.Repo)
	}
	if c.Head == c.Upstream {
		return res, nil
	}
	dir := m.RepoDir(r)
	if clean, err := gitx.IsClean(dir); err != nil {
		return res, err
	} else if !clean {
		return res, fmt.Errorf("%s has local modifications; commit or discard them first", dir)
	}

	if err := gitx.FastForward(dir, c.Upstream); err != nil {
		return res, err
	}
	head, err := gitx.Head(dir)
	if err != nil {
		return res, err
	}
	r.Head = head
	found, err := skill.Scan(dir)
	if err != nil {
		return res, err
	}
	oldNames := map[string]string{}
	for id, sk := range m.St.Skills {
		oldNames[id] = sk.Name
	}
	m.registerSkills(c.Repo, found)
	res.Notes = append(res.Notes, m.adoptOrphans(c.Repo, oldNames)...)
	res.Notes = append(res.Notes, m.resync(c.Repo)...)
	res.Notes = append(res.Notes, m.pinNote(c.Repo)...)
	return res, m.St.Save()
}

// adoptOrphans handles deployments whose skill vanished from the repo after
// an update. If the repo now has exactly one skill with the same name (the
// directory was moved), the deployment follows it; otherwise the link is
// removed because it would dangle. oldNames holds the skills' names before
// the rescan: an aliased link no longer carries its skill's name.
func (m *Manager) adoptOrphans(repoID string, oldNames map[string]string) []string {
	var notes []string
	prefix := repoID + ":"
	for _, d := range append([]*state.Deployment(nil), m.St.Deployments...) {
		if !strings.HasPrefix(d.Skill, prefix) || d.Pin != "" {
			continue // pinned deployments keep their own checkout
		}
		if _, ok := m.St.Skills[d.Skill]; ok {
			continue
		}
		name := filepath.Base(d.Target)
		if d.Alias != "" && oldNames[d.Skill] != "" {
			name = oldNames[d.Skill]
		}
		var match []string
		for id, sk := range m.St.Skills {
			if sk.Repo == repoID && sk.Name == name {
				match = append(match, id)
			}
		}
		if len(match) == 1 {
			old := d.Skill
			src, _ := m.SkillDir(match[0])
			if err := m.removeTarget(d, false); err != nil {
				notes = append(notes, fmt.Sprintf("%s moved to %s but %s could not be updated: %v", old, match[0], d.Target, err))
				continue
			}
			used, hash, err := link.Create(src, d.Target, link.Mode(d.Mode), match[0])
			if err != nil {
				m.St.RemoveDeployment(d.Target)
				notes = append(notes, fmt.Sprintf("failed to re-link %s: %v", d.Target, err))
				continue
			}
			d.Skill, d.Mode, d.SourceHash = match[0], string(used), hash
			notes = append(notes, fmt.Sprintf("%s moved to %s; re-linked %s", old, match[0], d.Target))
			continue
		}
		if err := m.removeTarget(d, false); err != nil {
			notes = append(notes, fmt.Sprintf("%s was deleted upstream; left %s in place (%v) and stopped managing it", d.Skill, d.Target, err))
		} else {
			notes = append(notes, fmt.Sprintf("%s was deleted upstream; removed %s", d.Skill, d.Target))
		}
		m.St.RemoveDeployment(d.Target)
	}
	return notes
}

// resync brings deployments of a repo's skills in line with the files on
// disk: re-copies stale copies and re-links skills whose name changed.
func (m *Manager) resync(repoID string) []string {
	var notes []string
	for _, d := range append([]*state.Deployment(nil), m.St.Deployments...) {
		sk, ok := m.St.Skills[d.Skill]
		if !ok || sk.Repo != repoID || d.Pin != "" {
			continue
		}
		src, _ := m.SkillDir(d.Skill)
		// An aliased link keeps its name when the skill is renamed.
		if d.Alias == "" && filepath.Base(d.Target) != sk.Name {
			notes = append(notes, m.relink(d, src, sk.Name))
			continue
		}
		if d.Mode != string(link.Copy) {
			continue
		}
		switch changed, err := m.recopy(d, src); {
		case err != nil:
			notes = append(notes, fmt.Sprintf("skipped %s: %v", d.Target, err))
		case changed:
			notes = append(notes, "re-copied "+d.Target)
		}
	}
	return notes
}

// recopy refreshes a copy deployment when its source changed. A copy edited
// by hand is left alone and reported as an error.
func (m *Manager) recopy(d *state.Deployment, src string) (bool, error) {
	hash, err := skill.Hash(src)
	if err != nil {
		return false, err
	}
	if hash == d.SourceHash {
		return false, nil
	}
	if err := m.checkRemovable(d); err != nil {
		return false, err
	}
	if err := link.Remove(d.Target); err != nil {
		return false, err
	}
	_, h, err := link.Create(src, d.Target, link.Copy, d.Skill)
	if err != nil {
		return false, err
	}
	d.SourceHash = h
	return true, nil
}

// relink moves a deployment to a new link name after the skill was renamed.
func (m *Manager) relink(d *state.Deployment, src, name string) string {
	newTarget := filepath.Join(filepath.Dir(d.Target), name)
	if other := m.St.DeploymentAt(newTarget); other != nil {
		return fmt.Sprintf("skill renamed to %q but %s is used by %s; %s left unchanged", name, newTarget, other.Skill, d.Target)
	}
	if err := m.checkFree(newTarget, d.Skill, src); err != nil {
		return fmt.Sprintf("skill renamed to %q: %v", name, err)
	}
	if err := m.removeTarget(d, false); err != nil {
		return fmt.Sprintf("skill renamed to %q: %v", name, err)
	}
	if err := link.Remove(newTarget); err != nil {
		return fmt.Sprintf("skill renamed to %q: %v", name, err)
	}
	used, hash, err := link.Create(src, newTarget, link.Mode(d.Mode), d.Skill)
	old := d.Target
	if err != nil {
		m.St.RemoveDeployment(old)
		return fmt.Sprintf("skill renamed to %q; failed to link %s: %v", name, newTarget, err)
	}
	d.Target, d.Mode, d.SourceHash = newTarget, string(used), hash
	return fmt.Sprintf("renamed %s -> %s", old, newTarget)
}

// PendingCount is the number of skills with an unapplied upstream change.
func (m *Manager) PendingCount() int {
	n := 0
	for _, sk := range m.St.Skills {
		if sk.Pending {
			n++
		}
	}
	return n
}

// Unnotified returns the checks with upstream changes that no desktop
// notification has been sent for yet, so a 6-hourly timer does not repeat
// itself until upstream moves again.
func (m *Manager) Unnotified(res []RepoCheck) []RepoCheck {
	var out []RepoCheck
	for _, c := range res {
		r := m.St.Repos[c.Repo]
		if c.Err == nil && !c.UpToDate() && r != nil && r.Notified != c.Upstream {
			out = append(out, c)
		}
	}
	return out
}

// MarkNotified records that the given checks were announced.
func (m *Manager) MarkNotified(res []RepoCheck) error {
	for _, c := range res {
		if r := m.St.Repos[c.Repo]; r != nil {
			r.Notified = c.Upstream
		}
	}
	return m.St.Save()
}
