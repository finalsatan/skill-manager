package tui

import (
	"image/color"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"
)

// styles holds every style the TUI uses. Colours adapt to a light or dark
// terminal background, but meaning never depends on colour alone: glyphs and
// words carry it, colour only reinforces.
type styles struct {
	accent  color.Color // active, linked
	pending color.Color // pending changes, updates
	danger  color.Color // errors, problems
	dim     color.Color // secondary text

	Accent  lipgloss.Style
	Pending lipgloss.Style
	Danger  lipgloss.Style
	Dim     lipgloss.Style
	Bold    lipgloss.Style
	Cursor  lipgloss.Style // selected row
	Help    help.Styles
}

func newStyles(isDark bool) styles {
	ld := lipgloss.LightDark(isDark)
	s := styles{
		accent:  ld(lipgloss.Color("#0550AE"), lipgloss.Color("#58A6FF")),
		pending: ld(lipgloss.Color("#BC4C00"), lipgloss.Color("#F0883E")),
		danger:  ld(lipgloss.Color("#CF222E"), lipgloss.Color("#FF7B72")),
		dim:     ld(lipgloss.Color("#6E7781"), lipgloss.Color("#8B949E")),
	}
	s.Accent = lipgloss.NewStyle().Foreground(s.accent)
	s.Pending = lipgloss.NewStyle().Foreground(s.pending)
	s.Danger = lipgloss.NewStyle().Foreground(s.danger)
	s.Dim = lipgloss.NewStyle().Foreground(s.dim)
	s.Bold = lipgloss.NewStyle().Bold(true)
	// The selected row is reversed as well as coloured, so it also shows on
	// terminals without colour.
	s.Cursor = lipgloss.NewStyle().Bold(true).Reverse(true)
	s.Help = help.DefaultStyles(isDark)
	return s
}
