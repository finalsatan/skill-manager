package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"skillet/internal/agents"
	"skillet/internal/deploy"
	"skillet/internal/i18n"
)

func dupSkills() []skillInfo {
	mk := func(repo, rel, name string) skillInfo {
		return skillInfo{ID: repo + ":" + rel, Name: name, Repo: repo, RelPath: rel}
	}
	return []skillInfo{
		mk("r1", ".openclaw/skills/pdf", "pdf"),
		mk("r1", "skills/pdf", "pdf"),
		mk("r1", "skills/docx", "docx"),
		mk("r2", "skills/pdf", "pdf"), // same name, other repo
	}
}

func TestPathHintOnlyForSameNameInOneRepo(t *testing.T) {
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: dupSkills(), Undeployed: true})
	var got []string
	for _, r := range rows {
		got = append(got, r.ID+"|"+r.PathHint)
	}
	// Name order; within a name the canonical copy first, then by path.
	want := []string{"r1:skills/docx|", "r1:skills/pdf|skills/pdf", "r1:.openclaw/skills/pdf|.openclaw/skills/pdf", "r2:skills/pdf|"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows:\n got %v\nwant %v", got, want)
	}
}

func TestPathHintCountsHiddenRowsToo(t *testing.T) {
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: dupSkills(), Undeployed: true})
	// A search that leaves only one of the pair does not change its label.
	got := filterRows(rows, false, ".openclaw")
	if len(got) != 1 || got[0].PathHint != ".openclaw/skills/pdf" {
		t.Errorf("filtered: %+v", got)
	}
	// The relative path is searchable, not just the id.
	if got := filterRows(rows, false, "skills/docx"); len(got) != 1 || got[0].Name != "docx" {
		t.Errorf("path search: %+v", got)
	}
}

func TestPathHintWithAnAlias(t *testing.T) {
	skills := dupSkills()[:2]
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: skills, Undeployed: true,
		Entries: []deploy.Entry{{Agent: "claude", Name: "pdf-old", Path: filepath.Join(proj, ".claude/skills/pdf-old"), Skill: "r1:.openclaw/skills/pdf", Health: "ok", Alias: "pdf-old"}}})
	for _, r := range rows {
		if r.PathHint == "" {
			t.Errorf("%s lost its hint", r.ID)
		}
		if r.ID == "r1:.openclaw/skills/pdf" && r.Alias != "pdf-old" {
			t.Errorf("alias: %+v", r)
		}
	}
}

func TestNameCellTruncation(t *testing.T) {
	cases := []struct {
		name, path string
		w          int
		left, hint string
	}{
		{"pdf", "", 10, "pdf       ", ""},
		{"pdf", "skills/pdf", 20, "pdf", "  skills/pdf     "},
		{"pdf", ".openclaw/skills/pdf", 16, "pdf", "  …skills/pdf"}, // the tail is kept
		{"pdf", ".openclaw/skills/pdf", 9, "pdf", "  …pdf"},
		{"pdf", ".openclaw/skills/pdf", 8, "pdf     ", ""}, // no room for a useful directory
		{"数据", "skills/数据", 14, "数据", "  …ls/数据"},
	}
	for _, c := range cases {
		left, hint := nameCell(c.name, c.path, c.w)
		if lipgloss.Width(left)+lipgloss.Width(hint) != c.w {
			t.Errorf("%v: width %d != %d", c, lipgloss.Width(left)+lipgloss.Width(hint), c.w)
		}
		if left != c.left || strings.TrimRight(hint, " ") != strings.TrimRight(c.hint, " ") {
			t.Errorf("%v: got %q %q", c, left, hint)
		}
	}
}

