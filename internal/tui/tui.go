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

	"github.com/aytzey/showagent/internal/session"
)

type previewMode int

const (
	firstMessage previewMode = iota
	lastMessage
	bothMessages
)

const (
	tableProviderWidth = 8
	tableDateWidth     = 16
	tableGapWidth      = 3

	headerHeight       = 2
	columnHeaderHeight = 1
	bottomSafetyRows   = 1
	detailLabelWidth   = 9
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
	_, _ = fmt.Fprint(w, renderTableRow(
		width,
		it.row,
		currentMode,
		index == m.Index(),
	))
}

var currentMode = firstMessage

type model struct {
	list      list.Model
	allRows   []session.Row
	providers map[session.Provider]bool
	mode      previewMode
	dangerous bool
	selected  *session.Row
	width     int
	height    int
}

func newModel(rows []session.Row, mode previewMode) model {
	providers := defaultProviderFilter(rows)
	items := itemsFromRows(filterRows(rows, providers))

	delegate := itemDelegate{}
	l := list.New(items, delegate, 100, 24)
	l.Title = "showagent"
	l.SetStatusBarItemName("session", "sessions")
	l.SetFilteringEnabled(true)
	l.SetShowFilter(true)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.DisableQuitKeybindings()
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "first")),
			key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "last")),
			key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "both")),
			key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "codex")),
			key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "claude")),
			key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yolo")),
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
			case "y":
				m.dangerous = !m.dangerous
				return m, m.list.NewStatusMessage("resume mode: " + resumeModeLabel(m.dangerous))
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) View() tea.View {
	currentMode = m.mode
	parts := []string{
		headerView(m),
		columnHeader(m.width),
		m.list.View(),
	}
	if detail := detailView(m); detail != "" {
		parts = append(parts, detail)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func (m *model) resizeList() {
	reserved := headerHeight + columnHeaderHeight + detailHeight(m.height) + bottomSafetyRows
	listHeight := max(5, m.height-reserved)
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

func Pick(rows []session.Row) (*session.Row, session.ResumeOptions, error) {
	program := tea.NewProgram(newModel(rows, firstMessage))
	finalModel, err := program.Run()
	if err != nil {
		return nil, session.ResumeOptions{}, err
	}
	m, ok := finalModel.(model)
	if !ok {
		return nil, session.ResumeOptions{}, nil
	}
	return m.selected, session.ResumeOptions{Dangerous: m.dangerous}, nil
}

func PrintTable(w io.Writer, rows []session.Row) {
	width := 120

	_, _ = fmt.Fprintln(w, tableLine(width, "AGENT", "UPDATED", "WORKSPACE", "PREVIEW"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(w, tableLine(
			width,
			string(row.Provider),
			localTime(row.LastAt),
			row.CWD,
			previewFor(row, firstMessage),
		))
	}
}

func headerView(m model) string {
	title := titleStyle.Render("showagent")
	stats := mutedStyle.Render(fmt.Sprintf("%d sessions  providers: %s  view: %s  resume: %s", len(m.list.Items()), providerLabel(m.providers), modeLabel(m.mode), resumeModeLabel(m.dangerous)))
	help := mutedStyle.Render("↑/↓ j/k move  / search  c codex  d claude  y yolo  f first  l last  b both  enter resume  q quit")
	return lipgloss.JoinVertical(lipgloss.Left, lipgloss.JoinHorizontal(lipgloss.Top, title, "  ", stats), help)
}

func columnHeader(width int) string {
	return headerStyle.Width(width).Render(tableLine(width, "AGENT", "UPDATED", "WORKSPACE", "USER MESSAGE"))
}

func detailView(m model) string {
	lineCount := detailLineCount(m.height)
	if lineCount == 0 {
		return ""
	}

	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return detailStyle.Width(max(20, m.width)).Render("No session selected.")
	}
	row := selected.row
	width := max(40, m.width)
	frameWidth, _ := detailStyle.GetFrameSize()
	valueWidth := max(8, width-frameWidth-detailLabelWidth)
	command := strings.Join(row.ResumeCommand(session.ResumeOptions{Dangerous: m.dangerous}), " ")
	lines := []string{
		labelStyle.Render("provider ") + string(row.Provider),
		labelStyle.Render("session  ") + row.ID,
		labelStyle.Render("resume   ") + resumeModeLabel(m.dangerous),
		labelStyle.Render("cwd      ") + truncateCells(row.CWD, valueWidth),
		labelStyle.Render("first    ") + truncateCells(emptyFallback(row.FirstUser), valueWidth),
		labelStyle.Render("last     ") + truncateCells(emptyFallback(bestLast(row)), valueWidth),
		labelStyle.Render("command  ") + command,
		labelStyle.Render("file     ") + truncateMiddle(row.File, valueWidth),
	}
	lines = lines[:min(lineCount, len(lines))]
	for i := range lines {
		lines[i] = truncateCells(lines[i], max(1, width-frameWidth))
	}
	return detailStyle.Width(width).Render(strings.Join(lines, "\n"))
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

func renderTableRow(width int, row session.Row, mode previewMode, selected bool) string {
	provider := string(row.Provider)
	if selected {
		providerWidth, _, _, _ := tableWidths(width)
		return selectedRowStyle.Width(width).Render(tableLine(
			width,
			providerPlainLabel(provider, providerWidth),
			localTime(row.LastAt),
			row.CWD,
			previewFor(row, mode),
		))
	}

	providerWidth, dateWidth, cwdWidth, previewWidth := tableWidths(width)
	parts := []string{
		providerBadge(provider, providerWidth),
		dateCellStyle.Width(dateWidth).Render(truncateCells(localTime(row.LastAt), dateWidth)),
		renderWorkspaceCell(row.CWD, cwdWidth),
		messageCellStyle.Width(previewWidth).Render(truncateCells(previewFor(row, mode), previewWidth)),
	}
	return padCells(strings.Join(parts, " "), width)
}

func providerBadge(provider string, width int) string {
	label := providerPlainLabel(provider, width)
	switch session.Provider(provider) {
	case session.ProviderCodex:
		return codexBadgeStyle.Width(width).Render(label)
	case session.ProviderClaude:
		return claudeBadgeStyle.Width(width).Render(label)
	default:
		return providerBadgeStyle.Width(width).Render(label)
	}
}

func providerPlainLabel(provider string, width int) string {
	return centerCell(" "+strings.ToUpper(provider)+" ", width)
}

func renderWorkspaceCell(cwd string, width int) string {
	value := truncateMiddle(cwd, width)
	base := filepath.Base(filepath.Clean(cwd))
	index := strings.LastIndex(value, base)
	if index <= 0 {
		return workspaceStyle.Width(width).Render(value)
	}
	prefix := value[:index]
	suffix := value[index:]
	rendered := workspaceParentStyle.Render(prefix) + workspaceBaseStyle.Render(suffix)
	return padCells(rendered, width)
}

func tableLine(width int, provider, date, cwd, preview string) string {
	providerWidth, dateWidth, cwdWidth, previewWidth := tableWidths(width)
	line := fmt.Sprintf(
		"%-*s %-*s %-*s %s",
		providerWidth,
		truncateCells(provider, providerWidth),
		dateWidth,
		truncateCells(date, dateWidth),
		cwdWidth,
		truncateMiddle(cwd, cwdWidth),
		truncateCells(preview, previewWidth),
	)
	return padCells(truncateCells(line, width), width)
}

func centerCell(value string, width int) string {
	value = truncateCells(value, width)
	valueWidth := lipgloss.Width(value)
	if valueWidth >= width {
		return value
	}
	left := (width - valueWidth) / 2
	right := width - valueWidth - left
	return strings.Repeat(" ", left) + value + strings.Repeat(" ", right)
}

func tableWidths(width int) (int, int, int, int) {
	if width <= tableProviderWidth+tableDateWidth+tableGapWidth+10 {
		providerWidth := min(tableProviderWidth, max(3, width/5))
		dateWidth := min(tableDateWidth, max(5, width/4))
		cwdWidth := max(5, width-providerWidth-dateWidth-tableGapWidth-5)
		previewWidth := max(1, width-providerWidth-dateWidth-cwdWidth-tableGapWidth)
		return providerWidth, dateWidth, cwdWidth, previewWidth
	}

	providerWidth := tableProviderWidth
	dateWidth := tableDateWidth
	cwdWidth := clamp(width/3, 22, 46)
	previewWidth := max(1, width-providerWidth-dateWidth-cwdWidth-tableGapWidth)
	return providerWidth, dateWidth, cwdWidth, previewWidth
}

func padCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	cellWidth := lipgloss.Width(value)
	if cellWidth >= width {
		return value
	}
	return value + strings.Repeat(" ", width-cellWidth)
}

func detailLineCount(height int) int {
	switch {
	case height < 16:
		return 0
	case height < 22:
		return 3
	case height < 30:
		return 5
	case height < 38:
		return 6
	default:
		return 7
	}
}

func detailHeight(height int) int {
	count := detailLineCount(height)
	if count == 0 {
		return 0
	}
	_, frameHeight := detailStyle.GetFrameSize()
	return count + frameHeight
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

func resumeModeLabel(dangerous bool) string {
	if dangerous {
		return "yolo"
	}
	return "normal"
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
			Bold(true)

	rowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D0D7DE"))

	selectedRowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#0D1117")).
				Background(lipgloss.Color("#A5D6FF")).
				Bold(true)

	providerBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#C9D1D9")).
				Background(lipgloss.Color("#30363D")).
				Bold(true)

	codexBadgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#1F6FEB")).
			Bold(true)

	claudeBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#0D1117")).
				Background(lipgloss.Color("#D2A8FF")).
				Bold(true)

	dateCellStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8B949E"))

	workspaceStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#D0D7DE"))

	workspaceParentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8B949E"))

	workspaceBaseStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E6EDF3")).
				Bold(true)

	messageCellStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#C9D1D9"))

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
		providerBadgeStyle = lipgloss.NewStyle().Bold(true)
		codexBadgeStyle = lipgloss.NewStyle().Bold(true)
		claudeBadgeStyle = lipgloss.NewStyle().Bold(true)
		dateCellStyle = lipgloss.NewStyle()
		workspaceStyle = lipgloss.NewStyle()
		workspaceParentStyle = lipgloss.NewStyle()
		workspaceBaseStyle = lipgloss.NewStyle().Bold(true)
		messageCellStyle = lipgloss.NewStyle()
		detailStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).MarginTop(1)
		labelStyle = lipgloss.NewStyle().Bold(true)
	}
}
