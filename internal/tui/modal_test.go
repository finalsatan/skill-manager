package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"skillet/internal/deploy"
	"skillet/internal/i18n"
	"skillet/internal/state"
)

func fakePlans() []skillPlan {
	return []skillPlan{{
		Change: change{ID: "r:skills/docx", Name: "docx", Enable: []string{"claude"}},
		Step: &step{
			Agents: []string{"claude"},
			Actions: []planAction{{
				Action: deploy.Action{Kind: "create", Skill: "r:skills/docx", Target: proj + "/.claude/skills/docx", Mode: "symlink", Agents: []string{"claude"}},
				Source: "/store/r/skills/docx",
			}},
			Visible:  map[string]int{"claude": 1, "opencode": 1},
			Unwanted: []string{"opencode"},
		},
	}}
}

// toModal toggles docx/claude and opens the preview with injected results.
func toModal(t *testing.T, l i18n.Lang) Model {
	m := loadedModel(t, l)
	m = send(t, m, press("space"))
	next, cmd := m.Update(press("enter"))
	m = next.(Model)
	if !m.modal.open || !m.modal.loading || cmd == nil {
		t.Fatalf("modal: %+v cmd=%v", m.modal, cmd)
	}
	return send(t, m, previewMsg{kind: scopeProject, plans: fakePlans(), manifest: true})
}

func TestModalPreviewEnglish(t *testing.T) {
	m := toModal(t, i18n.EN)
	v := plain(m.View())
	for _, want := range []string{
		"Review changes", "docx", "Enable for claude",
		"+ .claude/skills/docx → /store/r/skills/docx (symlink)",
		"Visible afterwards: claude, opencode", "Note: also visible to opencode",
		"skillet.toml will be updated.", "[ Apply ]", "[ Back ]",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("modal lacks %q:\n%s", want, v)
		}
	}
	if h := lipgloss.Height(v); h != 30 {
		t.Errorf("height %d", h)
	}
}

func TestModalPreviewChinese(t *testing.T) {
	m := toModal(t, i18n.ZH)
	v := plain(m.View())
	for _, want := range []string{"预览变更", "启用：claude", "之后可见于：claude, opencode", "将同步更新 skillet.toml。", "[ 应用 ]", "[ 返回 ]", "也会看到它"} {
		if !strings.Contains(v, want) {
			t.Errorf("modal lacks %q:\n%s", want, v)
		}
	}
	for i, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 100 {
			t.Errorf("line %d too wide: %q", i, l)
		}
	}
}

func TestModalIsModal(t *testing.T) {
	m := toModal(t, i18n.EN)
	for _, k := range []string{"q", "2", "s", "space", "f", "/", "a", "G"} {
		next, cmd := m.Update(press(k))
		m = next.(Model)
		if cmd != nil {
			t.Errorf("%q returned a command", k)
		}
	}
	if m.tab != tabSkills || m.scope != scopeProject || !m.modal.open || m.skills.capturing() {
		t.Fatalf("keys leaked: tab=%v scope=%v", m.tab, m.scope)
	}
	if n, _ := m.skills.pend[scopeProject].counts(); n != 1 {
		t.Errorf("pending changed: %v", m.skills.pend)
	}
	if _, cmd := m.Update(press("ctrl+c")); cmd == nil {
		t.Error("ctrl+c must quit")
	}
	// esc goes back, keeping the pending changes.
	m = send(t, m, press("esc"))
	if m.modal.open || m.skills.summary() == "" {
		t.Error("esc did not return to the matrix with pending changes")
	}
}

