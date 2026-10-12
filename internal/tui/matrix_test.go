package tui

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/finalsatan/skill-manager/internal/agents"
	"github.com/finalsatan/skill-manager/internal/deploy"
)

const proj = "/work/proj"

func testAgents() []agents.Agent {
	return []agents.Agent{
		{ID: "claude", ProjectDir: ".claude/skills"},
		{ID: "codex", ProjectDir: ".agents/skills"},
		{ID: "opencode", ProjectDir: ".opencode/skills"},
		{ID: "pi", ProjectDir: ".pi/skills"},
	}
}

func testSkills() []skillInfo {
	return []skillInfo{
		{ID: "r:skills/docx", Name: "docx", Repo: "r", RelPath: "skills/docx", Description: "Word files"},
		{ID: "r:skills/pdf", Name: "pdf", Repo: "r", RelPath: "skills/pdf", Description: "Read and write PDF", Head: "1234567890", RepoDir: "/store/r"},
		{ID: "r:skills/xlsx", Name: "xlsx", Repo: "r", RelPath: "skills/xlsx", Description: "Spreadsheets", Pending: true},
		{ID: "r:skills/slides", Name: "slides", Repo: "r", RelPath: "skills/slides", Pending: true},
	}
}

func testEntries() []deploy.Entry {
	claudePdf := filepath.Join(proj, ".claude/skills/pdf")
	return []deploy.Entry{
		// pdf: linked for claude, also seen by opencode through .claude/skills.
		{Agent: "claude", Name: "pdf", Path: claudePdf, Skill: "r:skills/pdf", Mode: "symlink", Health: "ok"},
		{Agent: "opencode", Name: "pdf", Path: claudePdf, Skill: "r:skills/pdf", Mode: "symlink", Health: "ok"},
		// xlsx: a broken link for codex, unpinned and pending.
		{Agent: "codex", Name: "xlsx", Path: filepath.Join(proj, ".agents/skills/xlsx"), Skill: "r:skills/xlsx", Mode: "symlink", Health: "broken"},
		// slides: pinned (so its pending flag is ignored) and renamed.
		{Agent: "pi", Name: "deck", Path: filepath.Join(proj, ".pi/skills/deck"), Skill: "r:skills/slides", Mode: "symlink", Health: "ok", Pin: "abcdef1234", Alias: "deck"},
		// A skill no longer in the store, still deployed (pinned).
		{Agent: "claude", Name: "gone", Path: filepath.Join(proj, ".claude/skills/gone"), Skill: "r:skills/gone", Mode: "symlink", Health: "ok", Pin: "ffff"},
		// Not ours.
		{Agent: "claude", Name: "local", Path: filepath.Join(proj, ".claude/skills/local"), Health: "unmanaged"},
		{Agent: "opencode", Name: "local", Path: filepath.Join(proj, ".claude/skills/local"), Health: "unmanaged"},
	}
}

func byName(rows []row) map[string]row {
	m := map[string]row{}
	for _, r := range rows {
		m[r.Name] = r
	}
	return m
}

func TestBuildRows(t *testing.T) {
	rows := buildRows(buildInput{
		Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: testSkills(), Entries: testEntries(),
		Targets: map[string]string{filepath.Join(proj, ".claude/skills/pdf"): "/store/r/skills/pdf"}, Undeployed: true,
	})
	var names []string
	for _, r := range rows {
		names = append(names, r.Name)
	}
	// Managed sorted by name, unmanaged last.
	if want := []string{"docx", "gone", "pdf", "slides", "xlsx", "local"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("rows = %v, want %v", names, want)
	}
	m := byName(rows)

	pdf := m["pdf"]
	if pdf.Cells["claude"] != cellLinked || pdf.Cells["opencode"] != cellVia || pdf.Cells["codex"] != cellOff {
		t.Errorf("pdf cells: %v", pdf.Cells)
	}
	if !reflect.DeepEqual(pdf.Visible, []string{"claude", "opencode"}) {
		t.Errorf("pdf visible: %v", pdf.Visible)
	}
	if len(pdf.Links) != 1 || pdf.Links[0].Target != "/store/r/skills/pdf" || !reflect.DeepEqual(pdf.Links[0].Agents, []string{"claude", "opencode"}) {
		t.Errorf("pdf links: %+v", pdf.Links)
	}
	if pdf.Health != "ok" || pdf.Head != "1234567890" || pdf.Mode != "symlink" {
		t.Errorf("pdf: %+v", pdf)
	}

	if d := m["docx"]; d.enabled() || d.Health != "" {
		t.Errorf("docx should be off: %+v", d)
	}

	x := m["xlsx"]
	if x.Cells["codex"] != cellProblem || x.Health != "broken" || !x.Pending {
		t.Errorf("xlsx: %+v", x)
	}

	sl := m["slides"]
	if sl.Pin != "abcdef1234" || sl.Alias != "deck" || sl.Pending || sl.Cells["pi"] != cellLinked {
		t.Errorf("slides: %+v", sl)
	}

	g := m["gone"]
	if g.ID != "r:skills/gone" || g.Repo != "r" || g.Cells["claude"] != cellLinked {
		t.Errorf("gone: %+v", g)
	}

	l := m["local"]
	if l.managed() || l.Cells["claude"] != cellUnmanaged || l.Cells["opencode"] != cellUnmanaged || l.Health != "unmanaged" {
		t.Errorf("local: %+v", l)
	}
}

