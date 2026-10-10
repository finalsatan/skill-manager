package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanAddRepo(t *testing.T) {
	e := setup(t, "auto")
	p, err := e.m.PlanAddRepo("owner/repo", "dev")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(e.m.Cfg.Store, "repos", "github.com", "owner", "repo")
	if p.ID != "github.com/owner/repo" || p.URL != "https://github.com/owner/repo.git" || p.Dir != want || p.Branch != "dev" {
		t.Fatalf("%+v", p)
	}
	if _, err := os.Stat(filepath.Dir(want)); err == nil {
		t.Fatal("planning created directories")
	}
	if _, err := e.m.PlanAddRepo(e.up, ""); err == nil || !strings.Contains(err.Error(), "already added") {
		t.Fatalf("want already added, got %v", err)
	}
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.PlanAddRepo("owner/repo", ""); err == nil || !strings.Contains(err.Error(), "not tracked") {
		t.Fatalf("want leftover dir refusal, got %v", err)
	}
	if _, err := e.m.PlanAddRepo("", ""); err == nil {
		t.Fatal("empty ref accepted")
	}
}

func TestPlanRemoveRepo(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	p, err := e.m.PlanRemoveRepo("local/up", false)
	if err != nil || len(p.Links) != 0 || len(p.Pins) != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	e.pin(t, v1, "claude")
	e.enable(t, []string{"web-search"}, "claude")

	p, err = e.m.PlanRemoveRepo("local/up", false)
	if err == nil || !strings.Contains(err.Error(), "--force") || len(p.Links) != 2 || len(p.Pins) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = e.m.PlanRemoveRepo("local/up", true)
	if err != nil || len(p.Links) != 2 || len(p.Pins) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	// Nothing was touched.
	if _, err := os.Lstat(e.pdfLink()); err != nil || len(e.m.St.Deployments) != 2 || len(pinDirs(t, e)) != 1 {
		t.Fatal("planning changed something")
	}
	if _, err := e.m.PlanRemoveRepo("local/nope", true); err == nil {
		t.Fatal("unknown repo accepted")
	}

	// A target replaced by a regular directory is not removable.
	os.Remove(e.pdfLink())
	if err := os.MkdirAll(e.pdfLink(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.PlanRemoveRepo("local/up", true); err == nil || !strings.Contains(err.Error(), "no longer managed") {
		t.Fatalf("want not removable, got %v", err)
	}
}

func TestRepoServes(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	if got := e.m.RepoServes("local/up"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	e.pin(t, v1, "codex")
	if got := e.m.RepoServes("local/up"); len(got) != 0 {
		t.Fatalf("pinned deployments must not count: %v", got)
	}
	e.enable(t, []string{"web-search"}, "claude", "codex")
	if got := strings.Join(e.m.RepoServes("local/up"), ","); got != "claude,codex" {
		t.Fatalf("%v", got)
	}
}
