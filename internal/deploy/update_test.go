package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/finalsatan/skill-manager/internal/agents"
)

func (e *env) commit(t *testing.T, msg string, edit func(up string)) {
	t.Helper()
	edit(e.up)
	git(t, e.up, "add", "-A")
	git(t, e.up, "commit", "-qm", msg)
}

func (e *env) check(t *testing.T) RepoCheck {
	t.Helper()
	res, err := e.m.Check(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results", len(res))
	}
	if res[0].Err != nil {
		t.Fatal(res[0].Err)
	}
	return res[0]
}

func (e *env) update(t *testing.T) UpdateResult {
	t.Helper()
	r, err := e.m.Update(e.check(t))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCheckReportsOnlyChangedSkills(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")

	if c := e.check(t); !c.UpToDate() {
		t.Fatalf("fresh repo not up to date: %+v", c)
	}

	e.commit(t, "touch readme", func(up string) {
		os.WriteFile(filepath.Join(up, "README.md"), []byte("hi"), 0o644)
	})
	c := e.check(t)
	if c.UpToDate() || c.Commits != 1 || len(c.Changes) != 0 {
		t.Fatalf("readme-only change: %+v", c)
	}

	e.commit(t, "pdf script", func(up string) {
		os.WriteFile(filepath.Join(up, "skills/pdf/run.sh"), []byte("echo"), 0o755)
	})
	e.commit(t, "new skill", func(up string) { writeSkill(t, up, "skills/new-one", "new-one") })
	c = e.check(t)
	if c.Commits != 3 || len(c.Changes) != 1 {
		t.Fatalf("check = %+v", c)
	}
	ch := c.Changes[0]
	if ch.Name != "pdf" || !ch.Code || ch.Deployed != 1 || ch.Removed || strings.Join(ch.Files, ",") != "run.sh" {
		t.Errorf("change = %+v", ch)
	}
	if strings.Join(c.NewSkills, ",") != "skills/new-one" {
		t.Errorf("new skills = %v", c.NewSkills)
	}
	if !e.m.St.Skills["local/up:skills/pdf"].Pending || e.m.St.Skills["local/up:skills/web-search"].Pending {
		t.Error("pending flags wrong")
	}
	st, _ := e.m.Status(e.proj)
	if len(st) == 0 || !st[0].Pending {
		t.Errorf("status does not show pending: %+v", st)
	}
	log, diff, err := e.m.RepoDiff(c, false, "local/up:skills/pdf")
	if err != nil || !strings.Contains(log, "pdf script") || strings.Contains(log, "new skill") || !strings.Contains(diff, "run.sh") {
		t.Errorf("RepoDiff log=%q diff=%q err=%v", log, diff, err)
	}
}

func TestUpdateSymlinkAndNewSkill(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")
	target := e.m.St.Deployments[0].Target

	e.commit(t, "edit pdf", func(up string) {
		os.WriteFile(filepath.Join(up, "skills/pdf/extra.md"), []byte("v2"), 0o644)
		writeSkill(t, up, "skills/new-one", "new-one")
	})
	r := e.update(t)
	if r.From == r.To {
		t.Fatal("head did not move")
	}
	if got := readFile(t, filepath.Join(target, "extra.md")); got != "v2" {
		t.Errorf("link does not show new content: %q", got)
	}
	if _, ok := e.m.St.Skills["local/up:skills/new-one"]; !ok {
		t.Error("new skill not registered")
	}
	if e.m.PendingCount() != 0 {
		t.Error("pending not cleared")
	}
	if c := e.check(t); !c.UpToDate() {
		t.Errorf("not up to date after update: %+v", c)
	}
}

func TestUpdateRecopiesCopiesButKeepsEdits(t *testing.T) {
	e := setup(t, "copy")
	e.enable(t, []string{"pdf", "web-search"}, "claude")
	var pdf, web string
	for _, d := range e.m.St.Deployments {
		if strings.HasSuffix(d.Target, "pdf") {
			pdf = d.Target
		} else {
			web = d.Target
		}
	}
	// The user edits the web-search copy by hand.
	os.WriteFile(filepath.Join(web, "SKILL.md"), []byte("mine"), 0o644)

	e.commit(t, "edit both", func(up string) {
		os.WriteFile(filepath.Join(up, "skills/pdf/extra.md"), []byte("v2"), 0o644)
		os.WriteFile(filepath.Join(up, "skills/web-search/extra.md"), []byte("v2"), 0o644)
	})
	r := e.update(t)
	if got := readFile(t, filepath.Join(pdf, "extra.md")); got != "v2" {
		t.Errorf("pdf copy not refreshed: %q", got)
	}
	if got := readFile(t, filepath.Join(web, "SKILL.md")); got != "mine" {
		t.Errorf("hand-edited copy overwritten: %q", got)
	}
	notes := strings.Join(r.Notes, "\n")
	if !strings.Contains(notes, "re-copied") || !strings.Contains(notes, "skipped") {
		t.Errorf("notes = %s", notes)
	}
	for _, d := range e.m.St.Deployments {
		if d.Target == pdf && e.m.Health(d) != "ok" {
			t.Errorf("pdf health = %s", e.m.Health(d))
		}
	}
}

func TestUpdateFollowsMovedSkillAndDropsDeleted(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf", "web-search"}, "claude", "codex")

	e.commit(t, "move pdf, delete web-search", func(up string) {
		os.MkdirAll(filepath.Join(up, "skills/docs"), 0o755)
		os.Rename(filepath.Join(up, "skills/pdf"), filepath.Join(up, "skills/docs/pdf"))
		os.RemoveAll(filepath.Join(up, "skills/web-search"))
	})
	c := e.check(t)
	removed := map[string]bool{}
	for _, ch := range c.Changes {
		removed[ch.Name] = ch.Removed
	}
	if !removed["pdf"] || !removed["web-search"] {
		t.Errorf("changes = %+v", c.Changes)
	}

	r, err := e.m.Update(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.m.St.Deployments) != 2 {
		t.Fatalf("deployments = %v\nnotes: %v", e.targets(), r.Notes)
	}
	for _, d := range e.m.St.Deployments {
		if d.Skill != "local/up:skills/docs/pdf" || e.m.Health(d) != "ok" {
			t.Errorf("deployment %s -> %s health %s", d.Target, d.Skill, e.m.Health(d))
		}
	}
	for _, dir := range []string{".claude/skills/web-search", ".agents/skills/web-search"} {
		if _, err := os.Lstat(filepath.Join(e.proj.Dir, dir)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", dir)
		}
	}
}

func TestUpdateRelinksRenamedSkill(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")
	e.commit(t, "rename", func(up string) {
		os.WriteFile(filepath.Join(up, "skills/pdf/SKILL.md"), []byte("---\nname: pdf-tools\ndescription: x\n---\n"), 0o644)
	})
	e.update(t)
	want := filepath.Join(e.proj.Dir, ".claude/skills/pdf-tools")
	if d := e.m.St.Deployments[0]; d.Target != want || e.m.Health(d) != "ok" {
		t.Errorf("deployment = %+v", d)
	}
	if _, err := os.Lstat(filepath.Join(e.proj.Dir, ".claude/skills/pdf")); !os.IsNotExist(err) {
		t.Error("old link left behind")
	}
}

func TestUpdateRefusesDirtyRepo(t *testing.T) {
	e := setup(t, "auto")
	e.commit(t, "edit", func(up string) {
		os.WriteFile(filepath.Join(up, "skills/pdf/extra.md"), []byte("v2"), 0o644)
	})
	c := e.check(t)
	repoDir := e.m.RepoDir(e.m.St.Repos["local/up"])
	os.WriteFile(filepath.Join(repoDir, "skills/pdf/SKILL.md"), []byte("local edit"), 0o644)
	if _, err := e.m.Update(c); err == nil || !strings.Contains(err.Error(), "local modifications") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoctor(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")
	target := e.m.St.Deployments[0].Target

	// Another project that is later deleted.
	gone := filepath.Join(filepath.Dir(e.proj.Dir), "gone")
	os.MkdirAll(gone, 0o755)
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: agents.Scope{Dir: gone}}); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(gone)
	os.Remove(target)
	// A hand-made skill with the same name that Codex will also see.
	writeSkill(t, e.proj.Dir, ".agents/skills/web-search", "web-search")
	e.enable(t, []string{"web-search"}, "claude")

	kinds := func(fix bool) map[string]Issue {
		issues, err := e.m.Doctor(fix)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Issue{}
		for _, is := range issues {
			out[is.Kind] = is
		}
		return out
	}
	got := kinds(false)
	for _, k := range []string{"missing", "project-gone", "clash"} {
		if _, ok := got[k]; !ok {
			t.Errorf("doctor did not report %s: %+v", k, got)
		}
	}
	if !strings.Contains(got["clash"].Detail, "opencode") {
		t.Errorf("clash = %+v", got["clash"])
	}

	fixed := kinds(true)
	if !fixed["missing"].Fixed || !fixed["project-gone"].Fixed {
		t.Errorf("not fixed: %+v", fixed)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Error("missing link not restored")
	}
	after := kinds(false)
	if len(after) != 1 || after["clash"].Kind == "" {
		t.Errorf("after fix: %+v", after)
	}
}

func TestUnnotifiedOncePerUpstream(t *testing.T) {
	m := setup(t, "auto").m
	r := m.St.Repos[m.RepoIDs()[0]]
	c := RepoCheck{Repo: m.RepoIDs()[0], Head: "a", Upstream: "b", Commits: 1}
	if got := m.Unnotified([]RepoCheck{c}); len(got) != 1 {
		t.Fatalf("first time: %d", len(got))
	}
	if err := m.MarkNotified([]RepoCheck{c}); err != nil {
		t.Fatal(err)
	}
	if r.Notified != "b" || len(m.Unnotified([]RepoCheck{c})) != 0 {
		t.Fatal("same upstream should not notify again")
	}
	c.Upstream = "c"
	if len(m.Unnotified([]RepoCheck{c})) != 1 {
		t.Fatal("new upstream should notify")
	}
}
