package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/finalsatan/skill-manager/internal/state"
)

func stateLocked() error { return state.ErrLocked }

func tea_size(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }
