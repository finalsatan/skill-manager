package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"skillet/internal/agents"
	"skillet/internal/deploy"
	"skillet/internal/i18n"
	"skillet/internal/state"
)

// repoRow is one repo of the list.
type repoRow struct {
	ID       string
	Skills   int
	Behind   int // commits upstream has that the clone lacks; -1 when not known
	Deployed int
}

// reposMsg is the result of loadRepos.
type reposMsg struct {
	rows []repoRow
	err  error
}

// repoInfoMsg carries one repo's details.
type repoInfoMsg struct {
	id   string
	info *deploy.RepoInfo
	err  error
}

// loadRepos lists the repos from the state and the last fetch (no network).
func loadRepos() tea.Cmd {
	return func() tea.Msg {
		var msg reposMsg
		msg.err = withManager(func(m *deploy.Manager) error {
			checks, err := m.Pending(nil)
			if err != nil {
				return err
			}
			behind := map[string]int{}
			for _, c := range checks {
				if r := m.St.Repos[c.Repo]; c.Err == nil && r != nil && r.Upstream != "" {
					behind[c.Repo] = c.Commits
				}
			}
			for _, id := range m.RepoIDs() {
				row := repoRow{ID: id, Behind: -1}
				if n, ok := behind[id]; ok {
					row.Behind = n
				}
				for _, sk := range m.St.Skills {
					if sk.Repo == id {
						row.Skills++
					}
				}
				for _, d := range m.St.Deployments {
					if repo, _, _ := strings.Cut(d.Skill, ":"); repo == id {
						row.Deployed++
					}
				}
				msg.rows = append(msg.rows, row)
			}
			return nil
		})
		return msg
	}
}

// loadRepoInfo reads one repo's details.
func loadRepoInfo(id string) tea.Cmd {
	return func() tea.Msg {
		msg := repoInfoMsg{id: id}
		msg.err = withManager(func(m *deploy.Manager) error {
			var err error
			msg.info, err = m.RepoInfo(id)
			return err
		})
		return msg
	}
}

// addRepoMsg is the result of addRepoCmd.
type addRepoMsg struct {
	plan   deploy.AddPlan
	skills int
	err    error
}

// addRepo plans and clones a repository: owner/repo, a URL or a local path.
func addRepo(m *deploy.Manager, ref string) (deploy.AddPlan, int, error) {
	plan, err := m.PlanAddRepo(ref, "")
	if err != nil {
		return plan, 0, err
	}
	_, found, err := m.AddRepo(ref, "")
	return plan, len(found), err
}

func addRepoCmd(ref string) tea.Cmd {
	return func() tea.Msg {
		var msg addRepoMsg
		msg.err = withManager(func(m *deploy.Manager) error {
			var err error
			msg.plan, msg.skills, err = addRepo(m, ref)
			return err
		})
		return msg
	}
}

// removePlanMsg is the dry run of a removal.
type removePlanMsg struct {
	plan deploy.RemovePlan
	err  error // why the removal would be refused
	open error // the store could not be opened
}

func removePlanCmd(id string) tea.Cmd {
	return func() tea.Msg {
		var msg removePlanMsg
		msg.open = withManager(func(m *deploy.Manager) error {
			msg.plan, msg.err = m.PlanRemoveRepo(id, true)
			return nil
		})
		return msg
	}
}

// removeDoneMsg is the result of removeRepoCmd.
type removeDoneMsg struct {
	id      string
	removed []string
	err     error
}

func removeRepoCmd(id string) tea.Cmd {
	return func() tea.Msg {
		msg := removeDoneMsg{id: id}
		msg.err = withManager(func(m *deploy.Manager) error {
			var err error
			msg.removed, err = m.RemoveRepo(id, true)
			return err
		})
		return msg
	}
}

// reposModel is the Repos tab.
type reposModel struct {
	rows    []repoRow
	loaded  bool
	cursor  int
	infos   map[string]repoInfoMsg
	asked   map[string]bool
	scroll  int // detail pane scroll
	w, h    int
	pending string // repo to select when the rows arrive
}