func TestSameNameRowsInEveryView(t *testing.T) {
	data := skillsData{Agents: testAgents(), Sections: []section{{Scope: agents.Scope{Dir: proj},
		Rows: buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: dupSkills()[:3], Undeployed: true})}}}
	m := newTestModel(t, i18n.EN)
	m = send(t, m, skillsMsg{kind: scopeProject, data: data}, press("enter")) // unfold r1
	v := plain(m.View())
	for _, w := range []string{"pdf  skills/pdf", "pdf  .openclaw/skills/pdf"} {
		if !strings.Contains(v, w) {
			t.Errorf("grouped lacks %q:\n%s", w, v)
		}
	}
	if strings.Contains(v, "docx  skills/docx") {
		t.Error("unique name got a directory")
	}
	m = send(t, m, press("v"))
	if v := plain(m.View()); !strings.Contains(v, "pdf  skills/pdf") || !strings.Contains(v, "pdf  .openclaw/skills/pdf") {
		t.Errorf("flat:\n%s", v)
	}
	// Selecting one shows its id and path in the detail pane.
	m = send(t, m, press("j"), press("j"))
	if v := plain(m.View()); !strings.Contains(v, "r1:.openclaw/skills/pdf") {
		t.Errorf("detail pane:\n%s", v)
	}
	// All scopes.
	all := send(t, m, press("s"), press("s"), skillsMsg{kind: scopeAll, data: data})
	if v := plain(all.View()); !strings.Contains(v, "pdf  .openclaw/skills/pdf") {
		t.Errorf("all-scopes:\n%s", v)
	}
}

func TestClashMarker(t *testing.T) {
	a, b := "r1:skills/pdf", "r1:legacy/pdf"
	skills := []skillInfo{{ID: a, Name: "pdf", Repo: "r1", RelPath: "skills/pdf"}, {ID: b, Name: "pdf", Repo: "r1", RelPath: "legacy/pdf"}, {ID: "r1:skills/docx", Name: "docx", Repo: "r1", RelPath: "skills/docx"}}
	pa, pb := filepath.Join(proj, ".claude/skills/pdf"), filepath.Join(proj, ".agents/skills/pdf")
	entries := []deploy.Entry{
		{Agent: "claude", Name: "pdf", Path: pa, Skill: a, Health: "ok"},
		{Agent: "codex", Name: "pdf", Path: pb, Skill: b, Health: "ok"},
		// OpenCode reads both directories: it sees two different pdf skills.
		{Agent: "opencode", Name: "pdf", Path: pa, Skill: a, Health: "ok"},
		{Agent: "opencode", Name: "pdf", Path: pb, Skill: b, Health: "ok"},
	}
	clashes := deploy.Clashes(entries)
	if len(clashes) != 1 {
		t.Fatalf("clashes: %+v", clashes)
	}
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: skills, Entries: entries, Undeployed: true, Clashes: clashes})
	for _, r := range rows {
		want := r.Name == "pdf"
		if r.Clash != want {
			t.Errorf("%s: Clash=%v", r.ID, r.Clash)
		}
	}
	for _, l := range []i18n.Lang{i18n.EN, i18n.ZH} {
		m := newTestModel(t, l)
		m = send(t, m, skillsMsg{kind: scopeProject, data: skillsData{Agents: testAgents(), Sections: []section{{Scope: agents.Scope{Dir: proj}, Rows: rows}}}}, press("v"))
		want := map[i18n.Lang]string{i18n.EN: "⚠ name clash", i18n.ZH: "⚠ 同名冲突"}[l]
		if v := plain(m.View()); strings.Count(v, want) != 2 {
			t.Errorf("%s: want 2x %q:\n%s", l, want, v)
		}
	}
}

