package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	list "charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
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
	columnHeaderHeight = 1
	bottomSafetyRows   = 0
	detailLabelWidth   = 11
)

// Selection is what Pick/Run hand back to the caller once the user chooses a
// session to resume.
type Selection struct {
	Row     session.Row
	Options session.ResumeOptions
}

type sessionMutation int

const (
	mutationConvert sessionMutation = iota
	mutationBranch
)

type sessionMutationMsg struct {
	kind sessionMutation
	row  session.Row
	err  error
}

type sessionsLoadedMsg struct {
	rows []session.Row
}

var handoffScopes = []session.HandoffOptions{
	{},
	{MaxTurns: 200},
	{MaxTurns: 100},
	{MaxTurns: 50},
	{MaxTurns: 20},
	{MaxTurns: 10},
}

// renderState is shared by the model and the list item delegate so the delegate
// can render rows without reaching for package-global state.
type renderState struct {
	mode  previewMode
	theme *theme
}

type item struct {
	row session.Row
}

func (i item) FilterValue() string { return i.row.FilterValue() }

type itemDelegate struct {
	state *renderState
}

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                            { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(item)
	if !ok {
		return
	}
	_, _ = fmt.Fprint(w, renderTableRow(d.state.theme, m.Width(), it.row, d.state.mode, index == m.Index()))
}

type model struct {
	list        list.Model
	spinner     spinner.Model
	help        help.Model
	keys        keyMap
	render      *renderState
	allRows     []session.Row
	providers   map[session.Provider]bool
	mode        previewMode
	dangerous   bool
	handoff     session.HandoffOptions
	selected    *Selection
	deleteArmed string
	busy        string
	loading     bool
	isDark      bool
	width       int
	height      int
}

func newModel(rows []session.Row, mode previewMode) model {
	return buildModel(rows, mode, false)
}

func newLoadingModel(mode previewMode) model {
	return buildModel(nil, mode, true)
}

func buildModel(rows []session.Row, mode previewMode, loading bool) model {
	render := &renderState{mode: mode, theme: newTheme(true)}
	providers := defaultProviderFilter(rows)
	items := itemsFromRows(filterRows(rows, providers))

	l := list.New(items, itemDelegate{state: render}, 80, 20)
	l.SetFilteringEnabled(true)
	l.SetShowFilter(true)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.DisableQuitKeybindings()

	// The default list keymap binds f/l/b/d/h/u to paging. Restrict paging to
	// the page keys so those letters are free for our preview/provider actions.
	km := list.DefaultKeyMap()
	km.NextPage = key.NewBinding(key.WithKeys("pgdown", "right"), key.WithHelp("pgdn", "next page"))
	km.PrevPage = key.NewBinding(key.WithKeys("pgup", "left"), key.WithHelp("pgup", "prev page"))
	l.KeyMap = km

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(render.theme.spinner))
	h := help.New()
	h.Styles = render.theme.help

	return model{
		list:      l,
		spinner:   sp,
		help:      h,
		keys:      defaultKeys(),
		render:    render,
		allRows:   rows,
		providers: providers,
		mode:      mode,
		loading:   loading,
		isDark:    true,
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
	if m.loading {
		cmds = append(cmds, m.spinner.Tick, loadSessionsCmd())
	}
	return tea.Batch(cmds...)
}

