package tui

import "charm.land/bubbles/v2/key"

// keyMap holds every binding the picker reacts to. It satisfies help.KeyMap so
// the bubbles help component can render both the compact and full layouts.
// Stateful bindings (Providers, Target, Scope, Preview, Yolo) get their help
// text rewritten per frame by model.dynamicKeys so the bar shows live values.
type keyMap struct {
	Up        key.Binding
	Down      key.Binding
	Page      key.Binding
	Search    key.Binding
	Resume    key.Binding
	Collapse  key.Binding
	Compound  key.Binding
	Target    key.Binding
	Convert   key.Binding
	Branch    key.Binding
	Delete    key.Binding
	Preview   key.Binding
	Scope     key.Binding
	Yolo      key.Binding
	Providers key.Binding
	Rescan    key.Binding
	Help      key.Binding
	Quit      key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Page:     key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "page")),
		Search:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Resume:   key.NewBinding(key.WithKeys("enter", "ctrl+m"), key.WithHelp("enter", "resume")),
		Collapse: key.NewBinding(key.WithKeys("space", " "), key.WithHelp("space", "collapse")),
		Compound: key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "compound")),
		Target:   key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "target")),
		Convert:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "preview")),
		Branch:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "branch")),
		Delete:   key.NewBinding(key.WithKeys("d", "delete", "backspace"), key.WithHelp("d/del", "delete")),
		Preview:  key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "preview")),
		Scope:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "scope")),
		Yolo:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yolo")),
		Providers: key.NewBinding(
			key.WithKeys(providerFilterKeys()...),
			key.WithHelp("1-9", "providers"),
		),
		Rescan: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rescan")),
		Help:   key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// providerFilterKeys binds one digit per potential provider slot. A digit's
// meaning follows the discovered provider order, so new providers pick up the
// next free digit without a keymap change.
func providerFilterKeys() []string {
	keys := make([]string, 0, 9)
	for digit := '1'; digit <= '9'; digit++ {
		keys = append(keys, string(digit))
	}
	return keys
}

// ShortHelp is the one-line hint shown under the header. Entries are ordered
// by importance because narrow terminals truncate the tail.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Resume, k.Search, k.Providers, k.Preview, k.Target, k.Scope, k.Convert, k.Branch, k.Delete, k.Rescan, k.Yolo, k.Compound, k.Help, k.Quit}
}

// FullHelp is the multi-column layout shown when the user presses "?".
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Page, k.Search},
		{k.Resume, k.Collapse, k.Compound, k.Convert, k.Branch},
		{k.Delete, k.Preview, k.Target, k.Scope, k.Yolo},
		{k.Providers, k.Rescan, k.Help, k.Quit},
	}
}
