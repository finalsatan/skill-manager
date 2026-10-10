package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"skillet/internal/agents"
	"skillet/internal/deploy"
)

// healthRank orders states from best to worst; a matrix row shows the worst.
var healthRank = map[string]int{
	"ok": 0, "outdated": 1, "modified": 2, "wrong-target": 3, "broken": 4, "missing": 5,
}

type statusRow struct {
	name    string
	skill   string // empty for unmanaged
	visible map[string]int
	health  string
	pending bool
}

// printStatusMatrix prints one row per skill and one column per agent. A cell
// is ✓ when the agent sees the skill, ✓(n) when it sees it from n locations.
func printStatusMatrix(out io.Writer, all []agents.Agent, entries []deploy.Entry) error {
	rows := map[string]*statusRow{}
	var keys []string
	for _, e := range entries {
		key := e.Name + "\x00" + e.Skill
		r, ok := rows[key]
		if !ok {
			r = &statusRow{name: e.Name, skill: e.Skill, visible: map[string]int{}, health: "ok"}
			if e.Skill == "" {
				r.health = "unmanaged"
			}
			rows[key] = r
			keys = append(keys, key)
		}
		if e.Skill != "" && healthRank[e.Health] > healthRank[r.health] {
			r.health = e.Health
		}
		r.pending = r.pending || e.Pending
		if e.Health != "missing" {
			r.visible[e.Agent]++
		}
	}
	sort.Strings(keys)

	w := table(out)
	header := []string{"SKILL"}
	for _, a := range all {
		header = append(header, a.ID)
	}
	header = append(header, "STATE", "SOURCE")
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, k := range keys {
		r := rows[k]
		cells := []string{r.name}
		for _, a := range all {
			switch n := r.visible[a.ID]; {
			case n == 0:
				cells = append(cells, "-")
			case n == 1:
				cells = append(cells, "✓")
			default:
				cells = append(cells, fmt.Sprintf("✓(%d)", n))
			}
		}
		src := "-"
		if r.skill != "" {
			src = shortSource(r.skill)
		}
		st := r.health
		if st == "ok" && r.pending {
			st = "update"
		}
		cells = append(cells, st, src)
		fmt.Fprintln(w, strings.Join(cells, "\t"))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	for _, c := range deploy.Clashes(entries) {
		fmt.Fprintf(out, "warning: %s sees %d different skills named %q\n", c.Agent, len(c.Paths), c.Name)
	}
	for _, k := range keys {
		if r := rows[k]; r.health == "missing" || r.health == "broken" {
			fmt.Fprintf(out, "warning: %s link is %s; run `skillet enable %s -a <agents>` again to restore it\n", r.name, r.health, r.name)
		}
	}
	for _, k := range keys {
		if rows[k].pending {
			fmt.Fprintln(out, "Upstream changes are waiting: run `skillet update` to review them.")
			break
		}
	}
	fmt.Fprintln(out, "\n✓(n): visible from n locations. Run `skillet status -l` for link paths.")
	return nil
}

// printStatusLong prints one line per agent and location.
func printStatusLong(out io.Writer, entries []deploy.Entry, s agents.Scope) error {
	w := table(out)
	fmt.Fprintln(w, "AGENT\tSKILL\tSTATE\tSOURCE\tPATH")
	for _, e := range entries {
		src := "-"
		if e.Skill != "" {
			src = shortSource(e.Skill)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.Agent, e.Name, e.Health, src, display(e.Path, s))
	}
	return w.Flush()
}

// shortSource renders a skill id as its repo, dropping the github.com host.
func shortSource(id string) string {
	repo := id
	if i := strings.Index(id, ":"); i >= 0 {
		repo = id[:i]
	}
	return strings.TrimPrefix(repo, "github.com/")
}
