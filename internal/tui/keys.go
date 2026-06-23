package tui

import "charm.land/bubbles/v2/key"

// keyMap holds every binding the picker reacts to. It satisfies help.KeyMap so
// the bubbles help component can render both the compact and full layouts.
type keyMap struct {
	Up       key.Binding
	Down     key.Binding
	Page     key.Binding
	Search   key.Binding
	Resume   key.Binding
	Collapse key.Binding
	Compound key.Binding
	Convert  key.Binding
	Branch   key.Binding
	Delete   key.Binding
	Preview  key.Binding
	Scope    key.Binding
	Yolo     key.Binding
	Codex    key.Binding
	Claude   key.Binding
	Help     key.Binding
	Quit     key.Binding
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
		Convert:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "hand off")),
		Branch:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "branch")),
		Delete:   key.NewBinding(key.WithKeys("delete", "backspace"), key.WithHelp("del", "delete")),
		Preview:  key.NewBinding(key.WithKeys("f", "l", "b"), key.WithHelp("f/l/b", "preview")),
		Scope:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "scope")),
		Yolo:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yolo")),
		Codex:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "codex")),
		Claude:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "claude")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:     key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp is the one-line hint shown under the header.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Resume, k.Collapse, k.Yolo, k.Compound, k.Convert, k.Branch, k.Delete, k.Preview, k.Search, k.Help, k.Quit}
}

// FullHelp is the multi-column layout shown when the user presses "?".
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Page, k.Search},
		{k.Resume, k.Collapse, k.Compound, k.Convert},
		{k.Branch, k.Delete, k.Preview, k.Scope},
		{k.Yolo, k.Codex, k.Claude, k.Help, k.Quit},
	}
}