func loadSessionsCmd() tea.Cmd {
	return func() tea.Msg {
		return sessionsLoadedMsg{rows: session.Discover()}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeList()
		return m, nil
	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		m.applyTheme()
		return m, nil
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case sessionsLoadedMsg:
		m.loading = false
		m.allRows = msg.rows
		m.providers = defaultProviderFilter(msg.rows)
		cmd := m.list.SetItems(itemsFromRows(filterRows(msg.rows, m.providers)))
		m.resizeList()
		return m, cmd
	case sessionMutationMsg:
		return m.applyMutation(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.reconcileDeleteArm()
	return m, cmd
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.loading {
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		return m, nil
	}

	// While typing a filter, every key belongs to the list.
	if m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		m.reconcileDeleteArm()
		return m, cmd
	}

	// esc clears an applied filter before it is allowed to quit the app.
	if msg.String() == "esc" && m.list.IsFiltered() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		m.reconcileDeleteArm()
		return m, cmd
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = !m.help.ShowAll
		m.resizeList()
		return m, nil
	}

	if m.busy != "" {
		return m, m.list.NewStatusMessage(m.busy + " in progress…")
	}

	switch {
	case key.Matches(msg, m.keys.Resume):
		return m.selectResume()
	case key.Matches(msg, m.keys.Convert):
		return m.startMutation(m.convertSelected(), "conversion", "converting session…")
	case key.Matches(msg, m.keys.Branch):
		return m.startMutation(m.branchSelected(), "branch", "creating session branch…")
	case key.Matches(msg, m.keys.Delete):
		return m, m.deleteSelected()
	case key.Matches(msg, m.keys.Yolo):
		m.dangerous = !m.dangerous
		return m, m.list.NewStatusMessage("resume mode: " + resumeModeLabel(m.dangerous))
	case key.Matches(msg, m.keys.Scope):
		m.handoff = nextHandoffScope(m.handoff)
		return m, m.list.NewStatusMessage("hand-off scope: " + m.handoff.Label())
	case key.Matches(msg, m.keys.Codex):
		return m, m.toggleProvider(session.ProviderCodex)
	case key.Matches(msg, m.keys.Claude):
		return m, m.toggleProvider(session.ProviderClaude)
	}

	// Preview keys are handled individually so each selects a distinct mode.
	switch msg.String() {
	case "f":
		m.setMode(firstMessage)
		return m, m.list.NewStatusMessage("preview: first user message")
	case "l":
		m.setMode(lastMessage)
		return m, m.list.NewStatusMessage("preview: latest user message")
	case "b":
		m.setMode(bothMessages)
		return m, m.list.NewStatusMessage("preview: first + latest")
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.reconcileDeleteArm()
	return m, cmd
}

func (m model) applyMutation(msg sessionMutationMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	m.deleteArmed = ""
	if msg.err != nil {
		return m, m.list.NewStatusMessage("action failed: " + msg.err.Error())
	}

	m.providers[msg.row.Provider] = true
	m.allRows = upsertAndSortRows(m.allRows, msg.row)
	filtered := filterRows(m.allRows, m.providers)
	m.list.ResetFilter()
	cmd := m.list.SetItems(itemsFromRows(filtered))
	if index := indexOfRow(filtered, msg.row); index >= 0 {
		m.list.Select(index)
	}

	status := "converted to " + string(msg.row.Provider) + " · press enter to resume"
	if msg.kind == mutationBranch {
		status = "branched " + string(msg.row.Provider) + " session · press enter to resume"
	}
	return m, tea.Batch(cmd, m.list.NewStatusMessage(status))
}

func (m model) startMutation(cmd tea.Cmd, busy, status string) (tea.Model, tea.Cmd) {
	if cmd == nil {
		return m, m.list.NewStatusMessage("no session selected")
	}
	m.busy = busy
	return m, tea.Batch(cmd, m.list.NewStatusMessage(status))
}

func (m *model) setMode(mode previewMode) {
	m.mode = mode
	m.render.mode = mode
}

func (m *model) applyTheme() {
	t := newTheme(m.isDark)
	m.render.theme = t
	m.spinner.Style = t.spinner
	m.help.Styles = t.help
}

// reconcileDeleteArm clears a pending delete confirmation whenever the cursor
// moves off the armed row, so the two-press safety can never be bypassed by
// navigating away and back.
func (m *model) reconcileDeleteArm() {
	if m.deleteArmed == "" {
		return
	}
	if sel, ok := m.list.SelectedItem().(item); !ok || rowKey(sel.row) != m.deleteArmed {
		m.deleteArmed = ""
	}
}

func (m *model) resizeList() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.help.SetWidth(m.width)
	chrome := lipgloss.Height(m.headerView()) + columnHeaderHeight + m.detailHeight() + lipgloss.Height(m.helpView()) + bottomSafetyRows
	listHeight := max(3, m.height-chrome)
	m.list.SetSize(m.width, listHeight)
}

func (m *model) toggleProvider(provider session.Provider) tea.Cmd {
	if !providerExists(m.allRows, provider) {
		return m.list.NewStatusMessage(fmt.Sprintf("no %s sessions found", provider))
	}
	if m.providers[provider] && enabledProviderCount(m.providers) == 1 {
		return m.list.NewStatusMessage("keep at least one provider visible")
	}

	m.providers[provider] = !m.providers[provider]
	cmd := m.list.SetItems(itemsFromRows(filterRows(m.allRows, m.providers)))
	m.list.ResetSelected()
	return tea.Batch(cmd, m.list.NewStatusMessage("providers: "+providerLabel(m.providers)))
}

