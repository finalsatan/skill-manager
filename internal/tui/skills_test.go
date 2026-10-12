package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/i18n"
)

func testData() skillsData {
	rows := buildRows(buildInput{
		Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: testSkills(), Entries: testEntries(), Undeployed: true,
		Targets: map[string]string{proj + "/.claude/skills/pdf": "/store/r/skills/pdf"},
	})
	return skillsData{Agents: testAgents(), Sections: []section{{Scope: agents.Scope{Dir: proj}, Rows: rows}}}
}

func loadedModel(t *testing.T, l i18n.Lang) Model {
	m := newTestModel(t, l)
	// The grouped view is the default; most tests look at the flat list.
	return send(t, m, skillsMsg{kind: scopeProject, data: testData()}, press("v"))
}

// groupedModel is the model as a user first sees it.
func groupedModel(t *testing.T, l i18n.Lang) Model {
	m := newTestModel(t, l)
	return send(t, m, skillsMsg{kind: scopeProject, data: testData()})
}

func selectedName(m Model) string {
	if it := m.skills.selected(); it != nil {
		return it.r.Name
	}
	return ""
}

func TestSkillsViewEnglish(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	v := plain(m.View())
	for _, want := range []string{
		"SKILL", "claude", "opencode", "REPO", "STATUS",
		"pdf", "docx", "xlsx", "local", "●", "◐", "○", "✗", "■",
		"↑ update", "pinned abcdef1", "deck ← slides", "✗ broken", "unmanaged",
		"● linked", "6 of 6",
		"q quit",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	// docx sorts first; its detail is shown.
	if selectedName(m) != "docx" || !strings.Contains(v, "Not enabled in this scope.") {
		t.Errorf("selected %q:\n%s", selectedName(m), v)
	}
}

func TestSkillsViewChinese(t *testing.T) {
	m := loadedModel(t, i18n.ZH)
	v := plain(m.View())
	for _, want := range []string{"技能", "仓库", "状态", "↑ 有更新", "固定 abcdef1", "✗ 损坏", "未托管", "● 已启用", "6 / 6", "在当前范围内未启用。", "退出"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	for i, line := range strings.Split(v, "\n") {
		if w := lipgloss.Width(line); w > 100 {
			t.Errorf("line %d is %d cells wide: %q", i, w, line)
		}
	}
}

func TestSkillsNavigationAndDetail(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("j"), press("j")) // docx, gone, pdf
	if selectedName(m) != "pdf" {
		t.Fatalf("selected %q", selectedName(m))
	}
	v := plain(m.View())
	for _, want := range []string{
		"r:skills/pdf", "/store/r", "1234567", "symlink",
		".claude/skills/pdf → /store/r/skills/pdf", "claude, opencode",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("detail lacks %q:\n%s", want, v)
		}
	}
	m = send(t, m, press("k"), press("up"), press("up"))
	if selectedName(m) != "docx" {
		t.Errorf("up past the top moved to %q", selectedName(m))
	}
	m = send(t, m, press("G"))
	if selectedName(m) != "local" {
		t.Errorf("G -> %q", selectedName(m))
	}
	m = send(t, m, press("down"))
	if selectedName(m) != "local" {
		t.Errorf("down past the end -> %q", selectedName(m))
	}
	m = send(t, m, press("g"))
	if selectedName(m) != "docx" {
		t.Errorf("g -> %q", selectedName(m))
	}
	// Pending note for xlsx.
	m = send(t, m, press("j"), press("j"), press("j"), press("j"))
	if selectedName(m) != "xlsx" || !strings.Contains(plain(m.View()), "Upstream has changes") {
		t.Errorf("xlsx detail:\n%s", plain(m.View()))
	}
}

func TestSkillsFilterToggle(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("j"), press("j")) // pdf
	m = send(t, m, press("f"))
	v := plain(m.View())
	if strings.Contains(v, "docx") || !strings.Contains(v, "[only enabled]") || !strings.Contains(v, "5 of 6") {
		t.Errorf("filtered view:\n%s", v)
	}
	if selectedName(m) != "pdf" {
		t.Errorf("selection lost: %q", selectedName(m))
	}
	m = send(t, m, press("f"))
	if !strings.Contains(plain(m.View()), "docx") {
		t.Error("filter did not toggle off")
	}
}

func TestSkillsSearch(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = send(t, m, press("/"))
	if !m.skills.capturing() {
		t.Fatal("/ did not start the search")
	}
	// While typing, command keys are text.
	for _, c := range "q2s" {
		m = send(t, m, press(string(c)))
	}
	if m.tab != tabSkills || m.scope != scopeProject || m.skills.query != "q2s" {
		t.Fatalf("keys leaked: tab=%v scope=%v query=%q", m.tab, m.scope, m.skills.query)
	}
	if !strings.Contains(plain(m.View()), "No skills match.") {
		t.Errorf("no-match view:\n%s", plain(m.View()))
	}
	m = send(t, m, press("esc"))
	if m.skills.capturing() || m.skills.query != "" || selectedName(m) != "docx" {
		t.Errorf("esc: capturing=%v query=%q sel=%q", m.skills.capturing(), m.skills.query, selectedName(m))
	}

	m = send(t, m, press("/"))
	for _, c := range "spread" {
		m = send(t, m, press(string(c)))
	}
	m = send(t, m, press("enter"))
	v := plain(m.View())
	if m.skills.capturing() || selectedName(m) != "xlsx" || strings.Contains(v, "docx") || !strings.Contains(v, "1 of 6") {
		t.Errorf("search result: sel=%q\n%s", selectedName(m), v)
	}
	// Back to normal keys: q quits.
	if _, cmd := m.Update(press("q")); cmd == nil {
		t.Error("q after search should quit")
	}
}

