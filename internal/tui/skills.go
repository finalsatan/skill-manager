package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"skillet/internal/agents"
	"skillet/internal/i18n"
)

// item is one line of the list: a scope section header (all-scopes view,
// never selectable), a repo group header (grouped view) or a row.
type item struct {
	header string
	grp    *groupHeader
	r      *row
	sec    int // index into data.Sections
}

// selectable reports whether the cursor may rest on the item.
func (it item) selectable() bool { return it.r != nil || it.grp != nil }

// groupHeader is the header line of a repo group and what the detail pane
// says about it.
type groupHeader struct {
	key      string // repo id, or unmanagedKey
	title    string
	total    int // skills in the group
	shown    int // of those, matching the filters
	enabled  int
	pending  int // pending cell changes
	updates  int // skills with an upstream update
	repoDir  string
	head     string
	expanded bool // the stored fold state
	open     bool // its rows are listed: expanded, or a search is active
}

// unmanagedKey is the group of entries skillet did not create.
const unmanagedKey = "\x00unmanaged"

// groupKey is the group a row belongs to: its repo, or the unmanaged group.
func groupKey(r *row) string {
	if !r.managed() {
		return unmanagedKey
	}
	return r.Repo
}

// skillsModel is the Skills tab: a matrix of skills by agent with a detail
// pane for the selected row.
type skillsModel struct {
	data    skillsData
	all     bool // data holds one section per deployed scope
	loaded  bool
	items   []item
	total   int // rows before filtering
	shown   int // rows matching the filters, folded or not
	cursor  int // index into items; always a row when there is one
	offset  int // first visible item
	w, h    int
	listH   int
	only    bool // only skills enabled for some agent
	query   string
	search  textinput.Model
	editing bool // the search box has focus and gets every key

	kind   scopeKind // the scope the data belongs to
	col    int       // focused agent column
	pend   map[scopeKind]pendingSet
	notice string // a message for the status line, taken by the root

	flat bool                          // one list of skills instead of repo groups
	fold map[scopeKind]map[string]bool // group key -> expanded, per scope
}

func newSkillsModel() skillsModel {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetWidth(30)
	return skillsModel{search: ti, pend: map[scopeKind]pendingSet{}, fold: map[scopeKind]map[string]bool{}}
}

// capturing reports whether the tab wants every key, so the root must not
// treat q, digits or s as commands.
func (m skillsModel) capturing() bool { return m.editing }

func (m *skillsModel) setData(d skillsData, kind scopeKind) {
	m.data, m.kind, m.all, m.loaded = d, kind, kind == scopeAll, true
	m.col = min(m.col, max(len(d.Agents)-1, 0))
	// Groups seen for the first time start collapsed; known groups keep their
	// fold state.
	if !m.all {
		if m.fold[kind] == nil {
			m.fold[kind] = map[string]bool{}
		}
		live := map[string]bool{}
		for _, sec := range d.Sections {
			for key := range m.groupStats(sec.Rows) {
				live[key] = true
				if _, ok := m.fold[kind][key]; !ok {
					m.fold[kind][key] = false
				}
			}
		}
		for key := range m.fold[kind] {
			if !live[key] {
				delete(m.fold[kind], key) // the repo is gone
			}
		}
	}
	m.rebuild()
}

// grouped reports whether the list is shown in repo groups.
func (m skillsModel) grouped() bool { return !m.flat && !m.all }

// indent is how far skill rows are indented under their group header.
func (m skillsModel) indent() int {
	if m.grouped() {
		return 2
	}
	return 0
}

// groupStats summarises the rows by group, before any filtering.
func (m skillsModel) groupStats(rows []row) map[string]*groupHeader {
	out := map[string]*groupHeader{}
	for i := range rows {
		r := &rows[i]
		key := groupKey(r)
		g := out[key]
		if g == nil {
			g = &groupHeader{key: key, title: shortRepo(r.Repo), repoDir: r.RepoDir, head: r.Head}
			if key == unmanagedKey {
				g.title = i18n.T("group.unmanaged")
			}
			out[key] = g
		}
		g.total++
		if r.enabled() {
			g.enabled++
		}
		if r.Pending {
			g.updates++
		}
		if g.repoDir == "" {
			g.repoDir, g.head = r.RepoDir, r.Head
		}
		if r.managed() {
			g.pending += len(m.pend[m.kind][r.ID])
		}
	}
	return out
}

