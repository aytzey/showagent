package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aytzey/showagent/internal/session"
)

func selectedFromModel(t *testing.T, value tea.Model) *Selection {
	t.Helper()
	switch m := value.(type) {
	case model:
		return m.selected
	case *model:
		return m.selected
	default:
		t.Fatalf("unexpected model type %T", value)
		return nil
	}
}

func asModel(t *testing.T, value tea.Model) model {
	t.Helper()
	m, ok := value.(model)
	if !ok {
		t.Fatalf("unexpected model type %T", value)
	}
	return m
}

func sizedModel(rows []session.Row) model {
	m := newModel(rows, firstMessage)
	m.width = 110
	m.height = 36
	m.resizeList()
	return m
}

func TestPreviewModes(t *testing.T) {
	row := session.Row{
		Provider:  session.ProviderCodex,
		ID:        "id",
		LastAt:    time.Now(),
		CWD:       "/tmp",
		FirstUser: "first",
		LastUser:  "last",
	}

	if got := previewFor(row, firstMessage); got != "first" {
		t.Fatalf("first preview = %q", got)
	}
	if got := previewFor(row, lastMessage); got != "last" {
		t.Fatalf("last preview = %q", got)
	}
	if got := previewFor(row, bothMessages); got != "first | last" {
		t.Fatalf("both preview = %q", got)
	}
}

func TestTruncateCells(t *testing.T) {
	if got := truncateCells("abcdef", 4); got != "a..." {
		t.Fatalf("truncateCells = %q", got)
	}
	if got := truncateCells("abc", 4); got != "abc" {
		t.Fatalf("short truncateCells = %q", got)
	}
}

func TestComposeLineAlignsHeaderAndRows(t *testing.T) {
	width := 96
	header := composeLine(width, "  ", "AGENT", "UPDATED", "WORKSPACE", "USER MESSAGE")
	row := composeLine(width, "  ", "codex", "2026-06-22 10:24", "/home/aytug", "preview")

	if lipgloss.Width(header) != width {
		t.Fatalf("header width = %d, want %d", lipgloss.Width(header), width)
	}
	if lipgloss.Width(row) != width {
		t.Fatalf("row width = %d, want %d", lipgloss.Width(row), width)
	}
	if strings.Index(header, "WORKSPACE") != strings.Index(row, "/home/aytug") {
		t.Fatalf("workspace column mismatch:\n%q\n%q", header, row)
	}
	if strings.Index(header, "USER MESSAGE") != strings.Index(row, "preview") {
		t.Fatalf("preview column mismatch:\n%q\n%q", header, row)
	}
}

func TestRenderTableRowFitsWidth(t *testing.T) {
	th := newTheme(true)
	width := 118
	row := session.Row{
		Provider:  session.ProviderClaude,
		ID:        "id",
		LastAt:    time.Date(2026, 6, 22, 10, 24, 0, 0, time.Local),
		CWD:       "/projects/Machinity-Kanban",
		FirstUser: strings.Repeat("preview ", 30),
	}

	if got := lipgloss.Width(renderTableRow(th, width, row, firstMessage, false)); got != width {
		t.Fatalf("renderTableRow width = %d, want %d", got, width)
	}
	if got := lipgloss.Width(renderTableRow(th, width, row, firstMessage, true)); got != width {
		t.Fatalf("selected renderTableRow width = %d, want %d", got, width)
	}
}

func TestDetailViewFitsWidth(t *testing.T) {
	rows := []session.Row{{
		Provider:  session.ProviderCodex,
		ID:        "019eee0c-9361-7330-b0f4-b887cbe7fab6",
		LastAt:    time.Now(),
		CWD:       "/home/aytug",
		File:      "/home/aytug/.codex/sessions/session.jsonl",
		FirstUser: strings.Repeat("long message ", 30),
		LastUser:  strings.Repeat("last message ", 30),
	}}

	m := newModel(rows, firstMessage)
	m.width = 100
	m.height = 32
	m.resizeList()
	detail := m.detailView()
	if got := lipgloss.Width(detail); got > m.width {
		t.Fatalf("detail width = %d, want <= %d\n%s", got, m.width, detail)
	}
}