func TestNameTakenHintOnRealStore(t *testing.T) {
	rs := newStoreWith(t, "skills/pdf", "legacy/pdf")
	first, second := rs.repo+":skills/pdf", rs.repo+":legacy/pdf"
	if am := applyCmd(scopeProject, rs.proj, []change{{ID: first, Name: "pdf", Enable: []string{"claude"}}})().(applyMsg); am.err != nil {
		t.Fatal(am.err)
	}
	// The other pdf wants the same link name in the same directory.
	cs := []change{{ID: second, Name: "pdf", Enable: []string{"claude"}}}
	pm := previewCmd(scopeProject, rs.proj, cs)().(previewMsg)
	if pm.err != nil || len(pm.plans) != 1 || !errors.Is(pm.plans[0].Err, deploy.ErrNameTaken) {
		t.Fatalf("preview: %+v", pm)
	}
	for l, hint := range map[i18n.Lang]string{i18n.EN: "Press R on the skill to give it another link name, then enable it", i18n.ZH: "可在该 skill 上按 R 改名后再启用"} {
		m := loadedModel(t, l)
		m = send(t, m, press("space"), press("enter"), pm)
		v := plain(m.View())
		if !strings.Contains(v, "is already used") || !strings.Contains(v, hint) {
			t.Errorf("%s: hint missing:\n%s", l, v)
		}
	}
	// Other failures get no such hint.
	if errors.Is(errors.New("x"), deploy.ErrNameTaken) {
		t.Fatal("sentinel matches everything")
	}
	// An alias lets the second one in.
	cs[0].Alias = "pdf-legacy"
	pm = previewCmd(scopeProject, rs.proj, cs)().(previewMsg)
	if pm.err != nil || pm.plans[0].Err != nil {
		t.Fatalf("aliased preview: %+v", pm.plans[0].Err)
	}
	if am := applyCmd(scopeProject, rs.proj, cs)().(applyMsg); am.err != nil {
		t.Fatal(am.err)
	}
	if !exists(filepath.Join(rs.links, "pdf")) || !exists(filepath.Join(rs.links, "pdf-legacy")) {
		t.Error("both links should exist")
	}
}

// The user's real case: a repo that ships every skill twice, canonical and
// as an agent-specific variant under a dot-directory.
func TestPonytailLayout(t *testing.T) {
	names := []string{"ponytail", "ponytail-audit", "ponytail-debt", "ponytail-gain", "ponytail-help", "ponytail-review"}
	var rels []string
	for _, n := range names {
		rels = append(rels, ".openclaw/skills/"+n, "skills/"+n)
	}
	rs := newStoreWith(t, rels...)
	msg := loadSkills(scopeProject, rs.proj)().(skillsMsg)
	if msg.err != nil || len(msg.data.Sections[0].Rows) != 12 {
		t.Fatalf("load: %+v", msg.err)
	}

	m := newTestModel(t, i18n.EN)
	m = send(t, m, tea_size(100, 40), msg, press("enter")) // unfold the repo
	lines := strings.Split(plain(m.View()), "\n")
	find := func(sub string) int {
		for i, l := range lines {
			if strings.Contains(l, sub) {
				return i
			}
		}
		return -1
	}
	last := -1
	for _, n := range names {
		canon, variant := find("  skills/"+n), find("  .openclaw/skills/"+n)
		if canon < 0 || variant < 0 {
			t.Fatalf("%s: labels missing or truncated at 100 columns:\n%s", n, strings.Join(lines, "\n"))
		}
		if !(last < canon && canon < variant) {
			t.Errorf("%s: canonical row (%d) must come first, then its variant (%d), after the previous name (%d)", n, canon, variant, last)
		}
		last = variant
	}
	for _, l := range lines {
		if strings.Contains(l, "ponytail") && strings.Contains(l, "…") && !strings.Contains(l, "ponytail-") {
			t.Errorf("truncated row: %q", l)
		}
	}
	for i, l := range lines {
		if lipgloss.Width(l) > 100 {
			t.Errorf("line %d is %d cells", i, lipgloss.Width(l))
		}
	}

	// Flat view: a wide terminal shows everything; at 100 the tail of the
	// directory is kept.
	flat := send(t, m, press("v"), tea_size(130, 40))
	fl := plain(flat.View())
	for _, n := range names {
		if !strings.Contains(fl, "  .openclaw/skills/"+n) || !strings.Contains(fl, "  skills/"+n) {
			t.Errorf("flat 130: %s truncated", n)
		}
	}
	narrow := plain(send(t, flat, tea_size(100, 40)).View())
	if !strings.Contains(narrow, "skills/ponytail-review") {
		t.Errorf("flat 100 lost the directory's tail:\n%s", narrow)
	}
}
