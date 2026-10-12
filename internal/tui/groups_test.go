package tui

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"skillet/internal/agents"
	"skillet/internal/deploy"
	"skillet/internal/i18n"
)

func groupSkills() []skillInfo {
	mk := func(repo, name string, pending bool) skillInfo {
		return skillInfo{ID: "github.com/" + repo + ":skills/" + name, Name: name, Repo: "github.com/" + repo, RelPath: "skills/" + name,
			Description: name + " skill", Pending: pending, RepoDir: "/store/repos/github.com/" + repo, Head: "abcdef1234567"}
	}
	return []skillInfo{
		mk("acme/alpha", "a1", false), mk("acme/alpha", "a2", false),
		mk("acme/beta", "b1", false), mk("acme/beta", "b2", false),
		mk("me/gamma", "g1", true),
	}
}

func groupEntries() []deploy.Entry {
	return []deploy.Entry{
		{Agent: "claude", Name: "a1", Path: filepath.Join(proj, ".claude/skills/a1"), Skill: "github.com/acme/alpha:skills/a1", Mode: "symlink", Health: "ok"},
		// Deployed, but its repo is no longer in the store.
		{Agent: "claude", Name: "old", Path: filepath.Join(proj, ".claude/skills/old"), Skill: "github.com/zzz/deleted:skills/old", Mode: "symlink", Health: "ok", Pin: "ffff"},
		{Agent: "claude", Name: "local", Path: filepath.Join(proj, ".claude/skills/local"), Health: "unmanaged"},
	}
}

func groupData() skillsData {
	rows := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(), Skills: groupSkills(), Entries: groupEntries(), Undeployed: true})
	return skillsData{Agents: testAgents(), Sections: []section{{Scope: agents.Scope{Dir: proj}, Rows: rows}}}
}

func groupedAt(t *testing.T, l i18n.Lang) Model {
	m := newTestModel(t, l)
	return send(t, m, skillsMsg{kind: scopeProject, data: groupData()})
}

// layoutOf lists the items as "▾key" / "▸key" for headers and "  name" for rows.
func layoutOf(m Model) []string {
	var out []string
	for _, it := range m.skills.items {
		switch {
		case it.grp != nil:
			g := "▸"
			if it.grp.open {
				g = "▾"
			}
			out = append(out, g+it.grp.title)
		case it.r != nil:
			out = append(out, "  "+it.r.Name)
		}
	}
	return out
}

func cursorOn(m Model) string {
	if m.skills.cursor >= len(m.skills.items) {
		return ""
	}
	it := m.skills.items[m.skills.cursor]
	if it.grp != nil {
		return "▪" + it.grp.title
	}
	if it.r != nil {
		return it.r.Name
	}
	return ""
}

func TestGroupedLayoutAndInitialFold(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	// Repo id order, unmanaged last; groups with something enabled start open,
	// a repo gone from the store sits under its own id.
	want := []string{"▾acme/alpha", "  a1", "  a2", "▸acme/beta", "▸me/gamma", "▾zzz/deleted", "  old", "▾Unmanaged", "  local"}
	if got := layoutOf(m); !reflect.DeepEqual(got, want) {
		t.Fatalf("layout:\n got %v\nwant %v", got, want)
	}
	if cursorOn(m) != "▪acme/alpha" {
		t.Errorf("cursor on %q", cursorOn(m))
	}
	v := plain(m.View())
	for _, w := range []string{"▾ acme/alpha", "(2 skills · 1 enabled)", "▸ acme/beta", "(2 skills · 0 enabled)", "▸ me/gamma", "↑", "▾ Unmanaged", "(1 skills · 1 enabled)"} {
		if !strings.Contains(v, w) {
			t.Errorf("view lacks %q:\n%s", w, v)
		}
	}
	if !strings.Contains(v, "  a1") {
		t.Errorf("rows are not indented:\n%s", v)
	}
}

func TestGroupedChinese(t *testing.T) {
	m := groupedAt(t, i18n.ZH)
	v := plain(m.View())
	for _, w := range []string{"▾ acme/alpha", "（2 个 · 已启用 1）", "▾ 未托管", "space 折叠/展开 · v 平铺视图"} {
		if !strings.Contains(v, w) {
			t.Errorf("view lacks %q:\n%s", w, v)
		}
	}
}

