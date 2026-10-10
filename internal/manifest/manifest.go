// Package manifest reads and writes skillet.toml, the per-project list of
// repos and skills that lets a teammate recreate the same setup.
package manifest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"skillet/internal/skill"
	"skillet/internal/state"
)

// FileName is the manifest's name in the project root.
const FileName = "skillet.toml"

const header = "# Managed by skillet. Restore with `skillet restore`.\n\n"

type Repo struct {
	ID     string `toml:"id"`
	URL    string `toml:"url"`
	Branch string `toml:"branch,omitempty"`
}

// Skill is one skill of the project. Agents are the agents it was requested
// for; the link mode is machine specific and not recorded.
type Skill struct {
	ID     string   `toml:"id"`
	Agents []string `toml:"agents"`
	Pin    string   `toml:"pin,omitempty"` // full commit sha; empty follows the repo
	As     string   `toml:"as,omitempty"`  // link name override (`enable --as`)
}

type Manifest struct {
	Repos  []Repo  `toml:"repo"`
	Skills []Skill `toml:"skill"`
}

// IsLocal reports whether a repo id comes from a local path, which a
// teammate cannot clone.
func IsLocal(repoID string) bool { return strings.HasPrefix(repoID, "local/") }

// RepoOf is the repo id part of a skill id (<repo id>:<path in repo>).
func RepoOf(skillID string) string {
	repo, _, _ := strings.Cut(skillID, ":")
	return repo
}

// Build derives a manifest from the project's deployments. order lists agent
// ids in registry order, which is the order agents are written in.
func Build(repos map[string]*state.Repo, deps []*state.Deployment, order []string) (*Manifest, error) {
	rank := map[string]int{}
	for i, id := range order {
		rank[id] = i
	}
	type entry struct {
		agents map[string]bool
		pin    string
		alias  string
	}
	skills := map[string]*entry{}
	for _, d := range deps {
		e := skills[d.Skill]
		if e == nil {
			e = &entry{agents: map[string]bool{}}
			skills[d.Skill] = e
		}
		for _, a := range d.Agents {
			e.agents[a] = true
		}
		if e.pin == "" {
			e.pin = d.Pin
		}
		if e.alias == "" {
			e.alias = d.Alias
		}
	}
	m := &Manifest{}
	used := map[string]bool{}
	for id, e := range skills {
		s := Skill{ID: id, Pin: e.pin, As: e.alias}
		for a := range e.agents {
			s.Agents = append(s.Agents, a)
		}
		sort.Slice(s.Agents, func(i, j int) bool {
			ri, iok := rank[s.Agents[i]]
			rj, jok := rank[s.Agents[j]]
			if iok != jok {
				return iok
			}
			if ri != rj {
				return ri < rj
			}
			return s.Agents[i] < s.Agents[j]
		})
		m.Skills = append(m.Skills, s)
		used[RepoOf(id)] = true
	}
	for id := range used {
		r, ok := repos[id]
		if !ok {
			return nil, fmt.Errorf("a deployed skill belongs to missing repo %s", id)
		}
		m.Repos = append(m.Repos, Repo{ID: id, URL: r.URL, Branch: r.Branch})
	}
	m.sort()
	return m, nil
}

func (m *Manifest) sort() {
	sort.Slice(m.Repos, func(i, j int) bool { return m.Repos[i].ID < m.Repos[j].ID })
	sort.Slice(m.Skills, func(i, j int) bool { return m.Skills[i].ID < m.Skills[j].ID })
}

// Load reads and validates a manifest. Unknown keys are errors, so typos do
// not silently drop a setting.
func Load(path string) (*Manifest, error) {
	var m Manifest
	md, err := toml.DecodeFile(path, &m)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s not found; create it with `skillet save`", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		keys := make([]string, len(un))
		for i, k := range un {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown key(s): %s", path, strings.Join(keys, ", "))
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.sort()
	return &m, nil
}

// Validate checks the structure. Agent names are checked by the caller
// against its registry.
func (m *Manifest) Validate() error {
	repos := map[string]bool{}
	for _, r := range m.Repos {
		switch {
		case r.ID == "":
			return fmt.Errorf("[[repo]] without id")
		case r.URL == "":
			return fmt.Errorf("repo %s has no url", r.ID)
		case repos[r.ID]:
			return fmt.Errorf("repo %s is listed twice", r.ID)
		}
		repos[r.ID] = true
	}
	seen := map[string]bool{}
	for _, s := range m.Skills {
		repo, rel, ok := strings.Cut(s.ID, ":")
		if !ok || repo == "" || rel == "" {
			return fmt.Errorf("invalid skill id %q (want <repo id>:<path>)", s.ID)
		}
		for _, seg := range strings.Split(rel, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return fmt.Errorf("invalid skill id %q: bad path segment %q", s.ID, seg)
			}
		}
		switch {
		case !repos[repo]:
			return fmt.Errorf("skill %s: repo %s is not listed under [[repo]]", s.ID, repo)
		case seen[s.ID]:
			return fmt.Errorf("skill %s is listed twice", s.ID)
		case len(s.Agents) == 0:
			return fmt.Errorf("skill %s has no agents", s.ID)
		case strings.HasPrefix(s.Pin, "-"):
			return fmt.Errorf("skill %s: invalid pin %q", s.ID, s.Pin)
		case s.As != "" && !skill.ValidName(s.As):
			return fmt.Errorf("skill %s: invalid alias %q (lowercase letters, digits and hyphens, 1-64 chars)", s.ID, s.As)
		}
		seen[s.ID] = true
	}
	return nil
}

// Save writes the manifest atomically, repos and skills sorted so the file
// is stable in diffs.
func Save(path string, m *Manifest) error {
	out := &Manifest{Repos: append([]Repo{}, m.Repos...), Skills: append([]Skill{}, m.Skills...)}
	out.sort()
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(out); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".skillet.toml.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
