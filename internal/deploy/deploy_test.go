package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"skillet/internal/agents"
	"skillet/internal/config"
	"skillet/internal/link"
)

type env struct {
	m    *Manager
	up   string
	proj agents.Scope
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeSkill(t *testing.T, root, rel, name string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: test skill\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setup creates a fake home, an upstream repo with two skills, a project
// directory and an initialized store with the repo added.
func setup(t *testing.T, mode string) *env {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}

	up := filepath.Join(root, "up")
	writeSkill(t, up, "skills/pdf", "pdf")
	writeSkill(t, up, "skills/web-search", "web-search")
	git(t, up, "init", "-q", "-b", "main")
	git(t, up, "add", "-A")
	git(t, up, "commit", "-qm", "init")

	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}

	m, err := OpenWith(&config.Config{Store: filepath.Join(root, "store"), LinkMode: mode})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	if _, _, err := m.AddRepo(up, ""); err != nil {
		t.Fatal(err)
	}
	return &env{m: m, up: up, proj: agents.Scope{Dir: proj}}
}

func (e *env) enable(t *testing.T, skills []string, ags ...string) []Result {
	t.Helper()
	res, err := e.m.Enable(ChangeOpts{Skills: skills, Agents: ags, Scope: e.proj})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *env) targets() []string {
	var out []string
	for _, d := range e.m.St.Deployments {
		rel, _ := filepath.Rel(e.proj.Dir, d.Target)
		out = append(out, filepath.ToSlash(rel)+"="+strings.Join(d.Agents, ","))
	}
	sort.Strings(out)
	return out
}

func TestEnableAllAgentsCreatesTwoLinks(t *testing.T) {
	e := setup(t, "auto")
	res := e.enable(t, []string{"pdf"}, "all")

	want := []string{".agents/skills/pdf=codex,pi", ".claude/skills/pdf=claude,opencode"}
	if got := e.targets(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if v := res[0].Visible; v["opencode"] != 2 || v["claude"] != 1 || v["pi"] != 1 {
		t.Errorf("visible = %v", v)
	}
	for _, d := range e.m.St.Deployments {
		if h := e.m.Health(d); h != "ok" {
			t.Errorf("%s health %s", d.Target, h)
		}
	}
	// Enabling again changes nothing.
	again := e.enable(t, []string{"pdf"}, "claude")
	for _, a := range again[0].Actions {
		if a.Kind != "keep" {
			t.Errorf("unexpected action %+v", a)
		}
	}
}

func TestEnableReportsUnwantedVisibility(t *testing.T) {
	e := setup(t, "auto")
	res := e.enable(t, []string{"web-search"}, "codex")
	if got := strings.Join(res[0].Unwanted, ","); got != "opencode,pi" {
		t.Errorf("unwanted = %q", got)
	}
}

func TestDisableReplansRemainingAgents(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude", "opencode")

	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	// Claude must stop seeing it, so OpenCode moves to its own directory.
	want := []string{".opencode/skills/pdf=opencode"}
	if got := e.targets(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if _, err := os.Lstat(filepath.Join(e.proj.Dir, ".claude/skills/pdf")); !os.IsNotExist(err) {
		t.Error(".claude link not removed")
	}

	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	if len(e.m.St.Deployments) != 0 {
		t.Errorf("deployments left: %v", e.targets())
	}
}

func TestEnableRefusesUnmanagedTarget(t *testing.T) {
	e := setup(t, "auto")
	own := filepath.Join(e.proj.Dir, ".claude/skills/pdf")
	writeSkill(t, e.proj.Dir, ".claude/skills/pdf", "pdf")

	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude", "codex"}, Scope: e.proj}); err == nil {
		t.Fatal("expected refusal")
	}
	if len(e.m.St.Deployments) != 0 {
		t.Error("partial deployment recorded after refusal")
	}
	if _, err := os.Lstat(filepath.Join(e.proj.Dir, ".agents/skills/pdf")); !os.IsNotExist(err) {
		t.Error("codex link created although the operation was refused")
	}
	if tg, _ := link.Inspect(own); tg.Kind != link.Other {
		t.Error("user's directory was modified")
	}
	st, _ := e.m.Status(e.proj)
	if len(st) == 0 || st[0].Health != "unmanaged" {
		t.Errorf("status = %+v", st)
	}
}

func TestCopyModeDetectsEdits(t *testing.T) {
	e := setup(t, "copy")
	e.enable(t, []string{"pdf"}, "claude")
	d := e.m.St.Deployments[0]
	if d.Mode != "copy" || d.SourceHash == "" {
		t.Fatalf("deployment = %+v", d)
	}
	if err := os.WriteFile(filepath.Join(d.Target, "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h := e.m.Health(d); h != "modified" {
		t.Errorf("health = %s, want modified", h)
	}
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Scope: e.proj}); err == nil {
		t.Fatal("disable removed a hand-edited copy")
	}
}

func TestRepairsDeletedLink(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")
	target := e.m.St.Deployments[0].Target
	os.Remove(target)
	if h := e.m.Health(e.m.St.Deployments[0]); h != "missing" {
		t.Errorf("health = %s", h)
	}
	res := e.enable(t, []string{"pdf"}, "claude")
	if res[0].Actions[0].Kind != "repair" {
		t.Errorf("actions = %+v", res[0].Actions)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Error("link not repaired")
	}
}

func TestRemoveRepo(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf", "web-search"}, "claude")
	if _, err := e.m.RemoveRepo("local/up", false); err == nil {
		t.Fatal("removed a repo that is still deployed")
	}
	removed, err := e.m.RemoveRepo("local/up", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || len(e.m.St.Skills) != 0 || len(e.m.St.Deployments) != 0 {
		t.Errorf("removed=%v skills=%d deployments=%d", removed, len(e.m.St.Skills), len(e.m.St.Deployments))
	}
}

func TestResolveSkillAmbiguity(t *testing.T) {
	e := setup(t, "auto")
	other := filepath.Join(filepath.Dir(e.up), "other")
	writeSkill(t, other, "pdf", "pdf")
	git(t, other, "init", "-q", "-b", "main")
	git(t, other, "add", "-A")
	git(t, other, "commit", "-qm", "init")
	if _, _, err := e.m.AddRepo(other, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.ResolveSkill("pdf"); err == nil {
		t.Error("expected ambiguity error")
	}
	if id, err := e.m.ResolveSkill("other/pdf"); err != nil || id != "local/other:pdf" {
		t.Errorf("ResolveSkill(other/pdf) = %q, %v", id, err)
	}
}
