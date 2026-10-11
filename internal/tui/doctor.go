package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"skillet/internal/agents"
	"skillet/internal/deploy"
	"skillet/internal/i18n"
)

// doctorItem is an issue with the skill it concerns, when it has one.
type doctorItem struct {
	deploy.Issue
	Skill string // skill id of the deployment at Target
	Name  string
}

// severity groups the issues: 0 problems that break something, 1 cleanup.
func severity(kind string) int {
	switch kind {
	case "outdated", "modified", "orphan", "project-gone", "orphan-pin":
		return 1
	}
	return 0
}

// kindLabel is the localized name of an issue kind.
func kindLabel(kind string) string {
	k := "doctor.kind." + kind
	if t := i18n.T(k); t != k {
		return t
	}
	return kind
}

// doctorMsg is the result of loadDoctor.
type doctorMsg struct {
	items []doctorItem
	err   error
}

// loadDoctor runs the checks without fixing anything. The informational
// "pending" finding belongs to the Updates tab and is left out.
func loadDoctor() tea.Cmd {
	return func() tea.Msg {
		var msg doctorMsg
		msg.err = withManager(func(m *deploy.Manager) error {
			issues, err := m.Doctor(false)
			for _, is := range issues {
				if is.Kind == "pending" {
					continue
				}
				it := doctorItem{Issue: is}
				if d := m.St.DeploymentAt(is.Target); d != nil {
					it.Skill = d.Skill
					if sk := m.St.Skills[d.Skill]; sk != nil {
						it.Name = sk.Name
					}
				}
				msg.items = append(msg.items, it)
			}
			sort.SliceStable(msg.items, func(i, j int) bool {
				a, b := msg.items[i], msg.items[j]
				if severity(a.Kind) != severity(b.Kind) {
					return severity(a.Kind) < severity(b.Kind)
				}
				if a.Kind != b.Kind {
					return a.Kind < b.Kind
				}
				return a.Target < b.Target
			})
			return err
		})
		return msg
	}
}

// doctorFixMsg is the result of fixCmd.
type doctorFixMsg struct {
	issues []deploy.Issue
	err    error
}

// fixCmd repairs everything `doctor --fix` can.
func fixCmd() tea.Cmd {
	return func() tea.Msg {
		var msg doctorFixMsg
		msg.err = withManager(func(m *deploy.Manager) error {
			var err error
			msg.issues, err = m.Doctor(true)
			return err
		})
		return msg
	}
}

// fixSummary counts what a fix run did.
func fixSummary(issues []deploy.Issue) (fixed, failed, open int) {
	for _, is := range issues {
		switch {
		case is.Kind == "pending":
		case is.Fixed:
			fixed++
		case is.Err != nil:
			failed++
		default:
			open++
		}
	}
	return
}

// doctorModel is the Doctor tab.
type doctorModel struct {
	items  []doctorItem
	loaded bool
	cursor int
	w, h   int
}

func (m *doctorModel) setSize(w, h int) { m.w, m.h = w, h }

func (m *doctorModel) setItems(items []doctorItem) {
	m.items, m.loaded = items, true
	m.cursor = min(m.cursor, max(len(items)-1, 0))
}

func (m doctorModel) selected() *doctorItem {
	if m.cursor < len(m.items) {
		return &m.items[m.cursor]
	}
	return nil
}

// fixable lists the issues Fix would repair.
func (m doctorModel) fixable() []doctorItem {
	var out []doctorItem
	for _, it := range m.items {
		if it.Fixable() {
			out = append(out, it)
		}
	}
	return out
}

type doctorKeys struct {
	Up, Down, Fix, Skill key.Binding
}

func newDoctorKeys() doctorKeys {
	return doctorKeys{
		Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", i18n.T("help.move"))),
		Down:  key.NewBinding(key.WithKeys("down", "j")),
		Fix:   key.NewBinding(key.WithKeys("F"), key.WithHelp("F", i18n.T("help.fix"))),
		Skill: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", i18n.T("help.toskill"))),
	}
}

