package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/config"
	"github.com/finalsatan/skill-manager/internal/link"
	"github.com/finalsatan/skill-manager/internal/state"
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

func TestRemoveRepoForceFailureKeepsStateInSync(t *testing.T) {
	e := setup(t, "symlink")
	e.enable(t, []string{"pdf", "web-search"}, "claude")
	// Deployments run in target order, so pdf is unlinked before web-search fails.
	pdf := filepath.Join(e.proj.Dir, ".claude", "skills", "pdf")
	web := filepath.Join(e.proj.Dir, ".claude", "skills", "web-search")
	outside := t.TempDir()
	if err := os.Remove(web); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, web); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	removed, err := e.m.RemoveRepo("local/up", true)
	if err == nil {
		t.Fatal("expected an error for the replaced link")
	}
	if len(removed) != 1 || removed[0] != pdf {
		t.Errorf("removed = %v, want [%s]", removed, pdf)
	}
	if _, err := os.Lstat(pdf); !os.IsNotExist(err) {
		t.Errorf("pdf link still on disk: %v", err)
	}

	st, err := state.Peek(e.m.Cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Deployments) != 1 || st.Deployments[0].Target != web {
		t.Errorf("saved deployments = %+v, want only %s", st.Deployments, web)
	}
	if _, ok := st.Repos["local/up"]; !ok || len(st.Skills) != 2 {
		t.Errorf("repo/skills dropped: repos=%d skills=%d", len(st.Repos), len(st.Skills))
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

func TestCopyWithFileSymlinkIsNotModified(t *testing.T) {
	e := setup(t, "copy")
	src, err := e.m.SkillDir("local/up:skills/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(src, "alias.txt")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	e.enable(t, []string{"pdf"}, "claude")
	d := e.m.St.Deployments[0]
	if h := e.m.Health(d); h != "ok" {
		t.Errorf("health = %s, want ok", h)
	}
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Scope: e.proj}); err != nil {
		t.Errorf("disable: %v", err)
	}
}

func TestEnableRefusesEditedLeftoverCopy(t *testing.T) {
	e := setup(t, "copy")
	e.enable(t, []string{"pdf"}, "claude")
	d := e.m.St.Deployments[0]
	e.m.St.RemoveDeployment(d.Target) // copy left behind, not in state
	edited := filepath.Join(d.Target, "SKILL.md")
	if err := os.WriteFile(edited, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj}); err == nil {
		t.Fatal("adopted and deleted an edited leftover copy")
	}
	if got := readFile(t, edited); got != "mine" {
		t.Errorf("edits lost: %q", got)
	}
}

func TestDisableKeepsUserReplacedLink(t *testing.T) {
	e := setup(t, "symlink")
	e.enable(t, []string{"pdf"}, "claude")
	target := e.m.St.Deployments[0].Target
	other := filepath.Join(t.TempDir(), "mine")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(target)
	if err := os.Symlink(other, target); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Scope: e.proj}); err == nil {
		t.Fatal("disable removed a link not pointing into the store")
	}
	if _, err := os.Lstat(target); err != nil {
		t.Error("user's link was removed")
	}
}

// Disabling claude and enabling codex in one Set plans the links for the final
// agent set only: the dry run shows what applying does.
func TestSetMixedEnableDisable(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude", "opencode") // one link in .claude/skills serves both

	o := ChangeOpts{Skills: []string{"pdf"}, Scope: e.proj}
	dry := o
	dry.DryRun = true
	plan, err := e.m.Set(dry, []string{"codex"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(e.targets(), " "); got != ".claude/skills/pdf=claude,opencode" {
		t.Fatalf("dry run changed the state: %s", got)
	}
	if _, err := os.Lstat(filepath.Join(e.proj.Dir, ".agents/skills/pdf")); !os.IsNotExist(err) {
		t.Fatal("dry run created a link")
	}

	res, err := e.m.Set(o, []string{"codex"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	// OpenCode also reads .agents/skills, so one link serves both.
	want := []string{".agents/skills/pdf=codex,opencode"}
	if got := e.targets(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if _, err := os.Lstat(filepath.Join(e.proj.Dir, ".claude/skills/pdf")); !os.IsNotExist(err) {
		t.Error(".claude link not removed")
	}
	if v := res[0].Visible; v["claude"] != 0 || v["codex"] == 0 || v["opencode"] == 0 {
		t.Errorf("visible = %v", v)
	}
	// A dry run reports the requested mode ("auto"), apply the one it used.
	for i := range plan[0].Actions {
		plan[0].Actions[i].Mode, res[0].Actions[i].Mode = "", ""
	}
	if !reflect.DeepEqual(plan[0].Actions, res[0].Actions) || !reflect.DeepEqual(plan[0].Visible, res[0].Visible) {
		t.Errorf("dry run %+v differs from apply %+v", plan[0], res[0])
	}

	// Errors: nothing to do, and the same agent on both sides.
	if _, err := e.m.Set(o, nil, nil); err == nil {
		t.Error("empty Set accepted")
	}
	if _, err := e.m.Set(o, []string{"codex"}, []string{"codex"}); err == nil {
		t.Error("conflicting Set accepted")
	}
}
