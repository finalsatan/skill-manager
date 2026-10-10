package deploy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"skillet/internal/gitx"
	"skillet/internal/link"
	"skillet/internal/skill"
	"skillet/internal/state"
)

// AddRepo clones a repository into the store and registers its skills.
func (m *Manager) AddRepo(ref, branch string) (string, []skill.Found, error) {
	src, err := gitx.ParseSource(ref)
	if err != nil {
		return "", nil, err
	}
	if _, ok := m.St.Repos[src.ID]; ok {
		return "", nil, fmt.Errorf("repo %s is already added", src.ID)
	}
	rel := path.Join("repos", src.ID)
	dst := filepath.Join(m.Cfg.Store, filepath.FromSlash(rel))
	if !link.Within(dst, m.Cfg.Store) {
		return "", nil, fmt.Errorf("repo %s would be stored outside the store", src.ID)
	}
	if _, err := os.Stat(dst); err == nil {
		return "", nil, fmt.Errorf("%s already exists but is not tracked; remove it or add the repo again after deleting it", dst)
	}
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

// RemoveRepo deletes a repo from the store. With force, its deployments are
// removed first; otherwise any deployment blocks the removal.
func (m *Manager) RemoveRepo(id string, force bool) ([]string, error) {
	r, ok := m.St.Repos[id]
	if !ok {
		return nil, fmt.Errorf("unknown repo %s", id)
	}
	var deps []*state.Deployment
	for _, d := range m.St.Deployments {
		if sk, ok := m.St.Skills[d.Skill]; ok && sk.Repo == id {
			deps = append(deps, d)
		}
	}
	var removed []string
	if len(deps) > 0 && !force {
		return nil, fmt.Errorf("%d deployment(s) use skills from %s; disable them first or pass --force", len(deps), id)
	}
	for _, d := range deps {
		if err := m.removeTarget(d, false); err != nil {
			return removed, err
		}
		m.St.RemoveDeployment(d.Target)
		removed = append(removed, d.Target)
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
