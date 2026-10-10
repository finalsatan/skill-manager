// Package agents describes where each coding agent looks for skills and
// decides which directories a skill must be linked into.
package agents

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"skillet/internal/config"
)

// Agent describes one coding agent's skill discovery rules.
//
// ProjectDir/GlobalDir are where skillet writes links for that agent.
// ProjectReads/GlobalReads are every directory the agent scans (including its
// own), which is what decides who actually sees a link.
type Agent struct {
	ID           string
	Name         string
	ProjectDir   string   // relative to the project root
	GlobalDir    string   // absolute
	ProjectReads []string // relative to the project root
	GlobalReads  []string // absolute

	// Binary and ConfigDirs are only used to detect whether the agent is
	// installed; custom agents have neither.
	Binary     string
	ConfigDirs []string // absolute
}

// Install is the result of looking for an agent on this machine.
type Install struct {
	Known bool   // false when the agent has nothing to detect it by
	Found bool   // a binary on PATH or an existing config directory
	Via   string // what was found: the binary's path or the config directory
}

// Detect looks for the agent's binary on PATH, then for its config directory.
func (a Agent) Detect() Install {
	if a.Binary == "" && len(a.ConfigDirs) == 0 {
		return Install{}
	}
	in := Install{Known: true}
	if a.Binary != "" {
		if p, err := exec.LookPath(a.Binary); err == nil {
			in.Found, in.Via = true, p
			return in
		}
	}
	for _, d := range a.ConfigDirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			in.Found, in.Via = true, d
			return in
		}
	}
	return in
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Builtin returns the built-in agents in planning order. Agents whose
// directory is read by others come first so that one link can serve several
// agents (Claude's dir also serves OpenCode, .agents also serves pi).
func Builtin() ([]Agent, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	claudeHome := envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	piHome := envOr("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
	xdgConfig := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	codexHome := envOr("CODEX_HOME", filepath.Join(home, ".codex"))
	userAgents := filepath.Join(home, ".agents", "skills")
	userClaude := filepath.Join(claudeHome, "skills")

	return []Agent{
		{
			ID: "claude", Name: "Claude Code",
			ProjectDir: ".claude/skills", GlobalDir: userClaude,
			ProjectReads: []string{".claude/skills"},
			GlobalReads:  []string{userClaude},
			Binary:       "claude", ConfigDirs: []string{claudeHome},
		},
		{
			ID: "codex", Name: "Codex",
			ProjectDir: ".agents/skills", GlobalDir: userAgents,
			ProjectReads: []string{".agents/skills"},
			// ~/.codex/skills is the legacy location Codex still reads.
			GlobalReads: []string{userAgents, filepath.Join(codexHome, "skills")},
			Binary:      "codex", ConfigDirs: []string{codexHome},
		},
		{
			ID: "opencode", Name: "OpenCode",
			ProjectDir: ".opencode/skills", GlobalDir: filepath.Join(xdgConfig, "opencode", "skills"),
			ProjectReads: []string{".opencode/skills", ".claude/skills", ".agents/skills"},
			GlobalReads:  []string{filepath.Join(xdgConfig, "opencode", "skills"), userClaude, userAgents},
			Binary:       "opencode", ConfigDirs: []string{filepath.Join(xdgConfig, "opencode")},
		},
		{
			ID: "pi", Name: "pi",
			ProjectDir: ".pi/skills", GlobalDir: filepath.Join(piHome, "skills"),
			ProjectReads: []string{".pi/skills", ".agents/skills"},
			GlobalReads:  []string{filepath.Join(piHome, "skills"), userAgents},
			Binary:       "pi", ConfigDirs: []string{filepath.Join(home, ".pi"), piHome},
		},
	}, nil
}

// Registry is the built-in agents merged with the user's custom definitions.
type Registry struct {
	list []Agent
}

func NewRegistry(custom []config.CustomAgent) (*Registry, error) {
	list, err := Builtin()
	if err != nil {
		return nil, err
	}
	for _, c := range custom {
		if c.ID == "" {
			return nil, fmt.Errorf("custom agent without id in config")
		}
		a := Agent{ID: c.ID, Name: c.Name}
		idx := -1
		for i := range list {
			if list[i].ID == c.ID {
				idx, a = i, list[i]
			}
		}
		if c.Name != "" {
			a.Name = c.Name
		}
		// A changed target dir replaces the old one in the read list, unless the
		// user spells out the read list explicitly.
		if c.ProjectDir != "" {
			old := a.ProjectDir
			a.ProjectDir = filepath.ToSlash(c.ProjectDir)
			a.ProjectReads = replaceOrAdd(a.ProjectReads, old, a.ProjectDir)
		}
		if c.GlobalDir != "" {
			g, err := config.ExpandHome(c.GlobalDir)
			if err != nil {
				return nil, err
			}
			old := a.GlobalDir
			a.GlobalDir = g
			a.GlobalReads = replaceOrAdd(a.GlobalReads, old, g)
		}
		if len(c.ProjectReads) > 0 {
			a.ProjectReads = nil
			for _, r := range c.ProjectReads {
				a.ProjectReads = append(a.ProjectReads, filepath.ToSlash(r))
			}
		}
		if len(c.GlobalReads) > 0 {
			a.GlobalReads = nil
			for _, r := range c.GlobalReads {
				g, err := config.ExpandHome(r)
				if err != nil {
					return nil, err
				}
				a.GlobalReads = append(a.GlobalReads, g)
			}
		}
		if a.Name == "" {
			a.Name = a.ID
		}
		if idx >= 0 {
			list[idx] = a
		} else {
			list = append(list, a)
		}
	}
	return &Registry{list: list}, nil
}

func replaceOrAdd(list []string, old, now string) []string {
	out := make([]string, 0, len(list)+1)
	found := false
	for _, v := range list {
		if v == old {
			v = now
		}
		if v == now {
			if found {
				continue
			}
			found = true
		}
		out = append(out, v)
	}
	if !found {
		out = append([]string{now}, out...)
	}
	return out
}

func (r *Registry) All() []Agent { return r.list }

func (r *Registry) Get(id string) (Agent, bool) {
	for _, a := range r.list {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// Resolve turns user-supplied ids ("all" allowed) into agents in planning order.
func (r *Registry) Resolve(ids []string) ([]Agent, error) {
	want := map[string]bool{}
	for _, id := range ids {
		for _, part := range strings.Split(id, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if part == "all" {
				for _, a := range r.list {
					want[a.ID] = true
				}
				continue
			}
			if _, ok := r.Get(part); !ok {
				return nil, fmt.Errorf("unknown agent %q (known: %s)", part, strings.Join(r.IDs(), ", "))
			}
			want[part] = true
		}
	}
	var out []Agent
	for _, a := range r.list {
		if want[a.ID] {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *Registry) IDs() []string {
	ids := make([]string, len(r.list))
	for i, a := range r.list {
		ids[i] = a.ID
	}
	return ids
}

// Scope is where links go: a project root, or the user's global dirs.
type Scope struct {
	Global bool
	Dir    string // project root, absolute; empty when Global
}

// TargetDir is the directory skillet links into for this agent.
func (a Agent) TargetDir(s Scope) string {
	if s.Global {
		return a.GlobalDir
	}
	if a.ProjectDir == "" {
		return ""
	}
	return filepath.Join(s.Dir, filepath.FromSlash(a.ProjectDir))
}

// ReadDirs is every directory this agent scans in the given scope.
func (a Agent) ReadDirs(s Scope) []string {
	if s.Global {
		return a.GlobalReads
	}
	out := make([]string, 0, len(a.ProjectReads))
	for _, r := range a.ProjectReads {
		out = append(out, filepath.Join(s.Dir, filepath.FromSlash(r)))
	}
	return out
}

// Reads reports whether the agent scans dir in this scope.
func (a Agent) Reads(s Scope, dir string) bool {
	for _, r := range a.ReadDirs(s) {
		if samePath(r, dir) {
			return true
		}
	}
	return false
}

// Placement is one directory to link into and the requested agents it serves.
type Placement struct {
	Dir    string
	Owner  string   // agent whose TargetDir this is
	Agents []string // requested agents that will see the link
}

// Plan picks the fewest link directories so that every requested agent sees
// the skill. existing lists directories that already hold this skill and are
// reused before creating new links. Agents are visited in registry order, so
// an agent already served by an earlier directory gets no link of its own.
func Plan(requested []Agent, s Scope, existing []string) ([]Placement, error) {
	var out []Placement
	add := func(dir, owner string) *Placement {
		for i := range out {
			if samePath(out[i].Dir, dir) {
				return &out[i]
			}
		}
		out = append(out, Placement{Dir: dir, Owner: owner})
		return &out[len(out)-1]
	}
	for _, a := range requested {
		var served *Placement
		for i := range out {
			if a.Reads(s, out[i].Dir) {
				served = &out[i]
				break
			}
		}
		if served == nil {
			for _, d := range existing {
				if a.Reads(s, d) {
					served = add(d, "")
					break
				}
			}
		}
		if served == nil {
			dir := a.TargetDir(s)
			if dir == "" {
				return nil, fmt.Errorf("agent %s has no %s skills directory", a.ID, scopeName(s))
			}
			served = add(dir, a.ID)
		}
		served.Agents = append(served.Agents, a.ID)
	}
	return out, nil
}

func scopeName(s Scope) string {
	if s.Global {
		return "global"
	}
	return "project"
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// SamePath compares two cleaned paths, case-insensitively on Windows.
func SamePath(a, b string) bool { return samePath(a, b) }