func TestFoldWithSpaceAndEnterOnHeader(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("space")) // collapse alpha
	if got := layoutOf(m); got[0] != "▸acme/alpha" || got[1] != "▸acme/beta" {
		t.Fatalf("after space: %v", got)
	}
	// enter on a header folds and never opens the preview, even with pending changes.
	m = send(t, m, press("enter"))
	if got := layoutOf(m); got[0] != "▾acme/alpha" || m.modal.open {
		t.Fatalf("after enter: %v modal=%v", got, m.modal.open)
	}
	m = send(t, m, press("j"), press("space")) // a1: pending enable on claude? (a1 is on) -> disable
	if m.skills.summary() == "" {
		t.Fatal("no pending change")
	}
	m = send(t, m, press("k"), press("enter"))
	if m.modal.open {
		t.Error("enter on a header opened the preview")
	}
	if got := layoutOf(m); got[0] != "▸acme/alpha" {
		t.Errorf("header did not fold: %v", got)
	}
	// ... and enter on a skill row still previews. (Pending is in alpha, so open it.)
	m = send(t, m, press("enter"), press("j"), press("enter"))
	if !m.modal.open {
		t.Error("enter on a skill with pending changes did not preview")
	}
}

func TestHeaderIgnoresCellKeys(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	col := m.skills.col
	m = send(t, m, press("l"), press("h"), press("a"), press("p"), press("R"), press("x"))
	if m.skills.col != col || m.skills.summary() != "" || m.modal.open {
		t.Errorf("header reacted: col=%d summary=%q modal=%v", m.skills.col, m.skills.summary(), m.modal.open)
	}
	if m.statusErr {
		t.Errorf("error on a header key: %q", m.status)
	}
}

func TestFoldAllAndCursorRelocation(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("j"), press("j")) // a2, inside alpha
	if cursorOn(m) != "a2" {
		t.Fatalf("cursor on %q", cursorOn(m))
	}
	m = send(t, m, press("z")) // some group is open -> collapse all
	for _, l := range layoutOf(m) {
		if strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "▾") {
			t.Fatalf("not all collapsed: %v", layoutOf(m))
		}
	}
	if cursorOn(m) != "▪acme/alpha" {
		t.Errorf("cursor not moved to its group header: %q", cursorOn(m))
	}
	m = send(t, m, press("z")) // none open -> expand all
	// 5 headers (4 repos and the unmanaged group) and 7 rows.
	if len(layoutOf(m)) != 12 {
		t.Errorf("not all expanded: %v", layoutOf(m))
	}
	if cursorOn(m) != "▪acme/alpha" {
		t.Errorf("cursor lost: %q", cursorOn(m))
	}
}

func TestCursorMovesToHeaderWhenGroupCollapses(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("G")) // last item: local, in Unmanaged
	if cursorOn(m) != "local" {
		t.Fatalf("G -> %q", cursorOn(m))
	}
	m = send(t, m, press("z"))
	if cursorOn(m) != "▪Unmanaged" {
		t.Errorf("cursor %q", cursorOn(m))
	}
}

func TestNavigationOverHeaders(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	var seen []string
	for range 12 {
		seen = append(seen, cursorOn(m))
		m = send(t, m, press("j"))
	}
	want := []string{"▪acme/alpha", "a1", "a2", "▪acme/beta", "▪me/gamma", "▪zzz/deleted", "old", "▪Unmanaged", "local", "local", "local", "local"}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("walk:\n got %v\nwant %v", seen, want)
	}
	m = send(t, m, press("g"))
	if cursorOn(m) != "▪acme/alpha" {
		t.Errorf("g -> %q", cursorOn(m))
	}
	m = send(t, m, press("pgdown"))
	if cursorOn(m) == "▪acme/alpha" {
		t.Error("pgdown did not move")
	}
}

func TestSearchOpensGroupsWithoutChangingFoldState(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	stored := map[string]bool{}
	for k, v := range m.skills.fold[scopeProject] {
		stored[k] = v
	}
	m = send(t, m, press("/"))
	for _, c := range "b2" {
		m = send(t, m, press(string(c)))
	}
	if got := layoutOf(m); !reflect.DeepEqual(got, []string{"▾acme/beta", "  b2"}) {
		t.Fatalf("search layout: %v", got)
	}
	if v := plain(m.View()); !strings.Contains(v, "(1/2 skills · 0 enabled)") {
		t.Errorf("header counts under a search:\n%s", v)
	}
	if !reflect.DeepEqual(m.skills.fold[scopeProject], stored) {
		t.Errorf("search changed the stored state: %v", m.skills.fold[scopeProject])
	}
	// Folding does nothing while searching (every group with matches is open).
	m = send(t, m, press("enter"), press("space"))
	if got := layoutOf(m); !reflect.DeepEqual(got, []string{"▾acme/beta", "  b2"}) {
		t.Fatalf("folded during a search: %v", got)
	}
	m = send(t, m, press("enter")) // confirm the search... then clear it
	m = send(t, m, press("/"), press("esc"))
	if got := layoutOf(m); got[3] != "▸acme/beta" {
		t.Errorf("beta did not return to collapsed: %v", got)
	}
}