// reset drops the data while a new scope loads. Pending changes are kept per
// scope, so they are still there when the user comes back.
func (m *skillsModel) reset(kind scopeKind) {
	*m = skillsModel{search: m.search, only: m.only, query: m.query, w: m.w, h: m.h, kind: kind, pend: m.pend, flat: m.flat, fold: m.fold}
	m.layout()
}

func (m *skillsModel) setSize(w, h int) {
	m.w, m.h = w, h
	m.layout()
}

// layout splits the height: search line, column titles, list, legend, detail.
func (m *skillsModel) layout() {
	detail := 0
	switch {
	case m.h >= 18:
		detail = 9
	case m.h >= 12:
		detail = 5
	}
	m.listH = m.h - 3 - detail
	if m.listH < 1 {
		m.listH = 1
	}
	m.scrollToCursor()
}

// itemKey identifies a selectable item across rebuilds.
func itemKey(it item) string {
	switch {
	case it.r != nil:
		return fmt.Sprintf("r:%d/%s", it.sec, it.r.Key)
	case it.grp != nil:
		return "g:" + it.grp.key
	}
	return ""
}

// rebuild recomputes the visible items from the data and the filters, keeping
// the selection when the item is still there. A row that is now folded away
// leaves the cursor on its group's header, and a header that is gone (flat
// view) on the group's first row.
func (m *skillsModel) rebuild() {
	prev, prevGroup, prevHeader := "", "", false
	if m.cursor < len(m.items) {
		it := m.items[m.cursor]
		prev = itemKey(it)
		switch {
		case it.r != nil:
			prevGroup = groupKey(it.r)
		case it.grp != nil:
			prevGroup, prevHeader = it.grp.key, true
		}
	}
	m.items, m.total, m.shown = nil, 0, 0
	for si := range m.data.Sections {
		sec := &m.data.Sections[si]
		m.total += len(sec.Rows)
		rows := filterRows(sec.Rows, m.only, m.query)
		m.shown += len(rows)
		if len(rows) == 0 {
			continue
		}
		switch {
		case m.all:
			title := sec.Scope.Dir
			if sec.Scope.Global {
				title = i18n.T("section.global")
			}
			m.items = append(m.items, item{header: title, sec: si})
			for i := range rows {
				m.items = append(m.items, item{r: &rows[i], sec: si})
			}
		case m.flat:
			for i := range rows {
				m.items = append(m.items, item{r: &rows[i], sec: si})
			}
		default:
			m.groupItems(si, sec.Rows, rows)
		}
	}
	m.cursor = m.edgeIndex(false)
	found := false
	for i, it := range m.items {
		if it.selectable() && prev != "" && itemKey(it) == prev {
			m.cursor, found = i, true
			break
		}
	}
	if !found && prevGroup != "" {
		for i, it := range m.items {
			if (!prevHeader && it.grp != nil && it.grp.key == prevGroup) || (prevHeader && it.r != nil && groupKey(it.r) == prevGroup) {
				m.cursor = i
				break
			}
		}
	}
	m.scrollToCursor()
}

// groupItems appends the grouped list of one section: a header per repo in
// id order, unmanaged entries last, and under each expanded header its rows.
// all are the section's rows, rows those matching the filters.
func (m *skillsModel) groupItems(si int, all, rows []row) {
	stats := m.groupStats(all)
	buckets := map[string][]int{}
	for i := range rows {
		k := groupKey(&rows[i])
		buckets[k] = append(buckets[k], i)
	}
	var keys []string
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == unmanagedKey) != (keys[j] == unmanagedKey) {
			return keys[j] == unmanagedKey
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		g := *stats[k]
		g.shown = len(buckets[k])
		g.expanded = m.fold[m.kind][k]
		g.open = g.expanded || m.query != ""
		m.items = append(m.items, item{grp: &g, sec: si})
		if !g.open {
			continue
		}
		for _, i := range buckets[k] {
			m.items = append(m.items, item{r: &rows[i], sec: si})
		}
	}
}

// onHeader reports whether the cursor is on a group header.
func (m skillsModel) onHeader() bool {
	return m.cursor < len(m.items) && m.items[m.cursor].grp != nil
}

// toggleFold folds or unfolds the group under the cursor. While a search is
// active every group with matches is open, so there is nothing to fold.
func (m *skillsModel) toggleFold() {
	if !m.onHeader() || m.query != "" {
		return
	}
	k := m.items[m.cursor].grp.key
	m.fold[m.kind][k] = !m.fold[m.kind][k]
	m.rebuild()
}

