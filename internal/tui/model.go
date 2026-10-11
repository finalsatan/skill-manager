// Package tui is skillet's terminal UI, built on Bubble Tea.
//
// Every operation on the store runs inside a tea.Cmd that opens the state,
// works and closes it again (see withManager); a Manager is never held while
// the user is thinking.
package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"skillet/internal/agents"
	"skillet/internal/config"
	"skillet/internal/i18n"
	"skillet/internal/state"
)

type tab int

const (
	tabSkills tab = iota
	tabUpdates
	tabRepos
	tabDoctor
	numTabs
)

var tabKeys = [numTabs]string{"tab.skills", "tab.updates", "tab.repos", "tab.doctor"}

// scopeKind is what the scope switch cycles through.
type scopeKind int

const (
	scopeProject scopeKind = iota
	scopeGlobal
	scopeAll
	numScopes
)

var scopeKeys = [numScopes]string{"scope.project", "scope.global", "scope.all"}

// Options configures the TUI.
type Options struct {
	Project agents.Scope // the project scope: the git root of the working directory
}

// Run starts the TUI and returns when the user quits.
func Run(opts Options) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	i18n.Set(i18n.Detect(cfg.Lang, os.Getenv))
	_, err = tea.NewProgram(New(opts)).Run()
	return err
}

// Model is the root model: tab bar, scope switch, the active tab and the
// footer.
type Model struct {
	opts   Options
	st     styles
	isDark bool

	width, height int

	tab   tab
	scope scopeKind

	// counts shown in the tab bar, valid once countsLoaded.
	countsLoaded    bool
	pending, issues int

	status    string
	statusErr bool

	skills  skillsModel
	updates updatesModel
	repos   reposModel

	modal    modalState
	applying bool   // an operation is running in a command; input is ignored meanwhile
	busyKey  string // i18n key of what it is doing
	spin     spinner.Model

	help help.Model
}

func New(opts Options) Model {
	m := Model{opts: opts, isDark: true, help: help.New(), skills: newSkillsModel(), updates: newUpdatesModel(), repos: newReposModel(), spin: spinner.New(spinner.WithSpinner(spinner.Dot))}
	m.restyle()
	return m
}

// restyle rebuilds the styles for the current background.
func (m *Model) restyle() {
	m.st = newStyles(m.isDark)
	m.help.Styles = m.st.Help
	m.spin.Style = lipgloss.NewStyle().Foreground(m.st.accent)
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, loadCounts(), m.loadSkills())
}

