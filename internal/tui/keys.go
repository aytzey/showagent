package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// keyMap holds every binding the picker reacts to. It satisfies help.KeyMap so
// the bubbles help component can render both the compact and full layouts.
// Stateful bindings (Providers, Target, Scope, Preview, Yolo) get their help
// text rewritten per frame by model.dynamicKeys so the bar shows live values.
type keyMap struct {
	Up        key.Binding
	Down      key.Binding
	Page      key.Binding
	Search    key.Binding
	Import    key.Binding
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
		Import:   key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "import")),
		Resume:   key.NewBinding(key.WithKeys("enter", "ctrl+m"), key.WithHelp("enter", "resume")),
		Collapse: key.NewBinding(key.WithKeys("space", " "), key.WithHelp("space", "collapse")),
		Compound: key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "compound")),
		Target:   key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "target")),
		Convert:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "hand off")),
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

// ShortHelp is the always-visible bottom bar: only the core actions, short
// enough for an 80-column terminal. Everything else lives behind "?".
func (k keyMap) ShortHelp() []key.Binding {
	more := k.Help
	more.SetHelp("?", "all keys")
	return []key.Binding{k.Resume, k.Convert, k.Search, k.Import, k.Quit, more}
}

// FullHelp is the multi-column layout shown when the user presses "?".
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Page, k.Search, k.Import},
		{k.Resume, k.Collapse, k.Compound, k.Convert, k.Branch},
		{k.Delete, k.Preview, k.Target, k.Scope, k.Yolo},
		{k.Providers, k.Rescan, k.Help, k.Quit},
	}
}

// keysPanelHeight is the always-visible keys box: a group-title row, four
// key rows, and the top and bottom borders.
const keysPanelHeight = 7

// minListRows keeps the session list usable; below it the keys box gives way
// to the one-line help bar.
const minListRows = 5

type keyHint struct{ key, desc, value string }

var keyGroupTitles = []string{"SESSION", "HAND OFF", "FIND", "MORE"}

// keyGroups lists every picker key in four fixed groups with live values, so
// the box shows the current target, turns, preview and mode.
func (m model) keyGroups() [][]keyHint {
	target := string(m.selectedHandoffTarget())
	resume := keyHint{"enter", "resume", ""}
	if _, ok := m.list.SelectedItem().(headerItem); ok {
		resume.desc = "open/close"
	}
	handoff := keyHint{"x", "hand off", ""}
	if target != "" {
		handoff = keyHint{"x", "hand off →", target}
	}
	if selected, ok := m.list.SelectedItem().(item); ok && m.selectedConversionPreview(selected.row) != nil {
		handoff.desc = "confirm →"
	}
	agents := "1"
	if n := len(discoveredProviders(m.allRows)); n > 1 {
		agents = fmt.Sprintf("1-%d", min(n, 9))
	}
	preview := modeShort(m.mode)
	if m.mode == bothMessages {
		preview = "both"
	}
	return [][]keyHint{
		{resume, {"n", "branch", ""}, {"C", "compound", ""}, {"d", "delete", ""}},
		{handoff, {"o", "target", target}, {"t", "turns", m.handoff.Label()}, {"i", "import", ""}},
		{{"/", "search", ""}, {"p", "preview", preview}, {agents, "agents", ""}, {"space", "collapse", ""}},
		{{"y", "mode", resumeModeLabel(m.dangerous)}, {"r", "rescan", ""}, {"?", "hide keys", ""}, {"q", "quit", ""}},
	}
}

func (m model) keysPanelFits() bool {
	return m.width >= 80 && m.height-3-m.detailHeight()-keysPanelHeight >= minListRows
}

// bottomView is the keys box when it fits and is not hidden, otherwise the
// one-line help bar (or the "?" overlay on small terminals).
func (m model) bottomView() string {
	if !m.keysHidden && m.keysPanelFits() {
		return m.keysPanel()
	}
	// Clamp before joining: JoinVertical pads every line to the widest one,
	// so one overlong line would otherwise clip the whole screen.
	return clampLineWidths(m.helpView(), m.width)
}

func (m model) keysPanel() string {
	th := m.render.theme
	const gap = 3
	groups := m.keyGroups()
	columns := make([][]string, len(groups))
	widths := make([]int, len(groups))
	for index, group := range groups {
		keyWidth := 0
		for _, hint := range group {
			keyWidth = max(keyWidth, lipgloss.Width(hint.key))
		}
		cells := []string{th.keyGroup.Render(keyGroupTitles[index])}
		for _, hint := range group {
			cell := th.keyCap.Render(padCells(hint.key, keyWidth)) + " " + th.keyDesc.Render(hint.desc)
			if hint.value != "" {
				value := th.keyValue.Render(hint.value)
				if hint.key == "y" && m.dangerous {
					value = th.yoloChip.Render(hint.value)
				}
				cell += " " + value
			}
			cells = append(cells, cell)
		}
		for _, cell := range cells {
			widths[index] = max(widths[index], lipgloss.Width(cell))
		}
		columns[index] = cells
	}
	// Columns sit on a fixed grid so they do not shift as values change;
	// when that does not fit, the widest column shrinks and truncates.
	innerW := m.width - 4
	slot := min((innerW-gap*(len(widths)-1))/len(widths), 30)
	grid := make([]int, len(widths))
	for index, width := range widths {
		grid[index] = max(width, slot)
	}
	if sum(grid)+gap*(len(grid)-1) <= innerW {
		widths = grid
	}
	for sum(widths)+gap*(len(widths)-1) > innerW && maxIndex(widths) >= 0 && widths[maxIndex(widths)] > 8 {
		widths[maxIndex(widths)]--
	}
	lines := make([]string, keysPanelHeight-2)
	for row := range lines {
		cells := make([]string, len(columns))
		for index, column := range columns {
			cells[index] = padCells(truncateCells(column[row], widths[index]), widths[index])
		}
		lines[row] = strings.Join(cells, strings.Repeat(" ", gap))
	}
	return titledBox(th.keysEdge, th.boxTitle, "Keys", fitLines(lines, len(lines), innerW), m.width)
}

func sum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func maxIndex(values []int) int {
	best := -1
	for index, value := range values {
		if best < 0 || value > values[best] {
			best = index
		}
	}
	return best
}