func TestEnterAndCtrlMSelectResume(t *testing.T) {
	row := session.Row{
		Provider: session.ProviderClaude,
		ID:       "resume-id",
		LastAt:   time.Now(),
		CWD:      "/tmp",
		File:     "/tmp/resume.jsonl",
	}

	tests := []tea.KeyPressMsg{
		tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}),
		tea.KeyPressMsg(tea.Key{Code: 'm', Mod: tea.ModCtrl}),
	}
	for _, msg := range tests {
		updated, _ := newModel([]session.Row{row}, firstMessage).Update(msg)
		selected := selectedFromModel(t, updated)
		if selected == nil {
			t.Fatalf("%q did not select a row", msg.String())
		}
		if selected.Row.ID != "resume-id" {
			t.Fatalf("%q selected row %q, want resume-id", msg.String(), selected.Row.ID)
		}
	}
}

func TestSelectResumeWithEmptyList(t *testing.T) {
	m := sizedModel(nil)
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if selectedFromModel(t, updated) != nil {
		t.Fatal("enter selected a session from an empty list")
	}
	if cmd == nil {
		t.Fatal("expected a status-message command, got nil")
	}
}

func TestUpsertAndSortRowsSelectsNewSession(t *testing.T) {
	old := session.Row{
		Provider: session.ProviderCodex,
		ID:       "old",
		LastAt:   time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC),
		File:     "/tmp/old.jsonl",
	}
	newRow := session.Row{
		Provider: session.ProviderClaude,
		ID:       "new",
		LastAt:   time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC),
		File:     "/tmp/new.jsonl",
	}

	rows := upsertAndSortRows([]session.Row{old}, newRow)
	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(rows))
	}
	if rows[0].ID != "new" {
		t.Fatalf("new row was not sorted first: %#v", rows)
	}
	if index := indexOfRow(rows, newRow); index != 0 {
		t.Fatalf("indexOfRow = %d, want 0", index)
	}
}

func TestSessionMutationClearsFilterAndSelectsNewRow(t *testing.T) {
	old := session.Row{
		Provider:  session.ProviderClaude,
		ID:        "old",
		LastAt:    time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC),
		File:      "/tmp/old.jsonl",
		FirstUser: "old message",
	}
	newRow := session.Row{
		Provider:  session.ProviderCodex,
		ID:        "new",
		LastAt:    time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC),
		File:      "/tmp/new.jsonl",
		FirstUser: "new message",
	}

	m := sizedModel([]session.Row{old})
	m.list.SetFilterText("does-not-match")
	if !m.list.IsFiltered() {
		t.Fatal("expected filter to be applied before mutation")
	}

	updated, _ := m.Update(sessionMutationMsg{kind: mutationConvert, row: newRow})
	got := asModel(t, updated)
	if got.list.IsFiltered() || got.list.FilterValue() != "" {
		t.Fatalf("filter was not reset; state=%s value=%q", got.list.FilterState(), got.list.FilterValue())
	}
	selected, ok := got.list.SelectedItem().(item)
	if !ok {
		t.Fatal("expected selected item after mutation")
	}
	if selected.row.ID != "new" {
		t.Fatalf("selected row = %q, want new", selected.row.ID)
	}
}

func TestBusyMutationBlocksSecondAction(t *testing.T) {
	row := session.Row{
		Provider:  session.ProviderClaude,
		ID:        "old",
		LastAt:    time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC),
		File:      "/tmp/old.jsonl",
		FirstUser: "old message",
	}
	newRow := session.Row{
		Provider:  session.ProviderCodex,
		ID:        "new",
		LastAt:    time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC),
		File:      "/tmp/new.jsonl",
		FirstUser: "new message",
	}

	updated, cmd := newModel([]session.Row{row}, firstMessage).Update(tea.KeyPressMsg(tea.Key{Code: 'x'}))
	if cmd == nil {
		t.Fatal("expected convert command")
	}
	busy := asModel(t, updated)
	if busy.busy != "conversion" {
		t.Fatalf("busy = %q, want conversion", busy.busy)
	}

	stillBusy, _ := busy.Update(tea.KeyPressMsg(tea.Key{Code: 'n'}))
	if got := asModel(t, stillBusy); got.busy != "conversion" {
		t.Fatalf("busy after second action = %q, want conversion", got.busy)
	}

	notResumed, _ := asModel(t, stillBusy).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if selectedFromModel(t, notResumed) != nil {
		t.Fatal("enter selected a session while mutation was busy")
	}

	done, _ := asModel(t, notResumed).Update(sessionMutationMsg{kind: mutationConvert, row: newRow})
	got := asModel(t, done)
	if got.busy != "" {
		t.Fatalf("busy after mutation = %q, want empty", got.busy)
	}
	selected, ok := got.list.SelectedItem().(item)
	if !ok || selected.row.ID != "new" {
		t.Fatalf("selected row after mutation = %#v, want new", got.list.SelectedItem())
	}
}

