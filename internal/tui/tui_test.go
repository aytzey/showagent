package tui

import (
	"os"
	"path/filepath"
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
	m := newModel(rows)
	m.width = 110
	m.height = 36
	m.resizeList()
	return m
}

// withFakeCommands puts executable stand-ins for the named provider CLIs on a
// temp PATH, so availability checks stay hermetic no matter what the host has
// installed.
func withFakeCommands(t *testing.T, names ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
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

func TestRightCellsReturnsLongestSuffix(t *testing.T) {
	if got := rightCells("/a/b/c", 4); got != "/b/c" {
		t.Fatalf("rightCells = %q, want %q", got, "/b/c")
	}
	if got := rightCells("abc", 3); got != "abc" {
		t.Fatalf("exact-fit rightCells = %q, want %q", got, "abc")
	}
	if got := rightCells("abc", 0); got != "" {
		t.Fatalf("zero-width rightCells = %q, want empty", got)
	}
	// Wide runes count as two cells each.
	if got := rightCells("日本語", 4); got != "本語" {
		t.Fatalf("wide-rune rightCells = %q, want %q", got, "本語")
	}
	if got := rightCells("日本語", 3); got != "語" {
		t.Fatalf("odd-width wide-rune rightCells = %q, want %q", got, "語")
	}
}

func TestTruncateMiddleKeepsBasename(t *testing.T) {
	value := "/home/user/projects/some-extremely-long-workspace-name/repo"
	width := 24
	got := truncateMiddle(value, width)
	if !strings.HasSuffix(got, "/repo") {
		t.Fatalf("truncateMiddle = %q, basename lost", got)
	}
	if w := lipgloss.Width(got); w > width {
		t.Fatalf("truncateMiddle = %q, width %d exceeds %d", got, w, width)
	}
}

func TestTruncateMiddleFavorsSuffix(t *testing.T) {
	// Sibling workspaces that differ only near the tail must stay
	// distinguishable after truncation.
	a := truncateMiddle("/home/user/code/monorepo/services/api-server", 30)
	b := truncateMiddle("/home/user/code/monorepo/services/web-client", 30)
	if a == b {
		t.Fatalf("sibling paths truncate identically: %q", a)
	}
	if !strings.HasSuffix(a, "api-server") || !strings.HasSuffix(b, "web-client") {
		t.Fatalf("basenames lost: %q / %q", a, b)
	}
}

func TestTruncateMiddleEdgeCases(t *testing.T) {
	if got := truncateMiddle("abc", 3); got != "abc" {
		t.Fatalf("exact-fit truncateMiddle = %q, want %q", got, "abc")
	}
	// Width smaller than the ellipsis falls back to a hard cut.
	if got := truncateMiddle("abcdef", 2); got != "ab" {
		t.Fatalf("tiny-width truncateMiddle = %q, want %q", got, "ab")
	}
	if got := truncateMiddle("abcdef", 0); got != "" {
		t.Fatalf("zero-width truncateMiddle = %q, want empty", got)
	}
	// Wide runes must not overflow the target width.
	wide := "/日本語/日本語/日本語/basename"
	got := truncateMiddle(wide, 20)
	if w := lipgloss.Width(got); w > 20 {
		t.Fatalf("wide-rune truncateMiddle = %q, width %d exceeds 20", got, w)
	}
	if !strings.HasSuffix(got, "basename") {
		t.Fatalf("wide-rune truncateMiddle = %q, basename lost", got)
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

	m := newModel(rows)
	m.width = 100
	m.height = 32
	m.resizeList()
	detail := m.detailView()
	if got := lipgloss.Width(detail); got > m.width {
		t.Fatalf("detail width = %d, want <= %d\n%s", got, m.width, detail)
	}
	// Long values must be truncated, never wrapped: a wrapped line would make
	// the panel taller than the height budget and push the help bar off-screen.
	if got := lipgloss.Height(detail); got != m.detailHeight() {
		t.Fatalf("detail height = %d, want exactly the %d-row budget\n%s", got, m.detailHeight(), detail)
	}
}

func TestEnterAndCtrlMSelectResume(t *testing.T) {
	withFakeCommands(t, "claude")
	row := session.Row{
		Provider: session.ProviderClaude,
		ID:       "resume-id",
		LastAt:   time.Now(),
		CWD:      t.TempDir(),
		File:     "/tmp/resume.jsonl",
	}

	tests := []tea.KeyPressMsg{
		tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}),
		tea.KeyPressMsg(tea.Key{Code: 'm', Mod: tea.ModCtrl}),
	}
	for _, msg := range tests {
		updated, _ := newModel([]session.Row{row}).Update(msg)
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
	// Hand-off candidates require the target CLI on PATH, so provide a fake
	// codex binary; discovery must not depend on what the host has installed.
	withFakeCommands(t, "codex")

	updated, cmd := newModel([]session.Row{row}).Update(tea.KeyPressMsg(tea.Key{Code: 'x'}))
	if cmd == nil {
		t.Fatal("expected preview command")
	}
	busy := asModel(t, updated)
	if busy.busy != "preview" {
		t.Fatalf("busy = %q, want preview", busy.busy)
	}

	stillBusy, _ := busy.Update(tea.KeyPressMsg(tea.Key{Code: 'n'}))
	if got := asModel(t, stillBusy); got.busy != "preview" {
		t.Fatalf("busy after second action = %q, want preview", got.busy)
	}

	notResumed, _ := asModel(t, stillBusy).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if selectedFromModel(t, notResumed) != nil {
		t.Fatal("enter selected a session while mutation was busy")
	}

	previewed, _ := asModel(t, notResumed).Update(conversionPreviewMsg{
		row:     row,
		target:  session.ProviderCodex,
		options: session.HandoffOptions{},
		preview: session.ConversionPreview{
			SourceProvider: session.ProviderClaude,
			SourceID:       row.ID,
			TargetProvider: session.ProviderCodex,
			Scope:          "all",
			TransferTurns:  1,
			Dropped:        []string{"tool calls"},
		},
	})
	pm := asModel(t, previewed)
	if pm.busy != "" || pm.pendingConvert == nil {
		t.Fatalf("preview did not arm conversion confirmation: busy=%q pending=%#v", pm.busy, pm.pendingConvert)
	}

	converting, cmd := pm.Update(tea.KeyPressMsg(tea.Key{Code: 'x'}))
	if cmd == nil {
		t.Fatal("second x should start conversion")
	}
	cm := asModel(t, converting)
	if cm.busy != "conversion" {
		t.Fatalf("busy after confirm = %q, want conversion", cm.busy)
	}

	done, _ := cm.Update(sessionMutationMsg{kind: mutationConvert, row: newRow})
	got := asModel(t, done)
	if got.busy != "" {
		t.Fatalf("busy after mutation = %q, want empty", got.busy)
	}
	selected, ok := got.list.SelectedItem().(item)
	if !ok || selected.row.ID != "new" {
		t.Fatalf("selected row after mutation = %#v, want new", got.list.SelectedItem())
	}
}

func TestConversionPreviewRendersAndEscCancels(t *testing.T) {
	withFakeCommands(t, "claude")
	row := session.Row{
		Provider:  session.ProviderCodex,
		ID:        "source",
		LastAt:    time.Now(),
		CWD:       "/p/a",
		File:      "/tmp/source.jsonl",
		FirstUser: "hello",
	}
	m := sizedModel([]session.Row{row})
	previewed, _ := m.Update(conversionPreviewMsg{
		row:     row,
		target:  session.ProviderClaude,
		options: session.HandoffOptions{MaxTurns: 20},
		preview: session.ConversionPreview{
			SourceProvider: session.ProviderCodex,
			SourceID:       "source",
			TargetProvider: session.ProviderClaude,
			Scope:          "last 20",
			TransferTurns:  3,
			LastUser:       "fix the bug",
			Dropped:        []string{"tool calls", "runtime state"},
		},
	})
	got := asModel(t, previewed)
	if got.pendingConvert == nil {
		t.Fatal("preview message did not set pending conversion")
	}
	detail := got.detailView()
	for _, want := range []string{"codex", "claude", "last 20", "fix the bug", "press x again"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail missing %q:\n%s", want, detail)
		}
	}

	cancelled, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if asModel(t, cancelled).pendingConvert != nil {
		t.Fatal("esc did not clear conversion preview")
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

// TestPreviewCycle checks the single 'p' key cycles first -> latest -> both ->
// first, keeps the shared render state the delegate reads in sync, and never
// moves the cursor.
func TestPreviewCycle(t *testing.T) {
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

	for _, want := range []previewMode{lastMessage, bothMessages, firstMessage} {
		updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'p'}))
		m = asModel(t, updated)
		if m.mode != want {
			t.Fatalf("mode = %v, want %v", m.mode, want)
		}
		if m.render.mode != want {
			t.Fatalf("render.mode = %v, want %v (delegate would render the wrong column)", m.render.mode, want)
		}
	}
	if m.list.Index() != startIndex {
		t.Fatalf("preview key paged the list: index %d -> %d", startIndex, m.list.Index())
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

// TestProviderToggle drives the index-based filter keys: '1' toggles the first
// discovered provider (codex), '2' the second (claude), '3' the third (jcode),
// following session.ProviderOrder over the providers that actually have rows.
func TestProviderToggle(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "c1", LastAt: time.Now(), File: "/tmp/c1.jsonl"},
		{Provider: session.ProviderClaude, ID: "d1", LastAt: time.Now(), File: "/tmp/d1.jsonl"},
		{Provider: session.ProviderJCode, ID: "j1", LastAt: time.Now(), File: "/tmp/j1.json"},
	}
	m := sizedModel(rows)
	if got := sessionCount(m.list.VisibleItems()); got != 3 {
		t.Fatalf("initial sessions = %d, want 3", got)
	}

	off, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '1'}))
	got := asModel(t, off)
	if got.providers[session.ProviderCodex] {
		t.Fatal("codex should be disabled after toggle")
	}
	if n := sessionCount(got.list.VisibleItems()); n != 2 {
		t.Fatalf("filtered sessions = %d, want 2", n)
	}

	on, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: '1'}))
	got = asModel(t, on)
	if !got.providers[session.ProviderCodex] || sessionCount(got.list.VisibleItems()) != 3 {
		t.Fatalf("codex not re-enabled: enabled=%v sessions=%d", got.providers[session.ProviderCodex], sessionCount(got.list.VisibleItems()))
	}

	coff, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: '2'}))
	got = asModel(t, coff)
	if got.providers[session.ProviderClaude] {
		t.Fatal("claude should be disabled after '2' toggle")
	}

	joff, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: '3'}))
	got = asModel(t, joff)
	if got.providers[session.ProviderJCode] {
		t.Fatal("jcode should be disabled after '3' toggle")
	}
	if n := sessionCount(got.list.VisibleItems()); n != 1 {
		t.Fatalf("filtered sessions after claude+jcode toggles = %d, want 1", n)
	}

	// A digit with no discovered provider behind it must not blank the list.
	noop, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: '9'}))
	got = asModel(t, noop)
	if n := sessionCount(got.list.VisibleItems()); n != 1 {
		t.Fatalf("sessions after unbound digit = %d, want 1", n)
	}
}

