package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/finalsatan/skill-manager/internal/deploy"
	"github.com/finalsatan/skill-manager/internal/i18n"
)

func TestReposOnRealStore(t *testing.T) {
	rs := newRealStore(t)
	repo := strings.SplitN(rs.pdf, ":", 2)[0]
	if am := applyCmd(scopeProject, rs.proj, []change{{ID: rs.pdf, Name: "pdf", Enable: []string{"claude"}}})().(applyMsg); am.err != nil {
		t.Fatal(am.err)
	}

	rm := loadRepos()().(reposMsg)
	if rm.err != nil || len(rm.rows) != 1 || rm.rows[0].ID != repo || rm.rows[0].Skills != 2 || rm.rows[0].Deployed != 1 || rm.rows[0].Behind > 0 {
		t.Fatalf("repos: %+v", rm)
	}
	// An upstream commit and a fetch make it one behind.
	if err := os.WriteFile(filepath.Join(rs.up, "skills", "pdf", "notes.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, rs.up, "add", "-A")
	gitIn(t, rs.up, "commit", "-qm", "notes")
	if um := loadUpdates(true)().(updatesMsg); um.err != nil || len(um.items) != 1 {
		t.Fatalf("fetch: %+v", um)
	}
	if rm = loadRepos()().(reposMsg); rm.rows[0].Behind != 1 {
		t.Errorf("behind = %d", rm.rows[0].Behind)
	}

	im := loadRepoInfo(repo)().(repoInfoMsg)
	if im.err != nil || im.info.Branch != "main" || len(im.info.Skills) != 2 {
		t.Fatalf("info: %+v", im)
	}
	var deployed int
	for _, sk := range im.info.Skills {
		deployed += len(sk.Deployed)
	}
	if deployed != 1 {
		t.Errorf("deployed uses: %d", deployed)
	}

	// Add a second repository from a local path.
	up2 := filepath.Join(filepath.Dir(rs.up), "up2")
	if err := os.MkdirAll(filepath.Join(up2, "skills", "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(up2, "skills", "foo", "SKILL.md"), []byte("---\nname: foo\ndescription: d\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, up2, "init", "-q", "-b", "main")
	gitIn(t, up2, "add", "-A")
	gitIn(t, up2, "commit", "-qm", "init")
	am := addRepoCmd(up2)().(addRepoMsg)
	if am.err != nil || am.skills != 1 || am.plan.Dir == "" {
		t.Fatalf("add: %+v", am)
	}
	if _, err := os.Stat(filepath.Join(am.plan.Dir, ".git")); err != nil {
		t.Errorf("not cloned: %v", err)
	}
	if again := addRepoCmd(up2)().(addRepoMsg); again.err == nil || !strings.Contains(again.err.Error(), "already") {
		t.Errorf("second add: %+v", again)
	}

	// Removing the first repo shows its links, then removes them.
	pm := removePlanCmd(repo)().(removePlanMsg)
	if pm.open != nil || pm.err != nil || len(pm.plan.Links) != 1 {
		t.Fatalf("remove plan: %+v", pm)
	}
	if !exists(filepath.Join(rs.links, "pdf")) {
		t.Fatal("plan removed something")
	}
	dm := removeRepoCmd(repo)().(removeDoneMsg)
	if dm.err != nil || len(dm.removed) != 1 {
		t.Fatalf("remove: %+v", dm)
	}
	if exists(filepath.Join(rs.links, "pdf")) {
		t.Error("link survived")
	}
	if rm = loadRepos()().(reposMsg); len(rm.rows) != 1 || rm.rows[0].ID != am.plan.ID {
		t.Errorf("repos after remove: %+v", rm.rows)
	}
}

func fakeRepoRows() []repoRow {
	return []repoRow{
		{ID: "github.com/acme/skills", Skills: 12, Behind: 3, Deployed: 4},
		{ID: "github.com/me/mine", Skills: 2, Behind: -1, Deployed: 0},
		{ID: "local/up", Skills: 1, Behind: 0, Deployed: 1},
	}
}

func fakeInfo() *deploy.RepoInfo {
	return &deploy.RepoInfo{
		ID: "github.com/acme/skills", URL: "https://github.com/acme/skills.git", Branch: "main", Head: "0123456789abcdef", Path: "/store/repos/github.com/acme/skills",
		Pins: []deploy.RepoPin{{Rev: "abc1234", Deployments: 1}},
		Skills: []deploy.RepoSkill{
			{ID: "x:pdf", Name: "pdf", RelPath: "skills/pdf", Pending: true, Deployed: []deploy.RepoUse{{Scope: "project", Dir: "/work/proj", Agents: []string{"claude"}, Pin: "abc1234567"}}},
			{ID: "x:docx", Name: "docx", RelPath: "skills/docx"},
		},
	}
}

func reposAt(t *testing.T, l i18n.Lang) Model {
	m := newTestModel(t, l)
	m = send(t, m, press("3"))
	return send(t, m, reposMsg{rows: fakeRepoRows()}, repoInfoMsg{id: "github.com/acme/skills", info: fakeInfo()})
}

func TestReposViewEnglish(t *testing.T) {
	m := reposAt(t, i18n.EN)
	v := plain(m.View())
	for _, want := range []string{
		"[3 Repos]", "REPO", "SKILLS", "BEHIND", "DEPLOYED", "acme/skills", "12", "me/mine", "-",
		"https://github.com/acme/skills.git", "main @ 0123456", "abc1234  (1 deployment(s))", "pdf", "skills/pdf", "/work/proj → claude @ abc1234", "not enabled anywhere",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if lipgloss.Height(v) != 30 {
		t.Errorf("height %d", lipgloss.Height(v))
	}
}

func TestReposViewChinese(t *testing.T) {
	m := reposAt(t, i18n.ZH)
	v := plain(m.View())
	for _, want := range []string{"[3 仓库]", "技能数", "落后", "已部署", "地址", "尚未在任何地方启用", "固定检出"} {
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

func TestReposEmpty(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	m = send(t, m, press("3"), reposMsg{})
	if v := plain(m.View()); !strings.Contains(v, "No repositories yet.") || !strings.Contains(v, "Press n to add one.") {
		t.Errorf("empty:\n%s", v)
	}
}

func TestReposAddDialog(t *testing.T) {
	m := reposAt(t, i18n.EN)
	next, cmd := m.Update(press("n"))
	m = next.(Model)
	if !m.modal.open || cmd == nil {
		t.Fatal("n did not open the dialog")
	}
	if v := plain(m.View()); !strings.Contains(v, "Add repository") || !strings.Contains(v, "owner/repo, a Git URL") {
		t.Errorf("dialog:\n%s", v)
	}
	// Empty input cannot be confirmed; typed text goes to the input.
	m = send(t, m, press("enter"))
	if !m.modal.open || m.applying {
		t.Fatal("empty input accepted")
	}
	for _, c := range "me/new" {
		m = send(t, m, press(string(c)))
	}
	if m.modal.input.Value() != "me/new" || m.tab != tabRepos {
		t.Fatalf("input %q tab %v", m.modal.input.Value(), m.tab)
	}
	if !strings.Contains(plain(m.View()), "me/new") {
		t.Error("input not shown")
	}
	next, cmd = m.Update(press("enter"))
	m = next.(Model)
	if m.modal.open || !m.applying || cmd == nil || !strings.Contains(plain(m.View()), "Adding repository…") {
		t.Fatalf("not adding: %v", m.applying)
	}
	m = send(t, m, press("x"), press("n"))
	if m.modal.open {
		t.Error("input accepted while busy")
	}
	next, cmd = m.Update(addRepoMsg{plan: deploy.AddPlan{ID: "github.com/me/new", Dir: "/store/repos/github.com/me/new"}, skills: 5})
	m = next.(Model)
	if m.applying || cmd == nil || !strings.Contains(plain(m.View()), "Added me/new: 5 skill(s)") {
		t.Errorf("after add:\n%s", plain(m.View()))
	}
	m = send(t, m, addRepoMsg{err: errors.New("repo x is already added")})
	if !strings.Contains(plain(m.View()), "already added") {
		t.Error("error not shown")
	}
	// esc cancels the dialog.
	m = send(t, m, press("n"), press("esc"))
	if m.modal.open {
		t.Error("esc did not cancel")
	}
}

func TestReposRemoveDialog(t *testing.T) {
	m := reposAt(t, i18n.ZH)
	_, cmd := m.Update(press("x"))
	if cmd == nil {
		t.Fatal("x started nothing")
	}
	plan := deploy.RemovePlan{Repo: "github.com/acme/skills", Links: []string{"/work/proj/.claude/skills/pdf"}, Pins: []string{"/store/pins/x@abc"}}
	m = send(t, m, removePlanMsg{plan: plan})
	v := plain(m.View())
	for _, want := range []string{"移除 acme/skills", "将移除的链接：", "− /work/proj/.claude/skills/pdf", "将移除的固定检出：", "[ 移除 ]", "[ 返回 ]"} {
		if !strings.Contains(v, want) {
			t.Errorf("dialog lacks %q:\n%s", want, v)
		}
	}
	// Back has the focus: enter must not remove anything.
	if m.modal.button != btnBack {
		t.Fatal("default focus is not Back")
	}
	m = send(t, m, press("enter"))
	if m.modal.open || m.applying {
		t.Fatal("enter on Back removed")
	}
	// Confirm: move to the primary button.
	m = send(t, m, removePlanMsg{plan: plan}, press("left"))
	next, cmd := m.Update(press("enter"))
	m = next.(Model)
	if !m.applying || cmd == nil || !strings.Contains(plain(m.View()), "正在移除…") {
		t.Fatalf("not removing: %v", m.applying)
	}
	next, cmd = m.Update(removeDoneMsg{id: plan.Repo, removed: plan.Links})
	m = next.(Model)
	if m.applying || cmd == nil || !strings.Contains(plain(m.View()), "已移除 acme/skills（移除了 1 个链接）") {
		t.Errorf("after remove:\n%s", plain(m.View()))
	}
}

func TestReposRemoveRefused(t *testing.T) {
	m := reposAt(t, i18n.EN)
	m = send(t, m, removePlanMsg{plan: deploy.RemovePlan{Repo: "github.com/acme/skills", Links: []string{"/p/x"}}, err: errors.New("copy was modified by hand")})
	v := plain(m.View())
	if !strings.Contains(v, "Cannot remove:") || !strings.Contains(v, "copy was modified by hand") {
		t.Errorf("refusal not shown:\n%s", v)
	}
	m = send(t, m, press("left"), press("enter"))
	if !m.modal.open || m.applying {
		t.Error("Remove must be disabled when refused")
	}
	// Nothing deployed: the plain message.
	m = send(t, m, press("esc"), removePlanMsg{plan: deploy.RemovePlan{Repo: "local/up"}})
	if !strings.Contains(plain(m.View()), "only the clone is deleted") {
		t.Errorf("no-deploy dialog:\n%s", plain(m.View()))
	}
}

func TestReposJumps(t *testing.T) {
	m := reposAt(t, i18n.EN)
	// enter: Skills tab with the repo as search.
	m = send(t, m, loadedSkills(), press("enter"))
	if m.tab != tabSkills || m.skills.query != "github.com/acme/skills" {
		t.Fatalf("tab=%v query=%q", m.tab, m.skills.query)
	}
	// u on a repo with no pending changes explains instead of jumping.
	m = send(t, m, press("3"), press("j"), press("u"))
	if m.tab != tabRepos || !strings.Contains(plain(m.View()), "No pending updates for this repo") {
		t.Errorf("view:\n%s", plain(m.View()))
	}
	// u on the one that is behind selects it on the Updates tab.
	m = send(t, m, press("k"))
	next, cmd := m.Update(press("u"))
	m = next.(Model)
	if m.tab != tabUpdates || cmd == nil {
		t.Fatalf("tab=%v", m.tab)
	}
	m = send(t, m, updatesMsg{items: fakeItems()[1:2]}) // does not hold the repo: stays first
	m = send(t, m, press("3"), press("u"), updatesMsg{items: fakeItems()})
	if it := m.updates.selected(); it == nil || it.Check.Repo != "github.com/acme/skills" {
		t.Errorf("selected %+v", it)
	}
}

func loadedSkills() skillsMsg { return skillsMsg{kind: scopeProject, data: testData()} }
