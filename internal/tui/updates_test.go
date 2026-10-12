package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/finalsatan/skill-manager/internal/deploy"
	"github.com/finalsatan/skill-manager/internal/i18n"
)

func TestUpdatesOnRealStore(t *testing.T) {
	rs := newRealStore(t)
	if am := applyCmd(scopeProject, rs.proj, []change{{ID: rs.pdf, Name: "pdf", Enable: []string{"claude"}}})().(applyMsg); am.err != nil {
		t.Fatal(am.err)
	}
	// Nothing is pending, and loading without the network finds nothing.
	if um := loadUpdates(false)().(updatesMsg); um.err != nil || len(um.items) != 0 {
		t.Fatalf("initial: %+v", um)
	}

	// Upstream gains a commit touching pdf: a script and the SKILL.md.
	if err := os.WriteFile(filepath.Join(rs.up, "skills", "pdf", "run.sh"), []byte("echo hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rs.up, "skills", "pdf", "SKILL.md"), []byte("---\nname: pdf\ndescription: changed\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, rs.up, "add", "-A")
	gitIn(t, rs.up, "commit", "-qm", "add a script to pdf")

	// Not seen before a fetch.
	if um := loadUpdates(false)().(updatesMsg); len(um.items) != 0 {
		t.Fatalf("pending without fetch: %+v", um.items)
	}
	um := loadUpdates(true)().(updatesMsg)
	if um.err != nil || len(um.items) != 1 || !um.fetched {
		t.Fatalf("fetch: %+v", um)
	}
	it := um.items[0]
	c := it.Check
	if c.Commits != 1 || len(c.Changes) != 1 || c.Changes[0].Name != "pdf" || !c.Changes[0].Code || c.Changes[0].Deployed != 1 {
		t.Fatalf("check: %+v", c)
	}
	if len(it.Serves) != 1 || it.Serves[0] != "claude" || it.Last.IsZero() {
		t.Errorf("item: %+v", it)
	}

	dm := loadDiff(c)().(diffMsg)
	if dm.err != nil || !strings.Contains(dm.log, "add a script to pdf") || !strings.Contains(dm.stat, "run.sh") {
		t.Fatalf("diff: %+v", dm)
	}
	fm := loadFullDiff(c)().(fullDiffMsg)
	if fm.err != nil || !strings.Contains(fm.text, "+echo hi") {
		t.Fatalf("full diff: %+v", fm)
	}

	// The lock is free between operations; the update moves the clone.
	done := updateCmd([]deploy.RepoCheck{c})().(updateDoneMsg)
	if done.err != nil || len(done.done) != 1 || done.done[0].To != c.Upstream {
		t.Fatalf("update: %+v", done)
	}
	if _, err := os.Stat(filepath.Join(rs.links, "pdf", "run.sh")); err != nil {
		t.Errorf("link does not see the new file: %v", err)
	}
	if um := loadUpdates(false)().(updatesMsg); len(um.items) != 0 {
		t.Errorf("still pending: %+v", um.items)
	}
	// A stale check is refused and stops the run.
	again := updateCmd([]deploy.RepoCheck{c})().(updateDoneMsg)
	if again.err == nil || len(again.done) != 0 {
		t.Errorf("stale update: %+v", again)
	}
	s, isErr := updateSummary(done.done, nil)
	if isErr || !strings.Contains(s, "Updated") || strings.Contains(s, "Claude Code reloads") {
		t.Errorf("summary %q", s)
	}
}

func fakeItems() []updateItem {
	return []updateItem{
		{
			Check: deploy.RepoCheck{Repo: "github.com/acme/skills", Head: "a1", Upstream: "b2", Commits: 3, Changes: []deploy.SkillChange{
				{ID: "github.com/acme/skills:pdf", Name: "pdf", Code: true, Deployed: 2, Pinned: 1},
				{ID: "github.com/acme/skills:docx", Name: "docx", Deployed: 0},
			}, NewSkills: []string{"skills/pptx"}},
			Last: now().Add(-3 * time.Hour), Serves: []string{"claude", "codex"},
		},
		{Check: deploy.RepoCheck{Repo: "github.com/me/mine", Head: "c3", Upstream: "d4", Commits: 1, Changes: []deploy.SkillChange{{Name: "tool", Deployed: 1}}}, Serves: []string{"claude"}},
		{Check: deploy.RepoCheck{Repo: "github.com/bad/repo", Err: os.ErrInvalid}},
	}
}

func updatesModelAt(t *testing.T, l i18n.Lang) Model {
	m := newTestModel(t, l)
	m = send(t, m, press("2"))
	return send(t, m, updatesMsg{items: fakeItems()}, diffMsg{key: "github.com/acme/skills@b2", diffInfo: diffInfo{log: "abc1234 add pptx\ndef5678 fix pdf", stat: " skills/pdf/run.sh | 3 +++"}})
}

func TestUpdatesViewEnglish(t *testing.T) {
	m := updatesModelAt(t, i18n.EN)
	v := plain(m.View())
	for _, want := range []string{
		"[2 Updates]", "acme/skills", "3 commit(s)", "pdf, docx", "⚠ scripts changed", "1 pinned deployment(s) stay", "new: skills/pptx",
		"abc1234 add pptx", "skills/pdf/run.sh | 3 +++", "pdf: 2 deployment(s) follow", "docx: not enabled anywhere", "Last checked 3 h ago",
		"Claude Code reloads skills", "me/mine", "bad/repo",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if h := lipgloss.Height(v); h != 30 {
		t.Errorf("height %d", h)
	}
}

func TestUpdatesViewChinese(t *testing.T) {
	m := updatesModelAt(t, i18n.ZH)
	v := plain(m.View())
	for _, want := range []string{"[2 更新]", "3 个提交", "⚠ 含脚本改动", "1 个固定部署不会更新", "上次检查：3 小时前", "pdf：2 处部署随之更新", "重启 Codex"} {
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

func TestUpdatesEmptyState(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	m = send(t, m, press("2"), updatesMsg{})
	v := plain(m.View())
	if !strings.Contains(v, "Everything is up to date.") || !strings.Contains(v, "Press r to check upstream.") {
		t.Errorf("empty:\n%s", v)
	}
}

func TestUpdatesMarkAndConfirm(t *testing.T) {
	m := updatesModelAt(t, i18n.EN)
	// The error item cannot be marked.
	m = send(t, m, press("space"), press("j"), press("space"), press("j"), press("space"))
	if !m.updates.checked["github.com/acme/skills"] || !m.updates.checked["github.com/me/mine"] || m.updates.checked["github.com/bad/repo"] {
		t.Fatalf("checked: %v", m.updates.checked)
	}
	if !strings.Contains(plain(m.View()), "[x]") {
		t.Error("no checkbox shown")
	}
	next, _ := m.Update(press("enter"))
	m = next.(Model)
	v := plain(m.View())
	if !m.modal.open || !strings.Contains(v, "Update repositories") || !strings.Contains(v, "acme/skills") && !strings.Contains(v, "github.com/acme/skills") || !strings.Contains(v, "[ Update ]") {
		t.Fatalf("confirm dialog:\n%s", v)
	}
	// Nothing leaks through the dialog; esc cancels.
	m = send(t, m, press("r"), press("1"))
	if m.tab != tabUpdates || m.applying {
		t.Error("dialog leaked keys")
	}
	m = send(t, m, press("esc"))
	if m.modal.open {
		t.Fatal("esc did not close")
	}
	next, _ = m.Update(press("enter"))
	m = next.(Model)
	next, cmd := m.Update(press("enter"))
	m = next.(Model)
	if m.modal.open || !m.applying || cmd == nil || !strings.Contains(plain(m.View()), "Updating…") {
		t.Fatalf("not updating: applying=%v\n%s", m.applying, plain(m.View()))
	}
	m = send(t, m, press("j"), press("space")) // ignored while busy
	next, cmd = m.Update(updateDoneMsg{done: []updatedRepo{{Repo: "github.com/acme/skills", Serves: []string{"claude", "codex"}}}})
	m = next.(Model)
	if m.applying || cmd == nil {
		t.Error("still busy after the result")
	}
	if v := plain(m.View()); !strings.Contains(v, "Updated acme/skills") || !strings.Contains(v, "Claude Code reloads") {
		t.Errorf("summary:\n%s", v)
	}
}

func TestUpdatesSelectedWhenNothingChecked(t *testing.T) {
	m := updatesModelAt(t, i18n.EN)
	if got := m.updates.toUpdate(); len(got) != 1 || got[0].Repo != "github.com/acme/skills" {
		t.Errorf("toUpdate: %+v", got)
	}
	m = send(t, m, press("j"), press("j")) // the failed one
	if got := m.updates.toUpdate(); len(got) != 0 {
		t.Errorf("failed repo selectable: %+v", got)
	}
	next, _ := m.Update(press("enter"))
	if next.(Model).modal.open {
		t.Error("dialog opened for nothing")
	}
}

func TestUpdatesFullDiffViewer(t *testing.T) {
	m := updatesModelAt(t, i18n.EN)
	_, cmd := m.Update(press("D"))
	if cmd == nil {
		t.Fatal("D started nothing")
	}
	var lines []string
	for i := range 80 {
		lines = append(lines, "line "+string(rune('a'+i%26))+strings.Repeat("x", i))
	}
	m = send(t, m, fullDiffMsg{repo: "github.com/acme/skills", text: strings.Join(lines, "\n")})
	if !m.updates.full {
		t.Fatal("viewer not open")
	}
	v := plain(m.View())
	if !strings.Contains(v, "github.com/acme/skills") || !strings.Contains(v, "line a") || strings.Contains(v, "line z") {
		t.Errorf("viewer:\n%s", v)
	}
	// Keys go to the viewer; q and 3 do not quit or switch.
	m = send(t, m, press("pgdown"), press("3"))
	if m.tab != tabUpdates || !m.updates.full {
		t.Error("keys leaked out of the viewer")
	}
	if !strings.Contains(plain(m.View()), "line") || m.updates.vp.YOffset() == 0 {
		t.Error("viewer did not scroll")
	}
	m = send(t, m, press("esc"))
	if m.updates.full || !strings.Contains(plain(m.View()), "acme/skills") {
		t.Error("esc did not close the viewer")
	}
}

func TestUpdatesCheckStartsFetch(t *testing.T) {
	m := newTestModel(t, i18n.EN)
	m = send(t, m, press("2"), updatesMsg{})
	next, cmd := m.Update(press("r"))
	m = next.(Model)
	if !m.applying || cmd == nil || !strings.Contains(plain(m.View()), "Checking upstream…") {
		t.Fatalf("check: applying=%v", m.applying)
	}
	next, _ = m.Update(updatesMsg{fetched: true})
	m = next.(Model)
	if m.applying || !strings.Contains(plain(m.View()), "Checked: everything is up to date.") {
		t.Errorf("after check:\n%s", plain(m.View()))
	}
}

var _ = tea.KeyPressMsg{}
