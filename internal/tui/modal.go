package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"skillet/internal/agents"
	"skillet/internal/i18n"
)

// modalState is the preview dialog for the pending changes of one scope.
type modalState struct {
	open     bool
	kind     scopeKind
	scope    agents.Scope
	changes  []change
	loading  bool // the dry-run is still running
	openErr  error
	plans    []skillPlan
	manifest bool
	button   int // 0 Apply, 1 Back
	scroll   int
}

const (
	btnApply = iota
	btnBack
)

// blocked reports whether the changes cannot be applied: the dry-run failed
// for the store or for some skill.
func (s modalState) blocked() bool {
	if s.openErr != nil {
		return true
	}
	for _, p := range s.plans {
		if p.Err != nil {
			return true
		}
	}
	return false
}

// canApply is true when Apply may be pressed.
func (s modalState) canApply() bool { return !s.loading && !s.blocked() && len(s.plans) > 0 }

// modalKey handles a key press while the dialog is open. It returns the
// updated model and, on Apply, the command that applies the changes.
func (m Model) modalKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	m, cmd := m.modalKeyRaw(msg)
	if m.modal.open {
		_, bodyH := m.bodySize()
		_, maxScroll := scrollBounds(len(m.contentLines()), bodyH)
		m.modal.scroll = min(max(m.modal.scroll, 0), maxScroll)
	}
	return m, cmd
}

func (m Model) modalKeyRaw(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	md := m.modal
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.modal = modalState{}
	case "left", "shift+tab":
		md.button = btnApply
		m.modal = md
	case "right", "tab":
		md.button = btnBack
		m.modal = md
	case "up", "k":
		md.scroll--
		m.modal = md
	case "down", "j":
		md.scroll++
		m.modal = md
	case "pgup":
		md.scroll -= 5
		m.modal = md
	case "pgdown":
		md.scroll += 5
		m.modal = md
	case "enter":
		if md.button == btnBack {
			m.modal = modalState{}
			break
		}
		if !md.canApply() {
			break
		}
		m.modal = modalState{}
		m.applying = true
		m.status = ""
		return m, tea.Batch(applyCmd(md.kind, md.scope, md.changes), m.spin.Tick)
	}
	return m, nil
}

// startPreview opens the dialog and starts the dry-run for the current
// scope's pending changes.
func (m Model) startPreview() (Model, tea.Cmd) {
	cs := m.skills.changes()
	if len(cs) == 0 || m.scope == scopeAll {
		return m, nil
	}
	s := m.opts.Project
	if m.scope == scopeGlobal {
		s = agents.Scope{Global: true}
	}
	m.modal = modalState{open: true, kind: m.scope, scope: s, changes: cs, loading: true}
	return m, previewCmd(m.scope, s, cs)
}

// visibleText renders who sees a skill afterwards, in registry order.
func visibleText(v map[string]int, order []string) string {
	var parts []string
	for _, a := range order {
		switch n := v[a]; {
		case n == 1:
			parts = append(parts, a)
		case n > 1:
			parts = append(parts, i18n.T("modal.locations", a, n))
		}
	}
	if len(parts) == 0 {
		return i18n.T("modal.none")
	}
	return strings.Join(parts, ", ")
}

// stepLines renders one skill's plan: which agents are enabled and disabled,
// then the combined link actions and who sees the skill afterwards.
func stepLines(c change, st *step, s agents.Scope, order []string, sty styles) []string {
	var out []string
	if len(c.Enable) > 0 {
		out = append(out, "  "+sty.Bold.Render(i18n.T("modal.enable", strings.Join(c.Enable, ", "))))
	}
	if len(c.Disable) > 0 {
		out = append(out, "  "+sty.Bold.Render(i18n.T("modal.disable", strings.Join(c.Disable, ", "))))
	}
	if len(st.Actions) == 0 {
		out = append(out, "    "+sty.Dim.Render(i18n.T("modal.nochange")))
	}
	for _, a := range st.Actions {
		path := shortPath(a.Target, s)
		var line string
		switch a.Kind {
		case "create":
			if a.Source != "" {
				line = sty.Pending.Render(i18n.T("modal.create", path, shortPath(a.Source, s), a.Mode))
			} else {
				line = sty.Pending.Render(i18n.T("modal.create.nosrc", path, a.Mode))
			}
			if a.Pin != "" {
				line += sty.Dim.Render(" @ " + shortSHA(a.Pin))
			}
		case "replace":
			if a.Pin != "" {
				line = sty.Pending.Render(i18n.T("modal.replace.pin", path, a.Mode, shortSHA(a.Pin)))
			} else {
				line = sty.Pending.Render(i18n.T("modal.replace.repo", path, a.Mode))
			}
		case "repair":
			line = sty.Pending.Render(i18n.T("modal.repair", path, a.Mode))
		case "remove":
			line = sty.Pending.Render(i18n.T("modal.remove", path))
		default:
			line = a.Kind + " " + path
		}
		out = append(out, "    "+line)
	}
	out = append(out, "    "+sty.Dim.Render(i18n.T("modal.visible", visibleText(st.Visible, order))))
	if len(st.Unwanted) > 0 {
		out = append(out, "    "+sty.Pending.Render(i18n.T("modal.note", strings.Join(st.Unwanted, ", "))))
	}
	return out
}

