package deploy

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"skillet/internal/skill"
	"skillet/internal/state"
)

// RepoUse is one place a skill is linked or copied to.
type RepoUse struct {
	Scope  string   `json:"scope"`         // "project" or "global"
	Dir    string   `json:"dir,omitempty"` // project directory
	Target string   `json:"target"`
	Agents []string `json:"agents"`
	Mode   string   `json:"mode"`
	Pin    string   `json:"pin,omitempty"`
	Alias  string   `json:"alias,omitempty"`
}

// RepoSkill is one skill of a repo as found on disk now.
type RepoSkill struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	RelPath     string    `json:"rel_path"`
	Description string    `json:"description"`
	Pending     bool      `json:"pending,omitempty"`
	Warnings    []string  `json:"warnings,omitempty"`
	Deployed    []RepoUse `json:"deployed,omitempty"`
}

// RepoPin is a pin checkout of the repo under <store>/pins.
type RepoPin struct {
	Rev         string `json:"rev"` // as far as the directory name tells: the short sha
	Path        string `json:"path"`
	Deployments int    `json:"deployments"`
}

// RepoInfo is everything `repo show` prints about one repo. It is built from
// the state, the last fetch and a rescan of the clone, without network.
type RepoInfo struct {
	ID          string      `json:"id"`
	URL         string      `json:"url"`
	Branch      string      `json:"branch"`
	Head        string      `json:"head"`
	Upstream    string      `json:"upstream,omitempty"`
	Behind      int         `json:"behind"` // commits upstream has that the clone lacks, as of the last check
	LastChecked *time.Time  `json:"last_checked,omitempty"`
	Path        string      `json:"path"` // clone directory
	Pins        []RepoPin   `json:"pins,omitempty"`
	Skills      []RepoSkill `json:"skills"`
	// Problem is set when the last-fetched upstream cannot be compared (diverged
	// clone) or the clone cannot be scanned.
	Problem string `json:"problem,omitempty"`
}

// RepoInfo gathers the details of one repo.
func (m *Manager) RepoInfo(id string) (*RepoInfo, error) {
	r, ok := m.St.Repos[id]
	if !ok {
		return nil, fmt.Errorf("unknown repo %s", id)
	}
	info := &RepoInfo{ID: id, URL: r.URL, Branch: r.Branch, Head: r.Head, Upstream: r.Upstream, Path: m.RepoDir(r), Skills: []RepoSkill{}}
	if !r.LastChecked.IsZero() {
		t := r.LastChecked
		info.LastChecked = &t
	}
	// diffRepo also refreshes the skills' pending flags used below.
	chk := m.diffRepo(id)
	info.Behind = chk.Commits
	if chk.Err != nil {
		info.Problem = chk.Err.Error()
	}

	found, err := skill.Scan(info.Path)
	if err != nil {
		// Fall back to what was recorded when the repo was last scanned.
		if info.Problem != "" {
			info.Problem += "; "
		}
		info.Problem += "cannot scan clone: " + err.Error()
		found = nil
		for _, sk := range m.St.Skills {
			if sk.Repo == id {
				found = append(found, skill.Found{RelPath: sk.RelPath, Name: sk.Name, Description: sk.Description})
			}
		}
	}
	for _, f := range found {
		sid := state.SkillID(id, f.RelPath)
		rs := RepoSkill{ID: sid, Name: f.Name, RelPath: f.RelPath, Description: f.Description, Warnings: f.Warnings}
		if sk := m.St.Skills[sid]; sk != nil {
			rs.Pending = sk.Pending
		}
		for _, d := range m.St.DeploymentsOf(sid) {
			rs.Deployed = append(rs.Deployed, RepoUse{Scope: d.Scope, Dir: d.Dir, Target: d.Target, Agents: d.Agents, Mode: d.Mode, Pin: d.Pin, Alias: d.Alias})
		}
		info.Skills = append(info.Skills, rs)
	}
	sort.Slice(info.Skills, func(i, j int) bool { return info.Skills[i].RelPath < info.Skills[j].RelPath })

	for _, c := range m.pinCheckouts() {
		if c.Repo != id {
			continue
		}
		p := RepoPin{Path: c.Path, Rev: strings.TrimPrefix(filepath.Base(c.Path), filepath.Base(id)+"@")}
		for _, d := range m.St.Deployments {
			if repo, _ := splitSkillID(d.Skill); repo == id && d.Pin != "" && m.PinDir(id, d.Pin) == c.Path {
				p.Deployments++
			}
		}
		info.Pins = append(info.Pins, p)
	}
	return info, nil
}
