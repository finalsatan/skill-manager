package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skillet/internal/manifest"
)

func (e *env) enableAs(t *testing.T, skill, alias string, dry bool, ags ...string) []Result {
	t.Helper()
	res, err := e.m.Enable(ChangeOpts{Skills: []string{skill}, Agents: ags, Scope: e.proj, Alias: alias, DryRun: dry})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *env) link(name string) string { return filepath.Join(e.proj.Dir, ".claude/skills", name) }

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestEnableAsCreatesAliasedLink(t *testing.T) {
	e := setup(t, "auto")
	e.enableAs(t, "pdf", "pdf-tool", false, "claude")
	if !exists(e.link("pdf-tool")) || exists(e.link("pdf")) {
		t.Fatalf("targets = %v", e.targets())
	}
	d := e.m.St.Deployments[0]
	if d.Alias != "pdf-tool" || e.m.Health(d) != "ok" {
		t.Errorf("deployment = %+v", d)
	}

	// Enabling again without --as keeps the alias, also for a new agent.
	res := e.enable(t, []string{"pdf"}, "claude")
	for _, a := range res[0].Actions {
		if a.Kind != "keep" {
			t.Errorf("unexpected action %+v", a)
		}
	}
	e.enable(t, []string{"pdf"}, "codex")
	if got := strings.Join(e.targets(), " "); got != ".agents/skills/pdf-tool=codex .claude/skills/pdf-tool=claude" {
		t.Errorf("targets = %s", got)
	}
}

func TestEnableAsValidation(t *testing.T) {
	e := setup(t, "auto")
	for _, c := range []struct {
		skills []string
		alias  string
	}{
		{[]string{"pdf", "web-search"}, "x"},
		{[]string{"pdf"}, "Bad Name"},
		{[]string{"pdf"}, "-x"},
	} {
		_, err := e.m.Enable(ChangeOpts{Skills: c.skills, Agents: []string{"claude"}, Scope: e.proj, Alias: c.alias})
		if err == nil {
			t.Errorf("Enable(%v, as %q) succeeded", c.skills, c.alias)
		}
	}
	if len(e.m.St.Deployments) != 0 {
		t.Errorf("refused enable left deployments: %v", e.targets())
	}
}

func TestAliasResolvesClash(t *testing.T) {
	e := setup(t, "auto")
	e.commit(t, "second pdf", func(up string) { writeSkill(t, up, "other/pdf", "pdf") })
	e.update(t)
	repo := e.m.RepoIDs()[0]
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{repo + ":skills/pdf"}, Agents: []string{"claude"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{repo + ":other/pdf"}, Agents: []string{"claude"}, Scope: e.proj}); err == nil {
		t.Fatal("clashing name accepted")
	}
	e.enableAs(t, repo+":other/pdf", "pdf2", false, "claude")
	entries, _ := e.m.Status(e.proj)
	if len(Clashes(entries)) != 0 {
		t.Errorf("clash remains: %v", Clashes(entries))
	}
}

func TestDisableFindsAliasedLink(t *testing.T) {
	e := setup(t, "auto")
	e.enableAs(t, "pdf", "pdf-tool", false, "claude", "opencode")
	// Dropping claude re-plans opencode into its own directory, still aliased.
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.targets(), " "); got != ".opencode/skills/pdf-tool=opencode" {
		t.Fatalf("targets = %s", got)
	}
	// The alias names the skill too.
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf-tool"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	if len(e.m.St.Deployments) != 0 || exists(filepath.Join(e.proj.Dir, ".opencode/skills/pdf-tool")) {
		t.Errorf("left behind: %v", e.targets())
	}
}

func TestUpdateKeepsAliasWhenSkillRenamed(t *testing.T) {
	e := setup(t, "auto")
	e.enableAs(t, "pdf", "pdf-tool", false, "claude")
	e.commit(t, "rename", func(up string) {
		os.WriteFile(filepath.Join(up, "skills/pdf/SKILL.md"), []byte("---\nname: pdf-new\ndescription: x\n---\n"), 0o644)
	})
	r := e.update(t)
	if len(r.Notes) != 0 {
		t.Errorf("notes = %v", r.Notes)
	}
	d := e.m.St.Deployments[0]
	if d.Target != e.link("pdf-tool") || e.m.Health(d) != "ok" {
		t.Errorf("deployment = %+v", d)
	}

	// Moving the directory as well: the aliased link follows by the old name.
	e.commit(t, "move", func(up string) {
		if err := os.Rename(filepath.Join(up, "skills/pdf"), filepath.Join(up, "moved")); err != nil {
			t.Fatal(err)
		}
	})
	e.update(t)
	d = e.m.St.Deployments[0]
	if !strings.HasSuffix(d.Skill, ":moved") || d.Alias != "pdf-tool" || d.Target != e.link("pdf-tool") || e.m.Health(d) != "ok" {
		t.Errorf("deployment after move = %+v", d)
	}
}

