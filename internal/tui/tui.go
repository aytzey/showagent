package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	list "charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aytzey/showcodex/internal/session"
)

type previewMode int

const (
	firstMessage previewMode = iota
	lastMessage
	bothMessages
)

type item struct {
	row session.Row
}

func (i item) FilterValue() string {
	return i.row.FilterValue()
}

type itemDelegate struct{}

func (d itemDelegate) Height() int  { return 1 }
func (d itemDelegate) Spacing() int { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd {
	return nil
}

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}

	width := m.Width()
	providerWidth := 7
	dateWidth := 16
	cwdWidth := clamp(width/3, 22, 46)
	previewWidth := max(10, width-providerWidth-dateWidth-cwdWidth-8)

	style := rowStyle
	if index == m.Index() {
		style = selectedRowStyle
	}

	line := fmt.Sprintf(
		"%-*s %-*s %-*s %s",
		providerWidth,
		string(it.row.Provider),
		dateWidth,
		localTime(it.row.LastAt),
		cwdWidth,
		truncateMiddle(it.row.CWD, cwdWidth),
		truncateCells(previewFor(it.row, currentMode), previewWidth),
	)
	_, _ = fmt.Fprint(w, style.Width(width).Render(line))
}

var currentMode = firstMessage

type model struct {
	list      list.Model
	allRows   []session.Row
	providers map[session.Provider]bool
	mode      previewMode
	selected  *session.Row
	width     int
	height    int
}

func newModel(rows []session.Row, mode previewMode) model {
	providers := defaultProviderFilter(rows)
	items := itemsFromRows(filterRows(rows, providers))

	delegate := itemDelegate{}
	l := list.New(items, delegate, 100, 24)
	l.Title = "showcodex"
	l.SetStatusBarItemName("session", "sessions")
	l.SetFilteringEnabled(true)
	l.SetShowFilter(true)
	l.SetShowHelp(true)
	l.SetShowPagination(true)
	l.SetShowStatusBar(true)
	l.SetShowTitle(false)
	l.DisableQuitKeybindings()
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "first")),
			key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "last")),
			key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "both")),
			key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "codex")),
			key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "claude")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "resume")),
			key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		}
	}

	currentMode = mode
	return model{list: l, allRows: rows, providers: providers, mode: mode}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeList()
	case tea.KeyPressMsg:
		if !m.list.SettingFilter() {
			switch msg.String() {
			case "ctrl+c", "esc", "q":
				return m, tea.Quit
			case "enter":
				if selected, ok := m.list.SelectedItem().(item); ok {
					row := selected.row
					m.selected = &row
					return m, tea.Quit
				}
			case "f":
				m.mode = firstMessage
				currentMode = m.mode
			case "l":
				m.mode = lastMessage
				currentMode = m.mode
			case "b":
				m.mode = bothMessages
				currentMode = m.mode
			case "c":
				cmd := m.toggleProvider(session.ProviderCodex)
				return m, cmd
			case "d":
				cmd := m.toggleProvider(session.ProviderClaude)
				return m, cmd
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() tea.View {
	currentMode = m.mode
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		headerView(m),
		columnHeader(m.width),
		m.list.View(),
		detailView(m),
	)
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func (m *model) resizeList() {
	detailHeight := 7
	if m.height < 22 {
		detailHeight = 5
	}
	listHeight := max(5, m.height-detailHeight-4)
	m.list.SetSize(m.width, listHeight)
}

func (m *model) toggleProvider(provider session.Provider) tea.Cmd {
	if !providerExists(m.allRows, provider) {
		return m.list.NewStatusMessage(fmt.Sprintf("no %s sessions", provider))
	}
	if m.providers[provider] && enabledProviderCount(m.providers) == 1 {
		return m.list.NewStatusMessage("at least one provider stays enabled")
	}

	m.providers[provider] = !m.providers[provider]
	items := itemsFromRows(filterRows(m.allRows, m.providers))
	cmd := m.list.SetItems(items)
	m.list.ResetSelected()
	return tea.Batch(cmd, m.list.NewStatusMessage("providers: "+providerLabel(m.providers)))
}

func Pick(rows []session.Row) (*session.Row, error) {
	program := tea.NewProgram(newModel(rows, firstMessage))
	finalModel, err := program.Run()
	if err != nil {
		return nil, err
	}
	m, ok := finalModel.(model)
	if !ok {
		return nil, nil
	}
	return m.selected, nil
}

func PrintTable(w io.Writer, rows []session.Row) {
	width := 120
	providerWidth := 7
	dateWidth := 16
	cwdWidth := 38
	previewWidth := width - providerWidth - dateWidth - cwdWidth - 8

	_, _ = fmt.Fprintf(w, "%-*s %-*s %-*s %s\n", providerWidth, "SRC", dateWidth, "LAST", cwdWidth, "CWD", "PREVIEW")
	for _, row := range rows {
		_, _ = fmt.Fprintf(
			w,
			"%-*s %-*s %-*s %s\n",
			providerWidth,
			string(row.Provider),
			dateWidth,
			localTime(row.LastAt),
			cwdWidth,
			truncateMiddle(row.CWD, cwdWidth),
			truncateCells(previewFor(row, firstMessage), previewWidth),
		)
	}
}

func headerView(m model) string {
	title := titleStyle.Render("showcodex")
	stats := mutedStyle.Render(fmt.Sprintf("%d sessions  providers: %s  view: %s", len(m.list.Items()), providerLabel(m.providers), modeLabel(m.mode)))
	help := mutedStyle.Render("↑/↓ j/k move  / search  c codex  d claude  f first  l last  b both  enter resume  q quit")
	return lipgloss.JoinVertical(lipgloss.Left, lipgloss.JoinHorizontal(lipgloss.Top, title, "  ", stats), help)
}

func columnHeader(width int) string {
	providerWidth := 7
	dateWidth := 16
	cwdWidth := clamp(width/3, 22, 46)
	return headerStyle.Render(fmt.Sprintf("%-*s %-*s %-*s %s", providerWidth, "SRC", dateWidth, "LAST", cwdWidth, "CWD", "USER MESSAGE"))
}

func detailView(m model) string {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return detailStyle.Render("No session selected.")
	}
	row := selected.row
	width := max(40, m.width-4)
	command := strings.Join(row.ResumeCommand(), " ")
	lines := []string{
		labelStyle.Render("provider ") + string(row.Provider),
		labelStyle.Render("session  ") + row.ID,
		labelStyle.Render("cwd      ") + truncateCells(row.CWD, width-9),
		labelStyle.Render("first    ") + truncateCells(emptyFallback(row.FirstUser), width-9),
		labelStyle.Render("last     ") + truncateCells(emptyFallback(bestLast(row)), width-9),
		labelStyle.Render("command  ") + command,
		labelStyle.Render("file     ") + truncateMiddle(row.File, width-9),
	}
	if m.height < 22 {
		lines = lines[:5]
	}
	return detailStyle.Width(max(20, m.width-2)).Render(strings.Join(lines, "\n"))
}

func previewFor(row session.Row, mode previewMode) string {
	first := emptyFallback(row.FirstUser)
	last := bestLast(row)
	switch mode {
	case lastMessage:
		return emptyFallback(last)
	case bothMessages:
		if row.FirstUser != "" && row.LastUser != "" && row.FirstUser != row.LastUser {
			return row.FirstUser + " | " + row.LastUser
		}
	}
	return first
}

func bestLast(row session.Row) string {
	if row.LastUser != "" {
		return row.LastUser
	}
	return row.FirstUser
}

func itemsFromRows(rows []session.Row) []list.Item {
	items := make([]list.Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, item{row: row})
	}
	return items
}

