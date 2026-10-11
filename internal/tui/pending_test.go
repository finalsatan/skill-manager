package tui

import (
	"reflect"
	"strings"
	"testing"

	"skillet/internal/i18n"
)

func TestPendingSetToggleBack(t *testing.T) {
	p := pendingSet{}
	p.set("a", "claude", true, false)
	p.set("a", "codex", false, true)
	if plus, minus := p.counts(); plus != 1 || minus != 1 {
		t.Errorf("counts %d %d", plus, minus)
	}
	p.set("a", "claude", false, false) // back to the present
	p.set("a", "codex", true, true)
	if len(p) != 0 {
		t.Errorf("entries left: %v", p)
	}
}

func TestPendingChangesSorted(t *testing.T) {
	p := pendingSet{}
	p.set("r:z/pptx", "pi", false, true)
	p.set("r:z/pptx", "claude", false, true)
	p.set("r:a/docx", "opencode", true, false)
	p.set("r:a/docx", "claude", true, false)
	p.set("r:a/docx", "pi", false, true)
	names := map[string]string{"r:z/pptx": "pptx", "r:a/docx": "docx"}
	got := p.changes([]string{"claude", "codex", "opencode", "pi"}, func(id string) string { return names[id] })
	want := []change{
		{ID: "r:a/docx", Name: "docx", Enable: []string{"claude", "opencode"}, Disable: []string{"pi"}},
		{ID: "r:z/pptx", Name: "pptx", Disable: []string{"claude", "pi"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func cellOf(m Model, name, agent string) string {
	for _, sec := range m.skills.data.Sections {
		for i := range sec.Rows {
			if sec.Rows[i].Name == name {
				g, _ := m.skills.cellText(&sec.Rows[i], agent, m.st)
				return g
			}
		}
	}
	return "?"
}

func TestToggleCells(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	// docx row: all off. Focus claude (col 0), toggle on, then codex.
	m = send(t, m, press("space"), press("l"), press("space"))
	if cellOf(m, "docx", "claude") != "+" || cellOf(m, "docx", "codex") != "+" {
		t.Fatalf("cells: %s %s", cellOf(m, "docx", "claude"), cellOf(m, "docx", "codex"))
	}
	v := plain(m.View())
	if !strings.Contains(v, "2 changes pending (+2 −0)") || !strings.Contains(v, "+ enable  − disable") {
		t.Errorf("view:\n%s", v)
	}
	// Toggling back removes the entry.
	m = send(t, m, press("space"))
	if cellOf(m, "docx", "codex") != "○" {
		t.Errorf("codex: %s", cellOf(m, "docx", "codex"))
	}
	// pdf: claude ● and opencode ◐ turn to −; ✗ (xlsx codex) also.
	m = send(t, m, press("esc"))
	if len(m.skills.pend[scopeProject]) != 0 {
		t.Error("esc did not clear")
	}
	m = send(t, m, press("j"), press("j"), press("h"), press("space"), press("l"), press("l"), press("space"))
	if cellOf(m, "pdf", "claude") != "−" || cellOf(m, "pdf", "opencode") != "−" {
		t.Errorf("pdf: %s %s", cellOf(m, "pdf", "claude"), cellOf(m, "pdf", "opencode"))
	}
	m = send(t, m, press("j"), press("j"), press("h"), press("h")) // xlsx, first column
	if selectedName(m) != "xlsx" {
		t.Fatalf("selected %s", selectedName(m))
	}
	m = send(t, m, press("l"), press("space")) // codex ✗
	if cellOf(m, "xlsx", "codex") != "−" {
		t.Errorf("xlsx codex: %s", cellOf(m, "xlsx", "codex"))
	}
}

func TestToggleRow(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("a")) // docx: nothing visible -> all on
	for _, a := range []string{"claude", "codex", "opencode", "pi"} {
		if cellOf(m, "docx", a) != "+" {
			t.Errorf("docx %s: %s", a, cellOf(m, "docx", a))
		}
	}
	m = send(t, m, press("a")) // all would be visible -> all off, i.e. nothing pending
	if len(m.skills.pend[scopeProject]) != 0 {
		t.Errorf("pending: %v", m.skills.pend)
	}
	// pdf is visible to claude and opencode only: a turns the rest on.
	m = send(t, m, press("j"), press("j"), press("a"))
	if cellOf(m, "pdf", "claude") != "●" || cellOf(m, "pdf", "codex") != "+" || cellOf(m, "pdf", "pi") != "+" {
		t.Errorf("pdf row: %s %s %s", cellOf(m, "pdf", "claude"), cellOf(m, "pdf", "codex"), cellOf(m, "pdf", "pi"))
	}
	m = send(t, m, press("a")) // now everything would be visible -> disable all
	if cellOf(m, "pdf", "claude") != "−" || cellOf(m, "pdf", "codex") != "○" {
		t.Errorf("pdf row after second a: %s %s", cellOf(m, "pdf", "claude"), cellOf(m, "pdf", "codex"))
	}
}

func TestReadOnlyRows(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("G")) // local (unmanaged)
	m = send(t, m, press("space"))
	if !strings.Contains(plain(m.View()), "not managed by skillet") || len(m.skills.pend[scopeProject]) != 0 {
		t.Errorf("unmanaged:\n%s", plain(m.View()))
	}
	m = send(t, m, press("g"), press("j"), press("space"))
	if selectedName(m) != "gone" || !strings.Contains(plain(m.View()), "no longer exists in the store") {
		t.Errorf("gone (%s):\n%s", selectedName(m), plain(m.View()))
	}
	// The all-scopes view is read-only.
	m = send(t, m, press("s"), press("s"))
	d := testData()
	m = send(t, m, skillsMsg{kind: scopeAll, data: d}, press("space"))
	if !strings.Contains(plain(m.View()), "read-only") || len(m.skills.pend[scopeAll]) != 0 {
		t.Errorf("all:\n%s", plain(m.View()))
	}
}

func TestPendingKeptPerScope(t *testing.T) {
	m := loadedModel(t, i18n.ZH)
	m = send(t, m, press("space"))
	if !strings.Contains(plain(m.View()), "1 项待应用（+1 −0）") {
		t.Fatalf("summary:\n%s", plain(m.View()))
	}
	m = send(t, m, press("s")) // global: nothing pending there
	if strings.Contains(plain(m.View()), "项待应用") {
		t.Error("pending leaked into the global scope")
	}
	m = send(t, m, press("s"), press("s"), skillsMsg{kind: scopeProject, data: testData()})
	if !strings.Contains(plain(m.View()), "1 项待应用") || cellOf(m, "docx", "claude") != "+" {
		t.Errorf("pending lost:\n%s", plain(m.View()))
	}
}
