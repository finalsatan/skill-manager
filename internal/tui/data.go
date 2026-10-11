package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"skillet/internal/agents"
	"skillet/internal/deploy"
	"skillet/internal/i18n"
	"skillet/internal/state"
)

// withManager opens the store for the duration of fn. The TUI never keeps a
// Manager across user interaction: every load or operation runs in a tea.Cmd
// that opens, works and closes. A store locked by another process is reported
// as state.ErrLocked rather than waited for.
func withManager(fn func(m *deploy.Manager) error) error {
	m, err := deploy.OpenTry()
	if err != nil {
		return err
	}
	defer m.Close()
	return fn(m)
}

// errText turns a load error into a status-line message.
func errText(err error) string {
	if errors.Is(err, state.ErrLocked) {
		return i18n.T("locked")
	}
	return i18n.T("err.load", err)
}

// countsMsg carries the numbers shown in the tab bar.
type countsMsg struct {
	pending int // skills with upstream changes
	issues  int // doctor findings, not counting pending updates
	err     error
}

// loadCounts reads the pending-update and doctor counts. Doctor runs without
// fixing anything; pending updates are shown on their own tab, so the
// doctor's "pending" finding is not counted twice.
func loadCounts() tea.Cmd {
	return func() tea.Msg {
		var c countsMsg
		c.err = withManager(func(m *deploy.Manager) error {
			c.pending = m.PendingCount()
			issues, err := m.Doctor(false)
			for _, is := range issues {
				if is.Kind != "pending" {
					c.issues++
				}
			}
			return err
		})
		return c
	}
}

// section is one scope's rows. All-scopes view shows one per deployed scope.
type section struct {
	Scope agents.Scope
	Rows  []row
}

// skillsData is what the Skills tab shows.
type skillsData struct {
	Agents   []agents.Agent
	Sections []section
	Missing  []string // project directories that no longer exist (all-scopes view)
}

// skillsMsg is the result of loadSkills. kind says which scope it was asked
// for, so a stale answer after a scope switch is dropped.
type skillsMsg struct {
	kind scopeKind
	data skillsData
	err  error
}

// loadSkills reads the store and every needed scope's status. For the project
// and global scopes all store skills get a row; for "all" only skills that are
// enabled somewhere do, one section per deployed scope.
func loadSkills(kind scopeKind, project agents.Scope) tea.Cmd {
	return func() tea.Msg {
		msg := skillsMsg{kind: kind}
		msg.err = withManager(func(m *deploy.Manager) error {
			var infos []skillInfo
			for _, id := range m.SkillIDs() {
				sk := m.St.Skills[id]
				si := skillInfo{ID: id, Name: sk.Name, Repo: sk.Repo, RelPath: sk.RelPath, Description: sk.Description, Pending: sk.Pending}
				if r, ok := m.St.Repos[sk.Repo]; ok {
					si.RepoDir, si.Head = m.RepoDir(r), r.Head
				}
				infos = append(infos, si)
			}
			var scopes []agents.Scope
			switch kind {
			case scopeProject:
				scopes = []agents.Scope{project}
			case scopeGlobal:
				scopes = []agents.Scope{{Global: true}}
			default:
				set := m.DeployedScopes()
				scopes, msg.data.Missing = set.Scopes, set.Missing
			}
			msg.data.Agents = m.Agents.All()
			for _, s := range scopes {
				entries, err := m.Status(s)
				if err != nil {
					return err
				}
				targets := map[string]string{}
				for _, e := range entries {
					if d := m.St.DeploymentAt(e.Path); d != nil {
						if src, err := m.DeploymentSource(d); err == nil {
							targets[e.Path] = src
						}
					}
				}
				rows := buildRows(buildInput{Scope: s, Agents: msg.data.Agents, Skills: infos,
					Entries: entries, Targets: targets, Undeployed: kind != scopeAll})
				msg.data.Sections = append(msg.data.Sections, section{Scope: s, Rows: rows})
			}
			return nil
		})
		return msg
	}
}
