package tui

import "sort"

// pendingSet holds the changes the user has marked in one scope: for each
// skill id, the agents it should (true) or should no longer (false) be
// visible to. An entry exists only where the wish differs from what is on
// disk, so toggling a cell twice leaves nothing behind.
type pendingSet map[string]map[string]bool

// set records that agent should (want) or should not see skill id. visible is
// whether it does now; a wish equal to the present removes the entry.
func (p pendingSet) set(id, agent string, want, visible bool) {
	if want == visible {
		if m := p[id]; m != nil {
			delete(m, agent)
			if len(m) == 0 {
				delete(p, id)
			}
		}
		return
	}
	if p[id] == nil {
		p[id] = map[string]bool{}
	}
	p[id][agent] = want
}

// counts returns the number of pending enables and disables.
func (p pendingSet) counts() (enable, disable int) {
	for _, m := range p {
		for _, want := range m {
			if want {
				enable++
			} else {
				disable++
			}
		}
	}
	return
}

// change is what to do to one skill: enable for some agents, disable for others.
type change struct {
	ID      string
	Name    string
	Enable  []string
	Disable []string

	// Pin, Unpin, Alias and Unalias come from the pin and alias dialogs, which
	// re-enable the agents already served with one of these options.
	Pin     string
	Unpin   bool
	Alias   string
	Unalias bool
}

// changes converts the pending set into per-skill changes sorted by name then
// id. Agents follow order (the registry's order); name gives a skill's name.
func (p pendingSet) changes(order []string, name func(id string) string) []change {
	var out []change
	for id, m := range p {
		c := change{ID: id, Name: name(id)}
		for _, a := range order {
			if want, ok := m[a]; ok {
				if want {
					c.Enable = append(c.Enable, a)
				} else {
					c.Disable = append(c.Disable, a)
				}
			}
		}
		if len(c.Enable)+len(c.Disable) > 0 {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}
