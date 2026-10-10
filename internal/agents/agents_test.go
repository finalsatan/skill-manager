package agents

import (
	"path/filepath"
	"reflect"
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