// TestDeleteArmClearsOnNavigation guards the two-press delete safety: arming a
// delete then moving the cursor must disarm it, so navigating away and back can
// never delete without a fresh confirmation.
func TestDeleteArmClearsOnNavigation(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "a", LastAt: time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC), File: "/tmp/a.jsonl", FirstUser: "alpha"},
		{Provider: session.ProviderCodex, ID: "b", LastAt: time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC), File: "/tmp/b.jsonl", FirstUser: "bravo"},
	}
	m := sizedModel(rows)

	armed, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	am := asModel(t, armed)
	if am.deleteArmed == "" {
		t.Fatal("first delete press did not arm confirmation")
	}

	moved, _ := am.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	mm := asModel(t, moved)
	if mm.deleteArmed != "" {
		t.Fatalf("delete confirmation survived navigation: %q", mm.deleteArmed)
	}
}

// TestPreviewKeysDoNotPage guards against the default list keymap binding
// f/l/b to paging: pressing them must change the preview mode (including the
// shared render state the delegate reads) without moving the cursor.
func TestPreviewKeysDoNotPage(t *testing.T) {
	rows := make([]session.Row, 0, 5)
	for i := 0; i < 5; i++ {
		rows = append(rows, session.Row{
			Provider:  session.ProviderCodex,
			ID:        string(rune('a' + i)),
			LastAt:    time.Date(2026, 6, 22, 10, i, 0, 0, time.UTC),
			File:      "/tmp/" + string(rune('a'+i)) + ".jsonl",
			FirstUser: "first",
			LastUser:  "last",
		})
	}
	m := sizedModel(rows)
	startIndex := m.list.Index()

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'l'}))
	got := asModel(t, updated)
	if got.mode != lastMessage {
		t.Fatalf("mode = %v, want lastMessage", got.mode)
	}
	if got.render.mode != lastMessage {
		t.Fatalf("render.mode = %v, want lastMessage (delegate would render the wrong column)", got.render.mode)
	}
	if got.list.Index() != startIndex {
		t.Fatalf("preview key paged the list: index %d -> %d", startIndex, got.list.Index())
	}
}

func TestSessionsLoadedTransition(t *testing.T) {
	m := newLoadingModel(firstMessage)
	if !m.loading {
		t.Fatal("loading model should start in loading state")
	}
	rows := []session.Row{
		{Provider: session.ProviderClaude, ID: "x", LastAt: time.Now(), File: "/tmp/x.jsonl", FirstUser: "hi"},
	}
	updated, _ := m.Update(sessionsLoadedMsg{rows: rows})
	got := asModel(t, updated)
	if got.loading {
		t.Fatal("model should leave loading state after sessionsLoadedMsg")
	}
	if n := sessionCount(got.list.VisibleItems()); n != 1 {
		t.Fatalf("sessions after load = %d, want 1", n)
	}
}

func TestProviderToggle(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "c1", LastAt: time.Now(), File: "/tmp/c1.jsonl"},
		{Provider: session.ProviderClaude, ID: "d1", LastAt: time.Now(), File: "/tmp/d1.jsonl"},
	}
	m := sizedModel(rows)
	if got := sessionCount(m.list.VisibleItems()); got != 2 {
		t.Fatalf("initial sessions = %d, want 2", got)
	}

	off, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c'}))
	got := asModel(t, off)
	if got.providers[session.ProviderCodex] {
		t.Fatal("codex should be disabled after toggle")
	}
	if n := sessionCount(got.list.VisibleItems()); n != 1 {
		t.Fatalf("filtered sessions = %d, want 1", n)
	}

	on, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: 'c'}))
	got = asModel(t, on)
	if !got.providers[session.ProviderCodex] || sessionCount(got.list.VisibleItems()) != 2 {
		t.Fatalf("codex not re-enabled: enabled=%v sessions=%d", got.providers[session.ProviderCodex], sessionCount(got.list.VisibleItems()))
	}
}

func TestProviderToggleKeepsLastProvider(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "c1", LastAt: time.Now(), File: "/tmp/c1.jsonl"},
	}
	m := sizedModel(rows)

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c'}))
	got := asModel(t, updated)
	if !got.providers[session.ProviderCodex] {
		t.Fatal("the only provider must stay enabled")
	}
	if n := sessionCount(got.list.VisibleItems()); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
}

