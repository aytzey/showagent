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

// Action is what the caller should do with a Selection.
type Action int

const (
	// ActionResume resumes the session in its own provider.
	ActionResume Action = iota
	// ActionCompound runs a compound-engineering pass in the chosen Agent.
	ActionCompound
)

// Selection is what Pick/Run hand back to the caller once the user chooses a
// session to act on.
type Selection struct {
	Row     session.Row
	Options session.ResumeOptions
	Action  Action
	// Agent is the provider chosen to run a compound pass (ActionCompound).
	Agent session.Provider
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

type conversionPreviewMsg struct {
	row     session.Row
	target  session.Provider
	options session.HandoffOptions
	preview session.ConversionPreview
	err     error
}

type pendingConversion struct {
	rowKey  string
	target  session.Provider
	options session.HandoffOptions
	preview session.ConversionPreview
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

// headerItem is a selectable group header (one per workspace folder). It
// returns an empty filter value so it is hidden automatically while searching,
// which turns the grouped view into a flat result list during a search.
type headerItem struct {
	path      string
	count     int
	collapsed bool
}

func (h headerItem) FilterValue() string { return "" }

type itemDelegate struct {
	state *renderState
}

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                            { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	switch it := listItem.(type) {
	case headerItem:
		_, _ = fmt.Fprint(w, renderGroupHeader(d.state.theme, m.Width(), it, index == m.Index()))
	case item:
		_, _ = fmt.Fprint(w, renderTableRow(d.state.theme, m.Width(), it.row, d.state.mode, index == m.Index()))
	}
}

// groupedItems lays out rows under one header per workspace folder. Rows arrive
// globally newest-first, so the first time a folder appears marks that group's
// newest session: groups come out ordered newest-first, and rows stay
// newest-first within each group.
func groupedItems(rows []session.Row, collapsed map[string]bool) []list.Item {
	order := make([]string, 0)
	groups := make(map[string][]session.Row)
	for _, row := range rows {
		if _, seen := groups[row.CWD]; !seen {
			order = append(order, row.CWD)
		}
		groups[row.CWD] = append(groups[row.CWD], row)
	}

	items := make([]list.Item, 0, len(rows)+len(order))
	for _, cwd := range order {
		group := groups[cwd]
		isCollapsed := collapsed[cwd]
		items = append(items, headerItem{path: cwd, count: len(group), collapsed: isCollapsed})
		if !isCollapsed {
			for _, row := range group {
				items = append(items, item{row: row})
			}
		}
	}
	return items
}

func sessionCount(items []list.Item) int {
	count := 0
	for _, it := range items {
		if _, ok := it.(item); ok {
			count++
		}
	}
	return count
}

func selectFirstSession(l *list.Model) {
	for index, it := range l.VisibleItems() {
		if _, ok := it.(item); ok {
			l.Select(index)
			return
		}
	}
}

func selectRowItem(l *list.Model, target session.Row) {
	wanted := rowKey(target)
	for index, it := range l.VisibleItems() {
		if si, ok := it.(item); ok && rowKey(si.row) == wanted {
			l.Select(index)
			return
		}
	}
	selectFirstSession(l)
}

func selectHeaderItem(l *list.Model, path string) {
	for index, it := range l.VisibleItems() {
		if header, ok := it.(headerItem); ok && header.path == path {
			l.Select(index)
			return
		}
	}
}

type model struct {
	list             list.Model
	spinner          spinner.Model
	help             help.Model
	keys             keyMap
	render           *renderState
	allRows          []session.Row
	providers        map[session.Provider]bool
	collapsedGroups  map[string]bool
	mode             previewMode
	dangerous        bool
	handoff          session.HandoffOptions
	handoffTarget    session.Provider
	selected         *Selection
	deleteArmed      string
	pendingConvert   *pendingConversion
	busy             string
	loading          bool
	rescanning       bool
	rescanRow        *session.Row
	compoundChoosing bool
	compoundRow      *session.Row
	compoundNotice   string
	isDark           bool
	width            int
	height           int
}

func newModel(rows []session.Row) model {
	return buildModel(rows, firstMessage, false)
}

func newLoadingModel(mode previewMode) model {
	return buildModel(nil, mode, true)
}

func buildModel(rows []session.Row, mode previewMode, loading bool) model {
	render := &renderState{mode: mode, theme: newTheme(true)}
	providers := defaultProviderFilter(rows)
	collapsedGroups := map[string]bool{}
	items := groupedItems(filterRows(rows, providers), collapsedGroups)

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

	selectFirstSession(&l)

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(render.theme.spinner))
	h := help.New()
	h.Styles = render.theme.help

	return model{
		list:            l,
		spinner:         sp,
		help:            h,
		keys:            defaultKeys(),
		render:          render,
		allRows:         rows,
		providers:       providers,
		collapsedGroups: collapsedGroups,
		mode:            mode,
		loading:         loading,
		isDark:          true,
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
		wasRescan := m.rescanning
		m.loading = false
		m.rescanning = false
		m.pendingConvert = nil
		m.allRows = msg.rows
		if wasRescan {
			// A rescan keeps the user's provider filter and search; only the
			// initial load starts from the default all-providers view.
			m.providers = mergeProviderFilter(m.providers, msg.rows)
		} else {
			m.providers = defaultProviderFilter(msg.rows)
		}
		m.pruneCollapsedGroups()
		cmd := m.list.SetItems(m.currentItems())
		if wasRescan && m.rescanRow != nil {
			selectRowItem(&m.list, *m.rescanRow)
		} else {
			selectFirstSession(&m.list)
		}
		m.rescanRow = nil
		m.resizeList()
		if wasRescan {
			return m, tea.Batch(cmd, m.list.NewStatusMessage(fmt.Sprintf("rescan done: %d sessions", len(msg.rows))))
		}
		return m, cmd
	case sessionMutationMsg:
		return m.applyMutation(msg)
	case conversionPreviewMsg:
		return m.applyConversionPreview(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.reconcileDeleteArm()
	m.reconcilePendingConversion()
	return m, cmd
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.loading {
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		return m, nil
	}

	// The compound chooser is a modal: pick the agent by digit, or cancel.
	// Digits follow the registry order of compound-capable providers.
	if m.compoundChoosing {
		keyStr := msg.String()
		if agents := session.CompoundAgents(); len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
			if index := int(keyStr[0] - '1'); index < len(agents) {
				return m.startCompound(agents[index])
			}
		}
		switch keyStr {
		case "esc", "q", "ctrl+c":
			m.compoundChoosing = false
			m.compoundRow = nil
			m.compoundNotice = ""
			return m, nil
		}
		return m, nil
	}

	// While typing a filter, every key belongs to the list.
	if m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		m.reconcileDeleteArm()
		m.reconcilePendingConversion()
		return m, cmd
	}

	// esc peels back one layer of UI state (search, help overlay, armed
	// delete) and otherwise does nothing: it never quits the picker.
	if msg.String() == "esc" {
		if m.list.IsFiltered() {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			rebuild := m.rebuildList()
			m.reconcileDeleteArm()
			return m, tea.Batch(cmd, rebuild)
		}
		if m.help.ShowAll {
			m.help.ShowAll = false
			m.resizeList()
			return m, nil
		}
		if m.deleteArmed != "" {
			m.deleteArmed = ""
			return m, m.list.NewStatusMessage("delete cancelled")
		}
		if m.pendingConvert != nil {
			m.pendingConvert = nil
			return m, m.list.NewStatusMessage("conversion preview cleared")
		}
		return m, nil
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
		if header, ok := m.list.SelectedItem().(headerItem); ok {
			return m.toggleGroup(header.path)
		}
		return m.selectResume()
	case key.Matches(msg, m.keys.Collapse):
		if header, ok := m.list.SelectedItem().(headerItem); ok {
			return m.toggleGroup(header.path)
		}
		return m, m.list.NewStatusMessage("select a category header to collapse")
	case key.Matches(msg, m.keys.Compound):
		selected, ok := m.list.SelectedItem().(item)
		if !ok {
			return m, m.list.NewStatusMessage("no session selected")
		}
		// Capture the row now so the choice acts on what the user saw, even if
		// the list changes while the chooser is open.
		row := selected.row
		m.compoundRow = &row
		m.compoundChoosing = true
		m.compoundNotice = ""
		return m, nil
	case key.Matches(msg, m.keys.Target):
		m.pendingConvert = nil
		return m.cycleHandoffTarget()
	case key.Matches(msg, m.keys.Convert):
		return m.startConvert()
	case key.Matches(msg, m.keys.Branch):
		m.pendingConvert = nil
		return m.startMutation(m.branchSelected(), "branch", "creating session branch…")
	case key.Matches(msg, m.keys.Delete):
		return m, m.deleteSelected()
	case key.Matches(msg, m.keys.Yolo):
		m.dangerous = !m.dangerous
		return m, m.list.NewStatusMessage("resume mode: " + resumeModeLabel(m.dangerous))
	case key.Matches(msg, m.keys.Scope):
		m.pendingConvert = nil
		m.handoff = nextHandoffScope(m.handoff)
		return m, m.list.NewStatusMessage("hand-off scope: " + m.handoff.Label())
	case key.Matches(msg, m.keys.Preview):
		m.setMode(nextPreviewMode(m.mode))
		return m, m.list.NewStatusMessage("preview: " + modeLong(m.mode))
	case key.Matches(msg, m.keys.Rescan):
		m.pendingConvert = nil
		m.rescanning = true
		m.rescanRow = nil
		if selected, ok := m.list.SelectedItem().(item); ok {
			row := selected.row
			m.rescanRow = &row
		}
		return m, tea.Batch(loadSessionsCmd(), m.list.NewStatusMessage("rescanning session stores…"))
	case key.Matches(msg, m.keys.Providers):
		return m.toggleProviderIndex(msg.String())
	}

	if msg.String() == "/" {
		m.pendingConvert = nil
		rebuild := m.rebuildListForSearch()
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, tea.Batch(rebuild, cmd)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.reconcileDeleteArm()
	m.reconcilePendingConversion()
	return m, cmd
}

func (m model) applyMutation(msg sessionMutationMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	m.deleteArmed = ""
	m.pendingConvert = nil
	if msg.err != nil {
		return m, m.list.NewStatusMessage("action failed: " + msg.err.Error())
	}

	m.providers[msg.row.Provider] = true
	delete(m.collapsedGroups, msg.row.CWD)
	m.allRows = upsertAndSortRows(m.allRows, msg.row)
	m.list.ResetFilter()
	m.pruneCollapsedGroups()
	cmd := m.list.SetItems(m.currentItems())
	selectRowItem(&m.list, msg.row)

	recipe := session.RecipeFor(msg.row, session.ResumeOptions{Dangerous: m.dangerous})
	status := "converted to " + string(msg.row.Provider) + " · " + recipe.CommandString
	if msg.kind == mutationBranch {
		status = "branched " + string(msg.row.Provider) + " · " + recipe.CommandString
	}
	return m, tea.Batch(cmd, m.list.NewStatusMessage(status))
}

func (m model) applyConversionPreview(msg conversionPreviewMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	if msg.err != nil {
		m.pendingConvert = nil
		return m, m.list.NewStatusMessage("preview failed: " + msg.err.Error())
	}
	m.pendingConvert = &pendingConversion{
		rowKey:  rowKey(msg.row),
		target:  msg.target,
		options: msg.options,
		preview: msg.preview,
	}
	return m, m.list.NewStatusMessage("preview ready · press x again to write the " + string(msg.target) + " session")
}

func (m model) startMutation(cmd tea.Cmd, busy, status string) (tea.Model, tea.Cmd) {
	if cmd == nil {
		return m, m.list.NewStatusMessage("no session selected")
	}
	m.busy = busy
	return m, tea.Batch(cmd, m.list.NewStatusMessage(status))
}

func (m model) startConvert() (tea.Model, tea.Cmd) {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, m.list.NewStatusMessage("no session selected")
	}
	target := m.handoffTargetFor(selected.row)
	if target == "" {
		return m, m.list.NewStatusMessage("no hand-off target available")
	}
	if m.pendingConvert != nil &&
		m.pendingConvert.rowKey == rowKey(selected.row) &&
		m.pendingConvert.target == target &&
		m.pendingConvert.options == m.handoff {
		return m.startMutation(m.convertRow(selected.row, target, m.handoff), "conversion", "converting to "+string(target)+"…")
	}
	return m.startMutation(m.previewConversion(selected.row, target, m.handoff), "preview", "building conversion preview…")
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

func (m *model) reconcilePendingConversion() {
	if m.pendingConvert == nil {
		return
	}
	selected, ok := m.list.SelectedItem().(item)
	if !ok || rowKey(selected.row) != m.pendingConvert.rowKey {
		m.pendingConvert = nil
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

func (m *model) currentItems() []list.Item {
	collapsed := m.collapsedGroups
	if m.list.SettingFilter() || m.list.IsFiltered() {
		collapsed = nil
	}
	return groupedItems(filterRows(m.allRows, m.providers), collapsed)
}

func (m *model) rebuildList() tea.Cmd {
	return m.list.SetItems(m.currentItems())
}

func (m *model) rebuildListForSearch() tea.Cmd {
	return m.list.SetItems(groupedItems(filterRows(m.allRows, m.providers), nil))
}

func (m *model) pruneCollapsedGroups() {
	visible := map[string]bool{}
	for _, row := range filterRows(m.allRows, m.providers) {
		visible[row.CWD] = true
	}
	for path := range m.collapsedGroups {
		if !visible[path] {
			delete(m.collapsedGroups, path)
		}
	}
}

func (m model) toggleGroup(path string) (tea.Model, tea.Cmd) {
	m.collapsedGroups[path] = !m.collapsedGroups[path]
	cmd := m.rebuildList()
	selectHeaderItem(&m.list, path)
	state := "expanded"
	if m.collapsedGroups[path] {
		state = "collapsed"
	}
	return m, tea.Batch(cmd, m.list.NewStatusMessage("category "+state+": "+collapseHome(path)))
}

func (m *model) toggleProvider(provider session.Provider) tea.Cmd {
	m.pendingConvert = nil
	if !providerExists(m.allRows, provider) {
		return m.list.NewStatusMessage(fmt.Sprintf("no %s sessions found", provider))
	}
	if m.providers[provider] && enabledProviderCount(m.providers) == 1 {
		return m.list.NewStatusMessage("keep at least one provider visible")
	}

	m.providers[provider] = !m.providers[provider]
	m.pruneCollapsedGroups()
	cmd := m.list.SetItems(m.currentItems())
	selectFirstSession(&m.list)
	return tea.Batch(cmd, m.list.NewStatusMessage("providers: "+providerLabel(m.providers)))
}

// toggleProviderIndex maps a digit key onto the discovered provider order:
// "1" toggles the first discovered provider, "2" the second, and so on.
func (m model) toggleProviderIndex(digit string) (tea.Model, tea.Cmd) {
	order := discoveredProviders(m.allRows)
	index := int(digit[0] - '1')
	if index < 0 || index >= len(order) {
		return m, m.list.NewStatusMessage("no provider on key " + digit)
	}
	return m, m.toggleProvider(order[index])
}

// discoveredProviders lists the providers that actually have sessions, in the
// stable ProviderOrder, so digit filter keys keep a predictable meaning.
func discoveredProviders(rows []session.Row) []session.Provider {
	var providers []session.Provider
	for _, provider := range session.ProviderOrder() {
		if providerExists(rows, provider) {
			providers = append(providers, provider)
		}
	}
	return providers
}

func (m model) selectResume() (tea.Model, tea.Cmd) {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, m.list.NewStatusMessage("no session selected")
	}
	// Validate before tea.Quit tears the TUI down, so a missing CLI or a
	// deleted workspace surfaces as a status line instead of a post-exit error.
	if err := session.ValidateResume(selected.row); err != nil {
		return m, m.list.NewStatusMessage("cannot resume: " + err.Error())
	}
	m.selected = &Selection{
		Row:     selected.row,
		Options: session.ResumeOptions{Dangerous: m.dangerous},
	}
	return m, tea.Quit
}

func (m *model) previewConversion(row session.Row, target session.Provider, options session.HandoffOptions) tea.Cmd {
	return func() tea.Msg {
		preview, err := session.PreviewConversion(row, target, options)
		return conversionPreviewMsg{row: row, target: target, options: options, preview: preview, err: err}
	}
}

func (m *model) convertRow(row session.Row, target session.Provider, options session.HandoffOptions) tea.Cmd {
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
	m.pruneCollapsedGroups()
	cmd := m.list.SetItems(m.currentItems())
	selectFirstSession(&m.list)
	return tea.Batch(cmd, m.list.NewStatusMessage("deleted "+string(selected.row.Provider)+" session"))
}

func (m model) startCompound(agent session.Provider) (tea.Model, tea.Cmd) {
	if m.compoundRow == nil {
		m.compoundChoosing = false
		return m, m.list.NewStatusMessage("no session selected")
	}
	// Keep the chooser open and explain the problem instead of quitting into
	// a launch that is known to fail.
	if err := session.ValidateCompound(*m.compoundRow, agent); err != nil {
		m.compoundNotice = "cannot run " + string(agent) + ": " + err.Error()
		return m, nil
	}
	m.selected = &Selection{
		Row:     *m.compoundRow,
		Options: session.ResumeOptions{Dangerous: m.dangerous},
		Action:  ActionCompound,
		Agent:   agent,
	}
	return m, tea.Quit
}

func (m model) View() tea.View {
	var content string
	switch {
	case m.loading:
		content = m.loadingView()
	case m.compoundChoosing:
		content = m.compoundView()
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
	helpBar := m.helpView()
	parts := []string{
		m.headerView(),
		columnHeader(m.render.theme, m.width, m.mode),
		m.list.View(),
		m.detailView(),
	}
	body := parts[:0]
	for _, p := range parts {
		if p != "" {
			body = append(body, p)
		}
	}
	content := lipgloss.JoinVertical(lipgloss.Left, body...)
	// Hard height budget: whatever else happens above (long paths, extra
	// banners), the help bar keeps its rows and stays on screen.
	if m.height > 0 {
		content = clampLines(content, m.height-lipgloss.Height(helpBar))
	}
	if content == "" {
		return helpBar
	}
	return lipgloss.JoinVertical(lipgloss.Left, content, helpBar)
}

// clampLines truncates value to at most maxLines terminal rows.
func clampLines(value string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) <= maxLines {
		return value
	}
	return strings.Join(lines[:maxLines], "\n")
}

// headerView renders the title plus as many stats chips as fit the width, so
// the header can never wrap and push the help bar off-screen.
func (m model) headerView() string {
	th := m.render.theme
	title := th.title.Render("showagent")
	stats := m.statsParts()
	for len(stats) > 0 {
		header := lipgloss.JoinHorizontal(lipgloss.Left, title, "  ", strings.Join(stats, "  "))
		if m.width <= 0 || lipgloss.Width(header) <= m.width {
			return header
		}
		stats = stats[:len(stats)-1]
	}
	return title
}

func (m model) statsParts() []string {
	th := m.render.theme
	parts := []string{
		th.muted.Render(fmt.Sprintf("%d/%d sessions", sessionCount(m.list.VisibleItems()), len(m.allRows))),
		th.chip.Render(providerLabel(m.providers)),
		th.muted.Render("preview " + modeShort(m.mode)),
	}
	if target := m.selectedHandoffTarget(); target != "" {
		parts = append(parts, th.muted.Render("target "+string(target)))
	}
	if m.dangerous {
		parts = append(parts, th.yoloChip.Render("YOLO"))
	}
	return parts
}

func (m model) helpView() string {
	return m.help.View(m.dynamicKeys())
}

// dynamicKeys copies the static keymap and rewrites the help text of stateful
// bindings, so the short bar and the '?' overlay both show current values.
func (m model) dynamicKeys() keyMap {
	keys := m.keys
	if label, desc, ok := m.providerFilterHelp(); ok {
		keys.Providers.SetHelp(label, desc)
	}
	target := "none"
	if t := m.selectedHandoffTarget(); t != "" {
		target = string(t)
	}
	keys.Target.SetHelp("o", "target:"+target)
	keys.Scope.SetHelp("t", "scope:"+m.handoff.Label())
	keys.Preview.SetHelp("p", "preview:"+modeShort(m.mode))
	keys.Yolo.SetHelp("y", "mode:"+resumeModeLabel(m.dangerous))
	return keys
}

// providerFilterHelp describes the digit filter keys for the providers that
// were actually discovered, with each provider's current on/off state.
func (m model) providerFilterHelp() (label, desc string, ok bool) {
	order := discoveredProviders(m.allRows)
	if len(order) == 0 {
		return "", "", false
	}
	labels := make([]string, 0, len(order))
	states := make([]string, 0, len(order))
	for index, provider := range order {
		labels = append(labels, fmt.Sprintf("%d", index+1))
		state := "off"
		if m.providers[provider] {
			state = "on"
		}
		states = append(states, fmt.Sprintf("%s:%s", provider, state))
	}
	return strings.Join(labels, "/"), strings.Join(states, " "), true
}

func (m model) detailView() string {
	count := m.detailLineCount()
	if count == 0 {
		return ""
	}
	th := m.render.theme
	width := max(40, m.width)
	// Style.Width covers border and padding too, so the text area is the
	// block width minus the full frame; lines are truncated to exactly that
	// so nothing can wrap and blow the height budget.
	frameW, _ := th.detail.GetFrameSize()
	innerW := max(10, width-frameW)
	valueW := max(8, innerW-detailLabelWidth)

	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		if header, isHeader := m.list.SelectedItem().(headerItem); isHeader {
			state := "expanded"
			if header.collapsed {
				state = "collapsed"
			}
			lines := []string{
				th.label.Render(padLabel("category")) + truncateMiddle(collapseHome(header.path), valueW),
				th.label.Render(padLabel("sessions")) + fmt.Sprintf("%d", header.count),
				th.label.Render(padLabel("state")) + state,
				th.hint.Render("enter/space → collapse or expand category"),
			}
			if len(lines) > count {
				lines = lines[:count]
			}
			for i := range lines {
				lines[i] = truncateCells(lines[i], innerW)
			}
			return th.detail.Width(width).Render(strings.Join(lines, "\n"))
		}
		msg := "No session selected."
		if m.list.IsFiltered() {
			msg = "No session matches this search · press esc to clear."
		}
		return th.detail.Width(width).Render(th.muted.Render(truncateCells(msg, innerW)))
	}
	row := selected.row

	var lines []string
	if m.deleteArmed == rowKey(row) {
		lines = append(lines, th.deleteBanner.Render("⚠ press delete again to permanently remove this session"))
	}
	if pending := m.selectedConversionPreview(row); pending != nil {
		lines = append(lines,
			th.label.Render(padLabel("handoff"))+fmt.Sprintf("%s → %s", pending.preview.SourceProvider, pending.preview.TargetProvider),
			th.label.Render(padLabel("scope"))+fmt.Sprintf("%s · %d turns", pending.preview.Scope, pending.preview.TransferTurns),
			th.label.Render(padLabel("last user"))+truncateCells(emptyFallback(pending.preview.LastUser), valueW),
			th.label.Render(padLabel("drops"))+truncateCells(strings.Join(pending.preview.Dropped, ", "), valueW),
			th.hint.Render("press x again to write · esc cancel · o target · t scope"),
		)
		if len(pending.preview.Warnings) > 0 {
			lines = append(lines, th.deleteBanner.Render(truncateCells(strings.Join(pending.preview.Warnings, "; "), innerW)))
		}
		if len(lines) > count {
			lines = lines[:count]
		}
		for i := range lines {
			lines[i] = truncateCells(lines[i], innerW)
		}
		return th.detail.Width(width).Render(strings.Join(lines, "\n"))
	}

	recipe := session.RecipeFor(row, session.ResumeOptions{Dangerous: m.dangerous})
	lines = append(lines,
		th.label.Render(padLabel("provider"))+m.providerWord(row.Provider),
		th.label.Render(padLabel("session"))+row.ID,
		th.label.Render(padLabel("workspace"))+truncateMiddle(collapseHome(row.CWD), valueW),
		th.label.Render(padLabel("storage"))+truncateMiddle(collapseHome(recipe.StorageLocation), valueW),
		th.label.Render(padLabel("command"))+truncateCells(recipe.CommandString, valueW),
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
	return th.detail.Width(width).Render(strings.Join(lines, "\n"))
}

func (m model) selectedConversionPreview(row session.Row) *pendingConversion {
	if m.pendingConvert == nil || m.pendingConvert.rowKey != rowKey(row) {
		return nil
	}
	return m.pendingConvert
}

func (m model) providerWord(p session.Provider) string {
	return providerBadge(m.render.theme, string(p), len(string(p))+2)
}

func (m model) resumeHint(row session.Row) string {
	if !session.ProviderCommandAvailable(row.Provider) {
		return fmt.Sprintf("%s CLI not found in PATH · install it or x → hand off", row.Provider)
	}
	if !m.dangerous {
		return fmt.Sprintf("enter → resume with %s (normal) · y → yolo", row.Provider)
	}
	if row.Provider == session.ProviderClaude {
		return fmt.Sprintf("enter → resume with %s · yolo: skips permission prompts · y → normal", row.Provider)
	}
	if row.Provider == session.ProviderJCode {
		return fmt.Sprintf("enter → resume with %s · yolo: no extra jcode flag · y → normal", row.Provider)
	}
	return fmt.Sprintf("enter → resume with %s · yolo: bypasses approvals & sandbox · y → normal", row.Provider)
}

func (m model) handoffHint(row session.Row) string {
	target := m.handoffTargetFor(row)
	if target == "" {
		return fmt.Sprintf("x → no hand-off target · n → branch · t → scope %s", m.handoff.Label())
	}
	return fmt.Sprintf("x → hand off to %s (%s) · o → target · n → branch · t → scope", target, m.handoff.Label())
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
		return 10
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
	body := th.title.Render("showagent") + "\n\n" + m.spinner.View() + " " + th.muted.Render("Scanning local agent sessions…")
	if m.width <= 0 || m.height <= 0 {
		return body
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}

func (m model) emptyView() string {
	th := m.render.theme
	lines := []string{
		th.title.Render("showagent"),
		"",
		th.muted.Render("No supported local sessions found."),
		"",
		th.muted.Render("Scanned:"),
	}
	for _, target := range session.ScanTargets() {
		line := fmt.Sprintf("  %-8s %s  (override with %s)", target.Provider, collapseHome(target.Path), target.EnvVar)
		if target.Note != "" {
			line += "  — " + target.Note
		}
		lines = append(lines, th.muted.Render(line))
	}
	lines = append(lines,
		"",
		th.hint.Render("Start a conversation with a supported agent, then press r to rescan."),
		th.hint.Render("Press q to quit."),
	)
	body := strings.Join(lines, "\n")
	if m.width <= 0 || m.height <= 0 {
		return body
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}

func (m model) compoundView() string {
	th := m.render.theme
	target := "the selected session"
	if m.compoundRow != nil {
		target = string(m.compoundRow.Provider) + " · " + baseName(m.compoundRow.CWD)
	}
	option := func(digit, name string, provider session.Provider) string {
		label := th.label.Render("["+digit+"]") + " " + name
		if !session.ProviderCommandAvailable(provider) {
			label += th.muted.Render(" (not installed)")
		}
		return label
	}
	options := make([]string, 0, 4)
	for index, agent := range session.CompoundAgents() {
		options = append(options, option(fmt.Sprintf("%d", index+1), session.DisplayName(agent), agent))
	}
	lines := []string{
		th.title.Render("Compound engineering"),
		"",
		th.muted.Render("Run a compound pass on " + target + " and"),
		th.muted.Render("pool the learnings for this project across agents."),
		"",
		strings.Join(options, "   "),
		"",
		th.hint.Render("esc cancel · current mode: " + resumeModeLabel(m.dangerous)),
	}
	if m.compoundNotice != "" {
		lines = append(lines, th.deleteBanner.Render(m.compoundNotice))
	}
	box := th.detail.Width(min(max(m.width-6, 30), 64)).Render(strings.Join(lines, "\n"))
	if m.width <= 0 || m.height <= 0 {
		return box
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// Pick runs the picker over an already-discovered set of rows (used in tests).
func Pick(rows []session.Row) (*Selection, error) {
	return runProgram(newModel(rows))
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

func defaultProviderFilter(rows []session.Row) map[session.Provider]bool {
	providers := map[session.Provider]bool{}
	for _, row := range rows {
		providers[row.Provider] = true
	}
	return providers
}

// mergeProviderFilter rebuilds the provider filter after a rescan: providers
// keep their previous on/off state, newly discovered ones start enabled, and
// an all-off result falls back to the default filter so the list never goes
// permanently blank.
func mergeProviderFilter(previous map[session.Provider]bool, rows []session.Row) map[session.Provider]bool {
	providers := map[session.Provider]bool{}
	for _, row := range rows {
		enabled, known := previous[row.Provider]
		if !known {
			enabled = true
		}
		providers[row.Provider] = enabled
	}
	if enabledProviderCount(providers) == 0 {
		return defaultProviderFilter(rows)
	}
	return providers
}

// nextPreviewMode cycles first -> latest -> both -> first.
func nextPreviewMode(mode previewMode) previewMode {
	switch mode {
	case firstMessage:
		return lastMessage
	case lastMessage:
		return bothMessages
	default:
		return firstMessage
	}
}

func modeLong(mode previewMode) string {
	switch mode {
	case lastMessage:
		return "latest user message"
	case bothMessages:
		return "first + latest user messages"
	default:
		return "first user message"
	}
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
	for _, provider := range session.ProviderOrder() {
		if providers[provider] {
			values = append(values, string(provider))
		}
	}
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, "+")
}

func (m model) selectedHandoffTarget() session.Provider {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return ""
	}
	return m.handoffTargetFor(selected.row)
}

func (m model) handoffTargetFor(row session.Row) session.Provider {
	candidates := m.handoffCandidates(row)
	if len(candidates) == 0 {
		return ""
	}
	if m.handoffTarget != "" && m.handoffTarget != row.Provider {
		for _, candidate := range candidates {
			if candidate == m.handoffTarget {
				return candidate
			}
		}
	}
	return candidates[0]
}

// handoffCandidates lists the providers row can convert to. A provider only
// qualifies when its CLI is on PATH: converting to a tool that cannot resume
// the result would strand the session.
func (m model) handoffCandidates(row session.Row) []session.Provider {
	var candidates []session.Provider
	for _, provider := range session.ProviderOrder() {
		if provider == row.Provider {
			continue
		}
		if session.ProviderCommandAvailable(provider) {
			candidates = append(candidates, provider)
		}
	}
	return candidates
}

func (m model) cycleHandoffTarget() (tea.Model, tea.Cmd) {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, m.list.NewStatusMessage("no session selected")
	}
	candidates := m.handoffCandidates(selected.row)
	if len(candidates) == 0 {
		return m, m.list.NewStatusMessage("no hand-off target available")
	}

	current := m.handoffTargetFor(selected.row)
	next := candidates[0]
	for index, candidate := range candidates {
		if candidate == current {
			next = candidates[(index+1)%len(candidates)]
			break
		}
	}
	m.handoffTarget = next
	return m, m.list.NewStatusMessage("hand-off target: " + string(next))
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