func defaultProviderFilter(rows []session.Row) map[session.Provider]bool {
	providers := map[session.Provider]bool{}
	for _, row := range rows {
		providers[row.Provider] = true
	}
	return providers
}

func filterRows(rows []session.Row, providers map[session.Provider]bool) []session.Row {
	filtered := make([]session.Row, 0, len(rows))
	for _, row := range rows {
		if providers[row.Provider] {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func providerExists(rows []session.Row, provider session.Provider) bool {
	for _, row := range rows {
		if row.Provider == provider {
			return true
		}
	}
	return false
}

func enabledProviderCount(providers map[session.Provider]bool) int {
	count := 0
	for _, enabled := range providers {
		if enabled {
			count++
		}
	}
	return count
}

func providerLabel(providers map[session.Provider]bool) string {
	var values []string
	for _, provider := range []session.Provider{session.ProviderCodex, session.ProviderClaude} {
		if providers[provider] {
			values = append(values, string(provider))
		}
	}
	return strings.Join(values, "+")
}

func emptyFallback(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}

func modeLabel(mode previewMode) string {
	switch mode {
	case lastMessage:
		return "last user"
	case bothMessages:
		return "first + last user"
	default:
		return "first user"
	}
}

func localTime(value time.Time) string {
	return value.Local().Format("2006-01-02 15:04")
}

func truncateCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return string([]rune(value)[:min(len([]rune(value)), width)])
	}

	var builder strings.Builder
	for _, r := range value {
		next := builder.String() + string(r)
		if lipgloss.Width(next)+3 > width {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String() + "..."
}

func truncateMiddle(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return truncateCells(value, width)
	}
	clean := filepath.Clean(value)
	right := min(width/2, lipgloss.Width(clean))
	suffix := rightCells(clean, right)
	prefixWidth := width - lipgloss.Width(suffix) - 3
	return truncateCells(clean, prefixWidth) + "..." + suffix
}

func rightCells(value string, width int) string {
	runes := []rune(value)
	for i := len(runes); i >= 0; i-- {
		candidate := string(runes[i:])
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return ""
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7DCAFF")).
			Padding(0, 1)

	mutedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8B949E"))

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C9D1D9")).
			Background(lipgloss.Color("#30363D")).
			Bold(true).
			Padding(0, 1)

	rowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D0D7DE"))

	selectedRowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#0D1117")).
				Background(lipgloss.Color("#A5D6FF")).
				Bold(true)

	detailStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3FB950")).
			Padding(0, 1).
			MarginTop(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA657")).
			Bold(true)
)

func init() {
	if os.Getenv("NO_COLOR") != "" {
		titleStyle = lipgloss.NewStyle().Bold(true)
		mutedStyle = lipgloss.NewStyle()
		headerStyle = lipgloss.NewStyle().Bold(true)
		rowStyle = lipgloss.NewStyle()
		selectedRowStyle = lipgloss.NewStyle().Reverse(true).Bold(true)
		detailStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).MarginTop(1)
		labelStyle = lipgloss.NewStyle().Bold(true)
	}
}
