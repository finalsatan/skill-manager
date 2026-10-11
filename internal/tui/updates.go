package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"skillet/internal/deploy"
	"skillet/internal/i18n"
)

// now is replaced by tests.
var now = time.Now

// updateItem is a repo with upstream changes (or a failed check).
type updateItem struct {
	Check deploy.RepoCheck
	Last  time.Time
	// Serves are the agents that see an unpinned deployment from the repo.
	Serves []string
}

// diffInfo is the commit log and diffstat of one pending update.
type diffInfo struct {
	log, stat string
	err       error
}

// updatesMsg is the result of loadUpdates.
type updatesMsg struct {
	items   []updateItem
	fetched bool // the repos were fetched from the network first
	err     error
}

// diffMsg carries a repo's log and diffstat; fullDiffMsg its full diff.
type diffMsg struct {
	key string
	diffInfo
}

type fullDiffMsg struct {
	repo string
	text string
	err  error
}

// updateKey identifies one pending update: the repo and the commit it moves to.
func updateKey(c deploy.RepoCheck) string { return c.Repo + "@" + c.Upstream }

// loadUpdates reads the pending updates: from the last fetch, or with fetch
// from the network first. It opens the store itself and closes it again.
func loadUpdates(fetch bool) tea.Cmd {
	return func() tea.Msg {
		msg := updatesMsg{fetched: fetch}
		msg.err = withManager(func(m *deploy.Manager) error {
			var res []deploy.RepoCheck
			var err error
			if fetch {
				res, err = m.Check(nil)
			} else {
				res, err = m.Pending(nil)
			}
			if err != nil {
				return err
			}
			for _, c := range res {
				if c.UpToDate() {
					continue
				}
				it := updateItem{Check: c, Serves: m.RepoServes(c.Repo)}
				if r := m.St.Repos[c.Repo]; r != nil {
					it.Last = r.LastChecked
				}
				msg.items = append(msg.items, it)
			}
			return nil
		})
		return msg
	}
}

// loadDiff reads the commit log and diffstat of a pending update.
func loadDiff(c deploy.RepoCheck) tea.Cmd {
	return func() tea.Msg {
		msg := diffMsg{key: updateKey(c)}
		msg.err = withManager(func(m *deploy.Manager) error {
			var err error
			msg.log, msg.stat, err = m.RepoDiff(c, false)
			return err
		})
		return msg
	}
}

// loadFullDiff reads the full diff of a pending update.
func loadFullDiff(c deploy.RepoCheck) tea.Cmd {
	return func() tea.Msg {
		msg := fullDiffMsg{repo: c.Repo}
		msg.err = withManager(func(m *deploy.Manager) error {
			var err error
			_, msg.text, err = m.RepoDiff(c, true)
			return err
		})
		return msg
	}
}

// updatedRepo is one repo that was updated.
type updatedRepo struct {
	Repo     string
	From, To string
	Notes    []string
	Serves   []string
}

// updateDoneMsg is the result of updateCmd.
type updateDoneMsg struct {
	done []updatedRepo
	err  error
}

// updateRepos updates each repo in turn and stops at the first error.
func updateRepos(m *deploy.Manager, checks []deploy.RepoCheck) ([]updatedRepo, error) {
	var done []updatedRepo
	for _, c := range checks {
		r, err := m.Update(c)
		if err != nil {
			return done, fmt.Errorf("%s: %w", c.Repo, err)
		}
		done = append(done, updatedRepo{Repo: c.Repo, From: r.From, To: r.To, Notes: r.Notes, Serves: m.RepoServes(c.Repo)})
	}
	return done, nil
}

// updateCmd opens the store, updates the repos and closes it. Update refuses
// if a repo moved since it was checked.
func updateCmd(checks []deploy.RepoCheck) tea.Cmd {
	return func() tea.Msg {
		var msg updateDoneMsg
		msg.err = withManager(func(m *deploy.Manager) error {
			var err error
			msg.done, err = updateRepos(m, checks)
			return err
		})
		return msg
	}
}

