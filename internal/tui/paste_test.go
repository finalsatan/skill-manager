package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"skillet/internal/i18n"
)

const pastedURL = "https://github.com/anthropics/skills"

func paste(s string) tea.PasteMsg { return tea.PasteMsg{Content: s} }

func TestPasteIntoAddRepoDialog(t *testing.T) {
	m := reposAt(t, i18n.EN)
	m = send(t, m, press("n"), paste(pastedURL+"\n"))
	if got := m.modal.input.Value(); got != pastedURL {
		t.Fatalf("input = %q", got)
	}
	if !strings.Contains(plain(m.View()), pastedURL) {
		t.Error("pasted text not shown")
	}
	// CRLF and a second paste append; enter starts the add with the clean URL.
	m = send(t, m, paste("\r\n"))
	if m.modal.input.Value() != pastedURL {
		t.Errorf("newline leaked: %q", m.modal.input.Value())
	}
	next, cmd := m.Update(press("enter"))
	m = next.(Model)
	if m.modal.open || !m.applying || cmd == nil {
		t.Fatalf("add not started: open=%v applying=%v", m.modal.open, m.applying)
	}
}

func TestPasteIntoSkillsSearch(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("/"), paste("spread\n"))
	if m.skills.query != "spread" || selectedName(m) != "xlsx" || !strings.Contains(plain(m.View()), "1 of 6") {
		t.Errorf("query=%q sel=%q", m.skills.query, selectedName(m))
	}
}

func TestPasteIntoPinAndAliasDialogs(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = rowFor(t, m, "pdf")
	m = send(t, m, press("p"), paste("abc1234\n"))
	if m.modal.input.Value() != "abc1234" {
		t.Errorf("pin input %q", m.modal.input.Value())
	}
	m = send(t, m, press("esc"), press("R"), paste("pdf-tool\n"))
	if m.modal.input.Value() != "pdf-tool" {
		t.Errorf("alias input %q", m.modal.input.Value())
	}
}

func TestPasteWithoutFocusedInputIsIgnored(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	before := plain(m.View())
	m = send(t, m, paste("a\nspace\nq/"+pastedURL))
	if plain(m.View()) != before || m.skills.capturing() || len(m.skills.pend[scopeProject]) != 0 || m.tab != tabSkills {
		t.Error("paste changed something with no input focused")
	}
	// Nor does it reach an input behind a dialog without one.
	m = send(t, m, press("space"), press("enter"), previewMsg{kind: scopeProject, plans: fakePlans()})
	m = send(t, m, paste("zzz"))
	if m.skills.query != "" || m.skills.search.Value() != "" {
		t.Error("paste reached the hidden search box")
	}
	// Other tabs: nothing focused either.
	m = send(t, m, press("esc"), press("2"), paste("x"), press("3"), paste("x"), press("4"), paste("x"))
	if m.modal.open {
		t.Error("paste opened a dialog")
	}
}

// ctrl+v makes the input read the system clipboard and answer with a message
// of its own; the model must hand it back. Headless, the read usually fails,
// which must change nothing.
func TestCtrlVAnswerReachesTheInput(t *testing.T) {
	m := reposAt(t, i18n.EN)
	m = send(t, m, press("n"), paste("abc"))
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("ctrl+v did not start a clipboard read")
	}
	m = send(t, m, cmd())
	if !m.modal.open || !strings.HasPrefix(m.modal.input.Value(), "abc") {
		t.Errorf("dialog broken: open=%v input=%q", m.modal.open, m.modal.input.Value())
	}
}
