package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/deploy"
)

type statusRow struct {
	name    string
	skill   string // empty for unmanaged
	pin     string
	visible map[string]int
	health  string
	pending bool
}

// printScopeStatus prints one scope's status in the matrix or long layout.
func printScopeStatus(out io.Writer, m *deploy.Manager, s agents.Scope, entries []deploy.Entry, long bool) error {
	fmt.Fprintf(out, "Scope: %s\n", display(deploy.ScopeLabel(s), agents.Scope{Global: true}))
	if len(entries) == 0 {
		fmt.Fprintln(out, "No skills visible to any agent.")
		return nil
	}
	if long {
		return printStatusLong(out, entries, s)
	}
	return printStatusMatrix(out, m.Agents.All(), entries)
}

// scopeStatus is one scope's entries in `status --all --json`.
type scopeStatus struct {
	Scope   string         `json:"scope"` // "global" or "project"
	Dir     string         `json:"dir,omitempty"`
	Missing bool           `json:"missing,omitempty"` // the project directory no longer exists
	Entries []deploy.Entry `json:"entries"`
}

// runStatusAll prints the status of every scope that has deployments.
func runStatusAll(cmd *cobra.Command, m *deploy.Manager, long, asJSON bool) error {
	out := cmd.OutOrStdout()
	set := m.DeployedScopes()
	if asJSON {
		res := []scopeStatus{}
		for _, s := range set.Scopes {
			entries, err := m.Status(s)
			if err != nil {
				return err
			}
			ss := scopeStatus{Scope: "project", Dir: s.Dir, Entries: entries}
			if s.Global {
				ss.Scope = "global"
			}
			res = append(res, ss)
		}
		for _, dir := range set.Missing {
			res = append(res, scopeStatus{Scope: "project", Dir: dir, Missing: true, Entries: []deploy.Entry{}})
		}
		return printJSON(out, res)
	}
	if len(set.Scopes) == 0 && len(set.Missing) == 0 {
		fmt.Fprintln(out, "No skills are enabled anywhere.")
		return nil
	}
	for i, s := range set.Scopes {
		entries, err := m.Status(s)
		if err != nil {
			return err
		}
		if i > 0 {
			fmt.Fprintln(out)
		}
		if err := printScopeStatus(out, m, s, entries, long); err != nil {
			return err
		}
	}
	for _, dir := range set.Missing {
		fmt.Fprintf(out, "\nnote: skipped %s, the directory no longer exists (`skillet doctor --fix` forgets it)\n", dir)
	}
	return nil
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
		if e.Skill != "" && deploy.HealthRank(e.Health) > deploy.HealthRank(r.health) {
			r.health = e.Health
		}
		if r.pin == "" {
			r.pin = e.Pin
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
			src = shortSource(r.skill) + pinRef(r.pin)
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
		fmt.Fprintf(out, "         rename one link with `skillet enable <skill> -a %s --as <alias>`\n", c.Agent)
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
			src = shortSource(e.Skill) + pinRef(e.Pin)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.Agent, e.Name, e.Health, src, display(e.Path, s))
	}
	return w.Flush()
}

// pinRef renders "@a1b2c3d" for a pinned deployment, nothing otherwise.
func pinRef(pin string) string {
	if pin == "" {
		return ""
	}
	return "@" + short(pin)
}

// shortSource renders a skill id as its repo, dropping the github.com host.
func shortSource(id string) string {
	repo := id
	if i := strings.Index(id, ":"); i >= 0 {
		repo = id[:i]
	}
	return strings.TrimPrefix(repo, "github.com/")
}
