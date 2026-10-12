package deploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/finalsatan/skill-manager/internal/agents"
)

func TestRepoInfo(t *testing.T) {
	e, v1 := pinSetup(t, "auto")
	e.pin(t, v1, "claude")
	e.enableAs(t, "web-search", "ws", false, "codex")
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: agents.Scope{Global: true}}); err != nil {
		t.Fatal(err)
	}
	// A skill with an invalid name shows its scan warning; upstream moves ahead.
	e.commit(t, "bad name", func(up string) {
		os.WriteFile(filepath.Join(up, "skills/web-search/SKILL.md"), []byte("---\nname: Web Search\ndescription: x\n---\n"), 0o644)
	})
	e.check(t)
	e.update(t)
	e.commit(t, "pdf v3", func(up string) { writeVersion(t, up, "v3") })
	e.check(t)

	info, err := e.m.RepoInfo(e.m.RepoIDs()[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Behind != 1 || info.Upstream == info.Head || info.LastChecked == nil || info.Branch != "main" {
		t.Errorf("info = %+v", info)
	}
	if len(info.Pins) != 1 || info.Pins[0].Deployments != 1 || info.Pins[0].Rev != v1[:7] {
		t.Errorf("pins = %+v", info.Pins)
	}
	skills := map[string]RepoSkill{}
	for _, s := range info.Skills {
		skills[s.RelPath] = s
	}
	pdf, ws := skills["skills/pdf"], skills["skills/web-search"]
	if !pdf.Pending || len(pdf.Deployed) != 2 || pdf.Deployed[0].Pin == "" && pdf.Deployed[1].Pin == "" {
		t.Errorf("pdf = %+v", pdf)
	}
	if ws.Pending || len(ws.Warnings) == 0 || len(ws.Deployed) != 1 || ws.Deployed[0].Alias != "ws" {
		t.Errorf("web-search = %+v", ws)
	}
	if _, err := e.m.RepoInfo("nope"); err == nil {
		t.Error("unknown repo accepted")
	}
}

func TestDeployedScopes(t *testing.T) {
	e := setup(t, "auto")
	gone := filepath.Join(t.TempDir(), "gone")
	os.MkdirAll(gone, 0o755)
	other := agents.Scope{Dir: gone}
	if set := e.m.DeployedScopes(); len(set.Scopes) != 0 {
		t.Fatalf("empty store: %+v", set)
	}
	e.enable(t, []string{"pdf"}, "claude")
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: other}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Enable(ChangeOpts{Skills: []string{"pdf"}, Agents: []string{"claude"}, Scope: agents.Scope{Global: true}}); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(gone)

	set := e.m.DeployedScopes()
	if len(set.Scopes) != 2 || !set.Scopes[0].Global || set.Scopes[1].Dir != e.proj.Dir {
		t.Errorf("scopes = %+v", set.Scopes)
	}
	if len(set.Missing) != 1 || set.Missing[0] != gone {
		t.Errorf("missing = %v", set.Missing)
	}
}
