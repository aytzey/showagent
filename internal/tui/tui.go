package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	list "charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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

type sessionDeleteMsg struct {
	row session.Row
	err error
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

// headerItem is a selectable group header (one per workspace folder). Search
// runs over flatItems, so headers never reach the filter.
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
		flat := m.FilterState() != list.Unfiltered
		_, _ = fmt.Fprint(w, renderTableRow(d.state.theme, m.Width(), it.row, d.state.mode, index == m.Index(), flat))
	}
}

// matchAllWords keeps the sessions whose search text contains every query
// word, case-insensitively, in input (flatItems: newest-first) order. Fuzzy subsequence
// matching ranked unrelated sessions above the ones containing the words.
func matchAllWords(term string, targets []string) []list.Rank {
	words := strings.Fields(strings.ToLower(term))
	var ranks []list.Rank
	for index, target := range targets {
		target = strings.ToLower(target)
		matched := true
		for _, word := range words {
			if !strings.Contains(target, word) {
				matched = false
				break
			}
		}
		if matched {
			ranks = append(ranks, list.Rank{Index: index})
		}
	}
	return ranks
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

// flatItems lists sessions without workspace headers in their global
// newest-first order, which is the order search results keep.
func flatItems(rows []session.Row) []list.Item {
	items := make([]list.Item, len(rows))
	for index, row := range rows {
		items[index] = item{row: row}
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

// rowItemIndex returns where target sits in items, or fallback if absent.
func rowItemIndex(items []list.Item, target session.Row, fallback int) int {
	wanted := rowKey(target)
	for index, it := range items {
		if si, ok := it.(item); ok && rowKey(si.row) == wanted {
			return index
		}
	}
	return fallback
}

// selectNearestSession keeps the cursor where it was after the list shrinks,
// landing on the session now at index (or the closest one after it, then
// before it) so deleting a row does not scroll the list back to the top.
func selectNearestSession(l *list.Model, index int) {
	visible := l.VisibleItems()
	if len(visible) == 0 {
		return
	}
	index = min(max(index, 0), len(visible)-1)
	for i := index; i < len(visible); i++ {
		if _, ok := visible[i].(item); ok {
			l.Select(i)
			return
		}
	}
	for i := index - 1; i >= 0; i-- {
		if _, ok := visible[i].(item); ok {
			l.Select(i)
			return
		}
	}
	l.Select(index)
}

// selectRowItem moves the cursor to target, or to the first session if
// target is no longer visible.
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
	importFlow       *importWizard
	keysHidden       bool
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
	l.Filter = matchAllWords
	l.FilterInput.Prompt = "search: "
	l.FilterInput.Placeholder = "words from a prompt, path, agent or id"
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetShowStatusBar(false)
	// The list only draws status messages beside its title, so keep an empty,
	// unstyled title: the row it uses is already reserved for the filter
	// input, and action failures would otherwise be silently invisible.
	l.Title = ""
	l.Styles.Title = lipgloss.NewStyle()
	l.Styles.TitleBar = lipgloss.NewStyle()
	l.StatusMessageLifetime = 4 * time.Second
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
		m.busy = ""
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
	case sessionDeleteMsg:
		return m.applyDelete(msg)
	case conversionPreviewMsg:
		return m.applyConversionPreview(msg)
	case importParsedMsg:
		return m.applyImportParsed(msg)
	case importPlannedMsg:
		return m.applyImportPlanned(msg)
	case importAppliedMsg:
		return m.applyImportApplied(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	if m.importFlow != nil {
		return m.updateImportInput(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.reconcileDeleteArm()
	m.reconcilePendingConversion()
	return m, cmd
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Keep the terminal's unconditional interrupt available inside every
	// import step, including while a native write is in progress.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	if m.loading {
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		return m, nil
	}

	if m.importFlow != nil {
		return m.updateImport(msg)
	}

	// The compound chooser is a modal: pick the agent by digit, or cancel.
	// Digits follow the registry order of compound-capable providers.
	if m.compoundChoosing {
		keyStr := msg.String()
		if agents := m.compoundAgents(); len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
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
		selected, hadSession := m.list.SelectedItem().(item)
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		// A cancelled or empty search returns to the grouped view.
		if m.list.FilterState() == list.Unfiltered {
			cmd = tea.Batch(cmd, m.rebuildList())
			if hadSession {
				selectRowItem(&m.list, selected.row)
			}
		}
		m.reconcileDeleteArm()
		m.reconcilePendingConversion()
		return m, cmd
	}

	// esc peels back one layer of UI state (search, help overlay, armed
	// delete) and otherwise does nothing: it never quits the picker.
	if msg.String() == "esc" {
		if m.list.IsFiltered() {
			selected, hadSession := m.list.SelectedItem().(item)
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			rebuild := m.rebuildList()
			// Stay on the session picked from the results instead of jumping
			// to the top group header.
			if hadSession {
				selectRowItem(&m.list, selected.row)
			}
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
		// "?" hides the keys box for a taller list; where the box cannot fit,
		// it opens the full help overlay instead.
		if m.keysPanelFits() {
			m.keysHidden = !m.keysHidden
		} else {
			m.help.ShowAll = !m.help.ShowAll
		}
		m.resizeList()
		return m, nil
	}

	if m.busy != "" {
		return m, m.list.NewStatusMessage(m.busy + " in progress…")
	}

	switch {
	case key.Matches(msg, m.keys.Import):
		return m.openImport()
	case key.Matches(msg, m.keys.Resume):
		if header, ok := m.list.SelectedItem().(headerItem); ok {
			return m.toggleGroup(header.path)
		}
		return m.selectResume()
	case key.Matches(msg, m.keys.Collapse):
		if header, ok := m.list.SelectedItem().(headerItem); ok {
			return m.toggleGroup(header.path)
		}
		return m, m.list.NewStatusMessage("select a workspace header to collapse it")
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
		return m.startDelete()
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
		m.busy = "rescan"
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
		selected, hadSession := m.list.SelectedItem().(item)
		rebuild := m.rebuildListForSearch()
		if hadSession {
			selectRowItem(&m.list, selected.row)
		}
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
	status := "converted to " + string(msg.row.Provider) + " · " + session.SafeDisplayText(recipe.CommandString)
	if msg.kind == mutationBranch {
		status = "branched " + string(msg.row.Provider) + " · " + session.SafeDisplayText(recipe.CommandString)
	}
	return m, tea.Batch(cmd, m.list.NewStatusMessage(status))
}

// applyDelete removes a deleted session from the list and keeps the cursor
// near where the deleted row was.
func (m model) applyDelete(msg sessionDeleteMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	m.deleteArmed = ""
	if msg.err != nil {
		return m, m.list.NewStatusMessage("delete failed: " + msg.err.Error())
	}

	m.allRows = removeRow(m.allRows, msg.row)
	if !providerExists(m.allRows, msg.row.Provider) {
		delete(m.providers, msg.row.Provider)
	}
	if enabledProviderCount(m.providers) == 0 {
		m.providers = defaultProviderFilter(m.allRows)
	}
	m.pruneCollapsedGroups()
	index := rowItemIndex(m.list.VisibleItems(), msg.row, m.list.Index())
	cmd := m.list.SetItems(m.currentItems())
	selectNearestSession(&m.list, index)
	return m, tea.Batch(cmd, m.list.NewStatusMessage("deleted "+string(msg.row.Provider)+" session"))
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
	return m, m.list.NewStatusMessage("preview ready · press x again to create the " + string(msg.target) + " session")
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
	chrome := lipgloss.Height(m.headerView()) + columnHeaderHeight + m.detailHeight() + lipgloss.Height(m.bottomView()) + bottomSafetyRows
	listHeight := max(3, m.height-chrome)
	m.list.SetSize(m.width, listHeight)
}

func (m *model) currentItems() []list.Item {
	rows := filterRows(m.allRows, m.providers)
	if m.list.SettingFilter() || m.list.IsFiltered() {
		return flatItems(rows)
	}
	return groupedItems(rows, m.collapsedGroups)
}

func (m *model) rebuildList() tea.Cmd {
	return m.list.SetItems(m.currentItems())
}

func (m *model) rebuildListForSearch() tea.Cmd {
	return m.list.SetItems(flatItems(filterRows(m.allRows, m.providers)))
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
	return m, tea.Batch(cmd, m.list.NewStatusMessage("workspace "+state+": "+collapseHome(path)))
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

func (m model) startDelete() (tea.Model, tea.Cmd) {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, m.list.NewStatusMessage("no session selected")
	}
	key := rowKey(selected.row)
	if m.deleteArmed != key {
		m.deleteArmed = key
		return m, m.list.NewStatusMessage("press d again to permanently delete this " + string(selected.row.Provider) + " session · esc keeps it")
	}

	row := selected.row
	m.deleteArmed = ""
	m.busy = "delete"
	deleteCmd := func() tea.Msg {
		return sessionDeleteMsg{row: row, err: session.Delete(row)}
	}
	return m, tea.Batch(deleteCmd, m.list.NewStatusMessage("deleting "+string(row.Provider)+" session…"))
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
	case m.importFlow != nil:
		content = m.importView()
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
	helpBar := m.bottomView()
	if m.list.FilterState() == list.FilterApplied {
		m.list.Title = m.render.theme.hint.Render(fmt.Sprintf("search: %s · esc clears", m.list.FilterValue()))
	}
	// The list's first row holds the search input and status messages; draw
	// it above the column header so the header sits directly over the rows.
	searchRow, rows, _ := strings.Cut(m.list.View(), "\n")
	parts := []string{
		m.headerView(),
		searchRow,
		columnHeader(m.render.theme, m.width, m.mode, m.list.FilterState() != list.Unfiltered),
		rows,
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
		return clampLineWidths(helpBar, m.width)
	}
	return clampLineWidths(lipgloss.JoinVertical(lipgloss.Left, content, helpBar), m.width)
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

func clampLineWidths(value string, width int) string {
	if width <= 0 {
		return value
	}
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = truncateCells(lines[index], width)
	}
	return strings.Join(lines, "\n")
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

// statsParts is ordered by importance: headerView drops parts from the end
// when the terminal is narrow, so the YOLO warning is the last to go.
func (m model) statsParts() []string {
	th := m.render.theme
	var parts []string
	if m.dangerous {
		parts = append(parts, th.yoloChip.Render("YOLO"))
	}
	count := fmt.Sprintf("%d sessions", len(m.allRows))
	if shown := sessionCount(m.list.VisibleItems()); shown != len(m.allRows) {
		count = fmt.Sprintf("%d of %d sessions", shown, len(m.allRows))
	}
	parts = append(parts, th.muted.Render(count))
	if chips := m.providerChips(); chips != "" {
		parts = append(parts, chips)
	}
	return append(parts, th.hint.Render("· resume a coding-agent session, or hand it to another agent"))
}

// providerChips shows each discovered provider with the digit that toggles
// it; hidden providers are struck through.
func (m model) providerChips() string {
	th := m.render.theme
	chips := make([]string, 0, len(m.providers))
	for index, provider := range discoveredProviders(m.allRows) {
		label := fmt.Sprintf("%d %s", index+1, provider)
		if m.providers[provider] {
			chips = append(chips, th.badgeFor(provider).Padding(0, 1).Render(label))
		} else {
			chips = append(chips, th.muted.Strikethrough(true).Padding(0, 1).Render(label))
		}
	}
	return strings.Join(chips, " ")
}

func (m model) helpView() string {
	keys := m.dynamicKeys()
	if m.help.ShowAll {
		return m.help.View(keys)
	}
	// Drop hints before the last one ("? all keys") until the bar fits: the
	// help component overflows instead of truncating when "…" does not fit.
	short := keys.ShortHelp()
	if m.keysPanelFits() {
		short[len(short)-1].SetHelp("?", "show keys")
	}
	unbounded := m.help
	unbounded.SetWidth(0)
	for len(short) > 2 && m.width > 0 && lipgloss.Width(unbounded.ShortHelpView(short)) > m.width {
		short = append(short[:len(short)-2], short[len(short)-1])
	}
	return m.help.ShortHelpView(short)
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
	switch selected := m.list.SelectedItem().(type) {
	case headerItem:
		keys.Resume.SetHelp("enter", "expand/collapse")
	case item:
		if m.selectedConversionPreview(selected.row) != nil {
			keys.Convert.SetHelp("x", "confirm hand-off → "+target)
		} else if target != "none" {
			keys.Convert.SetHelp("x", "hand off → "+target)
		}
	}
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
	// Lines are truncated to the text area so nothing can wrap and blow the
	// height budget.
	frameW, _ := th.detail.GetFrameSize()
	innerW := max(10, width-frameW)
	valueW := max(8, innerW-detailLabelWidth)
	box := func(title string, lines []string) string {
		return titledBox(th.detailEdge, th.detailEdge, title, fitLines(lines, count, innerW), width)
	}

	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		if header, isHeader := m.list.SelectedItem().(headerItem); isHeader {
			state := "expanded"
			if header.collapsed {
				state = "collapsed"
			}
			return box("Workspace", []string{
				th.label.Render(padLabel("path")) + truncateMiddle(collapseHome(session.SafeDisplayText(header.path)), valueW),
				th.label.Render(padLabel("sessions")) + fmt.Sprintf("%d", header.count),
				th.label.Render(padLabel("state")) + state,
			})
		}
		msg := "No session selected."
		if m.list.IsFiltered() {
			msg = "No session matches this search · press esc to clear."
		}
		return box("Session", []string{th.muted.Render(msg)})
	}
	row := selected.row

	var lines []string
	if m.deleteArmed == rowKey(row) {
		lines = append(lines, th.deleteBanner.Render("⚠ press d again to permanently delete this session · esc keeps it"))
	}
	if pending := m.selectedConversionPreview(row); pending != nil {
		lines = append(lines, th.label.Render(padLabel("handoff"))+fmt.Sprintf("%s → %s · %s · %d turns · original stays",
			pending.preview.SourceProvider, pending.preview.TargetProvider, pending.preview.Scope, pending.preview.TransferTurns))
		// What gets dropped must be readable before confirming, even in the
		// 2-row panel, so it comes right after the summary and wraps.
		label := padLabel("drops")
		for _, part := range strings.Split(ansi.Wordwrap(strings.Join(pending.preview.Dropped, ", "), valueW, ""), "\n") {
			lines = append(lines, th.label.Render(label)+part)
			label = padLabel("")
		}
		if len(pending.preview.Warnings) > 0 {
			lines = append(lines, th.deleteBanner.Render(truncateCells(strings.Join(pending.preview.Warnings, "; "), innerW)))
		}
		lines = append(lines, th.label.Render(padLabel("last user"))+truncateCells(emptyFallback(session.SafeDisplayText(pending.preview.LastUser)), valueW))
		return box("Hand-off preview · x again to create", lines)
	}

	// Ordered by importance: short terminals show only the first lines.
	if !session.ProviderCommandAvailable(row.Provider) {
		lines = append(lines, th.deleteBanner.Render(fmt.Sprintf("%s CLI not found in PATH · install it, or hand the session off", row.Provider)))
	}
	lines = append(lines,
		th.label.Render(padLabel("first"))+truncateCells(emptyFallback(session.SafeDisplayText(row.FirstUser)), valueW),
		th.label.Render(padLabel("latest"))+truncateCells(emptyFallback(session.SafeDisplayText(bestLast(row))), valueW),
	)
	if m.dangerous {
		lines = append(lines, th.label.Render(padLabel("mode"))+th.yoloChip.Render("YOLO")+" "+yoloEffect(row.Provider))
	}
	recipe := session.RecipeFor(row, session.ResumeOptions{Dangerous: m.dangerous})
	lines = append(lines,
		th.label.Render(padLabel("workspace"))+truncateMiddle(collapseHome(session.SafeDisplayText(row.CWD)), valueW),
		th.label.Render(padLabel("updated"))+localTime(row.LastAt)+th.muted.Render(" ("+relativeTime(row.LastAt)+")"),
		th.label.Render(padLabel("session"))+m.providerWord(row.Provider)+" "+session.SafeDisplayText(row.ID),
		th.label.Render(padLabel("command"))+truncateCells(session.SafeDisplayText(recipe.CommandString), valueW),
	)
	return box("Session", lines)
}

// titledBox draws a rounded box of the given total width with title set
// into its top border.
func titledBox(edge, titleStyle lipgloss.Style, title, body string, width int) string {
	label := " " + title + " "
	fill := max(0, width-3-lipgloss.Width(label))
	top := edge.Render("╭─") + titleStyle.Render(label) + edge.Render(strings.Repeat("─", fill)+"╮")
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), false, true, true, true).
		BorderForeground(edge.GetForeground()).
		Padding(0, 1).
		Width(width)
	return top + "\n" + frame.Render(body)
}

// fitLines pads or cuts lines to exactly count rows of at most width cells, so
// the panel never wraps and keeps its height as the selection moves.
func fitLines(lines []string, count, width int) string {
	for len(lines) < count {
		lines = append(lines, "")
	}
	lines = lines[:count]
	for i := range lines {
		lines[i] = truncateCells(lines[i], width)
	}
	return strings.Join(lines, "\n")
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

// yoloEffect states what yolo mode actually changes for provider, without
// claiming a bypass the provider's CLI does not have.
func yoloEffect(provider session.Provider) string {
	switch provider {
	case session.ProviderClaude, session.ProviderAntigravity:
		return "skips permission prompts"
	case session.ProviderJCode, session.ProviderPi:
		return fmt.Sprintf("no extra %s flag", provider)
	default:
		return "bypasses approvals & sandbox"
	}
}

func (m model) detailLineCount() int {
	switch {
	case m.width > 0 && m.width < 40:
		return 0
	case m.height < 14:
		return 0
	case m.height < 20:
		return 2
	case m.height < 28:
		return 4
	default:
		return 6
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
	return clampLineWidths(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body), m.width)
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
		prefix := fmt.Sprintf("  %-8s ", target.Provider)
		suffix := fmt.Sprintf("  (override with %s)", target.EnvVar)
		pathWidth := max(1, m.width-lipgloss.Width(prefix)-lipgloss.Width(suffix))
		if m.width <= 0 {
			pathWidth = lipgloss.Width(target.Path)
		}
		line := prefix + truncateMiddle(collapseHome(session.SafeDisplayText(target.Path)), pathWidth) + suffix
		if target.Note != "" {
			note := "  — " + target.Note
			if m.width <= 0 || lipgloss.Width(line)+lipgloss.Width(note) <= m.width {
				line += note
			}
		}
		lines = append(lines, th.muted.Render(line))
	}
	lines = append(lines,
		"",
		th.hint.Render("Press i to Import conversation; press r to rescan local sessions."),
		th.hint.Render("Press q to quit."),
	)
	body := strings.Join(lines, "\n")
	if m.width <= 0 || m.height <= 0 {
		return body
	}
	return clampLineWidths(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body), m.width)
}

// compoundAgents lists the chooser's options for the captured row, so digits
// and labels never offer an agent that cannot run the pass.
func (m model) compoundAgents() []session.Provider {
	if m.compoundRow == nil {
		return session.CompoundAgents()
	}
	return session.CompoundAgentsFor(*m.compoundRow)
}

func (m model) compoundView() string {
	th := m.render.theme
	target := "the selected session"
	if m.compoundRow != nil {
		target = string(m.compoundRow.Provider) + " · " + baseName(session.SafeDisplayText(m.compoundRow.CWD))
	}
	option := func(digit, name string, provider session.Provider) string {
		label := th.label.Render("["+digit+"]") + " " + name
		if !session.ProviderCommandAvailable(provider) {
			label += th.muted.Render(" (not installed)")
		}
		return label
	}
	lines := []string{
		th.title.Render("Compound engineering"),
		"",
		th.muted.Render("Run a compound pass on " + target + " and"),
		th.muted.Render("pool the learnings for this project across agents."),
		"",
	}
	for index, agent := range m.compoundAgents() {
		lines = append(lines, option(fmt.Sprintf("%d", index+1), session.DisplayName(agent), agent))
	}
	lines = append(lines, "", th.hint.Render("press a number to choose · esc cancel · mode: "+resumeModeLabel(m.dangerous)))
	if m.compoundNotice != "" {
		lines = append(lines, th.deleteBanner.Render(m.compoundNotice))
	}
	boxWidth := 64
	if m.width > 0 {
		boxWidth = max(1, min(m.width, 64))
	}
	innerWidth := max(1, boxWidth-2)
	for index := range lines {
		lines[index] = truncateCells(lines[index], innerWidth)
	}
	box := th.detail.Width(boxWidth).Render(strings.Join(lines, "\n"))
	if m.width <= 0 || m.height <= 0 {
		return box
	}
	return clampLineWidths(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box), m.width)
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
// qualifies when showagent can write its sessions and its CLI is on PATH:
// converting to a tool that cannot resume the result would strand the session.
func (m model) handoffCandidates(row session.Row) []session.Provider {
	var candidates []session.Provider
	for _, provider := range session.ProviderOrder() {
		if provider == row.Provider || !session.AcceptsConversion(provider) {
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
