package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skillet/internal/agents"
	"skillet/internal/config"
	"skillet/internal/manifest"
)

// fresh opens a second, empty store next to e's and returns a manager for it
// with a new project directory.
func (e *env) fresh(t *testing.T) (*Manager, agents.Scope) {
	t.Helper()
	root := t.TempDir()
	m, err := OpenWith(&config.Config{Store: filepath.Join(root, "store"), LinkMode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	return m, agents.Scope{Dir: proj}
}

// saved builds the manifest of e's project through a file round trip.
func (e *env) saved(t *testing.T) *manifest.Manifest {
	t.Helper()
	if _, err := e.m.SaveManifest(e.proj); err != nil {
		t.Fatal(err)
	}
	mf, err := manifest.Load(ManifestPath(e.proj))
	if err != nil {
		t.Fatal(err)
	}
	return mf
}

func (e *env) restore(t *testing.T, mf *manifest.Manifest, o RestoreOpts) *RestoreResult {
	t.Helper()
	o.Manifest, o.Scope = mf, e.proj
	res, err := e.m.Restore(o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func noChange(t *testing.T, res *RestoreResult) {
	t.Helper()
	if len(res.Problems) > 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	for _, r := range res.Results {
		for _, a := range r.Actions {
			if a.Kind != "keep" {
				t.Errorf("%s: unexpected action %+v", r.Skill, a)
			}
		}
	}
}

func TestSaveWritesPinnedAndUnpinnedSkills(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude", "codex")
	e.enable(t, []string{"web-search"}, "pi")

	if _, err := e.m.SaveManifest(e.proj); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(ManifestPath(e.proj))
	if err != nil {
		t.Fatal(err)
	}
	up := e.up
	want := "# Managed by skillet. Restore with `skillet restore`.\n\n" +
		"[[repo]]\nid = \"local/up\"\nurl = \"" + up + "\"\nbranch = \"main\"\n\n" +
		"[[skill]]\nid = \"local/up:skills/pdf\"\nagents = [\"claude\", \"codex\"]\npin = \"" + v1 + "\"\n\n" +
		"[[skill]]\nid = \"local/up:skills/web-search\"\nagents = [\"pi\"]\n"
	if strings.TrimSpace(string(b)) != strings.TrimSpace(want) {
		t.Fatalf("manifest:\n%s\nwant:\n%s", b, want)
	}
	if _, err := e.m.BuildManifest(agents.Scope{Global: true}); err == nil {
		t.Error("global scope accepted")
	}
}

func TestRestoreIntoFreshStore(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude", "codex")
	e.enable(t, []string{"web-search"}, "pi")
	mf := e.saved(t)

	m2, proj2 := e.fresh(t)
	res, err := m2.Restore(RestoreOpts{Manifest: mf, Scope: proj2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) > 0 || len(res.Repos) != 1 || res.Repos[0].Status != "cloned" {
		t.Fatalf("result: %+v", res)
	}
	var got []string
	for _, d := range m2.St.Deployments {
		rel, _ := filepath.Rel(proj2.Dir, d.Target)
		got = append(got, filepath.ToSlash(rel)+"="+strings.Join(d.Agents, ",")+"@"+d.Pin)
	}
	want := map[string]bool{
		".claude/skills/pdf=claude@" + v1: true, ".agents/skills/pdf=codex@" + v1: true,
		".pi/skills/web-search=pi@": true,
	}
	if len(got) != len(want) {
		t.Fatalf("deployments = %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected deployment %s (all: %v)", g, got)
		}
	}
	if s := readFile(t, filepath.Join(proj2.Dir, ".claude/skills/pdf/version.txt")); s != "v1" {
		t.Errorf("pinned content = %q", s)
	}

	// Second run changes nothing.
	again, err := m2.Restore(RestoreOpts{Manifest: mf, Scope: proj2})
	if err != nil {
		t.Fatal(err)
	}
	if again.Repos[0].Status != "present" {
		t.Errorf("repo status %q", again.Repos[0].Status)
	}
	noChange(t, again)
}

func TestRestoreFetchesMissingPin(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude")
	mf := e.saved(t)

	m2, proj2 := e.fresh(t)
	// Clone first, then publish a commit the clone does not have yet.
	if _, _, err := m2.AddRepo(e.up, "main"); err != nil {
		t.Fatal(err)
	}
	e.commit(t, "v4", func(up string) { writeVersion(t, up, "v4") })
	v4 := revParse(t, e.up)
	mf.Skills[0].Pin = v4
	res, err := m2.Restore(RestoreOpts{Manifest: mf, Scope: proj2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) > 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	if d := m2.St.DeploymentAt(filepath.Join(proj2.Dir, ".claude/skills/pdf")); d == nil || d.Pin != v4 {
		t.Fatalf("deployment = %+v", d)
	}
}

func TestRestoreAdjustsAgentsAndPin(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude")
	mf := e.saved(t)

	// Drift: another agent added, pin dropped.
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"codex"}, Scope: e.proj, Unpin: true}); err != nil {
		t.Fatal(err)
	}
	res := e.restore(t, mf, RestoreOpts{})
	if len(res.Problems) > 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	if got := e.targets(); len(got) != 1 || got[0] != ".claude/skills/pdf=claude" {
		t.Fatalf("targets = %v", got)
	}
	if d := e.m.St.DeploymentAt(e.pdfLink()); d.Pin != v1 {
		t.Errorf("pin = %q, want %q", d.Pin, v1)
	}
	noChange(t, e.restore(t, mf, RestoreOpts{}))
}

func TestRestorePrune(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")
	mf := e.saved(t)
	e.enable(t, []string{"web-search"}, "claude")

	res := e.restore(t, mf, RestoreOpts{})
	if len(res.Kept) != 1 || res.Kept[0] != "local/up:skills/web-search" {
		t.Fatalf("kept = %v", res.Kept)
	}
	if len(e.targets()) != 2 {
		t.Fatalf("skill removed without --prune: %v", e.targets())
	}

	res = e.restore(t, mf, RestoreOpts{Prune: true})
	if len(res.Kept) != 0 {
		t.Errorf("kept with prune: %v", res.Kept)
	}
	if got := e.targets(); len(got) != 1 || got[0] != ".claude/skills/pdf=claude" {
		t.Fatalf("targets = %v", got)
	}
}

func TestRestoreDryRunChangesNothing(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")
	mf := e.saved(t)

	m2, proj2 := e.fresh(t)
	res, err := m2.Restore(RestoreOpts{Manifest: mf, Scope: proj2, DryRun: true, Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repos) != 1 || res.Repos[0].Status != "would clone" || len(res.Skipped) != 1 {
		t.Fatalf("result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(m2.Cfg.Store, "repos")); !os.IsNotExist(err) {
		t.Errorf("repos dir exists after dry run: %v", err)
	}
	if len(m2.St.Repos) != 0 || len(m2.St.Deployments) != 0 {
		t.Error("state changed")
	}

	// With the repo present, a dry run plans links but creates none.
	if _, _, err := m2.AddRepo(e.up, "main"); err != nil {
		t.Fatal(err)
	}
	res, err = m2.Restore(RestoreOpts{Manifest: mf, Scope: proj2, DryRun: true})
	if err != nil || len(res.Results) != 1 || res.Results[0].Actions[0].Kind != "create" {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if len(m2.St.Deployments) != 0 {
		t.Error("deployments created in dry run")
	}
	if _, err := os.Lstat(filepath.Join(proj2.Dir, ".claude")); !os.IsNotExist(err) {
		t.Errorf(".claude exists after dry run: %v", err)
	}
}

func TestRestoreReportsUnknownSkillAndContinues(t *testing.T) {
	e := setup(t, "auto")
	mf := &manifest.Manifest{
		Repos: []manifest.Repo{{ID: "local/up", URL: e.up, Branch: "main"}},
		Skills: []manifest.Skill{
			{ID: "local/up:skills/ghost", Agents: []string{"claude"}},
			{ID: "local/up:skills/pdf", Agents: []string{"claude"}},
		},
	}
	res := e.restore(t, mf, RestoreOpts{})
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "skills/ghost") {
		t.Fatalf("problems = %v", res.Problems)
	}
	if got := e.targets(); len(got) != 1 || got[0] != ".claude/skills/pdf=claude" {
		t.Fatalf("targets = %v", got)
	}

	mf.Skills[1].Agents = []string{"nope"}
	if _, err := e.m.Restore(RestoreOpts{Manifest: mf, Scope: e.proj}); err == nil {
		t.Error("unknown agent accepted")
	}
}

func TestRestoreWarnsOnBranchMismatch(t *testing.T) {
	e := setup(t, "auto")
	mf := &manifest.Manifest{Repos: []manifest.Repo{{ID: "local/up", URL: e.up, Branch: "other"}}}
	res := e.restore(t, mf, RestoreOpts{})
	if res.Repos[0].Status != "present" || res.Repos[0].Warning == "" || len(res.Problems) > 0 {
		t.Fatalf("result: %+v", res)
	}
}

func TestSyncManifestOnlyWhenPresent(t *testing.T) {
	e := setup(t, "auto")
	e.enable(t, []string{"pdf"}, "claude")
	if ok, err := e.m.SyncManifest(e.proj); ok || err != nil {
		t.Fatalf("sync without manifest = %v, %v", ok, err)
	}
	if _, err := os.Stat(ManifestPath(e.proj)); !os.IsNotExist(err) {
		t.Fatal("manifest created")
	}

	e.saved(t)
	e.enable(t, []string{"web-search"}, "pi")
	if ok, err := e.m.SyncManifest(e.proj); !ok || err != nil {
		t.Fatalf("sync = %v, %v", ok, err)
	}
	mf, err := manifest.Load(ManifestPath(e.proj))
	if err != nil || len(mf.Skills) != 2 {
		t.Fatalf("manifest = %+v, %v", mf, err)
	}
	if _, err := e.m.Disable(ChangeOpts{Skills: []string{"pdf"}, Scope: e.proj}); err != nil {
		t.Fatal(err)
	}
	e.m.SyncManifest(e.proj)
	if mf, _ = manifest.Load(ManifestPath(e.proj)); len(mf.Skills) != 1 || mf.Skills[0].ID != "local/up:skills/web-search" {
		t.Fatalf("manifest after disable = %+v", mf)
	}
}
