package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"skillet/internal/config"
	"skillet/internal/deploy"
	"skillet/internal/i18n"
)

func TestDoctorOnRealStore(t *testing.T) {
	rs := newRealStore(t)
	repo := strings.SplitN(rs.pdf, ":", 2)[0]
	if dm := loadDoctor()().(doctorMsg); dm.err != nil || len(dm.items) != 0 {
		t.Fatalf("clean store: %+v", dm)
	}
	if am := applyCmd(scopeProject, rs.proj, []change{{ID: rs.pdf, Name: "pdf", Enable: []string{"claude"}}})().(applyMsg); am.err != nil {
		t.Fatal(am.err)
	}
	// A deleted link (fixable) and a deleted clone (not fixable).
	if err := os.Remove(filepath.Join(rs.links, "pdf")); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cfg.Store, "repos", filepath.FromSlash(repo), ".git")); err != nil {
		t.Fatal(err)
	}

	dm := loadDoctor()().(doctorMsg)
	if dm.err != nil || len(dm.items) != 2 {
		t.Fatalf("issues: %+v", dm)
	}
	kinds := map[string]doctorItem{}
	for _, it := range dm.items {
		kinds[it.Kind] = it
	}
	if !kinds["missing"].Fixable() || kinds["repo-missing"].Fixable() {
		t.Errorf("fixability: %+v", kinds)
	}
	if kinds["missing"].Skill != rs.pdf || kinds["missing"].Name != "pdf" || kinds["repo-missing"].Skill != "" {
		t.Errorf("skill links: %+v", kinds)
	}
	// Loading never repairs.
	if exists(filepath.Join(rs.links, "pdf")) {
		t.Fatal("check repaired the link")
	}

	fm := fixCmd()().(doctorFixMsg)
	if fm.err != nil {
		t.Fatal(fm.err)
	}
	if fixed, failed, open := fixSummary(fm.issues); fixed != 1 || failed != 0 || open != 1 {
		t.Errorf("summary %d %d %d", fixed, failed, open)
	}
	if _, err := os.Stat(filepath.Join(rs.links, "pdf", "SKILL.md")); err != nil {
		t.Errorf("link not repaired: %v", err)
	}
	if dm = loadDoctor()().(doctorMsg); len(dm.items) != 1 || dm.items[0].Kind != "repo-missing" {
		t.Errorf("after fix: %+v", dm.items)
	}
}

func fakeIssues() []doctorItem {
	return []doctorItem{
		{Issue: deploy.Issue{Kind: "missing", Target: "/work/proj/.claude/skills/pdf", Detail: "link was deleted"}, Skill: "r:skills/pdf", Name: "pdf"},
		{Issue: deploy.Issue{Kind: "clash", Target: "/work/proj", Detail: "claude sees 2 different skills named \"pdf\"", Paths: []string{"/a/pdf", "/b/pdf"}}},
		{Issue: deploy.Issue{Kind: "orphan-pin", Target: "/store/pins/x@abc", Detail: "pinned checkout is not used by any deployment"}},
	}
}

func doctorAt(t *testing.T, l i18n.Lang) Model {
	m := newTestModel(t, l)
	m = send(t, m, press("4"))
	return send(t, m, doctorMsg{items: fakeIssues()})
}

func TestDoctorViewEnglish(t *testing.T) {
	m := doctorAt(t, i18n.EN)
	v := plain(m.View())
	for _, want := range []string{"[4 Doctor]", "── Problems ──", "── Cleanup ──", "link missing", "name clash", "unused pin checkout", "can be fixed", "link was deleted", "Skill: r:skills/pdf"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	m = send(t, m, press("j"))
	v = plain(m.View())
	if !strings.Contains(v, "needs your attention") || !strings.Contains(v, "/b/pdf") {
		t.Errorf("clash detail:\n%s", v)
	}
	if lipgloss.Height(v) != 30 {
		t.Errorf("height %d", lipgloss.Height(v))
	}
}

func TestDoctorViewChinese(t *testing.T) {
	m := doctorAt(t, i18n.ZH)
	v := plain(m.View())
	for _, want := range []string{"[4 体检]", "── 问题 ──", "── 清理 ──", "链接缺失", "同名冲突", "未使用的固定检出", "可自动修复", "技能: r:skills/pdf"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	for i, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 100 {
			t.Errorf("line %d too wide", i)
		}
	}
}

func TestDoctorEmpty(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	m = send(t, m, press("4"), doctorMsg{})
	if v := plain(m.View()); !strings.Contains(v, "No problems found.") {
		t.Errorf("empty:\n%s", v)
	}
}

func TestDoctorFixFlow(t *testing.T) {
	m := doctorAt(t, i18n.EN)
	next, _ := m.Update(press("F"))
	m = next.(Model)
	v := plain(m.View())
	if !m.modal.open || !strings.Contains(v, "These will be repaired:") || !strings.Contains(v, "link missing") || !strings.Contains(v, "unused pin checkout") || strings.Contains(v[strings.Index(v, "These"):], "name clash") {
		t.Fatalf("dialog:\n%s", v)
	}
	next, cmd := m.Update(press("enter"))
	m = next.(Model)
	if m.modal.open || !m.applying || cmd == nil || !strings.Contains(plain(m.View()), "Fixing…") {
		t.Fatal("not fixing")
	}
	next, cmd = m.Update(doctorFixMsg{issues: []deploy.Issue{{Kind: "missing", Fixed: true}, {Kind: "orphan-pin", Err: errors.New("x")}, {Kind: "clash"}, {Kind: "pending"}}})
	m = next.(Model)
	if m.applying || cmd == nil || !strings.Contains(plain(m.View()), "Fixed 1, failed 1, still open 1") {
		t.Errorf("summary:\n%s", plain(m.View()))
	}
	// Nothing fixable: the dialog says so and cannot be confirmed.
	m = send(t, m, doctorMsg{items: fakeIssues()[1:2]}, press("F"))
	if !strings.Contains(plain(m.View()), "Nothing here can be fixed automatically.") {
		t.Errorf("no-fix dialog:\n%s", plain(m.View()))
	}
	m = send(t, m, press("enter"))
	if !m.modal.open || m.applying {
		t.Error("confirmed with nothing to fix")
	}
}

func TestDoctorJumpToSkill(t *testing.T) {
	m := doctorAt(t, i18n.EN)
	m = send(t, m, loadedSkills(), press("enter"))
	if m.tab != tabSkills || m.skills.query != "r:skills/pdf" {
		t.Fatalf("tab=%v query=%q", m.tab, m.skills.query)
	}
	m = send(t, m, press("4"), press("j"), press("enter"))
	if m.tab != tabDoctor || !strings.Contains(plain(m.View()), "not tied to a skill") {
		t.Errorf("view:\n%s", plain(m.View()))
	}
}
