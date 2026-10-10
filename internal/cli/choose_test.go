package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"skillet/internal/agents"
	"skillet/internal/deploy"
	"skillet/internal/state"
)

type fakeChooser struct {
	skills, agents []string
	confirm        bool
	err            error

	skillOpts, agentOpts []option
	confirms             int
	onPrompt             func() // called whenever the user would be asked
}

func (f *fakeChooser) prompted() {
	if f.onPrompt != nil {
		f.onPrompt()
	}
}

func (f *fakeChooser) Skills(_ string, o []option) ([]string, error) {
	f.prompted()
	f.skillOpts = o
	return f.skills, f.err
}
func (f *fakeChooser) Agents(_ string, o []option) ([]string, error) {
	f.prompted()
	f.agentOpts = o
	return f.agents, f.err
}
func (f *fakeChooser) Confirm(string) (bool, error) {
	f.prompted()
	f.confirms++
	return f.confirm, f.err
}

// cliEnv sets up an isolated home, an upstream repo with two skills and an
// initialized store. It returns the project directory.
func cliEnv(t *testing.T) (proj string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SKILLET_CONFIG_DIR", filepath.Join(home, "cfg"))
	t.Setenv("SKILLET_NO_REMIND", "1")
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "CODEX_HOME", "PI_CODING_AGENT_DIR", "SKILLET_NO_INPUT", "CI"} {
		t.Setenv(k, "")
	}
	up := filepath.Join(root, "up")
	for _, n := range []string{"pdf", "web-search"} {
		dir := filepath.Join(up, "skills", n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + n + "\ndescription: test skill\n---\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", up, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	proj = filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, "init", "--store", filepath.Join(root, "store"))
	run(t, "repo", "add", up)
	return proj
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func withChooser(t *testing.T, f *fakeChooser, tty bool) {
	t.Helper()
	oldC, oldT := newChooser, stdinStdoutAreTTY
	newChooser = func() chooser { return f }
	stdinStdoutAreTTY = func() bool { return tty }
	t.Cleanup(func() { newChooser, stdinStdoutAreTTY = oldC, oldT })
}

func linked(proj, rel string) bool {
	_, err := os.Lstat(filepath.Join(proj, filepath.FromSlash(rel)))
	return err == nil
}

func TestEnableInteractive(t *testing.T) {
	proj := cliEnv(t)
	f := &fakeChooser{agents: []string{"claude"}, confirm: true}
	ids := skillIDs(t)
	f.skills = []string{ids["pdf"]}
	withChooser(t, f, true)

	out, err := run(t, "enable", "--dir", proj)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !linked(proj, ".claude/skills/pdf") || linked(proj, ".claude/skills/web-search") {
		t.Fatalf("links wrong:\n%s", out)
	}
	if f.confirms != 1 || !strings.Contains(out, "Dry run") || len(f.skillOpts) != 2 {
		t.Fatalf("confirms=%d opts=%d\n%s", f.confirms, len(f.skillOpts), out)
	}

	// Declining changes nothing.
	f.skills, f.confirm = []string{ids["web-search"]}, false
	out, err = run(t, "enable", "--dir", proj, "-a", "claude")
	if err != nil || linked(proj, ".claude/skills/web-search") || !strings.Contains(out, "nothing changed") {
		t.Fatalf("decline: %v\n%s", err, out)
	}
	// Already enabled skill is pre-selected next time.
	if !f.skillOpts[0].Selected || f.skillOpts[1].Selected {
		t.Fatalf("preselect: %+v", f.skillOpts)
	}

	// --yes skips the confirm.
	f.confirms = 0
	if out, err = run(t, "enable", "--dir", proj, "-a", "claude", "--yes"); err != nil || f.confirms != 0 || !linked(proj, ".claude/skills/web-search") {
		t.Fatalf("yes: %v confirms=%d\n%s", err, f.confirms, out)
	}

	// Disable interactively.
	f.skills, f.confirm = []string{ids["pdf"]}, true
	out, err = run(t, "disable", "--dir", proj)
	if err != nil || linked(proj, ".claude/skills/pdf") || !linked(proj, ".claude/skills/web-search") {
		t.Fatalf("disable: %v\n%s", err, out)
	}
	if len(f.skillOpts) != 2 || !strings.Contains(f.skillOpts[0].Label, "[enabled: claude]") {
		t.Fatalf("disable opts: %+v", f.skillOpts)
	}
}

func skillIDs(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	withManagerT(t, func(sk map[string]*state.Skill) {
		for id, s := range sk {
			m[s.Name] = id
		}
	})
	return m
}

func TestAbortAndNoPrompt(t *testing.T) {
	proj := cliEnv(t)
	ids := skillIDs(t)

	f := &fakeChooser{err: errAborted}
	withChooser(t, f, true)
	if _, err := run(t, "enable", "--dir", proj); !errors.Is(err, errAborted) {
		t.Fatalf("want errAborted, got %v", err)
	}
	if linked(proj, ".claude") {
		t.Fatal("abort changed something")
	}

	// --no-input, env and non-TTY never prompt.
	f2 := &fakeChooser{skills: []string{ids["pdf"]}, agents: []string{"claude"}, confirm: true}
	withChooser(t, f2, true)
	if _, err := run(t, "enable", "--dir", proj, "--no-input"); err == nil || !strings.Contains(err.Error(), "no skills given") {
		t.Fatalf("no-input: %v", err)
	}
	t.Setenv("SKILLET_NO_INPUT", "1")
	if _, err := run(t, "disable", "--dir", proj); err == nil || !strings.Contains(err.Error(), "no skills given") {
		t.Fatalf("env: %v", err)
	}
	t.Setenv("SKILLET_NO_INPUT", "")
	t.Setenv("CI", "true")
	if _, err := run(t, "enable", "--dir", proj); err == nil {
		t.Fatal("CI should not prompt")
	}
	t.Setenv("CI", "")
	withChooser(t, f2, false)
	if _, err := run(t, "enable", "--dir", proj); err == nil {
		t.Fatal("non-TTY should not prompt")
	}
	// Skills given but no agent, non-TTY: the deploy error.
	if _, err := run(t, "enable", "pdf", "--dir", proj); err == nil || !strings.Contains(err.Error(), "at least one agent") {
		t.Fatalf("missing agent: %v", err)
	}
	if f2.confirms != 0 || f2.skillOpts != nil {
		t.Fatal("chooser used")
	}
}

func TestEnableAsksOnlyForMissingAgents(t *testing.T) {
	proj := cliEnv(t)
	f := &fakeChooser{agents: []string{"codex"}, confirm: true}
	withChooser(t, f, true)
	out, err := run(t, "enable", "pdf", "--dir", proj)
	if err != nil || f.skillOpts != nil || !linked(proj, ".agents/skills/pdf") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestOptionBuilders(t *testing.T) {
	skills := map[string]*state.Skill{
		"r:b": {Repo: "r", Name: "b", Description: strings.Repeat("x", 100)},
		"r:a": {Repo: "r", Name: "a", Description: "does a"},
	}
	served := map[string][]string{"r:a": {"claude", "opencode"}}
	o := skillOptions(skills, served, true)
	if o[0].Value != "r:a" || !o[0].Selected || o[1].Selected {
		t.Fatalf("%+v", o)
	}
	if !strings.HasPrefix(o[0].Label, "a  r — does a") || !strings.Contains(o[0].Label, "[enabled: claude,opencode]") {
		t.Fatal(o[0].Label)
	}
	if len([]rune(o[1].Label)) > 90 || !strings.Contains(o[1].Label, "…") {
		t.Fatalf("not truncated: %q", o[1].Label)
	}
	if skillOptions(skills, served, false)[0].Selected {
		t.Fatal("preselect off")
	}

	served["r:gone"] = []string{"pi"}
	d := deployedOptions(skills, served)
	if len(d) != 2 || d[0].Value != "r:a" || d[1].Value != "r:gone" {
		t.Fatalf("%+v", d)
	}
}

func TestDefaultAgents(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "fakeagent"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	all := []agents.Agent{
		{ID: "claude", Binary: "fakeagent"},
		{ID: "codex", Binary: "nope-not-here"},
		{ID: "custom"},
	}
	// Nothing enabled yet: detected agents.
	got := defaultAgents(all, nil, []string{"s1"})
	if !got["claude"] || got["codex"] || got["custom"] {
		t.Fatalf("%v", got)
	}
	// All chosen skills served: the agents already serving them.
	got = defaultAgents(all, map[string][]string{"s1": {"codex"}, "s2": {"custom"}}, []string{"s1", "s2"})
	if got["claude"] || !got["codex"] || !got["custom"] {
		t.Fatalf("%v", got)
	}
	// One of them not served: fall back to detection.
	got = defaultAgents(all, map[string][]string{"s1": {"codex"}}, []string{"s1", "s2"})
	if !got["claude"] || got["codex"] {
		t.Fatalf("%v", got)
	}
	labels := agentOptions(all, got)
	if !strings.Contains(labels[0].Label, "(installed)") || !labels[0].Selected || labels[1].Selected {
		t.Fatalf("%+v", labels)
	}
}

func TestEnvTrue(t *testing.T) {
	for v, want := range map[string]bool{"": false, "0": false, "false": false, "no": false, "1": true, "true": true} {
		if envTrue(v) != want {
			t.Errorf("%q", v)
		}
	}
}

func withManagerT(t *testing.T, fn func(map[string]*state.Skill)) {
	t.Helper()
	if err := withManager(func(m *deploy.Manager) error { fn(m.St.Skills); return nil }); err != nil {
		t.Fatal(err)
	}
}
