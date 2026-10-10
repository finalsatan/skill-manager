// Package deploy implements skillet's operations on top of config, state,
// agents, git and links.
package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"skillet/internal/agents"
	"skillet/internal/config"
	"skillet/internal/gitx"
	"skillet/internal/link"
	"skillet/internal/state"
)

type Manager struct {
	Cfg    *config.Config
	Agents *agents.Registry
	St     *state.Handle
}

// Open loads config and agents and locks the state file. Call Close when done.
func Open() (*Manager, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return OpenWith(cfg)
}

func OpenWith(cfg *config.Config) (*Manager, error) {
	reg, err := agents.NewRegistry(cfg.Agents)
	if err != nil {
		return nil, err
	}
	st, err := state.Open(cfg.Store)
	if err != nil {
		return nil, err
	}
	return &Manager{Cfg: cfg, Agents: reg, St: st}, nil
}

func (m *Manager) Close() error { return m.St.Close() }

func (m *Manager) LinkMode() (link.Mode, error) { return link.ParseMode(m.Cfg.LinkMode) }

func (m *Manager) RepoDir(r *state.Repo) string {
	return filepath.Join(m.Cfg.Store, filepath.FromSlash(r.Path))
}

// SkillDir is the on-disk source directory of a skill.
func (m *Manager) SkillDir(id string) (string, error) {
	sk, ok := m.St.Skills[id]
	if !ok {
		return "", fmt.Errorf("unknown skill %s", id)
	}
	r, ok := m.St.Repos[sk.Repo]
	if !ok {
		return "", fmt.Errorf("skill %s belongs to missing repo %s", id, sk.Repo)
	}
	return filepath.Join(m.RepoDir(r), filepath.FromSlash(sk.RelPath)), nil
}

// ResolveSkill accepts a bare name, <repo-suffix>/<name>, or a full skill id.
func (m *Manager) ResolveSkill(ref string) (string, error) {
	if _, ok := m.St.Skills[ref]; ok {
		return ref, nil
	}
	name, repoSuffix := ref, ""
	if i := strings.LastIndex(ref, "/"); i >= 0 && !strings.Contains(ref, ":") {
		repoSuffix, name = ref[:i], ref[i+1:]
	}
	var matches []string
	for id, sk := range m.St.Skills {
		if sk.Name != name {
			continue
		}
		if repoSuffix != "" && sk.Repo != repoSuffix && !strings.HasSuffix(sk.Repo, "/"+repoSuffix) {
			continue
		}
		matches = append(matches, id)
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no skill matches %q (see `skillet list`)", ref)
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("%q is ambiguous: %s", ref, strings.Join(matches, ", "))
}

// ResolveRepo accepts a full repo id or a unique suffix such as owner/repo.
func (m *Manager) ResolveRepo(ref string) (string, error) {
	ref = strings.TrimSuffix(ref, ".git")
	if _, ok := m.St.Repos[ref]; ok {
		return ref, nil
	}
	if src, err := gitx.ParseSource(ref); err == nil {
		if _, ok := m.St.Repos[src.ID]; ok {
			return src.ID, nil
		}
	}
	var matches []string
	for id := range m.St.Repos {
		if strings.HasSuffix(id, "/"+ref) {
			matches = append(matches, id)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no repo matches %q (see `skillet repo list`)", ref)
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("%q is ambiguous: %s", ref, strings.Join(matches, ", "))
}

// ResolveScope picks the project root for dir (its git top level unless
// exact is set), or the global scope.
func ResolveScope(global bool, dir string, exact bool) (agents.Scope, error) {
	if global {
		return agents.Scope{Global: true}, nil
	}
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return agents.Scope{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return agents.Scope{}, err
	}
	if !fi.IsDir() {
		return agents.Scope{}, fmt.Errorf("%s is not a directory", abs)
	}
	if !exact {
		if top, err := gitx.Run(abs, "rev-parse", "--show-toplevel"); err == nil && top != "" {
			abs = filepath.Clean(top)
		}
	}
	return agents.Scope{Dir: abs}, nil
}

func inScope(d *state.Deployment, s agents.Scope) bool {
	if s.Global {
		return d.Scope == state.ScopeGlobal
	}
	return d.Scope == state.ScopeProject && agents.SamePath(d.Dir, s.Dir)
}

func scopeFields(s agents.Scope) (string, string) {
	if s.Global {
		return state.ScopeGlobal, ""
	}
	return state.ScopeProject, s.Dir
}

// ScopeLabel is a short human description of a scope.
func ScopeLabel(s agents.Scope) string {
	if s.Global {
		return "global"
	}
	return s.Dir
}