func TestOnlyEnabledHidesEmptyGroups(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("f"))
	want := []string{"▾acme/alpha", "  a1", "▾zzz/deleted", "  old", "▾Unmanaged", "  local"}
	if got := layoutOf(m); !reflect.DeepEqual(got, want) {
		t.Errorf("only enabled: %v", got)
	}
	if v := plain(m.View()); !strings.Contains(v, "(1/2 skills · 1 enabled)") {
		t.Errorf("counts:\n%s", v)
	}
}

func TestFoldStateSurvivesReloadAndNewReposFollowTheRule(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("j"), press("j"), press("j"), press("space")) // unfold beta
	if got := layoutOf(m); got[3] != "▾acme/beta" {
		t.Fatalf("beta not open: %v", got)
	}
	m = send(t, m, press("k"), press("k"), press("k"), press("space")) // fold alpha
	// Reload with the same data and an extra repo that has an enabled skill.
	d := groupData()
	extra := buildRows(buildInput{Scope: agents.Scope{Dir: proj}, Agents: testAgents(),
		Skills:     append(groupSkills(), skillInfo{ID: "github.com/new/repo:skills/n1", Name: "n1", Repo: "github.com/new/repo", RelPath: "skills/n1"}),
		Entries:    append(groupEntries(), deploy.Entry{Agent: "claude", Name: "n1", Path: filepath.Join(proj, ".claude/skills/n1"), Skill: "github.com/new/repo:skills/n1", Health: "ok"}),
		Undeployed: true})
	d.Sections[0].Rows = extra
	m = send(t, m, skillsMsg{kind: scopeProject, data: d})
	got := layoutOf(m)
	if got[0] != "▸acme/alpha" || got[1] != "▾acme/beta" {
		t.Errorf("fold state lost on reload: %v", got)
	}
	found := false
	for _, l := range got {
		found = found || l == "▾new/repo"
	}
	if !found {
		t.Errorf("new repo with an enabled skill should start open: %v", got)
	}
	// It also survives the view toggle.
	m = send(t, m, press("v"), press("v"))
	if got2 := layoutOf(m); got2[0] != "▸acme/alpha" {
		t.Errorf("after v v: %v", got2)
	}
}

func TestViewToggle(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("j"), press("j")) // a2
	m = send(t, m, press("v"))
	for _, l := range layoutOf(m) {
		if strings.HasPrefix(l, "▾") || strings.HasPrefix(l, "▸") {
			t.Fatalf("flat view has headers: %v", layoutOf(m))
		}
	}
	if len(layoutOf(m)) != 7 || cursorOn(m) != "a2" {
		t.Errorf("flat: %v cursor %q", layoutOf(m), cursorOn(m))
	}
	// Back to grouped; a2's group is open, the cursor stays on it.
	m = send(t, m, press("v"))
	if cursorOn(m) != "a2" || layoutOf(m)[0] != "▾acme/alpha" {
		t.Errorf("grouped again: %v cursor %q", layoutOf(m), cursorOn(m))
	}
	// A header selected in grouped view lands on the group's first skill in flat.
	m = send(t, m, press("g"), press("v"))
	if cursorOn(m) != "a1" {
		t.Errorf("header -> flat: %q", cursorOn(m))
	}
}

func TestViewToggleIsNoopInAllScopes(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("s"), press("s"), skillsMsg{kind: scopeAll, data: testData()})
	m = send(t, m, press("v"))
	if !strings.Contains(plain(m.View()), "stays flat") || m.skills.flat {
		t.Errorf("v in the all view: flat=%v\n%s", m.skills.flat, plain(m.View()))
	}
	m = send(t, m, press("z")) // nothing to fold either
	for _, it := range m.skills.items {
		if it.grp != nil {
			t.Fatal("group header in the all-scopes view")
		}
	}
}

func TestPendingCountInGroupHeader(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, press("j"), press("j"), press("space")) // a2: pending +1
	m = send(t, m, press("k"), press("k"), press("space")) // fold alpha
	v := plain(m.View())
	if !strings.Contains(v, "▸ acme/alpha") || !strings.Contains(v, "1 pending") {
		t.Errorf("pending not in the header:\n%s", v)
	}
	zh := groupedAt(t, i18n.ZH)
	zh = send(t, zh, press("j"), press("j"), press("space"))
	if !strings.Contains(plain(zh.View()), "1 项待应用") {
		t.Errorf("zh:\n%s", plain(zh.View()))
	}
}