func newReposModel() reposModel {
	return reposModel{infos: map[string]repoInfoMsg{}, asked: map[string]bool{}}
}

func (m *reposModel) setSize(w, h int) { m.w, m.h = w, h }

func (m *reposModel) setRows(rows []repoRow) {
	prev := ""
	if r := m.selected(); r != nil {
		prev = r.ID
	}
	if m.pending != "" {
		prev, m.pending = m.pending, ""
	}
	m.rows, m.loaded, m.cursor = rows, true, 0
	for i, r := range rows {
		if r.ID == prev {
			m.cursor = i
		}
	}
	// Details may have changed with the rows.
	m.infos, m.asked = map[string]repoInfoMsg{}, map[string]bool{}
}

func (m reposModel) selected() *repoRow {
	if m.cursor < len(m.rows) {
		return &m.rows[m.cursor]
	}
	return nil
}

// wantInfo returns the command loading the selected repo's details, once.
func (m *reposModel) wantInfo() tea.Cmd {
	r := m.selected()
	if r == nil || m.asked[r.ID] {
		return nil
	}
	m.asked[r.ID] = true
	return loadRepoInfo(r.ID)
}

type repoKeys struct {
	Up, Down, Add, Remove, Skills, Updates, PgUp, PgDown key.Binding
}

func newRepoKeys() repoKeys {
	return repoKeys{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", i18n.T("help.move"))),
		Down:    key.NewBinding(key.WithKeys("down", "j")),
		Add:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", i18n.T("help.add"))),
		Remove:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", i18n.T("help.remove"))),
		Skills:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", i18n.T("help.toskills"))),
		Updates: key.NewBinding(key.WithKeys("u"), key.WithHelp("u", i18n.T("help.toupdates"))),
		PgUp:    key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp/PgDn", i18n.T("help.page"))),
		PgDown:  key.NewBinding(key.WithKeys("pgdown")),
	}
}

func (m reposModel) helpGroups() [][]key.Binding {
	k := newRepoKeys()
	return [][]key.Binding{{k.Up, k.PgUp}, {k.Add, k.Remove}, {k.Skills, k.Updates}}
}

func (m reposModel) shortHelp() []key.Binding {
	k := newRepoKeys()
	return []key.Binding{k.Up, k.Add, k.Remove, k.Skills, k.Updates}
}

// Update moves the cursor and scrolls the details; the root handles the keys
// that start work or switch tabs.
func (m reposModel) Update(msg tea.KeyPressMsg) (reposModel, tea.Cmd) {
	k := newRepoKeys()
	switch {
	case key.Matches(msg, k.Up):
		m.cursor, m.scroll = max(m.cursor-1, 0), 0
	case key.Matches(msg, k.Down):
		m.cursor, m.scroll = min(m.cursor+1, max(len(m.rows)-1, 0)), 0
	case key.Matches(msg, k.PgUp):
		m.scroll = max(m.scroll-5, 0)
	case key.Matches(msg, k.PgDown):
		m.scroll += 5
	}
	return m, m.wantInfo()
}