// foldAll collapses every group, or expands them all when none is expanded.
func (m *skillsModel) foldAll() {
	if m.query != "" || m.all || m.flat {
		return
	}
	any := false
	for _, open := range m.fold[m.kind] {
		any = any || open
	}
	for k := range m.fold[m.kind] {
		m.fold[m.kind][k] = !any
	}
	m.rebuild()
}

// toggleView switches between the grouped and the flat list.
func (m *skillsModel) toggleView() {
	if m.all {
		m.notice = i18n.T("view.allflat")
		return
	}
	m.flat = !m.flat
	m.rebuild()
}

func (m skillsModel) selected() *item {
	if m.cursor < len(m.items) && m.items[m.cursor].r != nil {
		return &m.items[m.cursor]
	}
	return nil
}

// move steps to the next selectable item in direction d, skipping scope
// section headers.
func (m *skillsModel) move(d int) {
	for i := m.cursor + d; i >= 0 && i < len(m.items); i += d {
		if m.items[i].selectable() {
			m.cursor = i
			break
		}
	}
	m.scrollToCursor()
}

func (m *skillsModel) moveBy(n int) {
	d := 1
	if n < 0 {
		d, n = -1, -n
	}
	for range n {
		before := m.cursor
		m.move(d)
		if m.cursor == before {
			break
		}
	}
}

// edgeIndex is the index of the first (or last) selectable item, 0 when there
// is none.
func (m skillsModel) edgeIndex(last bool) int {
	idx := 0
	for i, it := range m.items {
		if !it.selectable() {
			continue
		}
		idx = i
		if !last {
			break
		}
	}
	return idx
}

func (m *skillsModel) jump(last bool) {
	m.cursor = m.edgeIndex(last)
	m.scrollToCursor()
}

func (m *skillsModel) scrollToCursor() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	// Show the scope section header above the first row of a section.
	if m.cursor == m.offset && m.cursor > 0 && m.items[m.cursor-1].r == nil && m.items[m.cursor-1].grp == nil {
		m.offset = m.cursor - 1
	}
	if m.listH > 0 && m.cursor >= m.offset+m.listH {
		m.offset = m.cursor - m.listH + 1
	}
	if m.offset < 0 || m.offset >= len(m.items) {
		m.offset = max(min(m.offset, len(m.items)-1), 0)
	}
}

type skillKeys struct {
	Up, Down, PgUp, PgDown, Top, Bottom, Filter, Search key.Binding
	Confirm, Clear                                      key.Binding
	Left, Right, Toggle, ToggleRow, Review              key.Binding
	Pin, Alias, View, FoldAll, Fold, Flat, Done         key.Binding
}

func newSkillKeys() skillKeys {
	return skillKeys{
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", i18n.T("help.move"))),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", i18n.T("help.move"))),
		PgUp:      key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp/PgDn", i18n.T("help.page"))),
		PgDown:    key.NewBinding(key.WithKeys("pgdown")),
		Top:       key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g/G", i18n.T("help.ends"))),
		Bottom:    key.NewBinding(key.WithKeys("G", "end")),
		Filter:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", i18n.T("help.filter"))),
		Search:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", i18n.T("help.search"))),
		Left:      key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/→", i18n.T("help.cell"))),
		Right:     key.NewBinding(key.WithKeys("right", "l")),
		Toggle:    key.NewBinding(key.WithKeys("space"), key.WithHelp("space", i18n.T("help.toggle"))),
		ToggleRow: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", i18n.T("help.row"))),
		Pin:       key.NewBinding(key.WithKeys("p"), key.WithHelp("p", i18n.T("help.pin"))),
		Alias:     key.NewBinding(key.WithKeys("R"), key.WithHelp("R", i18n.T("help.alias"))),
		View:      key.NewBinding(key.WithKeys("v"), key.WithHelp("v", i18n.T("help.view"))),
		Done:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", i18n.T("help.done"))),
		Fold:      key.NewBinding(key.WithKeys("space"), key.WithHelp("space/enter", i18n.T("help.fold"))),
		Flat:      key.NewBinding(key.WithKeys("v"), key.WithHelp("v", i18n.T("help.flat"))),
		FoldAll:   key.NewBinding(key.WithKeys("z"), key.WithHelp("z", i18n.T("help.foldall"))),
		Review:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", i18n.T("help.review"))),
		Confirm:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", i18n.T("help.confirm"))),
		Clear:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", i18n.T("help.clear"))),
	}
}

