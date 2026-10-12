package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/deploy"
)

func TestStatusMatrix(t *testing.T) {
	all := []agents.Agent{{ID: "claude"}, {ID: "codex"}, {ID: "opencode"}, {ID: "pi"}}
	const pdf = "github.com/anthropics/skills:skills/pdf"
	const other = "local/up:skills/pdf"
	entries := []deploy.Entry{
		{Agent: "claude", Name: "pdf", Skill: pdf, Health: "ok"},
		{Agent: "opencode", Name: "pdf", Skill: pdf, Health: "ok"},
		{Agent: "opencode", Name: "pdf", Skill: pdf, Health: "ok"},
		{Agent: "codex", Name: "pdf", Skill: other, Health: "ok"},
		{Agent: "opencode", Name: "pdf", Skill: other, Health: "ok"},
		{Agent: "codex", Name: "gone", Skill: "local/up:skills/gone", Health: "missing"},
		{Agent: "pi", Name: "mine", Health: "unmanaged"},
	}
	var buf bytes.Buffer
	if err := printStatusMatrix(&buf, all, entries); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"SKILL  claude  codex  opencode  pi  STATE      SOURCE",
		"gone   -       -      -         -   missing    local/up",
		"mine   -       -      -         ✓   unmanaged  -",
		"pdf    ✓       -      ✓(2)      -   ok         anthropics/skills",
		"pdf    -       ✓      ✓         -   ok         local/up",
		`warning: opencode sees 2 different skills named "pdf"`,
		"warning: gone link is missing",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `named "gone"`) {
		t.Errorf("missing link counted as a clash:\n%s", out)
	}
}