func TestProviderToggleKeepsLastProvider(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "c1", LastAt: time.Now(), File: "/tmp/c1.jsonl"},
	}
	m := sizedModel(rows)

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '1'}))
	got := asModel(t, updated)
	if !got.providers[session.ProviderCodex] {
		t.Fatal("the only provider must stay enabled")
	}
	if n := sessionCount(got.list.VisibleItems()); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
}

func TestYoloToggleChangesResumeHint(t *testing.T) {
	withFakeCommands(t, "codex")
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

func TestPiDangerousResumeHintDoesNotClaimUnsupportedBypass(t *testing.T) {
	withFakeCommands(t, "pi")
	row := session.Row{Provider: session.ProviderPi, ID: "pi-session", LastAt: time.Now(), File: "/tmp/pi.jsonl"}
	m := sizedModel([]session.Row{row})
	m.dangerous = true
	hint := m.resumeHint(row)
	if !strings.Contains(hint, "no extra pi flag") || strings.Contains(hint, "bypasses approvals") {
		t.Fatalf("Pi dangerous hint = %q", hint)
	}
}

func TestHandoffTargetCycling(t *testing.T) {
	// Only providers whose CLI is on PATH qualify as hand-off targets.
	withFakeCommands(t, "claude", "jcode")
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "c1", LastAt: time.Now(), File: "/tmp/c1.jsonl", FirstUser: "codex"},
		{Provider: session.ProviderClaude, ID: "d1", LastAt: time.Now().Add(-time.Minute), File: "/tmp/d1.jsonl", FirstUser: "claude"},
		{Provider: session.ProviderJCode, ID: "j1", LastAt: time.Now().Add(-2 * time.Minute), File: "/tmp/j1.json", FirstUser: "jcode"},
	}
	m := sizedModel(rows)
	selected, ok := m.list.SelectedItem().(item)
	if !ok || selected.row.Provider != session.ProviderCodex {
		t.Fatalf("expected codex selected, got %#v", m.list.SelectedItem())
	}
	if got := m.handoffTargetFor(selected.row); got != session.ProviderClaude {
		t.Fatalf("default target = %q, want claude", got)
	}

	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o'}))
	got := asModel(t, updated)
	if got.handoffTarget != session.ProviderJCode {
		t.Fatalf("cycled target = %q, want jcode", got.handoffTarget)
	}
	if !strings.Contains(got.handoffHint(selected.row), "jcode") {
		t.Fatalf("handoff hint missing jcode target: %q", got.handoffHint(selected.row))
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
	items := groupedItems(rows, nil)

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

func TestCategoryHeadersCanCollapseAndExpand(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "a1", CWD: "/p/alpha", LastAt: time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC), File: "/t/a1.jsonl", FirstUser: "x"},
		{Provider: session.ProviderClaude, ID: "b1", CWD: "/p/beta", LastAt: time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC), File: "/t/b1.jsonl", FirstUser: "y"},
		{Provider: session.ProviderClaude, ID: "b2", CWD: "/p/beta", LastAt: time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC), File: "/t/b2.jsonl", FirstUser: "z"},
	}
	m := sizedModel(rows)
	if _, ok := m.list.SelectedItem().(item); !ok {
		t.Fatalf("initial selection landed on a header: %T", m.list.SelectedItem())
	}

	onHeader, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	got := asModel(t, onHeader)
	header, ok := got.list.SelectedItem().(headerItem)
	if !ok || header.path != "/p/beta" {
		t.Fatalf("down should land on beta header, got %#v", got.list.SelectedItem())
	}

	collapsed, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got = asModel(t, collapsed)
	header, ok = got.list.SelectedItem().(headerItem)
	if !ok || !header.collapsed {
		t.Fatalf("enter should collapse selected header, got %#v", got.list.SelectedItem())
	}
	if n := sessionCount(got.list.VisibleItems()); n != 1 {
		t.Fatalf("visible sessions after collapse = %d, want 1", n)
	}

	expanded, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeySpace}))
	got = asModel(t, expanded)
	header, ok = got.list.SelectedItem().(headerItem)
	if !ok || header.collapsed {
		t.Fatalf("space should expand selected header, got %#v", got.list.SelectedItem())
	}
	if n := sessionCount(got.list.VisibleItems()); n != 3 {
		t.Fatalf("visible sessions after expand = %d, want 3", n)
	}
}

