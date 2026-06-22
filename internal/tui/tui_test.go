package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/aytzey/showagent/internal/session"
)

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

func TestTableLineAlignsHeaderAndRows(t *testing.T) {
	width := 96
	header := tableLine(width, "AGENT", "UPDATED", "WORKSPACE", "USER MESSAGE")
	row := tableLine(width, "codex", "2026-06-22 10:24", "/home/aytug", "preview")

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
	width := 118
	row := session.Row{
		Provider:  session.ProviderClaude,
		ID:        "id",
		LastAt:    time.Date(2026, 6, 22, 10, 24, 0, 0, time.Local),
		CWD:       "/projects/Machinity-Kanban",
		FirstUser: strings.Repeat("preview ", 30),
	}

	if got := lipgloss.Width(renderTableRow(width, row, firstMessage, false)); got != width {
		t.Fatalf("renderTableRow width = %d, want %d", got, width)
	}
	if got := lipgloss.Width(renderTableRow(width, row, firstMessage, true)); got != width {
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
	detail := detailView(m)
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

	m := newModel([]session.Row{old}, firstMessage)
	m.width = 100
	m.height = 32
	m.resizeList()
	m.list.SetFilterText("does-not-match")
	if !m.list.IsFiltered() {
		t.Fatal("expected filter to be applied before mutation")
	}

	updated, _ := m.Update(sessionMutationMsg{kind: mutationConvert, row: newRow})
	got := updated.(model)
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
	busy := updated.(model)
	if busy.busy != "conversion" {
		t.Fatalf("busy = %q, want conversion", busy.busy)
	}

	stillBusy, _ := busy.Update(tea.KeyPressMsg(tea.Key{Code: 'n'}))
	if got := stillBusy.(model); got.busy != "conversion" {
		t.Fatalf("busy after second action = %q, want conversion", got.busy)
	}

	notResumed, _ := stillBusy.(model).Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if selectedFromModel(t, notResumed) != nil {
		t.Fatal("enter selected a session while mutation was busy")
	}

	done, _ := notResumed.(model).Update(sessionMutationMsg{kind: mutationConvert, row: newRow})
	got := done.(model)
	if got.busy != "" {
		t.Fatalf("busy after mutation = %q, want empty", got.busy)
	}
	selected, ok := got.list.SelectedItem().(item)
	if !ok || selected.row.ID != "new" {
		t.Fatalf("selected row after mutation = %#v, want new", got.list.SelectedItem())
	}
}