// contentLines is the scrollable part of the dialog.
func (m Model) contentLines() []string {
	md := m.modal
	sty := m.st
	if md.loading {
		return []string{sty.Dim.Render(i18n.T("modal.loading"))}
	}
	if md.openErr != nil {
		return []string{sty.Danger.Render("✗ " + errText(md.openErr))}
	}
	order := m.agentOrder()
	var out []string
	for _, p := range md.plans {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, sty.Bold.Render(p.Change.Name)+" "+sty.Dim.Render(p.Change.ID))
		if p.Step != nil {
			out = append(out, stepLines(p.Change, p.Step, md.scope, order, sty)...)
		}
		if p.Err != nil {
			out = append(out, "  "+sty.Danger.Render("✗ "+p.Err.Error()))
		}
	}
	if md.manifest {
		out = append(out, "", sty.Dim.Render(i18n.T("modal.manifest")))
	}
	if md.blocked() {
		out = append(out, "", sty.Danger.Render(i18n.T("modal.blocked")))
	}
	return out
}

func (m Model) agentOrder() []string {
	var ids []string
	for _, a := range m.skills.data.Agents {
		ids = append(ids, a.ID)
	}
	return ids
}

// scrollBounds is how many content lines fit in a body of height h, and how
// far the content can scroll.
func scrollBounds(n, h int) (room, maxScroll int) {
	room = min(max(h-2-4, 1), n) // minus the border, title, two gaps and the buttons
	return room, max(n-room, 0)
}

// modalBox renders the dialog for a body area of w by h cells.
func (m Model) modalBox(w, h int) []string {
	md := m.modal
	innerW := min(max(w-6, 10), 96)
	content := m.contentLines()
	room, maxScroll := scrollBounds(len(content), h)
	scroll := min(max(md.scroll, 0), maxScroll)
	shown := content[scroll : scroll+room]

	title := m.st.Bold.Render(i18n.T("modal.title")) + " " + m.st.Dim.Render(scopeTitle(md.kind, md.scope))
	if maxScroll > 0 {
		title += m.st.Dim.Render(fmt.Sprintf("  (%d/%d)", scroll+1, maxScroll+1))
	}
	label := func(i int, key string, enabled bool) string {
		t := "[ " + i18n.T(key) + " ]"
		switch {
		case !enabled:
			return m.st.Dim.Render(t)
		case md.button == i:
			return m.st.Cursor.Render(t)
		}
		return t
	}
	buttons := label(btnApply, "modal.apply", md.canApply()) + "   " + label(btnBack, "modal.back", true)

	lines := append([]string{title, ""}, shown...)
	lines = append(lines, "", buttons+"   "+m.st.Dim.Render(i18n.T("modal.hint")))
	for i, l := range lines {
		l = ansi.Truncate(l, innerW, "…")
		lines[i] = l + strings.Repeat(" ", innerW-lipgloss.Width(l))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(m.st.accent).Padding(0, 1)
	return strings.Split(box.Render(strings.Join(lines, "\n")), "\n")
}

func scopeTitle(kind scopeKind, s agents.Scope) string {
	if kind == scopeGlobal {
		return i18n.T("scope.global")
	}
	return s.Dir
}

// overlayModal draws the dialog centred over the dimmed body.
func (m Model) overlayModal(body string, w, h int) string {
	bg := strings.Split(fit(body, w, h), "\n")
	for i, l := range bg {
		bg[i] = m.st.Dim.Render(ansi.Strip(l))
	}
	box := m.modalBox(w, h)
	bw := lipgloss.Width(box[0])
	top := max((h-len(box))/2, 0)
	left := max((w-bw)/2, 0)
	for i, bl := range box {
		y := top + i
		if y >= len(bg) {
			break
		}
		line := bg[y]
		bg[y] = ansi.Truncate(line, left, "") + strings.Repeat(" ", max(left-lipgloss.Width(ansi.Truncate(line, left, "")), 0)) +
			bl + ansi.TruncateLeft(line, left+bw, "")
	}
	return strings.Join(bg, "\n")
}
