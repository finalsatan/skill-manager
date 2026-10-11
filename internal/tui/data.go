package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"

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