// updatesModel is the Updates tab.
type updatesModel struct {
	items   []updateItem
	loaded  bool
	checked map[string]bool
	cursor  int
	diffs   map[string]diffInfo
	asked   map[string]bool // diffs already requested
	w, h    int

	full   bool // the full-diff viewer is open
	vp     viewport.Model
	fullOf string

	wantSelect string // repo to select when the items arrive
}

func newUpdatesModel() updatesModel {
	return updatesModel{checked: map[string]bool{}, diffs: map[string]diffInfo{}, asked: map[string]bool{}, vp: viewport.New()}
}

func (m *updatesModel) setSize(w, h int) {
	m.w, m.h = w, h
	m.vp.SetWidth(w)
	m.vp.SetHeight(max(h-2, 1))
}

func (m updatesModel) capturing() bool { return m.full }

func (m *updatesModel) setItems(items []updateItem) {
	var prev string
	if it := m.selected(); it != nil {
		prev = it.Check.Repo
	}
	if m.wantSelect != "" {
		prev, m.wantSelect = m.wantSelect, ""
	}
	m.items, m.loaded = items, true
	m.cursor = 0
	live := map[string]bool{}
	for i, it := range items {
		live[it.Check.Repo] = true
		if it.Check.Repo == prev {
			m.cursor = i
		}
	}
	for r := range m.checked {
		if !live[r] {
			delete(m.checked, r)
		}
	}
}

func (m updatesModel) selected() *updateItem {
	if m.cursor < len(m.items) {
		return &m.items[m.cursor]
	}
	return nil
}

// selectRepo moves the cursor to a repo, if it has pending changes.
func (m *updatesModel) selectRepo(id string) {
	for i, it := range m.items {
		if it.Check.Repo == id {
			m.cursor = i
		}
	}
}

// wantDiff returns the command that loads the selected repo's log and
// diffstat when it has not been asked for yet.
func (m *updatesModel) wantDiff() tea.Cmd {
	it := m.selected()
	if it == nil || it.Check.Err != nil {
		return nil
	}
	k := updateKey(it.Check)
	if m.asked[k] {
		return nil
	}
	m.asked[k] = true
	return loadDiff(it.Check)
}

func (m *updatesModel) setDiff(d diffMsg) { m.diffs[d.key] = d.diffInfo }

// toUpdate is the repos the update would touch: the checked ones, or the
// selected one when none is checked.
func (m updatesModel) toUpdate() []deploy.RepoCheck {
	var out []deploy.RepoCheck
	for _, it := range m.items {
		if m.checked[it.Check.Repo] && it.Check.Err == nil {
			out = append(out, it.Check)
		}
	}
	if len(out) == 0 {
		if it := m.selected(); it != nil && it.Check.Err == nil {
			out = append(out, it.Check)
		}
	}
	return out
}

type updateKeys struct {
	Up, Down, Mark, Check, Full, Update, Close key.Binding
}

func newUpdateKeys() updateKeys {
	return updateKeys{
		Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", i18n.T("help.move"))),
		Down:   key.NewBinding(key.WithKeys("down", "j")),
		Mark:   key.NewBinding(key.WithKeys("space"), key.WithHelp("space", i18n.T("help.mark"))),
		Check:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", i18n.T("help.check"))),
		Full:   key.NewBinding(key.WithKeys("D"), key.WithHelp("D", i18n.T("help.fulldiff"))),
		Update: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", i18n.T("help.update"))),
		Close:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", i18n.T("help.back"))),
	}
}

func (m updatesModel) helpGroups() [][]key.Binding {
	k := newUpdateKeys()
	if m.full {
		return [][]key.Binding{{k.Up, k.Close}}
	}
	return [][]key.Binding{{k.Up, k.Mark}, {k.Check, k.Full, k.Update}}
}

