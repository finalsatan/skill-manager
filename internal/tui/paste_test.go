package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/finalsatan/skill-manager/internal/i18n"
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

func ctrl(c rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: c, Mod: tea.ModCtrl} }

func TestSearchBoxKeepsFocusWhileWalkingTheList(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("/"))
	for _, c := range "skills" { // matches every row through its id
		m = send(t, m, press(string(c)))
	}
	if selectedName(m) != "docx" {
		t.Fatalf("start on %q", selectedName(m))
	}
	m = send(t, m, press("down"), press("down"))
	if selectedName(m) != "pdf" || m.skills.query != "skills" || !m.skills.editing {
		t.Fatalf("down down: %q query=%q editing=%v", selectedName(m), m.skills.query, m.skills.editing)
	}
	m = send(t, m, press("up"), press("up"), press("up"))
	if selectedName(m) != "docx" || m.skills.query != "skills" {
		t.Errorf("up at the top: %q", selectedName(m))
	}
	m = send(t, m, press("pgdown"))
	if selectedName(m) == "docx" || selectedName(m) == "gone" {
		t.Errorf("pgdown did not page: %q", selectedName(m))
	}
	m = send(t, m, press("pgup"))
	if selectedName(m) != "docx" {
		t.Errorf("pgup: %q", selectedName(m))
	}
	m = send(t, m, ctrl('n'))
	if selectedName(m) != "gone" {
		t.Errorf("ctrl+n: %q", selectedName(m))
	}
	m = send(t, m, ctrl('p'))
	if selectedName(m) != "docx" {
		t.Errorf("ctrl+p: %q", selectedName(m))
	}

	// Letters, digits and space are still text.
	for _, k := range []string{"j", "k", "g", "G", "h", "l", "1"} {
		m = send(t, m, press(k))
	}
	m = send(t, m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if m.skills.query != "skillsjkgGhl1 " || m.tab != tabSkills {
		t.Fatalf("query = %q", m.skills.query)
	}
	for range 8 {
		m = send(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	if m.skills.query != "skills" {
		t.Fatalf("after backspaces: %q", m.skills.query)
	}

	// enter leaves the box with the filter and the cursor on the chosen row;
	// space then toggles that row's focused cell.
	m = send(t, m, press("down"), press("down"), press("enter"))
	if m.skills.editing || m.skills.query != "skills" || selectedName(m) != "pdf" {
		t.Fatalf("after enter: editing=%v query=%q sel=%q", m.skills.editing, m.skills.query, selectedName(m))
	}
	m = send(t, m, press("space"))
	if cellOf(m, "pdf", "claude") != "−" || m.skills.summary() == "" {
		t.Errorf("space did not toggle pdf/claude: %s", cellOf(m, "pdf", "claude"))
	}
}

func TestSearchWalkingOverGroupHeaders(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("/"), press("a"), press("l"), press("p"))
	// Only alpha matches; its header and two rows are walkable.
	var seen []string
	for range 4 {
		seen = append(seen, cursorOn(m))
		m = send(t, m, press("down"))
	}
	if want := []string{"▪acme/alpha", "a1", "a2", "a2"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("walk %v, want %v", seen, want)
	}
	if m.skills.query != "alp" {
		t.Errorf("query %q", m.skills.query)
	}
	// ←/→ are not for the query; ctrl+a moves its cursor to the start.
	m = send(t, m, press("left"), press("x"))
	if m.skills.query != "alpx" {
		t.Errorf("left moved the query cursor: %q", m.skills.query)
	}
}

func TestSearchFooter(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("/"))
	if v := plain(m.View()); !strings.Contains(v, "↑/↓ move • ←/→ cell • enter done • esc clear") {
		t.Errorf("en footer:\n%s", v)
	}
	zh := loadedModel(t, i18n.ZH)
	zh = send(t, zh, press("/"))
	if v := plain(zh.View()); !strings.Contains(v, "↑/↓ 移动 • ←/→ 格 • enter 确定 • esc 清除") {
		t.Errorf("zh footer:\n%s", v)
	}
}

func TestSearchLeftRightMoveTheColumn(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("/"), press("s"), press("k"))
	col := m.skills.col
	m = send(t, m, press("right"), press("right"))
	if m.skills.col != col+2 || m.skills.query != "sk" || !m.skills.editing {
		t.Fatalf("right: col=%d query=%q", m.skills.col, m.skills.query)
	}
	m = send(t, m, press("left"))
	if m.skills.col != col+1 || m.skills.query != "sk" {
		t.Errorf("left: col=%d query=%q", m.skills.col, m.skills.query)
	}
	// A typed character still lands at the query's end: the arrows did not move it.
	m = send(t, m, press("i"))
	if m.skills.query != "ski" {
		t.Errorf("query %q", m.skills.query)
	}
	// The query's own cursor: ctrl+a to the start, ctrl+e to the end.
	m = send(t, m, ctrl('a'), press("x"))
	if m.skills.query != "xski" {
		t.Errorf("ctrl+a: %q", m.skills.query)
	}
	m = send(t, m, ctrl('e'), press("z"))
	if m.skills.query != "xskiz" {
		t.Errorf("ctrl+e: %q", m.skills.query)
	}
	m = send(t, m, ctrl('b'), press("y"))
	if m.skills.query != "xskiyz" {
		t.Errorf("ctrl+b: %q", m.skills.query)
	}
	// Enter leaves the box; the toggle acts on the column chosen while typing.
	m = send(t, m, press("esc"), press("/"))
	m = send(t, m, press("right"), press("enter"), press("space"))
	if cellOf(m, "docx", "codex") != "+" && cellOf(m, "docx", "opencode") != "+" {
		t.Errorf("space after enter did not use the chosen column (col=%d)", m.skills.col)
	}
}

func TestSearchLeftRightOnAHeaderDoNothing(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("/"), press("a"))
	col := m.skills.col
	m = send(t, m, press("right"), press("left"))
	if m.skills.col != col || m.skills.query != "a" {
		t.Errorf("col=%d query=%q", m.skills.col, m.skills.query)
	}
}