func TestCollapsedCategoriesAreSearchable(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "a1", CWD: "/p/alpha", LastAt: time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC), File: "/t/a1.jsonl", FirstUser: "alpha"},
		{Provider: session.ProviderClaude, ID: "b1", CWD: "/p/beta", LastAt: time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC), File: "/t/b1.jsonl", FirstUser: "needle"},
	}
	m := sizedModel(rows)

	onHeader, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	collapsed, _ := asModel(t, onHeader).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := asModel(t, collapsed)
	if n := sessionCount(got.list.VisibleItems()); n != 1 {
		t.Fatalf("visible sessions after collapse = %d, want 1", n)
	}

	searching, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: '/'}))
	got = asModel(t, searching)
	if n := sessionCount(got.list.VisibleItems()); n != 2 {
		t.Fatalf("sessions available after starting search = %d, want 2", n)
	}
}

func TestCompoundChooserSelectsAgent(t *testing.T) {
	withFakeCommands(t, "claude")
	rows := []session.Row{{Provider: session.ProviderCodex, ID: "x", CWD: t.TempDir(), LastAt: time.Now(), File: "/t/x.jsonl", FirstUser: "hi"}}
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
	m := newModel(rows)

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

// TestDKeyArmsAndConfirmsDelete covers the 'd' alias: the first press arms the
// same two-press confirmation as delete/backspace, the second press deletes.
func TestDKeyArmsAndConfirmsDelete(t *testing.T) {
	file := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := []session.Row{{Provider: session.ProviderClaude, ID: "x", CWD: "/p/a", LastAt: time.Now(), File: file, FirstUser: "hi"}}
	m := sizedModel(rows)

	armed, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	am := asModel(t, armed)
	if am.deleteArmed == "" {
		t.Fatal("'d' did not arm the delete confirmation")
	}

	deleting, cmd := am.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	dm := asModel(t, deleting)
	if cmd == nil || dm.busy != "delete" {
		t.Fatalf("second 'd' did not start async delete: busy=%q cmd=%v", dm.busy, cmd)
	}
	if len(dm.allRows) != 1 {
		t.Fatalf("row changed before async delete completed: %d rows", len(dm.allRows))
	}
	if err := session.Delete(rows[0]); err != nil {
		t.Fatal(err)
	}
	deleted, _ := dm.Update(sessionDeleteMsg{row: rows[0]})
	done := asModel(t, deleted)
	if done.busy != "" || len(done.allRows) != 0 {
		t.Fatalf("delete completion left busy=%q rows=%d", done.busy, len(done.allRows))
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("session file still exists after delete: %v", err)
	}
}

// TestEscDoesNotQuit: esc clears overlays and search but must never quit the
// picker; only q and ctrl+c do.
func TestEscDoesNotQuit(t *testing.T) {
	rows := []session.Row{{Provider: session.ProviderCodex, ID: "x", CWD: "/p/a", LastAt: time.Now(), File: "/t/x.jsonl", FirstUser: "hi"}}
	m := sizedModel(rows)

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if cmd != nil {
		t.Fatalf("esc on the main list produced a command: %#v", cmd())
	}
	if selectedFromModel(t, updated) != nil {
		t.Fatal("esc selected a session")
	}

	// esc collapses the '?' overlay instead of quitting.
	helped, _ := asModel(t, updated).Update(tea.KeyPressMsg(tea.Key{Code: '?'}))
	hm := asModel(t, helped)
	if !hm.help.ShowAll {
		t.Fatal("? did not expand help")
	}
	closed, cmd := hm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	cm := asModel(t, closed)
	if cm.help.ShowAll {
		t.Fatal("esc did not collapse the help overlay")
	}
	if cmd != nil {
		t.Fatalf("esc on the help overlay produced a command: %#v", cmd())
	}

	// q still quits.
	_, quitCmd := cm.Update(tea.KeyPressMsg(tea.Key{Code: 'q'}))
	if quitCmd == nil {
		t.Fatal("q did not quit")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatalf("q produced %#v, want tea.QuitMsg", quitCmd())
	}
}

// TestRescanPreservesCursorAndFilters: 'r' re-fires session discovery and the
// reload keeps both the cursor row and the provider filter.
func TestRescanPreservesCursorAndFilters(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "a", CWD: "/p/a", LastAt: time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC), File: "/t/a.jsonl", FirstUser: "alpha"},
		{Provider: session.ProviderCodex, ID: "b", CWD: "/p/a", LastAt: time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC), File: "/t/b.jsonl", FirstUser: "bravo"},
		{Provider: session.ProviderClaude, ID: "c", CWD: "/p/b", LastAt: time.Date(2026, 6, 22, 10, 0, 0, 0, time.UTC), File: "/t/c.jsonl", FirstUser: "charlie"},
	}
	m := sizedModel(rows)

	// Hide claude, then move the cursor to the second codex row.
	filtered, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '2'}))
	m = asModel(t, filtered)
	moved, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = asModel(t, moved)
	selected, ok := m.list.SelectedItem().(item)
	if !ok || selected.row.ID != "b" {
		t.Fatalf("cursor setup failed: %#v", m.list.SelectedItem())
	}

	rescanning, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r'}))
	if cmd == nil {
		t.Fatal("'r' did not produce a rescan command")
	}
	rm := asModel(t, rescanning)
	if !rm.rescanning {
		t.Fatal("'r' did not mark the model as rescanning")
	}
	if rm.busy != "rescan" {
		t.Fatalf("rescan busy = %q, want rescan", rm.busy)
	}
	blocked, _ := rm.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	if got := asModel(t, blocked); got.deleteArmed != "" || got.busy != "rescan" {
		t.Fatalf("mutation was not blocked during rescan: busy=%q armed=%q", got.busy, got.deleteArmed)
	}

	reloaded, _ := rm.Update(sessionsLoadedMsg{rows: rows})
	got := asModel(t, reloaded)
	if got.busy != "" || got.rescanning {
		t.Fatalf("rescan completion left busy=%q rescanning=%v", got.busy, got.rescanning)
	}
	if got.providers[session.ProviderClaude] {
		t.Fatal("rescan re-enabled a provider the user had hidden")
	}
	selected, ok = got.list.SelectedItem().(item)
	if !ok || selected.row.ID != "b" {
		t.Fatalf("rescan lost the cursor: %#v", got.list.SelectedItem())
	}
}