func (m updatesModel) shortHelp() []key.Binding {
	k := newUpdateKeys()
	if m.full {
		return []key.Binding{k.Up, k.Close}
	}
	return []key.Binding{k.Up, k.Mark, k.Update, k.Full}
}

// Update handles a key. Keys that start work (r, D, enter) are handled by the
// root, which owns the commands and dialogs; this moves the cursor, marks
// repos and scrolls the full-diff viewer.
func (m updatesModel) Update(msg tea.KeyPressMsg) (updatesModel, tea.Cmd) {
	k := newUpdateKeys()
	if m.full {
		if key.Matches(msg, k.Close) {
			m.full = false
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	switch {
	case key.Matches(msg, k.Up):
		m.cursor = max(m.cursor-1, 0)
	case key.Matches(msg, k.Down):
		m.cursor = min(m.cursor+1, max(len(m.items)-1, 0))
	case key.Matches(msg, k.Mark):
		if it := m.selected(); it != nil && it.Check.Err == nil {
			if m.checked[it.Check.Repo] {
				delete(m.checked, it.Check.Repo)
			} else {
				m.checked[it.Check.Repo] = true
			}
		}
	}
	return m, m.wantDiff()
}

// ago renders how long ago t was, coarsely.
func ago(t time.Time) string {
	d := now().Sub(t)
	switch {
	case d < time.Minute:
		return i18n.T("ago.now")
	case d < time.Hour:
		return i18n.T("ago.min", int(d/time.Minute))
	case d < 48*time.Hour:
		return i18n.T("ago.hour", int(d/time.Hour))
	}
	return i18n.T("ago.day", int(d/(24*time.Hour)))
}

func names(cs []deploy.SkillChange) string {
	var ns []string
	for _, c := range cs {
		ns = append(ns, c.Name)
	}
	return strings.Join(ns, ", ")
}

// itemLines renders one repo of the left pane.
func (m updatesModel) itemLines(it updateItem, sel bool, w int, st styles) []string {
	c := it.Check
	box := "[ ]"
	if m.checked[c.Repo] {
		box = "[x]"
	}
	if c.Err != nil {
		box = " ✗ "
	}
	marker := "  "
	if sel {
		marker = "▸ "
	}
	head := marker + box + " " + shortRepo(c.Repo)
	if c.Err == nil {
		head += "  " + i18n.T("updates.commits", c.Commits)
	}
	lines := []string{head}
	indent := "      "
	switch {
	case c.Err != nil:
		lines = append(lines, indent+c.Err.Error())
	default:
		if n := names(c.Changes); n != "" {
			lines = append(lines, indent+n)
		}
	}
	var warn []string
	code, pinned := false, 0
	for _, ch := range c.Changes {
		code = code || ch.Code
		pinned += ch.Pinned
		if ch.Removed {
			warn = append(warn, i18n.T("updates.removed", ch.Name))
		}
	}
	if len(c.NewSkills) > 0 {
		warn = append(warn, i18n.T("updates.newskills", strings.Join(c.NewSkills, ", ")))
	}
	if code {
		warn = append(warn, "⚠ "+i18n.T("updates.scripts"))
	}
	if pinned > 0 {
		warn = append(warn, i18n.T("updates.pinned", pinned))
	}
	nWarn := 0
	if c.Err == nil {
		for _, wl := range warn {
			lines = append(lines, indent+wl)
		}
		nWarn = len(warn)
	}
	for i, l := range lines {
		l = padTo(l, w)
		switch {
		case sel:
			l = st.Cursor.Render(l)
		case i == 0:
			l = st.Bold.Render(l)
		case c.Err != nil:
			l = st.Danger.Render(l)
		case i >= len(lines)-nWarn:
			l = st.Pending.Render(l)
		default:
			l = st.Dim.Render(l)
		}
		lines[i] = l
	}
	return lines
}

// detailLines renders the right pane for the selected repo.
func (m updatesModel) detailLines(it updateItem, st styles) []string {
	c := it.Check
	out := []string{st.Bold.Render(c.Repo)}
	if !it.Last.IsZero() {
		out = append(out, st.Dim.Render(i18n.T("updates.last", ago(it.Last))))
	}
	if c.Err != nil {
		return append(out, "", st.Danger.Render(i18n.T("updates.diverged", c.Err)))
	}
	d, ok := m.diffs[updateKey(c)]
	out = append(out, "", st.Accent.Render(i18n.T("updates.log")))
	switch {
	case !ok:
		out = append(out, "  "+st.Dim.Render(i18n.T("loading")))
	case d.err != nil:
		out = append(out, "  "+st.Danger.Render(d.err.Error()))
	default:
		for _, l := range strings.Split(d.log, "\n") {
			out = append(out, "  "+l)
		}
		out = append(out, "", st.Accent.Render(i18n.T("updates.stat")))
		if strings.TrimSpace(d.stat) == "" {
			out = append(out, "  "+st.Dim.Render(i18n.T("updates.nodiff")))
		}
		for _, l := range strings.Split(d.stat, "\n") {
			if strings.TrimSpace(l) != "" {
				out = append(out, "  "+l)
			}
		}
	}
	out = append(out, "", st.Accent.Render(i18n.T("updates.after")))
	for _, ch := range c.Changes {
		if ch.Deployed > 0 {
			out = append(out, "  "+i18n.T("updates.follow", ch.Name, ch.Deployed))
		} else {
			out = append(out, "  "+st.Dim.Render(i18n.T("updates.nodeploy", ch.Name)))
		}
	}
	if restartNeeded(it.Serves) {
		out = append(out, "", st.Pending.Render(i18n.T("updates.restart")))
	}
	return out
}

// restartNeeded reports whether an agent other than Claude Code, which reloads
// skills by itself, sees the repo's updated deployments.
func restartNeeded(serves []string) bool {
	for _, a := range serves {
		if a != "claude" {
			return true
		}
	}
	return false
}

func (m updatesModel) View(st styles) string {
	if m.w == 0 {
		return ""
	}
	if m.full {
		hint := st.Dim.Render(i18n.T("updates.diff.hint") + fmt.Sprintf("  %3.0f%%", m.vp.ScrollPercent()*100))
		title := st.Bold.Render(m.fullOf)
		return title + "\n" + m.vp.View() + "\n" + hint
	}
	if !m.loaded {
		return "\n  " + st.Dim.Render(i18n.T("loading"))
	}
	if len(m.items) == 0 {
		return "\n  " + st.Accent.Render(i18n.T("updates.empty")) + "\n  " + st.Dim.Render(i18n.T("updates.hint"))
	}
	lw := min(max(m.w*45/100, 30), m.w-10)
	rw := max(m.w-lw-3, 1)
	var left []string
	selStart, selEnd := 0, 0
	for i, it := range m.items {
		ls := m.itemLines(it, i == m.cursor, lw, st)
		if i == m.cursor {
			selStart, selEnd = len(left), len(left)+len(ls)
		}
		left = append(left, ls...)
	}
	off := 0
	if selEnd > m.h {
		off = selEnd - m.h
	}
	off = min(off, selStart)
	if off > len(left) {
		off = 0
	}
	left = left[off:]
	var right []string
	if it := m.selected(); it != nil {
		for _, l := range m.detailLines(*it, st) {
			right = append(right, strings.Split(ansi.Wrap(l, rw, ""), "\n")...)
		}
	}
	var b strings.Builder
	for i := 0; i < m.h; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = ansi.Truncate(right[i], rw, "…")
		}
		l += strings.Repeat(" ", max(lw-lipgloss.Width(l), 0))
		b.WriteString(l + st.Dim.Render(" │ ") + r)
		if i < m.h-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
