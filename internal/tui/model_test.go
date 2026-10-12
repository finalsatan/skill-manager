package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/i18n"
)

// plain is a view's content without styling.
func plain(v tea.View) string { return ansi.Strip(v.Content) }

func press(s string) tea.KeyPressMsg {
	switch s {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

// send feeds messages to the model, ignoring the commands they return.
func send(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func setLang(t *testing.T, l i18n.Lang) {
	t.Helper()
	old := i18n.Current()
	i18n.Set(l)
	t.Cleanup(func() { i18n.Set(old) })
}

func newTestModel(t *testing.T, l i18n.Lang) Model {
	setLang(t, l)
	m := New(Options{Project: agents.Scope{Dir: "/work/proj"}})
	return send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
}

func TestTabsAndCounts(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	if strings.Contains(plain(m.View()), "(3)") {
		t.Fatal("counts shown before they were loaded")
	}
	m = send(t, m, countsMsg{pending: 3, issues: 2})
	v := plain(m.View())
	for _, want := range []string{"[1 Skills]", "Updates (3)", "Doctor (2)", "Scope: project"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	m = send(t, m, press("3"))
	if m.tab != tabRepos || !strings.Contains(plain(m.View()), "[3 Repos]") {
		t.Errorf("tab = %v", m.tab)
	}
	m = send(t, m, press("tab"))
	if m.tab != tabDoctor {
		t.Errorf("tab key: %v", m.tab)
	}
}

func TestTabsChinese(t *testing.T) {
	m := newTestModel(t, i18n.ZH)
	m = send(t, m, countsMsg{pending: 1})
	v := plain(m.View())
	for _, want := range []string{"[1 技能]", "更新 (1)", "范围：项目"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	for i, line := range strings.Split(v, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Errorf("line %d is %d cells wide", i, w)
		}
	}
}

func TestScopeCycle(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	var got []scopeKind
	for range 3 {
		m = send(t, m, press("s"))
		got = append(got, m.scope)
	}
	if got[0] != scopeGlobal || got[1] != scopeAll || got[2] != scopeProject {
		t.Errorf("scope cycle: %v", got)
	}
}

func TestQuitAndHelp(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	_, cmd := m.Update(press("q"))
	if cmd == nil {
		t.Fatal("q did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q returned %T", cmd())
	}
	_, cmd = m.Update(press("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c did not quit")
	}
	m = send(t, m, press("?"))
	if !m.help.ShowAll {
		t.Error("? did not expand help")
	}
}

func TestErrorStatus(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	m = send(t, m, countsMsg{err: stateLocked()})
	if !strings.Contains(plain(m.View()), "busy") {
		t.Errorf("locked error not friendly:\n%s", plain(m.View()))
	}
}

func TestViewHeightMatchesWindow(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	if h := lipgloss.Height(plain(m.View())); h != 30 {
		t.Errorf("view is %d lines, want 30", h)
	}
}

func TestLongProjectPathKeepsItsEnd(t *testing.T) {
	m := newTestModel(t, i18n.ZH)
	m.opts.Project.Dir = "/very/long/" + strings.Repeat("directory/", 12) + "my-project"
	h := strings.SplitN(plain(m.View()), "\n", 2)[0]
	if !strings.HasSuffix(h, "my-project") || lipgloss.Width(h) > 100 || !strings.Contains(h, "更新") {
		t.Errorf("header %q (%d cells)", h, lipgloss.Width(h))
	}
}
