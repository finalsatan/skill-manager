package tui

import (
	"path/filepath"
	"sort"
	"strings"

	"skillet/internal/agents"
	"skillet/internal/deploy"
)

// cellState is what one agent sees of one skill.
type cellState int

const (
	cellOff       cellState = iota // not enabled
	cellLinked                     // linked directly in the agent's own directory
	cellVia                        // visible through another agent's directory
	cellProblem                    // a link exists but its health is not ok
	cellUnmanaged                  // an entry skillet did not create
)

// glyph returns the cell's symbol. Colour is only a reinforcement.
// visible reports whether the cell means the agent sees the skill.
func (c cellState) visible() bool { return c == cellLinked || c == cellVia || c == cellProblem }

func (c cellState) glyph() string {
	switch c {
	case cellLinked:
		return "●"
	case cellVia:
		return "◐"
	case cellProblem:
		return "✗"
	case cellUnmanaged:
		return "■"
	}
	return "○"
}

// skillInfo is a skill of the store as the matrix needs it.
type skillInfo struct {
	ID, Name, Repo, RelPath, Description string
	Pending                              bool
	RepoDir                              string // the clone's directory
	Head                                 string // the clone's HEAD commit
}

// link is one link (or copy) on disk and the agents that see it.
type link struct {
	Path   string
	Target string // the skill directory it points at, when known
	Mode   string
	Health string
	Agents []string
}

// row is one line of the matrix.
type row struct {
	Key         string // unique within a section
	ID          string // skill id; empty for unmanaged entries
	Name        string
	Repo        string
	RelPath     string
	Description string
	RepoDir     string
	Head        string
	InStore     bool // the skill still exists in the store

	Cells   map[string]cellState // by agent id
	Health  string               // worst health of its links; "" when not enabled
	Pin     string
	Alias   string // link name when it differs from the skill's name
	Pending bool   // upstream has changes
	Mode    string
	Links   []link
	Visible []string // agents that actually see it, in registry order
}

func (r *row) managed() bool { return r.ID != "" }

// enabled reports whether any agent sees the skill in this scope.
func (r *row) enabled() bool { return len(r.Visible) > 0 }

// buildInput is everything buildRows needs; all of it is plain data.
type buildInput struct {
	Scope   agents.Scope
	Agents  []agents.Agent
	Skills  []skillInfo       // the store's skills, in display order
	Entries []deploy.Entry    // Status(scope)
	Targets map[string]string // link path -> skill directory it points at
	// Undeployed adds a row for every store skill, enabled or not. Without it
	// only skills with an entry in the scope appear.
	Undeployed bool
}

