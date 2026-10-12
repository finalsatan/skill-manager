package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"skillet/internal/agents"
	"skillet/internal/config"
	"skillet/internal/deploy"
)

type realStore struct {
	repo  string // the repo id
	up    string // the local upstream repo
	proj  agents.Scope
	pdf   string // skill ids
	docx  string
	links string // proj/.claude/skills
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRealStore builds an isolated store with one local repo holding the skills
// pdf and docx, and an empty project directory.
func newRealStore(t *testing.T) *realStore {
	t.Helper()
	return newStoreWith(t, "skills/pdf", "skills/docx")
}

// newStoreWith is newRealStore with the skills at the given repo-relative
// directories; each skill is named after its directory.
func newStoreWith(t *testing.T, rels ...string) *realStore {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SKILLET_CONFIG_DIR", filepath.Join(root, "cfg"))
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}
	up := filepath.Join(root, "up")
	for _, rel := range rels {
		n := filepath.Base(rel)
		dir := filepath.Join(up, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + n + "\ndescription: test skill\n---\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, up, "init", "-q", "-b", "main")
	gitIn(t, up, "add", "-A")
	gitIn(t, up, "commit", "-qm", "init")

	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Store: filepath.Join(root, "store"), LinkMode: "auto"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m, err := deploy.OpenWith(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	repo, _, err := m.AddRepo(up, "")
	if err != nil {
		t.Fatal(err)
	}
	return &realStore{
		repo: repo, up: up, proj: agents.Scope{Dir: proj}, pdf: repo + ":skills/pdf", docx: repo + ":skills/docx",
		links: filepath.Join(proj, ".claude", "skills"),
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestPreviewThenApplyOnRealStore(t *testing.T) {
	rs := newRealStore(t)
	cs := []change{{ID: rs.pdf, Name: "pdf", Enable: []string{"claude", "codex"}}}

	pm := previewCmd(scopeProject, rs.proj, cs)().(previewMsg)
	if pm.err != nil || len(pm.plans) != 1 || pm.plans[0].Err != nil || pm.manifest {
		t.Fatalf("preview: %+v", pm)
	}
	en := pm.plans[0].Step
	if en == nil || len(en.Actions) != 2 || en.Visible["claude"] == 0 || en.Visible["codex"] == 0 {
		t.Fatalf("enable step: %+v", en)
	}
	if en.Actions[0].Kind != "create" || en.Actions[0].Source == "" {
		t.Errorf("action: %+v", en.Actions[0])
	}
	// A dry run creates nothing.
	if exists(rs.links) || exists(filepath.Join(rs.proj.Dir, ".agents")) {
		t.Fatal("preview touched the project")
	}
	// ... and does not hold the lock.
	m, err := deploy.OpenTry()
	if err != nil {
		t.Fatalf("lock held after preview: %v", err)
	}
	m.Close()

	am := applyCmd(scopeProject, rs.proj, cs)().(applyMsg)
	if am.err != nil || len(am.done) != 1 || len(am.done[0].Enable) == 0 || am.done[0].Name != "pdf" {
		t.Fatalf("apply: %+v", am)
	}
	for _, p := range []string{filepath.Join(rs.links, "pdf"), filepath.Join(rs.proj.Dir, ".agents", "skills", "pdf")} {
		if !exists(p) {
			t.Errorf("%s not created", p)
		}
		if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err != nil {
			t.Errorf("%s does not resolve to the skill: %v", p, err)
		}
	}

	// skillet.toml is refreshed once the project has one.
	m, err = deploy.OpenTry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveManifest(rs.proj); err != nil {
		t.Fatal(err)
	}
	m.Close()
	// pdf: stop claude, start pi, as one change; docx: start claude.
	cs2 := []change{
		{ID: rs.pdf, Name: "pdf", Enable: []string{"pi"}, Disable: []string{"claude"}},
		{ID: rs.docx, Name: "docx", Enable: []string{"claude"}},
	}
	agentsDir := filepath.Join(rs.proj.Dir, ".agents", "skills", "pdf")
	pm = previewCmd(scopeProject, rs.proj, cs2)().(previewMsg)
	if !pm.manifest || pm.plans[0].Step == nil || pm.plans[1].Step == nil {
		t.Fatalf("preview 2: %+v", pm)
	}
	if !exists(filepath.Join(rs.links, "pdf")) || exists(filepath.Join(rs.links, "docx")) {
		t.Fatal("second preview changed the disk")
	}
	// The preview of the mixed change is the plan applying follows.
	if v := pm.plans[0].Step.Visible; v["claude"] != 0 || v["codex"] == 0 || v["pi"] == 0 {
		t.Errorf("pdf preview visibility: %v", v)
	}
	am = applyCmd(scopeProject, rs.proj, cs2)().(applyMsg)
	if am.err != nil || len(am.done) != 2 || len(am.done[0].Disable) != 1 || len(am.done[0].Enable) != 1 {
		t.Fatalf("apply 2: %+v", am)
	}
	if !exists(agentsDir) || exists(filepath.Join(rs.links, "pdf")) || !exists(filepath.Join(rs.links, "docx")) {
		t.Error("links after second apply are wrong")
	}
	data, err := os.ReadFile(filepath.Join(rs.proj.Dir, "skillet.toml"))
	if err != nil || !strings.Contains(string(data), "docx") {
		t.Errorf("skillet.toml not updated: %v\n%s", err, data)
	}
}

func TestPreviewErrorAndApplyStopsAtFirstError(t *testing.T) {
	rs := newRealStore(t)
	cs := []change{
		{ID: rs.docx, Name: "docx", Enable: []string{"claude"}},
		{ID: rs.pdf + "-missing", Name: "zzz", Enable: []string{"claude"}},
		{ID: rs.pdf, Name: "pdf", Enable: []string{"claude"}},
	}
	pm := previewCmd(scopeProject, rs.proj, cs)().(previewMsg)
	if pm.err != nil || pm.plans[0].Err != nil || pm.plans[1].Err == nil || pm.plans[2].Err != nil {
		t.Fatalf("plans: %+v", pm.plans)
	}
	if !(modalState{plans: pm.plans}).blocked() {
		t.Error("an errored plan must block Apply")
	}
	am := applyCmd(scopeProject, rs.proj, cs)().(applyMsg)
	if am.err == nil || len(am.done) != 1 || am.done[0].Name != "docx" {
		t.Fatalf("apply: %+v", am)
	}
	if !exists(filepath.Join(rs.links, "docx")) || exists(filepath.Join(rs.links, "pdf")) {
		t.Error("apply did not stop at the first error")
	}
	s, isErr := applySummary(am.done, am.err)
	if !isErr || !strings.Contains(s, "Enabled docx") || !strings.Contains(s, "Failed") {
		t.Errorf("summary %q", s)
	}
}

func TestApplySummary(t *testing.T) {
	s, isErr := applySummary([]applied{{Name: "docx", Enable: []string{"claude"}}, {Name: "pptx", Disable: []string{"pi"}}, {Name: "both", Enable: []string{"a"}, Disable: []string{"b"}}}, nil)
	if isErr || s != "Enabled docx · Disabled pptx · Enabled both · Disabled both" {
		t.Errorf("%q %v", s, isErr)
	}
	if s, _ := applySummary(nil, nil); s != "Nothing changed." {
		t.Errorf("%q", s)
	}
}