// detailLines renders the selected repo's details.
func (m reposModel) detailLines(st styles) []string {
	r := m.selected()
	if r == nil {
		return nil
	}
	msg, ok := m.infos[r.ID]
	if !ok {
		return []string{st.Dim.Render(i18n.T("loading"))}
	}
	if msg.err != nil {
		return []string{st.Danger.Render(msg.err.Error())}
	}
	info := msg.info
	lw := 0
	labels := []string{"repos.url", "repos.branch", "repos.path", "repos.checked"}
	for _, l := range labels {
		lw = max(lw, lipgloss.Width(i18n.T(l)))
	}
	kv := func(label, v string) string {
		l := i18n.T(label)
		return st.Dim.Render(l+strings.Repeat(" ", lw-lipgloss.Width(l)+2)) + v
	}
	out := []string{
		kv("repos.url", info.URL),
		kv("repos.branch", info.Branch+" @ "+shortSHA(info.Head)),
		kv("repos.path", shortPath(info.Path, agents.Scope{Global: true})),
	}
	if info.LastChecked != nil {
		line := ago(*info.LastChecked)
		if info.Behind > 0 {
			line += "  " + st.Pending.Render("↑ "+i18n.T("updates.commits", info.Behind))
		}
		out = append(out, kv("repos.checked", line))
	}
	if info.Problem != "" {
		out = append(out, st.Danger.Render("✗ "+i18n.T("repos.problem", info.Problem)))
	}
	if len(info.Pins) > 0 {
		out = append(out, "", st.Accent.Render(i18n.T("repos.pins")))
		for _, p := range info.Pins {
			out = append(out, "  "+i18n.T("repos.pinuse", p.Rev, p.Deployments))
		}
	}
	out = append(out, "", st.Accent.Render(i18n.T("repos.skills")))
	for _, sk := range info.Skills {
		head := "  " + st.Bold.Render(sk.Name) + " " + st.Dim.Render(sk.RelPath)
		if sk.Pending {
			head += " " + st.Pending.Render("↑")
		}
		out = append(out, head)
		if len(sk.Deployed) == 0 {
			out = append(out, "      "+st.Dim.Render(i18n.T("repos.notdeployed")))
		}
		for _, u := range sk.Deployed {
			where := i18n.T("scope.global")
			if u.Scope == state.ScopeProject {
				where = u.Dir
			}
			line := "      " + where + " → " + strings.Join(u.Agents, ", ")
			if u.Pin != "" {
				line += st.Dim.Render(" @ " + shortSHA(u.Pin))
			}
			if u.Alias != "" {
				line += st.Dim.Render(" (" + u.Alias + ")")
			}
			out = append(out, line)
		}
	}
	return out
}

func behindText(n int) string {
	if n < 0 {
		return "-"
	}
	return fmt.Sprint(n)
}

func (m reposModel) View(st styles) string {
	if m.w == 0 {
		return ""
	}
	if !m.loaded {
		return "\n  " + st.Dim.Render(i18n.T("loading"))
	}
	if len(m.rows) == 0 {
		return "\n  " + st.Accent.Render(i18n.T("repos.empty")) + "\n  " + st.Dim.Render(i18n.T("repos.empty.hint"))
	}
	idW := lipgloss.Width(i18n.T("col.repo"))
	for _, r := range m.rows {
		idW = max(idW, lipgloss.Width(shortRepo(r.ID)))
	}
	idW = min(idW, max(m.w-40, 12))
	numW := func(key string) int { return max(lipgloss.Width(i18n.T(key)), 4) }
	sW, bW, dW := numW("col.skills"), numW("col.behind"), numW("col.deployed")
	line := func(a, b, c, d string) string {
		return padTo(a, idW) + "  " + padTo(b, sW) + "  " + padTo(c, bW) + "  " + padTo(d, dW)
	}
	var out []string
	out = append(out, st.Dim.Render("  "+line(i18n.T("col.repo"), i18n.T("col.skills"), i18n.T("col.behind"), i18n.T("col.deployed"))))
	listH := min(len(m.rows), max(m.h/2-1, 3))
	off := 0
	if m.cursor >= listH {
		off = m.cursor - listH + 1
	}
	for i := off; i < len(m.rows) && i < off+listH; i++ {
		r := m.rows[i]
		text := line(shortRepo(r.ID), fmt.Sprint(r.Skills), behindText(r.Behind), fmt.Sprint(r.Deployed))
		switch {
		case i == m.cursor:
			out = append(out, st.Cursor.Render(padTo("▸ "+text, m.w)))
		case r.Behind > 0:
			out = append(out, "  "+st.Pending.Render(text))
		default:
			out = append(out, "  "+text)
		}
	}
	out = append(out, st.Dim.Render(strings.Repeat("─", max(m.w, 1))))
	var detail []string
	for _, l := range m.detailLines(st) {
		detail = append(detail, strings.Split(ansi.Wrap(l, max(m.w-2, 10), ""), "\n")...)
	}
	room := max(m.h-len(out), 1)
	scroll := min(m.scroll, max(len(detail)-room, 0))
	for _, l := range detail[scroll:] {
		if len(out) >= m.h {
			break
		}
		out = append(out, " "+l)
	}
	return strings.Join(out, "\n")
}