// TestResumeValidationKeepsTUIAlive: enter on a session whose CLI is missing
// must show a status line instead of quitting into a doomed exec.
func TestResumeValidationKeepsTUIAlive(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	rows := []session.Row{{Provider: session.ProviderCodex, ID: "x", CWD: t.TempDir(), LastAt: time.Now(), File: "/t/x.jsonl", FirstUser: "hi"}}
	m := sizedModel(rows)

	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if selectedFromModel(t, updated) != nil {
		t.Fatal("enter selected a session although its CLI is missing")
	}
	if cmd == nil {
		t.Fatal("expected a status-message command explaining the failure")
	}
}

// TestCompoundChooserRejectsMissingAgent: picking an agent whose CLI is not on
// PATH keeps the chooser open and explains the problem.
func TestCompoundChooserRejectsMissingAgent(t *testing.T) {
	withFakeCommands(t, "claude")
	rows := []session.Row{{Provider: session.ProviderClaude, ID: "x", CWD: t.TempDir(), LastAt: time.Now(), File: "/t/x.jsonl", FirstUser: "hi"}}
	m := sizedModel(rows)

	opened, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'C'}))
	om := asModel(t, opened)
	chosen, _ := om.Update(tea.KeyPressMsg(tea.Key{Code: '1'})) // codex, not installed
	cm := asModel(t, chosen)
	if selectedFromModel(t, chosen) != nil {
		t.Fatal("missing agent still produced a selection")
	}
	if !cm.compoundChoosing {
		t.Fatal("chooser closed although the pick was rejected")
	}
	if !strings.Contains(cm.compoundNotice, "codex") {
		t.Fatalf("notice should name the missing CLI: %q", cm.compoundNotice)
	}
}