func TestSkillsSearchChinese(t *testing.T) {
	m := loadedModel(t, i18n.ZH)
	d := m.skills.data
	d.Sections[0].Rows[0].Description = "处理 Word 文档"
	m = send(t, m, skillsMsg{kind: scopeProject, data: d}, press("/"))
	for _, c := range "文档" {
		m = send(t, m, press(string(c)))
	}
	if selectedName(m) != "docx" || !strings.Contains(plain(m.View()), "搜索: 文档") {
		t.Errorf("sel=%q\n%s", selectedName(m), plain(m.View()))
	}
}

func TestSkillsAllScopesSections(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	m = send(t, m, press("s"), press("s")) // project -> global -> all
	if m.scope != scopeAll {
		t.Fatalf("scope %v", m.scope)
	}
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: testSkills(), Entries: testEntries()})
	d := skillsData{Agents: testAgents(), Sections: []section{
		{Scope: agents.Scope{Global: true}, Rows: rows[:1]},
		{Scope: agents.Scope{Dir: proj}, Rows: rows},
	}}
	// A stale answer for another scope is ignored.
	m = send(t, m, skillsMsg{kind: scopeProject, data: testData()})
	if m.skills.loaded {
		t.Error("stale data accepted")
	}
	m = send(t, m, skillsMsg{kind: scopeAll, data: d})
	v := plain(m.View())
	for _, want := range []string{"── Global ──", "── " + proj + " ──"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	// The cursor skips headers.
	start := m.skills.cursor
	if m.skills.items[start].r == nil || m.skills.items[start-1].r != nil {
		t.Errorf("cursor %d not on the first row", start)
	}
	m = send(t, m, press("j"))
	if m.skills.items[m.skills.cursor].sec != 1 {
		t.Errorf("did not cross into the next section: %d", m.skills.cursor)
	}
	if m.skills.items[m.skills.cursor-1].header == "" {
		t.Errorf("header not directly above the section's first row")
	}
}

func manyRows(n int) skillsData {
	var infos []skillInfo
	for i := range n {
		infos = append(infos, skillInfo{ID: fmt.Sprintf("r:s/skill%03d", i), Name: fmt.Sprintf("skill%03d", i), Repo: "r"})
	}
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: infos, Undeployed: true})
	return skillsData{Agents: testAgents(), Sections: []section{{Scope: agents.Scope{Dir: proj}, Rows: rows}}}
}

func TestSkillsScrolling(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	m = send(t, m, tea_size(80, 20), skillsMsg{kind: scopeProject, data: manyRows(100)}, press("v"))
	if got := m.skills.listH; got < 3 {
		t.Fatalf("list height %d", got)
	}
	for i := range 40 {
		m = send(t, m, press("j"))
		v := plain(m.View())
		want := fmt.Sprintf("skill%03d", i+1)
		if !strings.Contains(v, want) {
			t.Fatalf("after %d moves %s is not visible:\n%s", i+1, want, v)
		}
		if h := lipgloss.Height(v); h != 20 {
			t.Fatalf("view height %d", h)
		}
	}
	m = send(t, m, press("pgdown"))
	if got := selectedName(m); got <= "skill041" {
		t.Errorf("pgdown stayed at %s", got)
	}
	m = send(t, m, press("pgup"), press("pgup"))
	m = send(t, m, press("G"))
	if selectedName(m) != "skill099" || !strings.Contains(plain(m.View()), "skill099") {
		t.Errorf("G -> %s", selectedName(m))
	}
	m = send(t, m, press("g"))
	if selectedName(m) != "skill000" || !strings.Contains(plain(m.View()), "skill000") {
		t.Errorf("g -> %s", selectedName(m))
	}
}

func TestSkillsTinyAndNarrow(t *testing.T) {
	m := loadedModel(t, i18n.ZH)
	for _, sz := range [][2]int{{40, 8}, {20, 5}, {200, 60}, {1, 1}} {
		m = send(t, m, tea_size(sz[0], sz[1]))
		v := plain(m.View())
		if h := lipgloss.Height(v); h > sz[1] {
			t.Errorf("%v: view is %d lines", sz, h)
		}
		for i, line := range strings.Split(v, "\n") {
			if w := lipgloss.Width(line); w > sz[0] {
				t.Errorf("%v: line %d is %d cells", sz, i, w)
			}
		}
	}
}

func TestSkillsEmptyStore(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	m = send(t, m, skillsMsg{kind: scopeProject, data: skillsData{Agents: testAgents(), Sections: []section{{Scope: agents.Scope{Dir: proj}}}}})
	if !strings.Contains(plain(m.View()), "No skills in the store yet") {
		t.Errorf("view:\n%s", plain(m.View()))
	}
	// Keys on an empty list do not panic.
	m = send(t, m, press("j"), press("G"), press("g"), press("pgdown"), press("f"))
}