// helpGroups are the tab's bindings for the footer.
func (m skillsModel) helpGroups() [][]key.Binding {
	k := newSkillKeys()
	if m.editing {
		return [][]key.Binding{{k.Up, k.PgUp, k.Left}, {k.Done, k.Clear}}
	}
	return [][]key.Binding{{k.Up, k.PgUp, k.Top, k.Left}, {k.Toggle, k.ToggleRow, k.Review, k.Clear}, {k.Pin, k.Alias, k.Filter, k.Search}, {k.View, k.FoldAll}}
}

// shortHelp is the compact footer: the few keys that fit on one line.
func (m skillsModel) shortHelp() []key.Binding {
	k := newSkillKeys()
	if m.editing {
		return []key.Binding{k.Up, k.Left, k.Done, k.Clear}
	}
	if m.onHeader() {
		return []key.Binding{k.Up, k.Fold, k.FoldAll}
	}
	return []key.Binding{k.Up, k.Left, k.Toggle, k.Review, k.Search}
}

func (m skillsModel) Update(msg tea.Msg) (skillsModel, tea.Cmd) {
	k := newSkillKeys()
	if m.editing {
		if kp, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(kp, k.Confirm):
				m.editing = false
				m.search.Blur()
				return m, nil
			case key.Matches(kp, k.Clear):
				m.editing, m.query = false, ""
				m.search.SetValue("")
				m.search.Blur()
				m.rebuild()
				return m, nil
			}
		}
		// The list can be walked while typing; letters stay text.
		if kp, ok := msg.(tea.KeyPressMsg); ok {
			switch kp.String() {
			case "up", "ctrl+p":
				m.move(-1)
				return m, nil
			case "down", "ctrl+n":
				m.move(1)
				return m, nil
			case "pgup":
				m.moveBy(-max(m.listH-1, 1))
				return m, nil
			case "pgdown":
				m.moveBy(max(m.listH-1, 1))
				return m, nil
			case "left", "right":
				// The agent column, as outside the box; the query's own cursor
				// moves with home/end and ctrl+a/e/b/f.
				if !m.onHeader() {
					if kp.String() == "left" {
						m.col = max(m.col-1, 0)
					} else {
						m.col = min(m.col+1, max(len(m.data.Agents)-1, 0))
					}
				}
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		if v := m.search.Value(); v != m.query {
			m.query = v
			m.rebuild()
		}
		return m, cmd
	}
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(kp, k.Up):
		m.move(-1)
	case key.Matches(kp, k.Down):
		m.move(1)
	case key.Matches(kp, k.PgUp):
		m.moveBy(-max(m.listH-1, 1))
	case key.Matches(kp, k.PgDown):
		m.moveBy(max(m.listH-1, 1))
	case key.Matches(kp, k.Top):
		m.jump(false)
	case key.Matches(kp, k.Bottom):
		m.jump(true)
	case key.Matches(kp, k.Filter):
		m.only = !m.only
		m.rebuild()
	case key.Matches(kp, k.Search):
		m.editing = true
		return m, m.search.Focus()
	case key.Matches(kp, k.View):
		m.toggleView()
	case key.Matches(kp, k.FoldAll):
		m.foldAll()
	case m.onHeader() && key.Matches(kp, k.Toggle):
		m.toggleFold()
	case m.onHeader() && (key.Matches(kp, k.Left) || key.Matches(kp, k.Right) || key.Matches(kp, k.ToggleRow)):
		// Cells belong to skills, not to a group.
	case key.Matches(kp, k.Left):
		m.col = max(m.col-1, 0)
	case key.Matches(kp, k.Right):
		m.col = min(m.col+1, max(len(m.data.Agents)-1, 0))
	case key.Matches(kp, k.Toggle):
		m.toggle(false)
	case key.Matches(kp, k.ToggleRow):
		m.toggle(true)
	case key.Matches(kp, k.Clear):
		if len(m.pend[m.kind]) > 0 {
			delete(m.pend, m.kind)
			m.rebuild()
		}
	}
	return m, nil
}

// pending is the current scope's pending set.
func (m *skillsModel) pending() pendingSet {
	if m.pend[m.kind] == nil {
		m.pend[m.kind] = pendingSet{}
	}
	return m.pend[m.kind]
}

// wanted reports whether agent should see the row's skill once the pending
// changes are applied, and whether that differs from now.
func (m skillsModel) wanted(r *row, agent string) (want, changed bool) {
	vis := r.Cells[agent].visible()
	if w, ok := m.pend[m.kind][r.ID][agent]; ok {
		return w, true
	}
	return vis, false
}