func (m model) selectResume() (tea.Model, tea.Cmd) {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, m.list.NewStatusMessage("no session selected")
	}
	m.selected = &Selection{
		Row:     selected.row,
		Options: session.ResumeOptions{Dangerous: m.dangerous},
	}
	return m, tea.Quit
}

func (m *model) convertSelected() tea.Cmd {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return nil
	}
	row := selected.row
	target := session.OtherProvider(row.Provider)
	options := m.handoff
	return func() tea.Msg {
		converted, err := session.Convert(row, target, options)
		return sessionMutationMsg{kind: mutationConvert, row: converted, err: err}
	}
}

func (m *model) branchSelected() tea.Cmd {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return nil
	}
	row := selected.row
	return func() tea.Msg {
		branched, err := session.Branch(row)
		return sessionMutationMsg{kind: mutationBranch, row: branched, err: err}
	}
}

func (m *model) deleteSelected() tea.Cmd {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return m.list.NewStatusMessage("no session selected")
	}
	key := rowKey(selected.row)
	if m.deleteArmed != key {
		m.deleteArmed = key
		return m.list.NewStatusMessage("press delete again to permanently remove this " + string(selected.row.Provider) + " session")
	}

	if err := session.Delete(selected.row); err != nil {
		m.deleteArmed = ""
		return m.list.NewStatusMessage("delete failed: " + err.Error())
	}

	m.deleteArmed = ""
	m.allRows = removeRow(m.allRows, selected.row)
	if !providerExists(m.allRows, selected.row.Provider) {
		delete(m.providers, selected.row.Provider)
	}
	if enabledProviderCount(m.providers) == 0 {
		m.providers = defaultProviderFilter(m.allRows)
	}
	cmd := m.list.SetItems(itemsFromRows(filterRows(m.allRows, m.providers)))
	m.list.ResetSelected()
	return tea.Batch(cmd, m.list.NewStatusMessage("deleted "+string(selected.row.Provider)+" session"))
}

func (m model) View() tea.View {
	var content string
	switch {
	case m.loading:
		content = m.loadingView()
	case len(m.allRows) == 0:
		content = m.emptyView()
	default:
		content = m.browseView()
	}
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func (m model) browseView() string {
	parts := []string{
		m.headerView(),
		columnHeader(m.render.theme, m.width, m.mode),
		m.list.View(),
		m.detailView(),
		m.helpView(),
	}
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, out...)
}

func (m model) headerView() string {
	th := m.render.theme
	title := th.title.Render("showagent")
	return lipgloss.JoinHorizontal(lipgloss.Left, title, "  ", m.statsLine())
}

func (m model) statsLine() string {
	th := m.render.theme
	parts := []string{
		th.muted.Render(fmt.Sprintf("%d/%d sessions", len(m.list.VisibleItems()), len(m.allRows))),
		th.chip.Render(providerLabel(m.providers)),
		th.muted.Render("preview " + modeShort(m.mode)),
	}
	if m.dangerous {
		parts = append(parts, th.yoloChip.Render("YOLO"))
	}
	return strings.Join(parts, "  ")
}

func (m model) helpView() string {
	return m.help.View(m.keys)
}

func (m model) detailView() string {
	count := m.detailLineCount()
	if count == 0 {
		return ""
	}
	th := m.render.theme
	width := max(40, m.width)
	frameW, _ := th.detail.GetFrameSize()
	innerW := max(10, width-frameW)
	valueW := max(8, innerW-detailLabelWidth)

	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		msg := "No session selected."
		if m.list.IsFiltered() {
			msg = "No session matches this search · press esc to clear."
		}
		return th.detail.Width(innerW).Render(th.muted.Render(truncateCells(msg, innerW)))
	}
	row := selected.row

	var lines []string
	if m.deleteArmed == rowKey(row) {
		lines = append(lines, th.deleteBanner.Render("⚠ press delete again to permanently remove this session"))
	}
	lines = append(lines,
		th.label.Render(padLabel("provider"))+m.providerWord(row.Provider),
		th.label.Render(padLabel("session"))+row.ID,
		th.label.Render(padLabel("workspace"))+truncateMiddle(row.CWD, valueW),
		th.label.Render(padLabel("updated"))+localTime(row.LastAt),
		th.label.Render(padLabel("first"))+truncateCells(emptyFallback(row.FirstUser), valueW),
		th.label.Render(padLabel("latest"))+truncateCells(emptyFallback(bestLast(row)), valueW),
		th.hint.Render(m.resumeHint(row)),
		th.hint.Render(m.handoffHint(row)),
	)
	if len(lines) > count {
		lines = lines[:count]
	}
	for i := range lines {
		lines[i] = truncateCells(lines[i], innerW)
	}
	return th.detail.Width(innerW).Render(strings.Join(lines, "\n"))
}

