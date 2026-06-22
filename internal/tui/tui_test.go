package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/aytzey/showcodex/internal/session"
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
	header := tableLine(width, "SRC", "LAST", "CWD", "USER MESSAGE")
	row := tableLine(width, "codex", "2026-06-22 10:24", "/home/aytug", "preview")

	if lipgloss.Width(header) != width {
		t.Fatalf("header width = %d, want %d", lipgloss.Width(header), width)
	}
	if lipgloss.Width(row) != width {
		t.Fatalf("row width = %d, want %d", lipgloss.Width(row), width)
	}
	if strings.Index(header, "CWD") != strings.Index(row, "/home/aytug") {
		t.Fatalf("cwd column mismatch:\n%q\n%q", header, row)
	}
	if strings.Index(header, "USER MESSAGE") != strings.Index(row, "preview") {
		t.Fatalf("preview column mismatch:\n%q\n%q", header, row)
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