// readOnly gives the reason a row cannot be changed, as a message key.
func (m skillsModel) readOnly(r *row) string {
	switch {
	case m.all:
		return "ro.all"
	case !r.managed():
		return "ro.unmanaged"
	case !r.InStore:
		return "ro.gone"
	}
	return ""
}

// toggle flips the focused cell, or with whole the whole row: when every
// agent would end up seeing the skill all are turned off, else all on.
func (m *skillsModel) toggle(whole bool) {
	it := m.selected()
	if it == nil || len(m.data.Agents) == 0 {
		return
	}
	r := it.r
	if msgKey := m.readOnly(r); msgKey != "" {
		m.notice = i18n.T(msgKey)
		return
	}
	p := m.pending()
	if !whole {
		a := m.data.Agents[m.col].ID
		now, _ := m.wanted(r, a)
		p.set(r.ID, a, !now, r.Cells[a].visible())
		m.rebuild() // group headers count the pending changes
		return
	}
	all := true
	for _, a := range m.data.Agents {
		if w, _ := m.wanted(r, a.ID); !w {
			all = false
		}
	}
	for _, a := range m.data.Agents {
		p.set(r.ID, a.ID, !all, r.Cells[a.ID].visible())
	}
	m.rebuild()
}

// summary describes the pending changes of the current scope, empty if none.
func (m skillsModel) summary() string {
	plus, minus := m.pend[m.kind].counts()
	if plus+minus == 0 {
		return ""
	}
	switch {
	case minus == 0:
		return i18n.T("pending.summary.plus", plus)
	case plus == 0:
		return i18n.T("pending.summary.minus", minus)
	}
	return i18n.T("pending.summary", plus+minus, plus, minus)
}

// columns is the widths of the matrix columns, in terminal cells.
type columns struct {
	name, repo, status int
	agent              []int
}

func (m skillsModel) columns() columns {
	c := columns{}
	used := 2 // cursor marker
	for _, a := range m.data.Agents {
		w := max(lipgloss.Width(a.ID), 3) + 1
		c.agent = append(c.agent, w)
		used += w
	}
	rest := max(m.w-used-6, 24) // the gaps between name, repo and status
	nameW, repoW := 0, 0
	hinted := false
	for _, it := range m.items {
		if it.r != nil {
			hinted = hinted || it.r.PathHint != ""
			w := lipgloss.Width(it.r.Name) + m.indent()
			if it.r.PathHint != "" {
				w += 2 + lipgloss.Width(it.r.PathHint)
			}
			nameW = max(nameW, w)
			repoW = max(repoW, lipgloss.Width(shortRepo(it.r.Repo)))
		}
	}
	nameW = max(lipgloss.Width(i18n.T("col.skill")), nameW)
	repoW = max(lipgloss.Width(i18n.T("col.repo")), repoW)
	// Names and repos take what they need, within a share of the room; the
	// status column gets the rest, at least a third.
	c.name = min(nameW, rest*35/100)
	c.repo = min(repoW, rest*30/100)
	if hinted {
		// Directories that tell same-named skills apart are worth the room:
		// the name may take what the repo column and a short status leave.
		limit := rest - 10
		if !m.grouped() {
			limit -= max(c.repo, 6)
		}
		c.name = min(nameW, max(limit, c.name))
	}
	c.name, c.repo = max(c.name, 8), max(c.repo, 6)
	if m.grouped() {
		// The group header already says which repo; its width goes to the status.
		c.repo = 0
		c.status = max(rest+6-c.name-2, 8)
		return c
	}
	c.status = max(rest-c.name-c.repo, 8)
	return c
}

// nameCell lays out the name column: the name, then (when set) the skill's
// directory after two spaces, always w cells in all. The directory gives way
// first and keeps its tail, which is what tells same-named skills apart
// (`…cy/pdf`); the name is cut only when there is no room for even a short
// directory.
func nameCell(name, path string, w int) (left, hint string) {
	nw := lipgloss.Width(name)
	if path == "" || nw+2+4 > w {
		return padTo(name, w), ""
	}
	room := w - nw - 2
	pw := lipgloss.Width(path)
	if pw > room {
		path = "…" + ansi.TruncateLeft(path, pw-room+1, "")
	}
	hint = "  " + path
	return name, hint + strings.Repeat(" ", max(w-nw-lipgloss.Width(hint), 0))
}