func TestBuildRowsDeployedOnly(t *testing.T) {
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: testSkills(), Entries: testEntries()})
	if _, ok := byName(rows)["docx"]; ok {
		t.Error("undeployed skill listed without Undeployed")
	}
}

func TestWorstHealthWins(t *testing.T) {
	p := filepath.Join(proj, ".claude/skills/pdf")
	rows := buildRows(buildInput{
		Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: testSkills(),
		Entries: []deploy.Entry{
			{Agent: "claude", Name: "pdf", Path: p, Skill: "r:skills/pdf", Health: "ok"},
			{Agent: "codex", Name: "pdf", Path: filepath.Join(proj, ".agents/skills/pdf"), Skill: "r:skills/pdf", Health: "modified"},
			{Agent: "pi", Name: "pdf", Path: filepath.Join(proj, ".pi/skills/pdf"), Skill: "r:skills/pdf", Health: "missing"},
		},
	})
	if len(rows) != 1 || rows[0].Health != "missing" || rows[0].Cells["codex"] != cellProblem {
		t.Errorf("rows: %+v", rows)
	}
}

func TestGlobalScopeOwnDir(t *testing.T) {
	ags := []agents.Agent{{ID: "claude", GlobalDir: "/home/u/.claude/skills"}, {ID: "opencode", GlobalDir: "/home/u/.config/opencode/skills"}}
	p := "/home/u/.claude/skills/pdf"
	rows := buildRows(buildInput{Scope: agents.Scope{Global: true}, Agents: ags, Skills: testSkills(), Entries: []deploy.Entry{
		{Agent: "claude", Name: "pdf", Path: p, Skill: "r:skills/pdf", Health: "ok"},
		{Agent: "opencode", Name: "pdf", Path: p, Skill: "r:skills/pdf", Health: "ok"},
	}})
	if rows[0].Cells["claude"] != cellLinked || rows[0].Cells["opencode"] != cellVia {
		t.Errorf("cells: %v", rows[0].Cells)
	}
}

func TestFilterRows(t *testing.T) {
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: testSkills(), Entries: testEntries(), Undeployed: true})
	names := func(rs []row) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Name)
		}
		return out
	}
	if got := names(filterRows(rows, false, "")); len(got) != 6 {
		t.Errorf("no filter: %v", got)
	}
	if got := names(filterRows(rows, true, "")); !reflect.DeepEqual(got, []string{"gone", "pdf", "slides", "xlsx", "local"}) {
		t.Errorf("only enabled: %v", got)
	}
	// Name, description, repo and alias all match, case-insensitively.
	for q, want := range map[string][]string{
		"PDF":         {"pdf"},
		"spreadsheet": {"xlsx"},
		"word":        {"docx"},
		"deck":        {"slides"},
		"r:skills/g":  {"gone"},
		"nothing":     nil,
	} {
		if got := names(filterRows(rows, false, q)); !reflect.DeepEqual(got, want) {
			t.Errorf("query %q: %v, want %v", q, got, want)
		}
	}
	if got := names(filterRows(rows, true, "word")); got != nil {
		t.Errorf("only enabled + word: %v", got)
	}
}

func TestFilterRowsChinese(t *testing.T) {
	rows := []row{{Name: "pdf", Description: "读写 PDF 文件"}, {Name: "docx", Description: "处理 Word 文档"}}
	if got := filterRows(rows, false, "文档"); len(got) != 1 || got[0].Name != "docx" {
		t.Errorf("got %+v", got)
	}
}
