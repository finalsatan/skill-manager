package tui

import "skillet/internal/state"

func stateLocked() error { return state.ErrLocked }
