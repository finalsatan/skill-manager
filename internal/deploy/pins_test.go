package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/finalsatan/skill-manager/internal/link"
)

func revParse(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func writeVersion(t *testing.T, up, v string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(up, "skills/pdf/version.txt"), []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pinSetup returns an env whose upstream has a v1 commit (the one pinned in
// the tests) followed by a v2 commit, both already cloned into the store.
func pinSetup(t *testing.T, mode string) (*env, string) {
	t.Helper()
	e := setup(t, mode)
	e.commit(t, "v1", func(up string) { writeVersion(t, up, "v1") })
	v1 := revParse(t, e.up)
	e.commit(t, "v2", func(up string) { writeVersion(t, up, "v2") })
	e.update(t)
	return e, v1
}

func (e *env) pin(t *testing.T, rev string, ags ...string) []Result {
	t.Helper()
	res, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: ags, Scope: e.proj, Pin: rev})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *env) pdfLink() string { return filepath.Join(e.proj.Dir, ".claude/skills/pdf") }

func pinDirs(t *testing.T, e *env) []string {
	t.Helper()
	var out []string
	for _, c := range e.m.pinCheckouts() {
		out = append(out, c.Path)
	}
	return out
}

func TestPinSurvivesUpstreamUpdate(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1[:10], "claude")

	d := e.m.St.DeploymentAt(e.pdfLink())
	if d.Pin != v1 {
		t.Fatalf("pin = %q, want %q", d.Pin, v1)
	}
	wt := e.m.PinDir("local/up", v1)
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v1" {
		t.Fatalf("pinned content = %q", got)
	}
	if dest, _ := os.Readlink(e.pdfLink()); dest != filepath.Join(wt, "skills/pdf") {
		t.Errorf("link -> %s, want inside %s", dest, wt)
	}
	if h := e.m.Health(d); h != "ok" {
		t.Errorf("health %s", h)
	}
	entries, _ := e.m.Status(e.proj)
	if len(entries) != 2 || entries[0].Pin != v1 || entries[0].Pending {
		t.Errorf("entries = %+v", entries)
	}

	e.commit(t, "v3", func(up string) { writeVersion(t, up, "v3") })
	c := e.check(t)
	if len(c.Changes) != 1 || c.Changes[0].Deployed != 0 || c.Changes[0].Pinned != 1 {
		t.Errorf("changes = %+v", c.Changes)
	}
	r, err := e.m.Update(c)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v1" {
		t.Errorf("pinned link followed the update: %q", got)
	}
	if len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "1 pinned deployment(s) not updated (pinned to "+v1[:7]+")") {
		t.Errorf("notes = %v", r.Notes)
	}
}

func TestPinDryRunAndRepointAndUnpin(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v2" {
		t.Fatalf("unpinned content = %q", got)
	}

	// A dry run shows the replacement and changes nothing.
	res, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj, Pin: v1, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if a := res[0].Actions; len(a) != 1 || a[0].Kind != "replace" || a[0].Pin != v1 {
		t.Fatalf("dry-run actions = %+v", a)
	}
	if len(pinDirs(t, e)) != 0 || e.m.St.DeploymentAt(e.pdfLink()).Pin != "" {
		t.Fatal("dry run changed something")
	}

	e.pin(t, v1, "claude")
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v1" {
		t.Errorf("pinned content = %q", got)
	}
	if n := len(pinDirs(t, e)); n != 1 {
		t.Fatalf("pin dirs = %v", pinDirs(t, e))
	}

	// Re-pin to the second commit: the first worktree is no longer used.
	v2 := revParse(t, filepath.Join(e.m.Cfg.Store, "repos/local/up"))
	e.pin(t, v2, "claude")
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v2" {
		t.Errorf("re-pinned content = %q", got)
	}
	if dirs := pinDirs(t, e); len(dirs) != 1 || dirs[0] != e.m.PinDir("local/up", v2) {
		t.Fatalf("pin dirs = %v", dirs)
	}

	// Without --pin the link goes back to the main clone and the pin goes away.
	res, err = e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj, Unpin: true})
	if err != nil {
		t.Fatal(err)
	}
	if a := res[0].Actions; len(a) != 1 || a[0].Kind != "replace" || a[0].Pin != "" {
		t.Fatalf("unpin actions = %+v", a)
	}
	repo := filepath.Join(e.m.Cfg.Store, "repos/local/up")
	if dest, _ := os.Readlink(e.pdfLink()); dest != filepath.Join(repo, "skills/pdf") {
		t.Errorf("link -> %s", dest)
	}
	if e.m.St.DeploymentAt(e.pdfLink()).Pin != "" || len(pinDirs(t, e)) != 0 {
		t.Errorf("pin left behind: %v", pinDirs(t, e))
	}
	if _, err := os.Stat(e.m.PinDir("local/up", v1)); !os.IsNotExist(err) {
		t.Errorf("worktree dir still there: %v", err)
	}
}