// padTo truncates s to w cells and pads with spaces; widths are in cells, so
// double-width CJK characters line up.
func padTo(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

func centerIn(glyph string, w int) string {
	left := (w - lipgloss.Width(glyph)) / 2
	return strings.Repeat(" ", left) + glyph + strings.Repeat(" ", w-left-lipgloss.Width(glyph))
}

func shortRepo(id string) string { return strings.TrimPrefix(id, "github.com/") }

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// part is a piece of the status column with its style.
type part struct {
	text  string
	style func(styles) lipgloss.Style
}

func statusParts(r *row) []part {
	var ps []part
	if !r.managed() {
		return []part{{i18n.T("status.unmanaged"), func(s styles) lipgloss.Style { return s.Dim }}}
	}
	if r.Pending {
		ps = append(ps, part{"↑ " + i18n.T("status.update"), func(s styles) lipgloss.Style { return s.Pending }})
	}
	if r.Pin != "" {
		ps = append(ps, part{i18n.T("status.pinned", shortSHA(r.Pin)), func(s styles) lipgloss.Style { return s.Dim }})
	}
	if r.Alias != "" {
		ps = append(ps, part{r.Alias + " ← " + r.Name, func(s styles) lipgloss.Style { return s.Dim }})
	}
	if r.Clash {
		ps = append(ps, part{"⚠ " + i18n.T("status.clash"), func(s styles) lipgloss.Style { return s.Danger }})
	}
	if r.Health != "" && r.Health != "ok" {
		ps = append(ps, part{"✗ " + i18n.T("health."+r.Health), func(s styles) lipgloss.Style { return s.Danger }})
	}
	return ps
}

func (m skillsModel) cellStyle(c cellState, st styles) lipgloss.Style {
	switch c {
	case cellLinked, cellVia:
		return st.Accent
	case cellProblem:
		return st.Danger
	}
	return st.Dim
}

// cellText returns the glyph shown for an agent's cell, with its style.
func (m skillsModel) cellText(r *row, agent string, st styles) (string, lipgloss.Style) {
	if want, changed := m.wanted(r, agent); changed {
		if want {
			return "+", st.Pending.Bold(true)
		}
		return "−", st.Pending.Bold(true)
	}
	c := r.Cells[agent]
	return c.glyph(), m.cellStyle(c, st)
}

func (m skillsModel) renderRow(r *row, sel bool, c columns, st styles) string {
	indent := m.indent()
	// Selected rows are drawn in reverse video piece by piece, so the focused
	// cell can stand out: it is bracketed and not reversed.
	var b strings.Builder
	seg := func(text string, style lipgloss.Style) {
		if sel {
			b.WriteString(st.Cursor.Render(text))
		} else {
			b.WriteString(style.Render(text))
		}
	}
	plainStyle := lipgloss.NewStyle()
	marker := "  "
	if sel {
		marker = "▸ "
	}
	seg(marker, plainStyle)
	name, hint := nameCell(strings.Repeat(" ", indent)+r.Name, r.PathHint, c.name)
	seg(name, plainStyle)
	seg(hint, st.Dim)
	seg("  ", plainStyle)
	for i, a := range m.data.Agents {
		g, style := m.cellText(r, a.ID, st)
		if sel && i == m.col {
			cell := centerIn("["+g+"]", c.agent[i])
			b.WriteString(style.Bold(true).Render(cell))
			continue
		}
		seg(centerIn(g, c.agent[i]), style)
	}
	if m.grouped() {
		seg("  ", plainStyle)
	} else {
		seg(" "+padTo(shortRepo(r.Repo), c.repo)+"  ", st.Dim)
	}
	// Status parts are styled one by one, so truncate by cells first.
	room := c.status
	for i, p := range statusParts(r) {
		t := p.text
		if i > 0 {
			t = "  " + t
		}
		if room <= 0 {
			break
		}
		t = ansi.Truncate(t, room, "…")
		room -= lipgloss.Width(t)
		seg(t, p.style(st))
	}
	line := b.String()
	if sel {
		// Extend the bar to the full width.
		if gap := m.w - lipgloss.Width(line); gap > 0 {
			line += st.Cursor.Render(strings.Repeat(" ", gap))
		}
	}
	return line
}

func (m skillsModel) View(st styles) string {
	if m.w == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, m.searchLine(st))

	if !m.loaded {
		lines = append(lines, "", "  "+st.Dim.Render(i18n.T("loading")))
		return strings.Join(lines, "\n")
	}
	c := m.columns()
	head := "  " + padTo(i18n.T("col.skill"), c.name) + "  "
	for i, a := range m.data.Agents {
		head += centerIn(a.ID, c.agent[i])
	}
	if m.grouped() {
		head += "  " + i18n.T("col.status")
	} else {
		head += " " + padTo(i18n.T("col.repo"), c.repo) + "  " + i18n.T("col.status")
	}
	lines = append(lines, st.Dim.Render(ansi.Truncate(head, m.w, "")))

	list := m.listLines(c, st)
	lines = append(lines, list...)
	for len(list) < m.listH {
		list = append(list, "")
		lines = append(lines, "")
	}
	lines = append(lines, st.Dim.Render(ansi.Truncate(i18n.T("legend"), m.w, "…")))
	if m.h-3-m.listH > 0 {
		lines = append(lines, m.detail(m.h-3-m.listH, st)...)
	}
	return strings.Join(lines, "\n")
}