// loadSkills loads the Skills tab's data for the current scope.
func (m Model) loadSkills() tea.Cmd { return loadSkills(m.scope, m.opts.Project) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := m.update(msg)
	// The footer's height can change (help toggle, status line), and so the
	// room left for the tab.
	m.skills.setSize(m.bodySize())
	m.updates.setSize(m.bodySize())
	m.repos.setSize(m.bodySize())
	return m, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		m.restyle()
	case countsMsg:
		if msg.err != nil {
			m.setError(msg.err)
			break
		}
		m.countsLoaded, m.pending, m.issues = true, msg.pending, msg.issues
	case skillsMsg:
		switch {
		case msg.kind != m.scope:
			// Answer to a scope the user has already left.
		case msg.err != nil:
			m.setError(msg.err)
		default:
			m.skills.setData(msg.data, msg.kind)
		}
	case previewMsg:
		if m.modal.open && msg.kind == m.modal.kind {
			m.modal.loading, m.modal.plans, m.modal.manifest, m.modal.openErr = false, msg.plans, msg.manifest, msg.err
		}
	case applyMsg:
		m.applying = false
		delete(m.skills.pend, msg.kind)
		m.status, m.statusErr = applySummary(msg.done, msg.err)
		return m, tea.Batch(loadCounts(), m.loadSkills())
	case updatesMsg:
		if msg.fetched {
			m.applying = false
		}
		if msg.err != nil {
			m.setError(msg.err)
			break
		}
		m.updates.setItems(msg.items)
		cmds := []tea.Cmd{m.updates.wantDiff()}
		if msg.fetched {
			m.status, m.statusErr = i18n.T("updates.checked.ok"), false
			if len(msg.items) > 0 {
				m.status = i18n.T("updates.checked.n", len(msg.items))
			}
			cmds = append(cmds, loadCounts())
		}
		return m, tea.Batch(cmds...)
	case diffMsg:
		m.updates.setDiff(msg)
	case fullDiffMsg:
		if msg.err != nil {
			m.setError(msg.err)
			break
		}
		m.updates.full, m.updates.fullOf = true, msg.repo
		m.updates.vp.SetContent(msg.text)
		if strings.TrimSpace(msg.text) == "" {
			m.updates.vp.SetContent(i18n.T("updates.nodiff"))
		}
		m.updates.vp.GotoTop()
	case updateDoneMsg:
		m.applying = false
		m.status, m.statusErr = updateSummary(msg.done, msg.err)
		return m, tea.Batch(loadCounts(), loadUpdates(false), m.loadSkills())
	case reposMsg:
		if msg.err != nil {
			m.setError(msg.err)
			break
		}
		m.repos.setRows(msg.rows)
		return m, m.repos.wantInfo()
	case repoInfoMsg:
		m.repos.infos[msg.id] = msg
	case addRepoMsg:
		m.applying = false
		if msg.err != nil {
			m.status, m.statusErr = i18n.T("apply.failed", msg.err), true
			break
		}
		m.status, m.statusErr = i18n.T("repos.added", shortRepo(msg.plan.ID), msg.skills, shortPath(msg.plan.Dir, agents.Scope{Global: true})), false
		return m, tea.Batch(loadRepos(), loadCounts(), m.loadSkills())
	case removePlanMsg:
		return m.openRemoveDialog(msg), nil
	case removeDoneMsg:
		m.applying = false
		if msg.err != nil {
			m.status, m.statusErr = i18n.T("apply.failed", msg.err), true
		} else {
			m.status, m.statusErr = i18n.T("repos.removed", shortRepo(msg.id), len(msg.removed)), false
		}
		return m, tea.Batch(loadRepos(), loadCounts(), m.loadSkills(), loadUpdates(false))
	case spinner.TickMsg:
		if m.applying {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	default:
		// Cursor blinks and the like belong to the search box.
		if m.tab == tabSkills && m.skills.capturing() {
			var cmd tea.Cmd
			m.skills, cmd = m.skills.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) setError(err error) {
	m.status, m.statusErr = errText(err), true
}

func (m Model) handleKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if m.modal.open {
		return m.modalKey(msg)
	}
	if m.applying {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	k := newKeyMap(nil, nil)
	if key.Matches(msg, k.Quit) && (msg.String() == "ctrl+c" || !m.capturing()) {
		return m, tea.Quit
	}
	if m.capturing() {
		var cmd tea.Cmd
		if m.tab == tabUpdates {
			m.updates, cmd = m.updates.Update(msg)
		} else {
			m.skills, cmd = m.skills.Update(msg)
		}
		return m, cmd
	}
	if m.tab == tabUpdates {
		if m2, cmd, ok := m.updatesKey(msg); ok {
			return m2, cmd
		}
	}
	if m.tab == tabRepos {
		if m2, cmd, ok := m.reposKey(msg); ok {
			return m2, cmd
		}
	}
	switch {
	case key.Matches(msg, k.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	case key.Matches(msg, k.Scope):
		m.scope = (m.scope + 1) % numScopes
		m.status = ""
		m.skills.reset(m.scope)
		return m, m.loadSkills()
	case key.Matches(msg, k.Refresh):
		m.status = ""
		cmds := []tea.Cmd{loadCounts(), m.loadSkills()}
		if m.tab == tabRepos {
			cmds = append(cmds, loadRepos())
		}
		return m, tea.Batch(cmds...)
	}
	switch msg.String() {
	case "1", "2", "3", "4":
		return m.switchTab(tab(msg.String()[0] - '1'))
	case "tab":
		return m.switchTab((m.tab + 1) % numTabs)
	case "shift+tab":
		return m.switchTab((m.tab + numTabs - 1) % numTabs)
	}
	if m.tab == tabSkills && msg.String() == "enter" {
		return m.startPreview()
	}
	if m.tab == tabSkills {
		m.status, m.statusErr = "", false
		var cmd tea.Cmd
		m.skills, cmd = m.skills.Update(msg)
		if m.skills.notice != "" {
			m.status, m.statusErr, m.skills.notice = m.skills.notice, false, ""
		}
		return m, cmd
	}
	return m, nil
}

// capturing reports whether the active tab is reading text, in which case
// letters and digits are not commands.
func (m Model) capturing() bool {
	return (m.tab == tabSkills && m.skills.capturing()) || (m.tab == tabUpdates && m.updates.capturing())
}

// switchTab shows a tab and loads its data.
func (m Model) switchTab(t tab) (Model, tea.Cmd) {
	m.tab = t
	switch t {
	case tabUpdates:
		return m, loadUpdates(false)
	case tabRepos:
		return m, loadRepos()
	}
	return m, nil
}

// reposKey handles the keys that act on the Repos tab.
func (m Model) reposKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	k := newRepoKeys()
	m.status, m.statusErr = "", false
	row := m.repos.selected()
	switch {
	case key.Matches(msg, k.Add):
		return m.openAddDialog()
	case key.Matches(msg, k.Remove):
		if row == nil {
			return m, nil, true
		}
		return m, removePlanCmd(row.ID), true
	case key.Matches(msg, k.Skills):
		if row != nil {
			m.skills.setQuery(row.ID)
			m.tab = tabSkills
		}
		return m, nil, true
	case key.Matches(msg, k.Updates):
		if row == nil {
			return m, nil, true
		}
		if row.Behind <= 0 {
			m.status = i18n.T("repos.noupdates")
			return m, nil, true
		}
		m.updates.wantSelect = row.ID
		m2, cmd := m.switchTab(tabUpdates)
		return m2, cmd, true
	case key.Matches(msg, k.Up), key.Matches(msg, k.Down), key.Matches(msg, k.PgUp), key.Matches(msg, k.PgDown):
		var cmd tea.Cmd
		m.repos, cmd = m.repos.Update(msg)
		return m, cmd, true
	}
	return m, nil, false
}

// openAddDialog asks for a repository reference.
func (m Model) openAddDialog() (Model, tea.Cmd, bool) {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetWidth(48)
	focus := ti.Focus()
	m = m.openDialog(modalState{
		title: i18n.T("repos.add.title"), prompt: i18n.T("repos.add.prompt"), input: ti, hasInput: true,
		body: []string{m.st.Dim.Render(i18n.T("repos.add.hint"))}, okKey: "repos.add.ok",
		onOK: func(m Model, ref string) (Model, tea.Cmd) {
			m, tick := m.startBusy("repos.adding")
			return m, tea.Batch(addRepoCmd(ref), tick)
		},
	})
	return m, focus, true
}

// openRemoveDialog shows what removing a repo deletes, or why it is refused.
func (m Model) openRemoveDialog(msg removePlanMsg) Model {
	id := msg.plan.Repo
	d := modalState{title: i18n.T("repos.remove.title", shortRepo(id)), okKey: "repos.remove.ok", button: btnBack}
	switch {
	case msg.open != nil:
		d.body, d.disabled = []string{m.st.Danger.Render("✗ " + errText(msg.open))}, true
	default:
		if len(msg.plan.Links) > 0 {
			d.body = append(d.body, m.st.Bold.Render(i18n.T("repos.remove.links")))
			for _, l := range msg.plan.Links {
				d.body = append(d.body, "  "+m.st.Pending.Render("− "+shortPath(l, agents.Scope{Global: true})))
			}
		}
		if len(msg.plan.Pins) > 0 {
			d.body = append(d.body, m.st.Bold.Render(i18n.T("repos.remove.pins")))
			for _, p := range msg.plan.Pins {
				d.body = append(d.body, "  "+m.st.Pending.Render("− "+shortPath(p, agents.Scope{Global: true})))
			}
		}
		if len(d.body) == 0 {
			d.body = []string{m.st.Dim.Render(i18n.T("repos.remove.none"))}
		}
		if msg.err != nil {
			d.disabled = true
			d.body = append(d.body, "", m.st.Danger.Render("✗ "+i18n.T("repos.remove.refused")))
			for _, l := range strings.Split(msg.err.Error(), "\n") {
				d.body = append(d.body, "  "+m.st.Danger.Render(l))
			}
		}
	}
	d.onOK = func(m Model, _ string) (Model, tea.Cmd) {
		m, tick := m.startBusy("repos.removing")
		return m, tea.Batch(removeRepoCmd(id), tick)
	}
	return m.openDialog(d)
}

// updatesKey handles the keys that act on the Updates tab. ok is false for
// keys it does not own (tab switching, scope, help and quit stay global).
func (m Model) updatesKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	k := newUpdateKeys()
	m.status, m.statusErr = "", false
	switch {
	case key.Matches(msg, k.Check):
		m, tick := m.startBusy("updates.checking")
		return m, tea.Batch(loadUpdates(true), tick), true
	case key.Matches(msg, k.Full):
		if it := m.updates.selected(); it != nil && it.Check.Err == nil {
			return m, loadFullDiff(it.Check), true
		}
		return m, nil, true
	case key.Matches(msg, k.Update):
		return m.confirmUpdate(), nil, true
	case key.Matches(msg, k.Up), key.Matches(msg, k.Down), key.Matches(msg, k.Mark):
		var cmd tea.Cmd
		m.updates, cmd = m.updates.Update(msg)
		return m, cmd, true
	}
	return m, nil, false
}

// confirmUpdate asks before updating the checked repos (or the selected one).
func (m Model) confirmUpdate() Model {
	checks := m.updates.toUpdate()
	if len(checks) == 0 {
		return m
	}
	var body []string
	for _, c := range checks {
		body = append(body, m.st.Bold.Render(c.Repo)+"  "+m.st.Dim.Render(i18n.T("updates.commits", c.Commits)))
		if n := names(c.Changes); n != "" {
			body = append(body, "  "+n)
		}
	}
	return m.openDialog(modalState{
		title: i18n.T("updates.confirm.title"), body: body, okKey: "updates.ok",
		onOK: func(m Model, _ string) (Model, tea.Cmd) {
			m, tick := m.startBusy("updates.updating")
			return m, tea.Batch(updateCmd(checks), tick)
		},
	})
}

// keys returns the key bindings for the footer.
func (m Model) keys() keyMap {
	if m.tab == tabSkills {
		return newKeyMap(m.skills.helpGroups(), m.skills.shortHelp())
	}
	switch m.tab {
	case tabUpdates:
		return newKeyMap(m.updates.helpGroups(), m.updates.shortHelp())
	case tabRepos:
		return newKeyMap(m.repos.helpGroups(), m.repos.shortHelp())
	}
	return newKeyMap(nil, nil)
}

// bodySize is the room the active tab gets between the header and footer.
func (m Model) bodySize() (w, h int) {
	if m.width == 0 || m.height == 0 {
		return 0, 0
	}
	h = m.height - lipgloss.Height(m.header()) - lipgloss.Height(m.footer())
	return m.width, max(h, 1)
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m Model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	_, bodyH := m.bodySize()
	body := fit(m.body(), m.width, bodyH)
	if m.modal.open {
		body = m.overlayModal(body, m.width, bodyH)
	}
	out := m.header() + "\n" + body + "\n" + m.footer()
	// Never exceed the window, however small it is.
	return fit(out, m.width, m.height)
}

// header is the tab bar on the left and the scope indicator on the right.
func (m Model) header() string {
	var tabs []string
	for t := tab(0); t < numTabs; t++ {
		label := fmt.Sprintf("%d %s", int(t)+1, i18n.T(tabKeys[t]))
		var badge string
		switch {
		case t == tabUpdates && m.countsLoaded && m.pending > 0:
			badge = m.st.Pending.Render(fmt.Sprintf(" (%d)", m.pending))
		case t == tabDoctor && m.countsLoaded && m.issues > 0:
			badge = m.st.Danger.Render(fmt.Sprintf(" (%d)", m.issues))
		}
		if t == m.tab {
			// Brackets as well as colour mark the active tab.
			tabs = append(tabs, m.st.Accent.Bold(true).Render("["+label+"]")+badge)
		} else {
			tabs = append(tabs, " "+m.st.Dim.Render(label)+badge+" ")
		}
	}
	left := strings.Join(tabs, " ")
	right := m.st.Dim.Render(i18n.T("scope.label", i18n.T(scopeKeys[m.scope])))
	if m.scope == scopeProject && m.opts.Project.Dir != "" {
		// Keep the end of a long path: that is the part that tells projects apart.
		dir := shortPath(m.opts.Project.Dir, agents.Scope{Global: true})
		if avail := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 3; lipgloss.Width(dir) > avail && avail > 1 {
			dir = "…" + ansi.TruncateLeft(dir, lipgloss.Width(dir)-avail+1, "")
		}
		right += " " + m.st.Dim.Render(dir)
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left+" "+right, m.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

// body renders the active tab.
func (m Model) body() string {
	var msg string
	switch m.tab {
	case tabUpdates:
		return m.updates.View(m.st)
	case tabRepos:
		return m.repos.View(m.st)
	case tabDoctor:
		msg = i18n.T("doctor.soon")
	default:
		return m.skills.View(m.st)
	}
	return "\n  " + m.st.Dim.Render(msg)
}

// footer is the status line followed by the key help.
func (m Model) footer() string {
	status := ""
	if m.applying {
		status = m.spin.View() + " " + m.st.Accent.Render(i18n.T(m.busyKey))
	} else if sum := m.skills.summary(); m.status == "" && m.tab == tabSkills && sum != "" {
		status = m.st.Pending.Render(sum)
	} else if m.status != "" {
		if m.statusErr {
			status = m.st.Danger.Render("! " + m.status)
		} else {
			status = m.st.Accent.Render(m.status)
		}
	}
	if m.modal.open {
		return ansi.Truncate(status, m.width, "…") + "\n" + m.st.Dim.Render(ansi.Truncate(i18n.T("modal.hint"), m.width, "…"))
	}
	return ansi.Truncate(status, m.width, "…") + "\n" + m.help.View(m.keys())
}

// fit cuts or pads s to exactly h lines of at most w cells. Widths are
// measured in terminal cells, so CJK text lines up.
func fit(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "…")
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// applySummary is the status line after an apply: what was done, then the
// error if one stopped it. The bool is true for an error.
func applySummary(done []applied, err error) (string, bool) {
	var parts []string
	for _, d := range done {
		if len(d.Enable) > 0 {
			parts = append(parts, i18n.T("apply.enabled", d.Name))
		}
		if len(d.Disable) > 0 {
			parts = append(parts, i18n.T("apply.disabled", d.Name))
		}
	}
	if err != nil {
		msg := i18n.T("apply.failed", err)
		if errors.Is(err, state.ErrLocked) {
			msg = errText(err)
		}
		return strings.Join(append(parts, msg), " · "), true
	}
	if len(parts) == 0 {
		return i18n.T("apply.nothing"), false
	}
	return strings.Join(parts, " · "), false
}

// updateSummary is the status line after an update: the repos updated, the
// restart hint when it applies, or the error that stopped it.
func updateSummary(done []updatedRepo, err error) (string, bool) {
	var parts []string
	restart := false
	for _, d := range done {
		parts = append(parts, i18n.T("updates.done", shortRepo(d.Repo)))
		restart = restart || restartNeeded(d.Serves)
	}
	if err != nil {
		msg := i18n.T("apply.failed", err)
		if errors.Is(err, state.ErrLocked) {
			msg = errText(err)
		}
		return strings.Join(append(parts, msg), " · "), true
	}
	if len(parts) == 0 {
		return i18n.T("apply.nothing"), false
	}
	if restart {
		parts = append(parts, i18n.T("updates.restart"))
	}
	return strings.Join(parts, " · "), false
}
