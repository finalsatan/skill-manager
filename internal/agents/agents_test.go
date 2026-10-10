package agents

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"skillet/internal/config"
)

func registry(t *testing.T) *Registry {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	r, err := NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func dirs(ps []Placement, s Scope) map[string][]string {
	out := map[string][]string{}
	for _, p := range ps {
		rel, _ := filepath.Rel(s.Dir, p.Dir)
		out[filepath.ToSlash(rel)] = p.Agents
	}
	return out
}

func TestPlanUsesFewestDirs(t *testing.T) {
	r := registry(t)
	s := Scope{Dir: t.TempDir()}
	cases := []struct {
		ask      []string
		existing []string
		want     map[string][]string
	}{
		{[]string{"all"}, nil, map[string][]string{
			".claude/skills": {"claude", "opencode"},
			".agents/skills": {"codex", "pi"},
		}},
		{[]string{"opencode"}, nil, map[string][]string{".opencode/skills": {"opencode"}}},
		{[]string{"pi", "codex"}, nil, map[string][]string{".agents/skills": {"codex", "pi"}}},
		// An existing link in .claude/skills already serves opencode.
		{[]string{"claude", "opencode"}, []string{filepath.Join(s.Dir, ".claude/skills")},
			map[string][]string{".claude/skills": {"claude", "opencode"}}},
		{[]string{"opencode"}, []string{filepath.Join(s.Dir, ".agents/skills")},
			map[string][]string{".agents/skills": {"opencode"}}},
	}
	for _, c := range cases {
		as, err := r.Resolve(c.ask)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Plan(as, s, c.existing)
		if err != nil {
			t.Fatal(err)
		}
		if g := dirs(got, s); !reflect.DeepEqual(g, c.want) {
			t.Errorf("Plan(%v, existing=%v) = %v, want %v", c.ask, c.existing, g, c.want)
		}
	}
}

func TestResolveRejectsUnknown(t *testing.T) {
	r := registry(t)
	if _, err := r.Resolve([]string{"claude,cursor"}); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestCustomAgentOverridesDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r, err := NewRegistry([]config.CustomAgent{
		{ID: "pi", ProjectDir: ".pi/my-skills"},
		{ID: "hermes", ProjectDir: ".hermes/skills", GlobalDir: "~/.hermes/skills"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pi, _ := r.Get("pi")
	if want := []string{".pi/my-skills", ".agents/skills"}; !reflect.DeepEqual(pi.ProjectReads, want) {
		t.Errorf("pi reads %v, want %v", pi.ProjectReads, want)
	}
	h, ok := r.Get("hermes")
	if !ok || h.ProjectDir != ".hermes/skills" || len(h.GlobalReads) != 1 {
		t.Errorf("hermes = %+v", h)
	}
}

// fakeBinary puts an executable called name on PATH (and nothing else).
func fakeBinary(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestDetectInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", t.TempDir())
	r, err := NewRegistry([]config.CustomAgent{{ID: "hermes", ProjectDir: ".hermes/skills", GlobalDir: "~/.hermes/skills"}})
	if err != nil {
		t.Fatal(err)
	}
	found := func() map[string]string {
		out := map[string]string{}
		for _, a := range r.All() {
			in := a.Detect()
			switch {
			case !in.Known:
				out[a.ID] = "?"
			case in.Found:
				out[a.ID] = "yes"
			default:
				out[a.ID] = "no"
			}
		}
		return out
	}
	want := func(claude, codex, opencode, pi string) {
		t.Helper()
		got := found()
		exp := map[string]string{"claude": claude, "codex": codex, "opencode": opencode, "pi": pi, "hermes": "?"}
		if !reflect.DeepEqual(got, exp) {
			t.Errorf("detected %v, want %v", got, exp)
		}
	}
	want("no", "no", "no", "no")

	// Config directories count, wherever the agent's own variable points.
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)
	want("yes", "no", "yes", "no")
	codex := filepath.Join(t.TempDir(), "codex")
	os.MkdirAll(codex, 0o755)
	t.Setenv("CODEX_HOME", codex)
	r, _ = NewRegistry(nil)
	if in, _ := r.Get("codex"); !in.Detect().Found {
		t.Error("CODEX_HOME not honoured")
	}

	// A binary on PATH counts without any config directory.
	fakeBinary(t, "pi")
	t.Setenv("CODEX_HOME", "")
	r, _ = NewRegistry([]config.CustomAgent{{ID: "hermes", ProjectDir: ".hermes/skills", GlobalDir: "~/.hermes/skills"}})
	want("yes", "no", "yes", "yes")
	if pi, _ := r.Get("pi"); !strings.Contains(pi.Detect().Via, "pi") {
		t.Errorf("via = %q", pi.Detect().Via)
	}
}