func TestYoloToggleChangesResumeHint(t *testing.T) {
	row := session.Row{Provider: session.ProviderCodex, ID: "x", LastAt: time.Now(), File: "/tmp/x.jsonl", FirstUser: "msg"}
	m := sizedModel([]session.Row{row})
	if m.dangerous {
		t.Fatal("model should start with dangerous=false")
	}
	if !strings.Contains(m.resumeHint(row), "(normal)") {
		t.Fatalf("normal hint should mark normal mode: %q", m.resumeHint(row))
	}

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'y'}))
	got := asModel(t, updated)
	if !got.dangerous {
		t.Fatal("y did not enable dangerous mode")
	}
	if !strings.Contains(got.resumeHint(row), "yolo:") {
		t.Fatalf("yolo hint should describe yolo mode: %q", got.resumeHint(row))
	}

	back, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: 'y'}))
	if asModel(t, back).dangerous {
		t.Fatal("y did not toggle dangerous back off")
	}
}

func TestScopeCycling(t *testing.T) {
	row := session.Row{Provider: session.ProviderClaude, ID: "x", LastAt: time.Now(), File: "/tmp/x.jsonl", FirstUser: "msg"}
	m := sizedModel([]session.Row{row})
	if m.handoff.MaxTurns != 0 {
		t.Fatalf("initial scope MaxTurns = %d, want 0", m.handoff.MaxTurns)
	}

	for _, want := range []int{200, 100, 50, 20, 10, 0} {
		updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 't'}))
		m = asModel(t, updated)
		if m.handoff.MaxTurns != want {
			t.Fatalf("scope MaxTurns = %d, want %d", m.handoff.MaxTurns, want)
		}
	}
	if !strings.Contains(m.handoffHint(row), "all") {
		t.Fatalf("handoff hint after wrap = %q, want to contain 'all'", m.handoffHint(row))
	}
}

func TestHelpToggle(t *testing.T) {
	row := session.Row{Provider: session.ProviderClaude, ID: "x", LastAt: time.Now(), CWD: "/tmp", File: "/tmp/x.jsonl", FirstUser: "msg"}
	m := sizedModel([]session.Row{row})
	if m.help.ShowAll {
		t.Fatal("help should start collapsed")
	}

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '?'}))
	got := asModel(t, updated)
	if !got.help.ShowAll {
		t.Fatal("? did not expand help")
	}
	full := got.helpView()
	for _, want := range []string{"resume", "branch", "quit"} {
		if !strings.Contains(full, want) {
			t.Fatalf("full help missing %q:\n%s", want, full)
		}
	}

	back, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: '?'}))
	if asModel(t, back).help.ShowAll {
		t.Fatal("? did not collapse help again")
	}
}

func TestThemeRebuildOnBackgroundColor(t *testing.T) {
	m := sizedModel([]session.Row{{Provider: session.ProviderCodex, ID: "x", LastAt: time.Now(), File: "/tmp/x.jsonl"}})
	if !m.isDark {
		t.Fatal("model should default to a dark theme")
	}
	before := m.render.theme

	updated, _ := m.Update(tea.BackgroundColorMsg{Color: lipgloss.Color("#ffffff")})
	got := asModel(t, updated)
	if got.isDark {
		t.Fatal("white background should set isDark=false")
	}
	if got.render.theme == before {
		t.Fatal("theme was not rebuilt after background color change")
	}
}

func TestEscClearsAppliedFilter(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "a", LastAt: time.Now(), File: "/tmp/a.jsonl", FirstUser: "alpha"},
		{Provider: session.ProviderClaude, ID: "b", LastAt: time.Now(), File: "/tmp/b.jsonl", FirstUser: "bravo"},
	}
	m := sizedModel(rows)
	m.list.SetFilterText("alpha")
	if !m.list.IsFiltered() {
		t.Fatal("expected an applied filter")
	}

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	got := asModel(t, updated)
	if got.selected != nil {
		t.Fatal("esc on an applied filter quit instead of clearing the filter")
	}
	if got.list.IsFiltered() {
		t.Fatal("esc did not clear the applied filter")
	}
}

