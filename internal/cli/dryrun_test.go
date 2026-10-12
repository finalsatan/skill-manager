package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/finalsatan/skill-manager/internal/config"
	"github.com/finalsatan/skill-manager/internal/state"
)

func upstreamCommit(t *testing.T, proj, file string) {
	t.Helper()
	up := filepath.Join(filepath.Dir(proj), "up")
	if err := os.WriteFile(filepath.Join(up, "skills", "pdf", file), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "change " + file}} {
		cmd := exec.Command("git", append([]string{"-C", up, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func repoIDs(t *testing.T) []string {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Peek(cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for id := range st.Repos {
		ids = append(ids, id)
	}
	return ids
}

// lockFree makes a fake chooser assert that the state lock is not held while
// it is "prompting".
func lockFree(t *testing.T, f *fakeChooser) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	f.onPrompt = func() {
		h, err := state.TryOpen(cfg.Store)
		if err != nil {
			t.Errorf("state lock held while prompting: %v", err)
			return
		}
		h.Close()
	}
}

func TestRepoAddDryRun(t *testing.T) {
	proj := cliEnv(t)
	root := filepath.Dir(proj)
	other := filepath.Join(root, "nothing-here")
	out, err := run(t, "repo", "add", other, "--dry-run")
	if err != nil || !strings.Contains(out, "Would add") || !strings.Contains(out, other) || !strings.Contains(out, "dir:") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(repoIDs(t)) != 1 {
		t.Fatal("dry run registered a repo")
	}
	// Already added: refused.
	if _, err := run(t, "repo", "add", filepath.Join(root, "up"), "--dry-run"); err == nil || !strings.Contains(err.Error(), "already added") {
		t.Fatalf("want already added, got %v", err)
	}
}

func TestRepoRemoveDryRun(t *testing.T) {
	proj := cliEnv(t)
	id := repoIDs(t)[0]
	if out, err := run(t, "enable", "pdf", "-a", "claude", "--dir", proj); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := run(t, "repo", "remove", id, "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "--force") || !strings.Contains(out, "unlink") {
		t.Fatalf("want refusal listing the link: %v\n%s", err, out)
	}
	out, err = run(t, "repo", "remove", id, "--force", "--dry-run")
	if err != nil || !strings.Contains(out, "unlink") || !strings.Contains(out, "Dry run") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !linked(proj, ".claude/skills/pdf") || len(repoIDs(t)) != 1 {
		t.Fatal("dry run changed something")
	}
	// A hand-edited directory in place of the link blocks removal.
	os.Remove(filepath.Join(proj, ".claude/skills/pdf"))
	if err := os.MkdirAll(filepath.Join(proj, ".claude/skills/pdf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "repo", "remove", id, "--force", "--dry-run"); err == nil || !strings.Contains(err.Error(), "no longer managed") {
		t.Fatalf("want not removable, got %v", err)
	}
}

func TestUpdateDryRunAndUnlockedPrompt(t *testing.T) {
	proj := cliEnv(t)
	if out, err := run(t, "enable", "pdf", "-a", "claude,codex", "--dir", proj); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	upstreamCommit(t, proj, "extra.md")

	out, err := run(t, "update", "--dry-run")
	if err != nil || !strings.Contains(out, "change extra.md") || !strings.Contains(out, "Dry run, nothing applied") || strings.Contains(out, "restart Codex") {
		t.Fatalf("%v\n%s", err, out)
	}
	if linked(proj, ".claude/skills/pdf/extra.md") {
		t.Fatal("dry run applied the update")
	}
	// --no-fetch --dry-run works from the recorded state.
	if out, err = run(t, "update", "--no-fetch", "--dry-run"); err != nil || !strings.Contains(out, "change extra.md") {
		t.Fatalf("%v\n%s", err, out)
	}

	f := &fakeChooser{confirm: true}
	withChooser(t, f, true)
	lockFree(t, f)
	out, err = run(t, "update", "--no-fetch")
	if err != nil || f.confirms != 1 || !linked(proj, ".claude/skills/pdf/extra.md") {
		t.Fatalf("%v confirms=%d\n%s", err, f.confirms, out)
	}
	if strings.Count(out, "restart Codex") != 1 {
		t.Fatalf("want one reload note:\n%s", out)
	}
}

func TestUpdateReloadNoteOnlyForOtherAgents(t *testing.T) {
	proj := cliEnv(t)
	if out, err := run(t, "enable", "pdf", "-a", "claude", "--dir", proj); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	upstreamCommit(t, proj, "extra.md")
	out, err := run(t, "update", "--yes")
	if err != nil || !strings.Contains(out, "Updated") || strings.Contains(out, "restart Codex") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestInteractiveEnableDisableLockFree(t *testing.T) {
	proj := cliEnv(t)
	ids := skillIDs(t)
	f := &fakeChooser{skills: []string{ids["pdf"]}, agents: []string{"claude"}, confirm: true}
	withChooser(t, f, true)
	lockFree(t, f)
	if out, err := run(t, "enable", "--dir", proj); err != nil || f.confirms != 1 || !linked(proj, ".claude/skills/pdf") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := run(t, "disable", "--dir", proj); err != nil || f.confirms != 2 || linked(proj, ".claude/skills/pdf") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestSaveDryRun(t *testing.T) {
	proj := cliEnv(t)
	if out, err := run(t, "enable", "pdf", "-a", "claude", "--dir", proj); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := run(t, "save", "--dir", proj, "--dry-run")
	if err != nil || !strings.Contains(out, "pdf") || strings.Contains(out, "Wrote") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(proj, "skillet.toml")); err == nil {
		t.Fatal("dry run wrote skillet.toml")
	}
	real, err := run(t, "save", "--dir", proj)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, "skillet.toml"))
	if !strings.HasPrefix(out, string(data)) || real == "" {
		t.Fatalf("dry-run output differs from the file:\n%s\n---\n%s", out, data)
	}
}

func TestPendingJSON(t *testing.T) {
	proj := cliEnv(t)
	out, err := run(t, "pending", "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatalf("want [], got %q (%v)", out, err)
	}
	upstreamCommit(t, proj, "extra.md")
	if out, err := run(t, "check", "--quiet"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = run(t, "pending", "--json")
	var got []struct {
		Repo    string `json:"repo"`
		Commits int    `json:"commits"`
	}
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got) != 1 || got[0].Commits != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := run(t, "pending", "--json", "--hook-json"); err == nil {
		t.Fatal("flags should conflict")
	}
}
