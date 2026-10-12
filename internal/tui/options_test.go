package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/finalsatan/skill-manager/internal/i18n"
)

func TestPinAndAliasOnRealStore(t *testing.T) {
	rs := newRealStore(t)
	if am := applyCmd(scopeProject, rs.proj, []change{{ID: rs.pdf, Name: "pdf", Enable: []string{"claude", "codex"}}})().(applyMsg); am.err != nil {
		t.Fatal(am.err)
	}
	out, err := exec.Command("git", "-C", rs.up, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))
	link := filepath.Join(rs.links, "pdf")
	dest := func(p string) string {
		d, err := os.Readlink(p)
		if err != nil {
			t.Fatalf("readlink %s: %v", p, err)
		}
		return d
	}
	if strings.Contains(dest(link), "pins") {
		t.Fatal("pinned before pinning")
	}

	// Preview: the agents come from the store, nothing changes on disk.
	pm := optionChangeCmd(scopeProject, rs.proj, change{ID: rs.pdf, Name: "pdf", Pin: sha})().(previewMsg)
	if pm.err != nil || len(pm.changes) != 1 || len(pm.plans) != 1 || pm.plans[0].Err != nil {
		t.Fatalf("pin preview: %+v", pm)
	}
	if got := strings.Join(pm.changes[0].Enable, ","); got != "claude,codex" {
		t.Errorf("agents = %q", got)
	}
	if strings.Contains(dest(link), "pins") || exists(filepath.Join(filepath.Dir(rs.links), "..", "store", "pins")) {
		t.Error("preview pinned something")
	}
	am := applyCmd(scopeProject, rs.proj, pm.changes)().(applyMsg)
	if am.err != nil || len(am.done) != 1 || am.done[0].Pin != sha {
		t.Fatalf("pin apply: %+v", am)
	}
	if !strings.Contains(dest(link), "pins") {
		t.Errorf("link not pinned: %s", dest(link))
	}
	if s, _ := applySummary(am.done, nil); !strings.Contains(s, "Pinned pdf to "+sha[:7]) {
		t.Errorf("summary %q", s)
	}

	// Unpin.
	pm = optionChangeCmd(scopeProject, rs.proj, change{ID: rs.pdf, Name: "pdf", Unpin: true})().(previewMsg)
	if pm.err != nil || pm.plans[0].Err != nil {
		t.Fatalf("unpin preview: %+v", pm)
	}
	if am = applyCmd(scopeProject, rs.proj, pm.changes)().(applyMsg); am.err != nil || strings.Contains(dest(link), "pins") {
		t.Fatalf("unpin: %+v %s", am, dest(link))
	}

	// Alias, then back.
	pm = optionChangeCmd(scopeProject, rs.proj, change{ID: rs.pdf, Name: "pdf", Alias: "pdf-tool"})().(previewMsg)
	if pm.err != nil || pm.plans[0].Err != nil {
		t.Fatalf("alias preview: %+v", pm)
	}
	if !exists(link) || exists(filepath.Join(rs.links, "pdf-tool")) {
		t.Fatal("alias preview changed the disk")
	}
	if am = applyCmd(scopeProject, rs.proj, pm.changes)().(applyMsg); am.err != nil {
		t.Fatal(am.err)
	}
	if !exists(filepath.Join(rs.links, "pdf-tool")) || exists(link) {
		t.Error("link not renamed")
	}
	pm = optionChangeCmd(scopeProject, rs.proj, change{ID: rs.pdf, Name: "pdf", Unalias: true})().(previewMsg)
	if am = applyCmd(scopeProject, rs.proj, pm.changes)().(applyMsg); am.err != nil || !exists(link) || exists(filepath.Join(rs.links, "pdf-tool")) {
		t.Fatalf("unalias: %+v", am)
	}

	// Bad alias: planning fails and the error is carried; not enabled: no plan.
	pm = optionChangeCmd(scopeProject, rs.proj, change{ID: rs.pdf, Name: "pdf", Alias: "Bad Name"})().(previewMsg)
	if pm.err != nil || pm.plans[0].Err == nil || !(modalState{plans: pm.plans}).blocked() {
		t.Errorf("bad alias: %+v", pm)
	}
	pm = optionChangeCmd(scopeProject, rs.proj, change{ID: rs.docx, Name: "docx", Pin: sha})().(previewMsg)
	if pm.err == nil || !strings.Contains(pm.err.Error(), "not enabled") {
		t.Errorf("not enabled: %+v", pm)
	}
}