func (m model) providerWord(p session.Provider) string {
	return providerBadge(m.render.theme, string(p), len(string(p))+2)
}

func (m model) resumeHint(row session.Row) string {
	if !m.dangerous {
		return fmt.Sprintf("enter → resume with %s (normal)", row.Provider)
	}
	if row.Provider == session.ProviderClaude {
		return fmt.Sprintf("enter → resume with %s · yolo: skips permission prompts", row.Provider)
	}
	return fmt.Sprintf("enter → resume with %s · yolo: bypasses approvals & sandbox", row.Provider)
}

func (m model) handoffHint(row session.Row) string {
	other := session.OtherProvider(row.Provider)
	return fmt.Sprintf("x → hand off to %s (%s) · n → branch · t → scope", other, m.handoff.Label())
}

func (m model) detailLineCount() int {
	switch {
	case m.height < 14:
		return 0
	case m.height < 20:
		return 4
	case m.height < 28:
		return 6
	default:
		return 8
	}
}

func (m model) detailHeight() int {
	count := m.detailLineCount()
	if count == 0 {
		return 0
	}
	_, frameH := m.render.theme.detail.GetFrameSize()
	return count + frameH
}

func (m model) loadingView() string {
	th := m.render.theme
	body := th.title.Render("showagent") + "\n\n" + m.spinner.View() + " " + th.muted.Render("Scanning Codex and Claude sessions…")
	if m.width <= 0 || m.height <= 0 {
		return body
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}

func (m model) emptyView() string {
	th := m.render.theme
	body := th.title.Render("showagent") + "\n\n" +
		th.muted.Render("No Codex or Claude sessions found.") + "\n" +
		th.muted.Render("Looked in ~/.codex/sessions and ~/.claude/projects.") + "\n\n" +
		th.hint.Render("Press q to quit.")
	if m.width <= 0 || m.height <= 0 {
		return body
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}

// Pick runs the picker over an already-discovered set of rows (used in tests).
func Pick(rows []session.Row) (*Selection, error) {
	return runProgram(newModel(rows, firstMessage))
}

// Run launches the interactive picker, discovering sessions asynchronously with
// a loading spinner so startup never blocks on a blank screen.
func Run() (*Selection, error) {
	return runProgram(newLoadingModel(firstMessage))
}

func runProgram(m model) (*Selection, error) {
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, err
	}
	if fm, ok := final.(model); ok {
		return fm.selected, nil
	}
	return nil, nil
}

func padLabel(s string) string {
	return fmt.Sprintf("%-*s", detailLabelWidth, s)
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

func removeRow(rows []session.Row, removed session.Row) []session.Row {
	filtered := make([]session.Row, 0, len(rows))
	removedKey := rowKey(removed)
	for _, row := range rows {
		if rowKey(row) != removedKey {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func upsertAndSortRows(rows []session.Row, row session.Row) []session.Row {
	next := removeRow(rows, row)
	next = append(next, row)
	sort.SliceStable(next, func(i, j int) bool {
		return next[i].LastAt.After(next[j].LastAt)
	})
	return next
}

func indexOfRow(rows []session.Row, wanted session.Row) int {
	wantedKey := rowKey(wanted)
	for index, row := range rows {
		if rowKey(row) == wantedKey {
			return index
		}
	}
	return -1
}

func rowKey(row session.Row) string {
	return string(row.Provider) + "\x00" + row.ID + "\x00" + row.File
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
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, "+")
}

func nextHandoffScope(current session.HandoffOptions) session.HandoffOptions {
	for index, scope := range handoffScopes {
		if scope.MaxTurns == current.MaxTurns {
			return handoffScopes[(index+1)%len(handoffScopes)]
		}
	}
	return handoffScopes[0]
}

func resumeModeLabel(dangerous bool) string {
	if dangerous {
		return "yolo"
	}
	return "normal"
}
