package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"skillet/internal/agents"
	"skillet/internal/deploy"
)

// printRepoInfo prints `repo show` for one repo.
func printRepoInfo(out io.Writer, r *deploy.RepoInfo) error {
	w := table(out)
	fmt.Fprintf(w, "Repo:\t%s\n", r.ID)
	fmt.Fprintf(w, "URL:\t%s\n", r.URL)
	fmt.Fprintf(w, "Branch:\t%s\n", r.Branch)
	fmt.Fprintf(w, "Head:\t%s\n", short(r.Head))
	switch {
	case r.Upstream == "":
		fmt.Fprintf(w, "Upstream:\tnot checked yet\n")
	case r.Behind > 0:
		fmt.Fprintf(w, "Upstream:\t%s (%d commit(s) behind)\n", short(r.Upstream), r.Behind)
	case r.Upstream == r.Head:
		fmt.Fprintf(w, "Upstream:\t%s (up to date)\n", short(r.Upstream))
	default:
		fmt.Fprintf(w, "Upstream:\t%s\n", short(r.Upstream))
	}
	checked := "never"
	if r.LastChecked != nil {
		checked = r.LastChecked.Local().Format(time.DateTime)
	}
	fmt.Fprintf(w, "Last checked:\t%s\n", checked)
	fmt.Fprintf(w, "Path:\t%s\n", r.Path)
	if r.Problem != "" {
		fmt.Fprintf(w, "Problem:\t%s\n", r.Problem)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if len(r.Pins) > 0 {
		fmt.Fprintln(out, "\nPin checkouts:")
		for _, p := range r.Pins {
			fmt.Fprintf(out, "  %s  %s  (%d deployment(s))\n", p.Rev, display(p.Path, agents.Scope{Global: true}), p.Deployments)
		}
	}

	fmt.Fprintf(out, "\nSkills (%d):\n", len(r.Skills))
	for _, s := range r.Skills {
		flag := ""
		if s.Pending {
			flag = "  [update pending]"
		}
		fmt.Fprintf(out, "  %s  %s%s\n", s.Name, s.RelPath, flag)
		if s.Description != "" {
			fmt.Fprintf(out, "    %s\n", truncate(s.Description, 100))
		}
		for _, warn := range s.Warnings {
			fmt.Fprintf(out, "    warning: %s\n", warn)
		}
		for _, d := range s.Deployed {
			scope := "global"
			if d.Scope != "global" {
				scope = d.Dir
			}
			extra := pinSuffix(d.Pin)
			if d.Alias != "" {
				extra += " as " + d.Alias
			}
			fmt.Fprintf(out, "    used in %s: %s (%s) for %s%s\n", display(scope, agents.Scope{Global: true}), display(d.Target, agents.Scope{Global: true}), d.Mode, strings.Join(d.Agents, ", "), extra)
		}
	}
	return nil
}