func TestGroupDetailPane(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	v := plain(m.View())
	for _, w := range []string{"github.com/acme/alpha", "/store/repos/github.com/acme/alpha", "abcdef1", "Skills", "Enabled", "space fold/unfold · v flat view"} {
		if !strings.Contains(v, w) {
			t.Errorf("detail lacks %q:\n%s", w, v)
		}
	}
	m = send(t, m, press("j"), press("j"), press("j"), press("j")) // gamma: has an update
	if v := plain(m.View()); !strings.Contains(v, "With updates") {
		t.Errorf("gamma detail:\n%s", v)
	}
}

func TestFooterOffersViewKey(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, tea_size(140, 30), press("j")) // on a skill row
	if v := plain(m.View()); !strings.Contains(v, "space toggle") || !strings.Contains(v, "v view") || !strings.Contains(v, "q quit") {
		t.Errorf("wide footer on a row:\n%s", v)
	}
	m = send(t, m, tea_size(100, 30))
	v := plain(m.View())
	if !strings.Contains(v, "q quit") || !strings.Contains(v, "? help") {
		t.Errorf("narrow footer lost quit/help:\n%s", v)
	}
	m = send(t, m, press("?"))
	v = plain(m.View())
	if !strings.Contains(v, "v") || !strings.Contains(v, "fold all") {
		t.Errorf("full help lacks v/z:\n%s", v)
	}
}

func TestFooterOnGroupHeader(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	m = send(t, m, tea_size(140, 30))
	v := plain(m.View())
	for _, w := range []string{"space/enter fold/unfold", "z fold all", "v flat", "q quit", "? help"} {
		if !strings.Contains(v, w) {
			t.Errorf("header footer lacks %q:\n%s", w, v)
		}
	}
	for _, w := range []string{"space toggle", "enter review", "←/→"} {
		if strings.Contains(v, w) {
			t.Errorf("header footer still has %q", w)
		}
	}
	m = send(t, m, tea_size(80, 30))
	if v := plain(m.View()); !strings.Contains(v, "q quit") || !strings.Contains(v, "? help") {
		t.Errorf("narrow header footer lost quit/help:\n%s", v)
	}
	zh := groupedAt(t, i18n.ZH)
	zh = send(t, zh, tea_size(140, 30))
	if v := plain(zh.View()); !strings.Contains(v, "space/enter 折叠/展开") || !strings.Contains(v, "z 全部折叠") || !strings.Contains(v, "v 平铺") {
		t.Errorf("zh header footer:\n%s", v)
	}
	// Back on a row the usual footer returns.
	zh = send(t, zh, press("j"))
	if v := plain(zh.View()); !strings.Contains(v, "space 切换") || !strings.Contains(v, "enter 预览") {
		t.Errorf("zh row footer:\n%s", v)
	}
}

func TestGroupedHidesRepoColumn(t *testing.T) {
	m := groupedAt(t, i18n.EN)
	v := plain(m.View())
	head := strings.Split(v, "\n")[2]
	if strings.Contains(head, "REPO") || !strings.Contains(head, "STATUS") {
		t.Errorf("grouped column titles: %q", head)
	}
	for _, l := range strings.Split(v, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "a1") && strings.Contains(l, "acme/alpha") {
			t.Errorf("row repeats the repo: %q", l)
		}
	}
	// Flat view and the all-scopes view keep it.
	flat := send(t, m, press("v"))
	if h := strings.Split(plain(flat.View()), "\n")[2]; !strings.Contains(h, "REPO") {
		t.Errorf("flat titles: %q", h)
	}
	all := send(t, m, press("s"), press("s"), skillsMsg{kind: scopeAll, data: testData()})
	if h := strings.Split(plain(all.View()), "\n")[2]; !strings.Contains(h, "REPO") {
		t.Errorf("all-scopes titles: %q", h)
	}
	// The status takes the width: a long status is not cut at the old column.
	if !strings.Contains(v, "pinned ffff") {
		t.Errorf("status missing:\n%s", v)
	}
}

func TestGroupHeaderSpacing(t *testing.T) {
	en := plain(groupedAt(t, i18n.EN).View())
	if !strings.Contains(en, "acme/alpha (2 skills · 1 enabled)") {
		t.Errorf("en header:\n%s", en)
	}
	zh := plain(groupedAt(t, i18n.ZH).View())
	if !strings.Contains(zh, "acme/alpha（2 个 · 已启用 1）") || strings.Contains(zh, "alpha （") {
		t.Errorf("zh header:\n%s", zh)
	}
}