// buildRows turns the store's skills and a scope's status entries into
// matrix rows: managed skills sorted by name then id, unmanaged entries after.
func buildRows(in buildInput) []row {
	byID := map[string]*row{}
	var managed []*row
	info := map[string]skillInfo{}
	for _, s := range in.Skills {
		info[s.ID] = s
	}
	get := func(id string) *row {
		if r, ok := byID[id]; ok {
			return r
		}
		r := &row{Key: id, ID: id, Cells: map[string]cellState{}}
		if s, ok := info[id]; ok {
			r.Name, r.Repo, r.RelPath, r.Description = s.Name, s.Repo, s.RelPath, s.Description
			r.RepoDir, r.Head, r.Pending, r.InStore = s.RepoDir, s.Head, s.Pending, true
		} else {
			// Removed upstream but still deployed (pinned): derive what we can.
			r.Repo, r.RelPath, _ = strings.Cut(id, ":")
			r.Name = filepath.Base(filepath.FromSlash(r.RelPath))
		}
		byID[id] = r
		managed = append(managed, r)
		return r
	}
	if in.Undeployed {
		for _, s := range in.Skills {
			get(s.ID)
		}
	}

	own := map[string]string{}
	for _, a := range in.Agents {
		own[a.ID] = a.TargetDir(in.Scope)
	}
	unmanaged := map[string]*row{}
	var unmanagedRows []*row

	for _, e := range in.Entries {
		if e.Skill == "" {
			r, ok := unmanaged[e.Name]
			if !ok {
				r = &row{Key: "unmanaged:" + e.Name, Name: e.Name, Health: "unmanaged", Cells: map[string]cellState{}}
				unmanaged[e.Name] = r
				unmanagedRows = append(unmanagedRows, r)
			}
			r.Cells[e.Agent] = cellUnmanaged
			r.Links = addLink(r.Links, e, "")
			continue
		}
		r := get(e.Skill)
		cur := r.Cells[e.Agent]
		var next cellState
		switch {
		case e.Health != "ok":
			next = cellProblem
		case sameDir(filepath.Dir(e.Path), own[e.Agent]):
			next = cellLinked
		default:
			next = cellVia
		}
		r.Cells[e.Agent] = betterCell(cur, next)
		if r.Health == "" || deploy.HealthRank(e.Health) > deploy.HealthRank(r.Health) {
			r.Health = e.Health
		}
		if r.Pin == "" {
			r.Pin = e.Pin
		}
		if e.Alias != "" && e.Alias != r.Name {
			r.Alias = e.Alias
		}
		if e.Mode != "" && r.Mode == "" {
			r.Mode = e.Mode
		}
		r.Links = addLink(r.Links, e, in.Targets[e.Path])
	}

	order := map[string]int{}
	for i, a := range in.Agents {
		order[a.ID] = i
	}
	finish := func(r *row) {
		// A pinned deployment stays where it is when upstream moves.
		if r.Pin != "" {
			r.Pending = false
		}
		for _, a := range in.Agents {
			if r.Cells[a.ID] != cellOff {
				r.Visible = append(r.Visible, a.ID)
			}
		}
		sort.SliceStable(r.Links, func(i, j int) bool { return r.Links[i].Path < r.Links[j].Path })
		for i := range r.Links {
			sort.Slice(r.Links[i].Agents, func(a, b int) bool {
				return order[r.Links[i].Agents[a]] < order[r.Links[i].Agents[b]]
			})
		}
	}
	for _, r := range managed {
		finish(r)
	}
	for _, r := range unmanagedRows {
		finish(r)
	}

	sort.SliceStable(managed, func(i, j int) bool {
		if managed[i].Name != managed[j].Name {
			return managed[i].Name < managed[j].Name
		}
		return managed[i].ID < managed[j].ID
	})
	sort.SliceStable(unmanagedRows, func(i, j int) bool { return unmanagedRows[i].Name < unmanagedRows[j].Name })

	out := make([]row, 0, len(managed)+len(unmanagedRows))
	for _, r := range managed {
		out = append(out, *r)
	}
	for _, r := range unmanagedRows {
		out = append(out, *r)
	}
	return out
}

// betterCell merges two states of the same agent and skill: a problem outranks
// a direct link, which outranks a visible-through-another-agent link.
func betterCell(a, b cellState) cellState {
	rank := map[cellState]int{cellOff: 0, cellVia: 1, cellLinked: 2, cellProblem: 3, cellUnmanaged: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func sameDir(a, b string) bool { return a != "" && b != "" && agents.SamePath(a, b) }

// addLink records that e.Agent sees the link at e.Path.
func addLink(links []link, e deploy.Entry, target string) []link {
	for i := range links {
		if links[i].Path == e.Path {
			for _, a := range links[i].Agents {
				if a == e.Agent {
					return links
				}
			}
			links[i].Agents = append(links[i].Agents, e.Agent)
			return links
		}
	}
	return append(links, link{Path: e.Path, Target: target, Mode: e.Mode, Health: e.Health, Agents: []string{e.Agent}})
}

// filterRows keeps the rows that match the query and, with onlyEnabled, are
// enabled for at least one agent. The query is a case-insensitive substring of
// the name, alias, repo, id or description; an empty query matches everything.
func filterRows(rows []row, onlyEnabled bool, query string) []row {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []row
	for _, r := range rows {
		if onlyEnabled && !r.enabled() {
			continue
		}
		if q != "" {
			hay := strings.ToLower(strings.Join([]string{r.Name, r.Alias, r.Repo, r.ID, r.Description}, "\n"))
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}
