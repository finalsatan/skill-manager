// Package tui is skillet's terminal UI, built on Bubble Tea.
//
// Every operation on the store runs inside a tea.Cmd that opens the state,
// works and closes it again (see withManager); a Manager is never held while
// the user is thinking.
package tui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"skillet/internal/agents"
	"skillet/internal/config"
	"skillet/internal/i18n"
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

	help help.Model
}

func New(opts Options) Model {
	m := Model{opts: opts, isDark: true, help: help.New()}
	m.st = newStyles(m.isDark)
	m.help.Styles = m.st.Help
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, loadCounts())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		m.st = newStyles(m.isDark)
		m.help.Styles = m.st.Help
	case countsMsg:
		if msg.err != nil {
			m.setError(msg.err)
			break
		}
		m.countsLoaded, m.pending, m.issues = true, msg.pending, msg.issues
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) setError(err error) {
	m.status, m.statusErr = errText(err), true
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := newKeyMap(nil)
	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Help):
		m.help.ShowAll = !m.help.ShowAll
	case key.Matches(msg, k.Scope):
		m.scope = (m.scope + 1) % numScopes
		m.status = ""
	case key.Matches(msg, k.Refresh):
		m.status = ""
		return m, loadCounts()
	default:
		switch msg.String() {
		case "1", "2", "3", "4":
			m.tab = tab(msg.String()[0] - '1')
		case "tab":
			m.tab = (m.tab + 1) % numTabs
		case "shift+tab":
			m.tab = (m.tab + numTabs - 1) % numTabs
		}
	}
	return m, nil
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
	header := m.header()
	footer := m.footer()
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyH < 1 {
		bodyH = 1
	}
	return header + "\n" + fit(m.body(), m.width, bodyH) + "\n" + footer
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
		right += " " + m.st.Dim.Render(m.opts.Project.Dir)
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
		msg = i18n.T("updates.soon")
	case tabRepos:
		msg = i18n.T("repos.soon")
	case tabDoctor:
		msg = i18n.T("doctor.soon")
	default:
		msg = i18n.T("loading")
	}
	return "\n  " + m.st.Dim.Render(msg)
}

// footer is the status line followed by the key help.
func (m Model) footer() string {
	status := ""
	if m.status != "" {
		if m.statusErr {
			status = m.st.Danger.Render("! " + m.status)
		} else {
			status = m.st.Accent.Render(m.status)
		}
	}
	return ansi.Truncate(status, m.width, "…") + "\n" + m.help.View(newKeyMap(nil))
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
