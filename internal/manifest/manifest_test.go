package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skillet/internal/state"
)

func TestBuildSaveLoadRoundTrip(t *testing.T) {
	repos := map[string]*state.Repo{
		"github.com/b/z": {URL: "https://github.com/b/z.git", Branch: "main"},
		"github.com/a/y": {URL: "https://github.com/a/y.git", Branch: "dev"},
	}
	sha := strings.Repeat("a", 40)
	deps := []*state.Deployment{
		{Skill: "github.com/b/z:skills/web", Agents: []string{"pi"}},
		{Skill: "github.com/a/y:pdf", Agents: []string{"codex", "pi"}, Pin: sha},
		{Skill: "github.com/a/y:pdf", Agents: []string{"claude", "opencode"}, Pin: sha},
	}
	m, err := Build(repos, deps, []string{"claude", "codex", "opencode", "pi"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "# Managed by skillet. Restore with `skillet restore`.\n\n" +
		"[[repo]]\nid = \"github.com/a/y\"\nurl = \"https://github.com/a/y.git\"\nbranch = \"dev\"\n\n" +
		"[[repo]]\nid = \"github.com/b/z\"\nurl = \"https://github.com/b/z.git\"\nbranch = \"main\"\n\n" +
		"[[skill]]\nid = \"github.com/a/y:pdf\"\nagents = [\"claude\", \"codex\", \"opencode\", \"pi\"]\npin = \"" + sha + "\"\n\n" +
		"[[skill]]\nid = \"github.com/b/z:skills/web\"\nagents = [\"pi\"]\n"
	if strings.TrimSpace(string(b)) != strings.TrimSpace(want) {
		t.Fatalf("file:\n%s\nwant:\n%s", b, want)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repos) != 2 || len(got.Skills) != 2 || got.Skills[0].Pin != sha || got.Skills[1].Pin != "" {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestLoadRejectsBadManifests(t *testing.T) {
	cases := map[string]string{
		"unknown key":      "[[repo]]\nid = \"a/b\"\nurl = \"u\"\nbogus = 1\n",
		"no repo":          "[[skill]]\nid = \"a/b:x\"\nagents = [\"claude\"]\n",
		"no agents":        "[[repo]]\nid = \"a/b\"\nurl = \"u\"\n[[skill]]\nid = \"a/b:x\"\nagents = []\n",
		"bad id":           "[[repo]]\nid = \"a/b\"\nurl = \"u\"\n[[skill]]\nid = \"a/b\"\nagents = [\"claude\"]\n",
		"traversal":        "[[repo]]\nid = \"a/b\"\nurl = \"u\"\n[[skill]]\nid = \"a/b:../x\"\nagents = [\"claude\"]\n",
		"duplicate":        "[[repo]]\nid = \"a/b\"\nurl = \"u\"\n[[repo]]\nid = \"a/b\"\nurl = \"u\"\n",
		"not toml":         "[[repo\n",
		"repo without url": "[[repo]]\nid = \"a/b\"\n",
	}
	for name, body := range cases {
		p := filepath.Join(t.TempDir(), FileName)
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), FileName)); err == nil || !strings.Contains(err.Error(), "skillet save") {
		t.Errorf("missing file error = %v", err)
	}
}