// TestHelpBarVisibleAt80x24 renders the full browse view at a small terminal
// size with long workspace paths and asserts the help bar keeps its row.
func TestHelpBarVisibleAt80x24(t *testing.T) {
	long := "/home/user/projects/an-extremely-long-workspace-directory-name/nested/deeper/repo"
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "019eee0c-9361-7330-b0f4-b887cbe7fab6", CWD: long, LastAt: time.Now(), File: long + "/s.jsonl", FirstUser: strings.Repeat("long first message ", 20), LastUser: strings.Repeat("long last message ", 20)},
		{Provider: session.ProviderClaude, ID: "y", CWD: long + "-two", LastAt: time.Now(), File: "/t/y.jsonl", FirstUser: "hi"},
	}
	m := newModel(rows)
	m.width = 80
	m.height = 24
	m.resizeList()

	view := m.browseView()
	if got := lipgloss.Height(view); got > 24 {
		t.Fatalf("browse view height = %d, want <= 24", got)
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if w := lipgloss.Width(line); w > 80 {
			t.Fatalf("line %d width = %d > 80 (would wrap and push the help bar off-screen): %q", i, w, line)
		}
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "resume") {
		t.Fatalf("last line is not the help bar: %q", last)
	}
}

func TestViewsNeverOverflowNarrowTerminal(t *testing.T) {
	row := session.Row{
		Provider:  session.ProviderCodex,
		ID:        "019eee0c-9361-7330-b0f4-b887cbe7fab6",
		CWD:       "/home/user/projects/a-very-long-workspace-name/repo",
		LastAt:    time.Now(),
		File:      "/home/user/.codex/sessions/a-very-long-file.jsonl",
		FirstUser: strings.Repeat("long message ", 20),
	}
	for _, width := range []int{16, 24, 32, 39} {
		m := newModel([]session.Row{row})
		m.width = width
		m.height = 24
		m.resizeList()
		for name, view := range map[string]string{
			"browse":   m.browseView(),
			"compound": func() string { m.compoundRow = &row; return m.compoundView() }(),
		} {
			for index, line := range strings.Split(view, "\n") {
				if got := lipgloss.Width(line); got > width {
					t.Fatalf("%s width %d line %d overflows: %d cells: %q", name, width, index, got, line)
				}
			}
		}
	}
}

