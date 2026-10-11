package tui

import (
	"charm.land/bubbles/v2/key"

	"skillet/internal/i18n"
)

// keyMap is the global key bindings, plus the bindings of the active tab.
// It implements help.KeyMap. Bindings are built on demand so the help text
// follows the selected language.
type keyMap struct {
	Tabs    key.Binding
	Scope   key.Binding
	Refresh key.Binding
	Help    key.Binding
	Quit    key.Binding

	// tab holds the active tab's own bindings, shown before the global ones.
	tab [][]key.Binding
}

func newKeyMap(tab [][]key.Binding) keyMap {
	return keyMap{
		Tabs:    key.NewBinding(key.WithKeys("1", "2", "3", "4", "tab", "shift+tab"), key.WithHelp("1-4", i18n.T("help.tabs"))),
		Scope:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", i18n.T("help.scope"))),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", i18n.T("help.refresh"))),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", i18n.T("help.help"))),
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", i18n.T("help.quit"))),
		tab:     tab,
	}
}

func (k keyMap) global() []key.Binding {
	return []key.Binding{k.Tabs, k.Scope, k.Refresh, k.Help, k.Quit}
}

func (k keyMap) ShortHelp() []key.Binding {
	var out []key.Binding
	for _, g := range k.tab {
		out = append(out, g...)
	}
	return append(out, k.global()...)
}

func (k keyMap) FullHelp() [][]key.Binding {
	return append(append([][]key.Binding(nil), k.tab...), k.global())
}