func (m doctorModel) helpGroups() [][]key.Binding {
	k := newDoctorKeys()
	return [][]key.Binding{{k.Up}, {k.Fix, k.Skill}}
}

func (m doctorModel) shortHelp() []key.Binding {
	k := newDoctorKeys()
	return []key.Binding{k.Up, k.Fix, k.Skill}
}

func (m doctorModel) Update(msg tea.KeyPressMsg) doctorModel {
	k := newDoctorKeys()
	switch {
	case key.Matches(msg, k.Up):
		m.cursor = max(m.cursor-1, 0)
	case key.Matches(msg, k.Down):
		m.cursor = min(m.cursor+1, max(len(m.items)-1, 0))
	}
	return m
}

func (m doctorModel) detail(w int, st styles) []string {
	it := m.selected()
	if it == nil {
		return nil
	}
	fix := st.Dim.Render(i18n.T("doctor.manual"))
	if it.Fixable() {
		fix = st.Accent.Render(i18n.T("doctor.fixable"))
	}
	out := []string{st.Bold.Render(kindLabel(it.Kind)) + "  " + fix}
	if it.Target != "" {
		out = append(out, st.Dim.Render(i18n.T("doctor.target")+": ")+shortPath(it.Target, agents.Scope{Global: true}))
	}
	if it.Skill != "" {
		out = append(out, st.Dim.Render(i18n.T("doctor.skill")+": ")+it.Skill)
	}
	out = append(out, st.Dim.Render(i18n.T("doctor.detail")+": ")+it.Detail)
	for _, p := range it.Paths {
		out = append(out, "  "+shortPath(p, agents.Scope{Global: true}))
	}
	var wrapped []string
	for _, l := range out {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(l, max(w-2, 10), ""), "\n")...)
	}
	return wrapped
}

func (m doctorModel) View(st styles) string {
	if m.w == 0 {
		return ""
	}
	if !m.loaded {
		return "\n  " + st.Dim.Render(i18n.T("loading"))
	}
	if len(m.items) == 0 {
		return "\n  " + st.Accent.Render(i18n.T("doctor.empty")) + "\n  " + st.Dim.Render(i18n.T("doctor.hint"))
	}
	detailH := min(8, m.h/2)
	listH := max(m.h-detailH-1, 1)

	kindW := 0
	for _, it := range m.items {
		kindW = max(kindW, lipgloss.Width(kindLabel(it.Kind)))
	}
	var lines []string
	selLine := 0
	group := -1
	for i, it := range m.items {
		if g := severity(it.Kind); g != group {
			group = g
			title := "doctor.group.problems"
			if g == 1 {
				title = "doctor.group.cleanup"
			}
			lines = append(lines, st.Bold.Render("── "+i18n.T(title)+" ──"))
		}
		mark := "  "
		if i == m.cursor {
			mark = "▸ "
		}
		fix := "  "
		if it.Fixable() {
			fix = "✓ "
		}
		text := fmt.Sprintf("%s%s%s  %s", mark, fix, padTo(kindLabel(it.Kind), kindW), shortPath(it.Target, agents.Scope{Global: true}))
		text = padTo(text, m.w)
		if i == m.cursor {
			selLine = len(lines)
			lines = append(lines, st.Cursor.Render(text))
		} else {
			lines = append(lines, text)
		}
	}
	off := 0
	if selLine >= listH {
		off = selLine - listH + 1
	}
	lines = lines[min(off, len(lines)):]
	if len(lines) > listH {
		lines = lines[:listH]
	}
	for len(lines) < listH {
		lines = append(lines, "")
	}
	lines = append(lines, st.Dim.Render(strings.Repeat("─", max(m.w, 1))))
	for i, l := range m.detail(m.w, st) {
		if i >= detailH {
			break
		}
		lines = append(lines, " "+l)
	}
	return strings.Join(lines, "\n")
}