func TestModalButtons(t *testing.T) {
	m := toModal(t, i18n.EN)
	m = send(t, m, press("tab"))
	if m.modal.button != btnBack {
		t.Fatal("tab did not move to Back")
	}
	m = send(t, m, press("enter"))
	if m.modal.open || m.applying {
		t.Error("Back should close without applying")
	}
	m = toModal2(t, m)
	next, cmd := m.Update(press("enter")) // Apply is focused by default
	m = next.(Model)
	if m.modal.open || !m.applying || cmd == nil {
		t.Fatalf("apply: open=%v applying=%v cmd=%v", m.modal.open, m.applying, cmd)
	}
	if !strings.Contains(plain(m.View()), "Applying…") {
		t.Errorf("no progress shown:\n%s", plain(m.View()))
	}
	// Input is ignored while applying, except ctrl+c.
	before := m.skills.summary()
	m = send(t, m, press("space"), press("q"), press("1"), press("s"))
	if m.skills.summary() != before || m.scope != scopeProject {
		t.Error("input not ignored while applying")
	}
	if _, cmd := m.Update(press("ctrl+c")); cmd == nil {
		t.Error("ctrl+c ignored")
	}
	// The result arrives: status line, pending cleared, reload started.
	next, cmd = m.Update(applyMsg{kind: scopeProject, done: []applied{{Name: "docx", Enable: []string{"claude"}}, {Name: "pptx", Disable: []string{"pi"}}}})
	m = next.(Model)
	if m.applying || m.skills.summary() != "" || cmd == nil {
		t.Errorf("after apply: applying=%v summary=%q cmd=%v", m.applying, m.skills.summary(), cmd)
	}
	if v := plain(m.View()); !strings.Contains(v, "Enabled docx · Disabled pptx") {
		t.Errorf("summary missing:\n%s", v)
	}
}

// toModal2 reopens the preview on a model that already has pending changes.
func toModal2(t *testing.T, m Model) Model {
	next, _ := m.Update(press("enter"))
	m = next.(Model)
	return send(t, m, previewMsg{kind: scopeProject, plans: fakePlans()})
}

func TestModalApplyErrorShownInStatus(t *testing.T) {
	m := toModal(t, i18n.ZH)
	m = send(t, m, press("enter"))
	m = send(t, m, applyMsg{kind: scopeProject, done: []applied{{Name: "docx", Enable: []string{"claude"}}}, err: errors.New("boom")})
	v := plain(m.View())
	if !strings.Contains(v, "已启用 docx · 失败：boom") {
		t.Errorf("view:\n%s", v)
	}
	// A busy store gets the friendly message.
	m = send(t, m, applyMsg{kind: scopeProject, err: state.ErrLocked})
	if !strings.Contains(plain(m.View()), "存储正忙") {
		t.Errorf("view:\n%s", plain(m.View()))
	}
}

func TestModalBlockedByErrors(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("space"), press("enter"))
	plans := fakePlans()
	plans[0].Err = errors.New("target exists and is not ours")
	m = send(t, m, previewMsg{kind: scopeProject, plans: plans})
	v := plain(m.View())
	if !strings.Contains(v, "target exists and is not ours") || !strings.Contains(v, "Cannot apply") {
		t.Errorf("errors not shown:\n%s", v)
	}
	m = send(t, m, press("enter"))
	if !m.modal.open || m.applying {
		t.Error("Apply must be disabled when planning failed")
	}
	// Opening the store failed (locked).
	m = send(t, m, press("esc"), press("enter"), previewMsg{kind: scopeProject, err: state.ErrLocked})
	if !strings.Contains(plain(m.View()), "busy") || m.modal.canApply() {
		t.Errorf("locked preview:\n%s", plain(m.View()))
	}
}

func TestEnterWithoutPendingOrInAllScope(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	next, cmd := m.Update(press("enter"))
	if next.(Model).modal.open || cmd != nil {
		t.Error("enter without pending changes opened the modal")
	}
}

func TestModalScrollsLongContent(t *testing.T) {
	m := toModal(t, i18n.EN)
	var plans []skillPlan
	for i := range 12 {
		p := fakePlans()[0]
		p.Change.Name = string(rune('a'+i)) + "skill"
		plans = append(plans, p)
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 20}, previewMsg{kind: scopeProject, plans: plans})
	v := plain(m.View())
	if lipgloss.Height(v) != 20 || !strings.Contains(v, "askill") || strings.Contains(v, "lskill") || !strings.Contains(v, "[ Apply ]") {
		t.Fatalf("first page:\n%s", v)
	}
	for range 100 {
		m = send(t, m, press("down"))
	}
	if m.modal.scroll > 60 {
		t.Errorf("scroll not clamped: %d", m.modal.scroll)
	}
	if v = plain(m.View()); !strings.Contains(v, "lskill") || !strings.Contains(v, "[ Apply ]") {
		t.Errorf("last page:\n%s", v)
	}
}
