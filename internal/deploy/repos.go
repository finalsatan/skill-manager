package deploy

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/finalsatan/skill-manager/internal/gitx"
	"github.com/finalsatan/skill-manager/internal/link"
	"github.com/finalsatan/skill-manager/internal/skill"
	"github.com/finalsatan/skill-manager/internal/state"
)

// AddPlan is what AddRepo would do for a reference.
type AddPlan struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Dir    string `json:"dir"`    // where it would be cloned
	Branch string `json:"branch"` // empty: the remote's default branch
	rel    string
}

// PlanAddRepo parses ref and checks the conditions AddRepo refuses on,
// without touching the network or the disk.
func (m *Manager) PlanAddRepo(ref, branch string) (AddPlan, error) {
	src, err := gitx.ParseSource(ref)
	if err != nil {
		return AddPlan{}, err
	}
	if _, ok := m.St.Repos[src.ID]; ok {
		return AddPlan{}, fmt.Errorf("repo %s is already added", src.ID)
	}
	rel := path.Join("repos", src.ID)
	dst := filepath.Join(m.Cfg.Store, filepath.FromSlash(rel))
	p := AddPlan{ID: src.ID, URL: src.URL, Dir: dst, Branch: branch, rel: rel}
	if !link.Within(dst, m.Cfg.Store) {
		return p, fmt.Errorf("repo %s would be stored outside the store", src.ID)
	}
	if _, err := os.Stat(dst); err == nil {
		return p, fmt.Errorf("%s already exists but is not tracked; remove it or add the repo again after deleting it", dst)
	}
	return p, nil
}

// AddRepo clones a repository into the store and registers its skills.
func (m *Manager) AddRepo(ref, branch string) (string, []skill.Found, error) {
	plan, err := m.PlanAddRepo(ref, branch)
	if err != nil {
		return "", nil, err
	}
	src := gitx.Source{URL: plan.URL, ID: plan.ID}
	rel, dst := plan.rel, plan.Dir
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", nil, err
	}
	if err := gitx.Clone(src.URL, dst, branch); err != nil {
		os.RemoveAll(dst)
		return "", nil, err
	}
	if branch == "" {
		if branch, err = gitx.CurrentBranch(dst); err != nil {
			os.RemoveAll(dst)
			return "", nil, fmt.Errorf("detect default branch: %w", err)
		}
	}
	head, err := gitx.Head(dst)
	if err != nil {
		os.RemoveAll(dst)
		return "", nil, err
	}
	found, err := skill.Scan(dst)
	if err != nil {
		os.RemoveAll(dst)
		return "", nil, err
	}
	m.St.Repos[src.ID] = &state.Repo{URL: src.URL, Branch: branch, Path: rel, Head: head}
	m.registerSkills(src.ID, found)
	return src.ID, found, m.St.Save()
}

// registerSkills replaces the skill entries of a repo with a fresh scan.
func (m *Manager) registerSkills(repoID string, found []skill.Found) {
	for id, sk := range m.St.Skills {
		if sk.Repo == repoID {
			delete(m.St.Skills, id)
		}
	}
	for _, f := range found {
		m.St.Skills[state.SkillID(repoID, f.RelPath)] = &state.Skill{
			Repo: repoID, RelPath: f.RelPath, Name: f.Name, Description: f.Description,
		}
	}
}

// RemovePlan is what RemoveRepo would delete.
type RemovePlan struct {
	Repo  string
	Links []string // deployment targets (links or copies)
	Pins  []string // pin worktrees
}

// PlanRemoveRepo lists what RemoveRepo(id, force) would remove. A non-nil
// error is the reason it would refuse (the plan is still filled in).
func (m *Manager) PlanRemoveRepo(id string, force bool) (RemovePlan, error) {
	p := RemovePlan{Repo: id}
	if _, ok := m.St.Repos[id]; !ok {
		return p, fmt.Errorf("unknown repo %s", id)
	}
	var deps []*state.Deployment
	for _, d := range m.St.Deployments {
		if repo, _ := splitSkillID(d.Skill); repo == id {
			deps = append(deps, d)
			p.Links = append(p.Links, d.Target)
		}
	}
	for _, c := range m.pinCheckouts() {
		if c.Repo == id {
			p.Pins = append(p.Pins, c.Path)
		}
	}
	sort.Strings(p.Links)
	sort.Strings(p.Pins)
	if len(deps) > 0 && !force {
		return p, fmt.Errorf("%d deployment(s) use skills from %s; disable them first or pass --force", len(deps), id)
	}
	var errs []error
	for _, d := range deps {
		if err := m.checkRemovable(d); err != nil {
			errs = append(errs, err)
		}
	}
	return p, errors.Join(errs...)
}

// RemoveRepo deletes a repo from the store. With force, its deployments are
// removed first; otherwise any deployment blocks the removal.
func (m *Manager) RemoveRepo(id string, force bool) ([]string, error) {
	r, ok := m.St.Repos[id]
	if !ok {
		return nil, fmt.Errorf("unknown repo %s", id)
	}
	var deps []*state.Deployment
	for _, d := range m.St.Deployments {
		if repo, _ := splitSkillID(d.Skill); repo == id {
			deps = append(deps, d)
		}
	}
	var removed []string
	if len(deps) > 0 && !force {
		return nil, fmt.Errorf("%d deployment(s) use skills from %s; disable them first or pass --force", len(deps), id)
	}
	for _, d := range deps {
		if err := m.removeTarget(d, false); err != nil {
			// Forget the links already deleted; the repo stays registered.
			if len(removed) > 0 {
				err = errors.Join(err, m.St.Save())
			}
			return removed, err
		}
		m.St.RemoveDeployment(d.Target)
		removed = append(removed, d.Target)
	}
	if err := m.removeRepoPins(id); err != nil {
		return removed, errors.Join(err, m.St.Save())
	}
	for sid, sk := range m.St.Skills {
		if sk.Repo == id {
			delete(m.St.Skills, sid)
		}
	}
	delete(m.St.Repos, id)
	if err := m.St.Save(); err != nil {
		return removed, err
	}
	return removed, os.RemoveAll(m.RepoDir(r))
}

// RepoIDs returns repo ids sorted.
func (m *Manager) RepoIDs() []string {
	ids := make([]string, 0, len(m.St.Repos))
	for id := range m.St.Repos {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SkillIDs returns skill ids sorted by name, then id.
func (m *Manager) SkillIDs() []string {
	ids := make([]string, 0, len(m.St.Skills))
	for id := range m.St.Skills {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.St.Skills[ids[i]], m.St.Skills[ids[j]]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return ids[i] < ids[j]
	})
	return ids
}