func (m skillsModel) searchLine(st styles) string {
	var s string
	switch {
	case m.editing:
		s = "  " + st.Accent.Render(i18n.T("search.prompt")+": ") + m.search.View()
	case m.query != "":
		s = "  " + st.Accent.Render(i18n.T("search.prompt")+": ") + m.query
	default:
		s = "  "
	}
	if m.only {
		s += "  " + st.Accent.Render(i18n.T("filter.enabled"))
	}
	if !m.loaded {
		return s
	}
	count := st.Dim.Render(i18n.T("skills.count", m.shown, m.total))
	if gap := m.w - lipgloss.Width(s) - lipgloss.Width(count) - 1; gap > 0 {
		return s + strings.Repeat(" ", gap) + count
	}
	return s
}

func (m skillsModel) listLines(c columns, st styles) []string {
	if len(m.items) == 0 {
		msg := i18n.T("skills.nomatch")
		switch {
		case m.total == 0 && m.all:
			msg = i18n.T("skills.nothing")
		case m.total == 0:
			msg = i18n.T("skills.empty")
		}
		return []string{"", "  " + st.Dim.Render(msg)}[:min(2, m.listH)]
	}
	var out []string
	for i := m.offset; i < len(m.items) && len(out) < m.listH; i++ {
		it := m.items[i]
		if it.grp != nil {
			out = append(out, m.renderGroup(it.grp, i == m.cursor, st))
			continue
		}
		if it.r == nil {
			out = append(out, st.Bold.Render(ansi.Truncate("── "+it.header+" ──", m.w, "…")))
			continue
		}
		out = append(out, m.renderRow(it.r, i == m.cursor, c, st))
	}
	return out
}

// renderGroup draws a repo group's header: fold glyph, name, counts, and the
// pending changes and updates inside it.
func (m skillsModel) renderGroup(g *groupHeader, sel bool, st styles) string {
	glyph := "▸"
	if g.open {
		glyph = "▾"
	}
	counts := i18n.T("group.counts", g.total, g.enabled)
	if g.shown != g.total {
		counts = i18n.T("group.counts.f", g.shown, g.total, g.enabled)
	}
	title := glyph + " " + g.title
	var extra []string
	if g.pending > 0 {
		extra = append(extra, i18n.T("group.pending", g.pending))
	}
	if g.updates > 0 {
		extra = append(extra, "↑")
	}
	// Budget the cells: the title gives way first, the counts and markers stay.
	sep := i18n.T("group.sep")
	tail := sep + counts
	for _, e := range extra {
		tail += " " + e
	}
	room := max(m.w-2-lipgloss.Width(tail), 4)
	title = ansi.Truncate(title, room, "…")
	if sel {
		line := "  " + title + tail
		return st.Cursor.Render(padTo(line, m.w))
	}
	line := "  " + st.Bold.Render(title) + st.Dim.Render(sep+counts)
	if g.pending > 0 {
		line += " " + st.Pending.Render(i18n.T("group.pending", g.pending))
	}
	if g.updates > 0 {
		line += " " + st.Pending.Render("↑")
	}
	return ansi.Truncate(line, m.w, "…")
}