func TestPinSharedWorktreeAndDisable(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "all")
	if len(e.m.St.Deployments) != 2 || len(pinDirs(t, e)) != 1 {
		t.Fatalf("deployments %d, pins %v", len(e.m.St.Deployments), pinDirs(t, e))
	}
	// Disabling one agent keeps the shared worktree (and the pin).
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"codex", "pi"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	if len(pinDirs(t, e)) != 1 {
		t.Fatalf("pins = %v", pinDirs(t, e))
	}
	for _, d := range e.m.St.Deployments {
		if d.Pin != v1 {
			t.Errorf("pin lost on %s", d.Target)
		}
	}
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	if len(e.m.St.Deployments) != 0 || len(pinDirs(t, e)) != 0 {
		t.Errorf("deployments %d, pins %v", len(e.m.St.Deployments), pinDirs(t, e))
	}
}

func TestPinRefusedWhenSkillMissingAtRev(t *testing.T) {
	e := setup(t, "auto")
	first := revParse(t, e.up)
	e.commit(t, "add skill", func(up string) { writeSkill(t, up, "skills/late", "late") })
	e.update(t)

	_, err := e.m.Enable(ChangeOpts{Skills: []string{"late"}, Agents: []string{"claude"}, Scope: e.proj, Pin: first})
	if err == nil || !strings.Contains(err.Error(), "does not exist at") {
		t.Fatalf("err = %v", err)
	}
	_, err = e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj, Pin: "no-such-rev"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	// Nothing was created, even for the valid skill listed with the bad one.
	_, err = e.m.Enable(ChangeOpts{Skills: []string{"pdf", "late"}, Agents: []string{"claude"}, Scope: e.proj, Pin: first})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(e.m.St.Deployments) != 0 || len(pinDirs(t, e)) != 0 {
		t.Errorf("changes made: %v %v", e.m.St.Deployments, pinDirs(t, e))
	}
	if _, err := os.Lstat(e.pdfLink()); !os.IsNotExist(err) {
		t.Errorf("link created: %v", err)
	}
}

func TestPinCopyMode(t *testing.T) {
	e, v1 := pinSetup(t, "copy")
	e.pin(t, v1, "claude")
	d := e.m.St.DeploymentAt(e.pdfLink())
	if d.Mode != string(link.Copy) || d.Pin != v1 {
		t.Fatalf("deployment = %+v", d)
	}
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v1" {
		t.Fatalf("copied content = %q", got)
	}
	if h := e.m.Health(d); h != "ok" {
		t.Errorf("health %s", h)
	}
	// An update must not re-copy a pinned copy.
	e.commit(t, "v3", func(up string) { writeVersion(t, up, "v3") })
	e.update(t)
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v1" {
		t.Errorf("copy changed to %q", got)
	}
	// Unpinning re-copies from the main clone.
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj, Unpin: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v3" {
		t.Errorf("unpinned copy = %q", got)
	}
	if h := e.m.Health(e.m.St.DeploymentAt(e.pdfLink())); h != "ok" {
		t.Errorf("health %s", h)
	}
}