func rowFor(t *testing.T, m Model, name string) Model {
	t.Helper()
	m = send(t, m, press("g"))
	for i := 0; selectedName(m) != name && i < 20; i++ {
		m = send(t, m, press("j"))
	}
	if selectedName(m) != name {
		t.Fatalf("no row %s", name)
	}
	return m
}

func TestPinDialogFlow(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	m = rowFor(t, m, "pdf")
	next, cmd := m.Update(press("p"))
	m = next.(Model)
	if !m.modal.open || cmd == nil {
		t.Fatal("p did not open the dialog")
	}
	if v := plain(m.View()); !strings.Contains(v, "Pin pdf") || !strings.Contains(v, "Revision") {
		t.Errorf("dialog:\n%s", v)
	}
	for _, c := range "v1.2" {
		m = send(t, m, press(string(c)))
	}
	next, cmd = m.Update(press("enter"))
	m = next.(Model)
	if !m.modal.open || !m.modal.loading || cmd == nil {
		t.Fatalf("no preview started: %+v", m.modal.loading)
	}
	plans := fakePlans()
	plans[0].Change = change{ID: "r:skills/pdf", Name: "pdf", Enable: []string{"claude", "opencode"}, Pin: "v1.2"}
	m = send(t, m, previewMsg{kind: scopeProject, plans: plans, changes: []change{plans[0].Change}})
	v := plain(m.View())
	if !strings.Contains(v, "Pin to v1.2") || !strings.Contains(v, "Enable for claude, opencode") || !strings.Contains(v, "[ Apply ]") {
		t.Errorf("preview:\n%s", v)
	}
	next, cmd = m.Update(press("enter"))
	m = next.(Model)
	if !m.applying || cmd == nil {
		t.Fatal("apply did not start")
	}
	m = send(t, m, applyMsg{kind: scopeProject, done: []applied{{Name: "pdf", Pin: "v1.2"}}})
	if !strings.Contains(plain(m.View()), "Pinned pdf to v1.2") {
		t.Errorf("summary:\n%s", plain(m.View()))
	}
}

func TestAliasDialogChinese(t *testing.T) {
	m := loadedModel(t, i18n.ZH)
	m = rowFor(t, m, "pdf")
	m = send(t, m, press("R"))
	v := plain(m.View())
	if !m.modal.open || !strings.Contains(v, "重命名 pdf 的链接") || !strings.Contains(v, "链接名") || !strings.Contains(v, "[ 预览 ]") {
		t.Fatalf("dialog:\n%s", v)
	}
	// Empty input is allowed: it means back to the skill's own name.
	next, cmd := m.Update(press("enter"))
	m = next.(Model)
	if !m.modal.loading || cmd == nil {
		t.Fatal("empty alias not accepted")
	}
	ch := change{ID: "r:skills/pdf", Name: "pdf", Enable: []string{"claude"}, Unalias: true}
	plans := fakePlans()
	plans[0].Change = ch
	m = send(t, m, previewMsg{kind: scopeProject, plans: plans, changes: []change{ch}})
	if !strings.Contains(plain(m.View()), "链接名：使用技能自己的名字") {
		t.Errorf("preview:\n%s", plain(m.View()))
	}
}

func TestPinAndAliasRefusals(t *testing.T) {
	m := loadedModel(t, i18n.EN)
	cases := map[string]string{
		"docx":  "Enable the skill first",
		"gone":  "no longer exists in the store",
		"local": "not managed by skillet",
	}
	for name, want := range cases {
		m = rowFor(t, m, name)
		m = send(t, m, press("p"))
		if m.modal.open || !strings.Contains(plain(m.View()), want) {
			t.Errorf("%s: open=%v\n%s", name, m.modal.open, plain(m.View()))
		}
	}
	// Pending changes block it.
	m = rowFor(t, m, "pdf")
	m = send(t, m, press("space"), press("R"))
	if m.modal.open || !strings.Contains(plain(m.View()), "Apply or clear (esc) the pending changes first.") {
		t.Errorf("pending:\n%s", plain(m.View()))
	}
	// Not in the all-scopes view.
	m = send(t, m, press("s"), press("s"), skillsMsg{kind: scopeAll, data: testData()}, press("p"))
	if m.modal.open || !strings.Contains(plain(m.View()), "read-only") {
		t.Errorf("all:\n%s", plain(m.View()))
	}
}