// groupDetail is the detail pane for a group header.
func (m skillsModel) groupDetail(g *groupHeader, st styles) []string {
	labels := []string{"detail.id", "detail.repo", "detail.commit", "group.skills", "group.enabled", "group.updates"}
	lw := 0
	for _, l := range labels {
		lw = max(lw, lipgloss.Width(i18n.T(l)))
	}
	kv := func(label, v string) string {
		l := i18n.T(label)
		return st.Dim.Render(l+strings.Repeat(" ", lw-lipgloss.Width(l)+2)) + v
	}
	var out []string
	if g.key != unmanagedKey {
		out = append(out, kv("detail.id", g.key))
		if g.repoDir != "" {
			out = append(out, kv("detail.repo", shortPath(g.repoDir, agents.Scope{Global: true})))
		}
		if g.head != "" {
			out = append(out, kv("detail.commit", shortSHA(g.head)))
		}
	} else {
		out = append(out, st.Bold.Render(g.title), st.Dim.Render(i18n.T("detail.unmanaged")))
	}
	out = append(out, kv("group.skills", fmt.Sprint(g.total)), kv("group.enabled", fmt.Sprint(g.enabled)))
	if g.updates > 0 {
		out = append(out, kv("group.updates", st.Pending.Render(fmt.Sprint(g.updates))))
	}
	return append(out, st.Dim.Render(i18n.T("group.hint")))
}

// shortPath shows p relative to the project when inside it, with ~ for the
// home directory otherwise.
func shortPath(p string, s agents.Scope) string {
	if !s.Global && s.Dir != "" {
		if rel, err := filepath.Rel(s.Dir, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}

// detail renders the pane for the selected row in exactly n lines: a rule,
// then the facts about the row.
func (m skillsModel) detail(n int, st styles) []string {
	out := []string{st.Dim.Render(strings.Repeat("─", max(m.w, 1)))}
	it := m.selected()
	if m.onHeader() {
		out = append(out, m.groupDetail(m.items[m.cursor].grp, st)...)
	} else if it != nil {
		r := it.r
		scope := m.data.Sections[it.sec].Scope
		labels := []string{"detail.id", "detail.repo", "detail.commit", "detail.mode", "detail.links", "detail.visible"}
		lw := 0
		for _, l := range labels {
			lw = max(lw, lipgloss.Width(i18n.T(l)))
		}
		kv := func(label, v string) string {
			l := i18n.T(label)
			return st.Dim.Render(l+strings.Repeat(" ", lw-lipgloss.Width(l)+2)) + v
		}
		indent := strings.Repeat(" ", lw+2)
		switch {
		case !r.managed():
			out = append(out, kv("detail.id", r.Name))
			for i, l := range r.Links {
				lab := "detail.links"
				if i > 0 {
					out = append(out, indent+shortPath(l.Path, scope))
					continue
				}
				out = append(out, kv(lab, shortPath(l.Path, scope)))
			}
			out = append(out, st.Dim.Render(i18n.T("detail.unmanaged")))
		default:
			out = append(out, kv("detail.id", r.ID))
			if r.RepoDir != "" {
				out = append(out, kv("detail.repo", shortPath(r.RepoDir, scope)))
			}
			if r.Head != "" {
				out = append(out, kv("detail.commit", shortSHA(r.Head)))
			}
			if !r.enabled() {
				out = append(out, st.Dim.Render(i18n.T("detail.off")))
				break
			}
			if r.Mode != "" {
				out = append(out, kv("detail.mode", r.Mode))
			}
			for i, l := range r.Links {
				text := shortPath(l.Path, scope)
				if l.Target != "" {
					text += " → " + shortPath(l.Target, scope)
				}
				if l.Health != "ok" {
					text += "  " + st.Danger.Render("✗ "+i18n.T("health."+l.Health))
				}
				if i == 0 {
					out = append(out, kv("detail.links", text))
				} else {
					out = append(out, indent+text)
				}
			}
			out = append(out, kv("detail.visible", strings.Join(r.Visible, ", ")))
			if r.Pending {
				out = append(out, st.Pending.Render("↑ "+i18n.T("detail.pending")))
			}
		}
	}
	for i := range out {
		out[i] = ansi.Truncate(out[i], m.w, "…")
	}
	if len(out) > n {
		out = out[:n]
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out
}

// changes converts the current scope's pending set into per-skill changes.
func (m skillsModel) changes() []change {
	var order []string
	for _, a := range m.data.Agents {
		order = append(order, a.ID)
	}
	names := map[string]string{}
	for _, sec := range m.data.Sections {
		for _, r := range sec.Rows {
			if r.ID != "" {
				names[r.ID] = r.Name
			}
		}
	}
	return m.pend[m.kind].changes(order, func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		return id
	})
}

// setQuery sets the search text, as if typed.
func (m *skillsModel) setQuery(q string) {
	m.query = q
	m.search.SetValue(q)
	m.editing = false
	m.search.Blur()
	if m.loaded {
		m.rebuild()
	}
}