func TestPinSurvivesSkillDeletedUpstream(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude")
	e.commit(t, "drop pdf", func(up string) { os.RemoveAll(filepath.Join(up, "skills/pdf")) })
	r := e.update(t)
	for _, n := range r.Notes {
		if strings.Contains(n, "deleted upstream") {
			t.Errorf("pinned deployment was touched: %v", r.Notes)
		}
	}
	d := e.m.St.DeploymentAt(e.pdfLink())
	if d == nil || e.m.Health(d) != "ok" {
		t.Fatalf("deployment gone or unhealthy: %+v", d)
	}
	issues, err := e.m.Doctor(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, is := range issues {
		if is.Kind != "pending" {
			t.Errorf("unexpected issue %+v", is)
		}
	}
}

func TestRemoveRepoForceRemovesPins(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude")
	if _, err := e.m.RemoveRepo("local/up", false); err == nil {
		t.Fatal("expected refusal without --force")
	}
	removed, err := e.m.RemoveRepo("local/up", true)
	if err != nil || len(removed) != 1 {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	if _, err := os.Lstat(e.pdfLink()); !os.IsNotExist(err) {
		t.Errorf("link left: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.m.Cfg.Store, "pins/local")); !os.IsNotExist(err) {
		t.Errorf("pins left: %v", err)
	}
}

func TestDoctorOrphanAndMissingPin(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude")
	wt := e.m.PinDir("local/up", v1)

	// An orphan: a worktree no deployment uses.
	v2 := revParse(t, filepath.Join(e.m.Cfg.Store, "repos/local/up"))
	if err := e.m.ensurePin("local/up", v2); err != nil {
		t.Fatal(err)
	}
	orphan := e.m.PinDir("local/up", v2)
	issues, err := e.m.Doctor(false)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(issues); got != "orphan-pin" {
		t.Fatalf("issues = %v", issues)
	}
	if _, err := e.m.Doctor(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan worktree left: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("used worktree removed: %v", err)
	}

	// A missing worktree is recreated by --fix.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	issues, _ = e.m.Doctor(false)
	if got := kinds(issues); got != "pin-missing" {
		t.Fatalf("issues = %v", issues)
	}
	issues, err = e.m.Doctor(true)
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(issues); got != "pin-missing" || !issues[0].Fixed {
		t.Fatalf("fix issues = %+v", issues)
	}
	if got := readFile(t, e.pdfLink()+"/version.txt"); got != "v1" {
		t.Errorf("content after fix = %q", got)
	}
	if issues, _ = e.m.Doctor(false); kinds(issues) != "" {
		t.Errorf("still issues: %+v", issues)
	}
}

func kinds(issues []Issue) string {
	var k []string
	for _, is := range issues {
		k = append(k, is.Kind)
	}
	return strings.Join(k, ",")
}

func TestEnableWithoutPinKeepsPin(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude")
	// Adding an agent keeps the pin on the existing and the new link.
	res := e.enable(t, []string{"pdf"}, "codex")
	if len(res[0].Actions) == 0 {
		t.Fatal("no actions")
	}
	if len(e.m.St.Deployments) != 2 {
		t.Fatalf("deployments = %v", e.targets())
	}
	for _, d := range e.m.St.Deployments {
		if d.Pin != v1 {
			t.Errorf("%s pin = %q", d.Target, d.Pin)
		}
		if got := readFile(t, d.Target+"/version.txt"); got != "v1" {
			t.Errorf("%s content = %q", d.Target, got)
		}
	}
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj, Pin: v1, Unpin: true}); err == nil {
		t.Error("--pin with --unpin accepted")
	}
}

func TestDisablePinnedSkillDeletedUpstream(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude")
	e.commit(t, "drop pdf", func(up string) { os.RemoveAll(filepath.Join(up, "skills/pdf")) })
	e.update(t)

	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: e.proj}); err == nil {
		t.Error("enabling a deleted skill succeeded")
	}
	if _, err := e.m.Status(e.proj); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"pdf", "local/up:skills/pdf"} {
		if _, err := e.m.resolveSkill(ref, true); err != nil {
			t.Errorf("resolve %q: %v", ref, err)
		}
	}
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(e.pdfLink()); !os.IsNotExist(err) {
		t.Errorf("link left: %v", err)
	}
	if len(e.m.St.Deployments) != 0 || len(pinDirs(t, e)) != 0 {
		t.Errorf("deployments %d, pins %v", len(e.m.St.Deployments), pinDirs(t, e))
	}
}