func TestGroupedItemsOrdering(t *testing.T) {
	// alpha has the newest session overall; beta is older. Within alpha, the
	// newer row must come first.
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "a-new", CWD: "/p/alpha", LastAt: time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC), File: "/t/a1.jsonl"},
		{Provider: session.ProviderClaude, ID: "b-new", CWD: "/p/beta", LastAt: time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC), File: "/t/b1.jsonl"},
		{Provider: session.ProviderCodex, ID: "a-old", CWD: "/p/alpha", LastAt: time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC), File: "/t/a2.jsonl"},
	}
	// rows must arrive globally newest-first (as Discover provides).
	items := groupedItems(rows)

	wantHeaders := []string{"/p/alpha", "/p/beta"}
	gotHeaders := []string{}
	gotRows := []string{}
	for _, it := range items {
		switch v := it.(type) {
		case headerItem:
			gotHeaders = append(gotHeaders, v.path)
		case item:
			gotRows = append(gotRows, v.row.ID)
		}
	}
	if strings.Join(gotHeaders, ",") != strings.Join(wantHeaders, ",") {
		t.Fatalf("group order = %v, want %v", gotHeaders, wantHeaders)
	}
	// alpha group: a-new before a-old; then beta: b-new
	if strings.Join(gotRows, ",") != "a-new,a-old,b-new" {
		t.Fatalf("row order = %v, want [a-new a-old b-new]", gotRows)
	}
	// first item is a header
	if _, ok := items[0].(headerItem); !ok {
		t.Fatalf("first item should be a group header, got %T", items[0])
	}
}

func TestInitialSelectionAndNavSkipHeaders(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "a1", CWD: "/p/alpha", LastAt: time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC), File: "/t/a1.jsonl", FirstUser: "x"},
		{Provider: session.ProviderClaude, ID: "b1", CWD: "/p/beta", LastAt: time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC), File: "/t/b1.jsonl", FirstUser: "y"},
		{Provider: session.ProviderClaude, ID: "b2", CWD: "/p/beta", LastAt: time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC), File: "/t/b2.jsonl", FirstUser: "z"},
	}
	m := sizedModel(rows)
	if _, ok := m.list.SelectedItem().(item); !ok {
		t.Fatalf("initial selection landed on a header: %T", m.list.SelectedItem())
	}

	// Walk down across the beta group header — the cursor must never rest on it.
	cur := m
	for i := 0; i < 5; i++ {
		updated, _ := cur.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		cur = asModel(t, updated)
		if _, isHeader := cur.list.SelectedItem().(headerItem); isHeader {
			t.Fatalf("cursor landed on a header after %d downs", i+1)
		}
	}
}

func TestCompoundChooserSelectsAgent(t *testing.T) {
	rows := []session.Row{{Provider: session.ProviderCodex, ID: "x", CWD: "/p/a", LastAt: time.Now(), File: "/t/x.jsonl", FirstUser: "hi"}}
	m := sizedModel(rows)

	opened, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'C'}))
	om := asModel(t, opened)
	if !om.compoundChoosing {
		t.Fatal("C did not open the compound chooser")
	}
	if selectedFromModel(t, om) != nil {
		t.Fatal("opening the chooser should not select yet")
	}

	chosen, _ := om.Update(tea.KeyPressMsg(tea.Key{Code: '2'}))
	sel := selectedFromModel(t, chosen)
	if sel == nil || sel.Action != ActionCompound {
		t.Fatalf("choice did not yield a compound selection: %#v", sel)
	}
	if sel.Agent != session.ProviderClaude {
		t.Fatalf("agent = %q, want claude", sel.Agent)
	}
	if sel.Row.ID != "x" {
		t.Fatalf("row = %q, want x", sel.Row.ID)
	}
}

func TestCompoundChooserCancel(t *testing.T) {
	rows := []session.Row{{Provider: session.ProviderCodex, ID: "x", CWD: "/p/a", LastAt: time.Now(), File: "/t/x.jsonl", FirstUser: "hi"}}
	m := sizedModel(rows)
	opened, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'C'}))
	cancelled, _ := asModel(t, opened).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	cm := asModel(t, cancelled)
	if cm.compoundChoosing {
		t.Fatal("esc did not close the compound chooser")
	}
	if selectedFromModel(t, cancelled) != nil {
		t.Fatal("cancelling the chooser must not select anything")
	}
}

func TestWindowSizeUpdatesListSize(t *testing.T) {
	rows := []session.Row{{Provider: session.ProviderCodex, ID: "x", LastAt: time.Now(), File: "/tmp/x.jsonl", FirstUser: "hi"}}
	m := newModel(rows, firstMessage)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	got := asModel(t, updated)
	if got.width != 120 || got.height != 40 {
		t.Fatalf("model size = %dx%d, want 120x40", got.width, got.height)
	}
	if got.list.Width() != 120 {
		t.Fatalf("list width = %d, want 120", got.list.Width())
	}
	if got.list.Height() <= 0 {
		t.Fatalf("list height = %d, want > 0", got.list.Height())
	}
}