func TestEnableAsChangeMovesLink(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")

	dry := e.enableAs(t, "pdf", "pdf-tool", true, "claude")
	var kinds []string
	for _, a := range dry[0].Actions {
		kinds = append(kinds, a.Kind+":"+filepath.Base(a.Target))
	}
	if strings.Join(kinds, " ") != "create:pdf-tool remove:pdf" {
		t.Errorf("dry-run actions = %v", kinds)
	}
	if !exists(e.link("pdf")) || exists(e.link("pdf-tool")) {
		t.Fatal("dry run changed the filesystem")
	}

	e.enableAs(t, "pdf", "pdf-tool", false, "claude")
	if exists(e.link("pdf")) || !exists(e.link("pdf-tool")) || len(e.m.St.Deployments) != 1 {
		t.Fatalf("targets = %v", e.targets())
	}

	// --as with the skill's own name drops the alias again.
	e.enableAs(t, "pdf", "pdf", false, "claude")
	d := e.m.St.Deployments[0]
	if d.Alias != "" || d.Target != e.link("pdf") || exists(e.link("pdf-tool")) {
		t.Errorf("deployment = %+v", d)
	}
}

func TestManifestRecordsAliasAndRestoreAppliesIt(t *testing.T) {
	e := setup(t, "auto")
	e.enableAs(t, "pdf", "pdf-tool", false, "claude")
	e.enable(t, []string{"web-search"}, "claude")

	mf := e.saved(t)
	got := map[string]string{}
	for _, s := range mf.Skills {
		got[filepath.Base(s.ID)] = s.As
	}
	if got["pdf"] != "pdf-tool" || got["web-search"] != "" {
		t.Fatalf("as = %v", got)
	}
	b, _ := os.ReadFile(ManifestPath(e.proj))
	if !strings.Contains(string(b), `as = "pdf-tool"`) {
		t.Errorf("manifest:\n%s", b)
	}

	// A fresh store restores the alias; a store whose link has another name moves it.
	noChange(t, e.restore(t, mf, RestoreOpts{}))

	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj, Unalias: true}); err != nil {
		t.Fatal(err)
	}
	if !exists(e.link("pdf")) {
		t.Fatal("unalias did not rename the link")
	}
	res := e.restore(t, mf, RestoreOpts{})
	if len(res.Problems) > 0 {
		t.Fatal(res.Problems)
	}
	if !exists(e.link("pdf-tool")) || exists(e.link("pdf")) {
		t.Errorf("restore did not apply the alias: %v", e.targets())
	}

	// A manifest without `as` removes an alias that is set locally.
	for i := range mf.Skills {
		mf.Skills[i].As = ""
	}
	e.restore(t, mf, RestoreOpts{})
	if !exists(e.link("pdf")) || exists(e.link("pdf-tool")) {
		t.Errorf("restore kept the alias: %v", e.targets())
	}
}

func TestManifestRejectsInvalidAlias(t *testing.T) {
	m := &manifest.Manifest{
		Repos:  []manifest.Repo{{ID: "r", URL: "u"}},
		Skills: []manifest.Skill{{ID: "r:a", Agents: []string{"claude"}, As: "Bad Name"}},
	}
	if err := m.Validate(); err == nil {
		t.Error("invalid alias accepted")
	}
}

func TestEnableAsKeepsLinkMode(t *testing.T) {
	e := setup(t, "copy")
	e.enable(t, []string{"pdf"}, "claude")
	dry := e.enableAs(t, "pdf", "pdf-tool", true, "claude")
	for _, a := range dry[0].Actions {
		if a.Kind == "create" && a.Mode != "copy" {
			t.Errorf("dry-run create mode = %s", a.Mode)
		}
	}
	e.m.Cfg.LinkMode = "symlink" // the config default changes; the moved link stays a copy
	e.enableAs(t, "pdf", "pdf-tool", false, "claude")
	d := e.m.St.Deployments[0]
	if d.Target != e.link("pdf-tool") || d.Mode != "copy" {
		t.Fatalf("deployment = %+v", d)
	}
	if fi, err := os.Lstat(d.Target); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("not a copy: %v %v", fi, err)
	}
	// An explicit mode still wins.
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj, Alias: "pdf-x", Mode: "symlink"}); err != nil {
		t.Fatal(err)
	}
	if d := e.m.St.Deployments[0]; d.Mode != "symlink" {
		t.Errorf("explicit mode ignored: %+v", d)
	}
}