// TestHelpShowsProviderStateAndTargets: the help content must surface provider
// filter on/off state plus the current transfer target and scope.
func TestHelpShowsProviderStateAndTargets(t *testing.T) {
	withFakeCommands(t, "claude")
	rows := []session.Row{
		{Provider: session.ProviderCodex, ID: "c1", CWD: "/p/a", LastAt: time.Now(), File: "/t/c1.jsonl", FirstUser: "hi"},
		{Provider: session.ProviderClaude, ID: "d1", CWD: "/p/b", LastAt: time.Now().Add(-time.Minute), File: "/t/d1.jsonl", FirstUser: "hi"},
	}
	m := sizedModel(rows)
	m.help.ShowAll = true
	m.help.SetWidth(200)

	full := m.helpView()
	for _, want := range []string{"codex:on", "claude:on", "target:claude", "scope:all", "preview:first"} {
		if !strings.Contains(full, want) {
			t.Fatalf("full help missing %q:\n%s", want, full)
		}
	}

	toggled, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '2'}))
	tm := asModel(t, toggled)
	if !strings.Contains(tm.helpView(), "claude:off") {
		t.Fatalf("help does not reflect the disabled provider:\n%s", tm.helpView())
	}
}

// TestEmptyViewListsScannedDirs: the empty state names the scanned directories
// and their env overrides, and points at 'r' to rescan.
func TestEmptyViewListsScannedDirs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "jcode"))
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "empty-pi"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")

	m := sizedModel(nil)
	view := m.emptyView()
	for _, want := range []string{
		filepath.Join(root, "codex", "sessions"),
		filepath.Join(root, "claude", "projects"),
		"CODEX_HOME", "CLAUDE_HOME", "JCODE_HOME", "OPENCODE_DATA_HOME", "GEMINI_CLI_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR",
		"press r to rescan",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("empty view missing %q:\n%s", want, view)
		}
	}
}
