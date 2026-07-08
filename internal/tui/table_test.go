package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aytzey/showagent/internal/session"
)

func TestPrintTableIncludesFullSessionID(t *testing.T) {
	rows := []session.Row{
		{
			Provider:  session.ProviderCodex,
			ID:        "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb",
			LastAt:    time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
			CWD:       "/work/codex",
			FirstUser: "first codex message",
		},
	}

	var buf bytes.Buffer
	PrintTable(&buf, 120, rows)
	output := buf.String()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header + 1 row, got %d lines:\n%s", len(lines), output)
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "ID") {
		t.Fatalf("header must start with ID column: %q", lines[0])
	}
	if !strings.Contains(lines[1], "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb") {
		t.Fatalf("row must contain the full session id:\n%s", output)
	}
	if !strings.Contains(lines[1], "first codex message") {
		t.Fatalf("row must contain the first user message:\n%s", output)
	}
}

func TestPrintTableFallsBackToDefaultWidth(t *testing.T) {
	rows := []session.Row{
		{
			Provider: session.ProviderClaude,
			ID:       "cccccccc-1111-2222-3333-dddddddddddd",
			LastAt:   time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
			CWD:      strings.Repeat("/deep", 40),
		},
	}

	var zero, fallback bytes.Buffer
	PrintTable(&zero, 0, rows)
	PrintTable(&fallback, 120, rows)
	if zero.String() != fallback.String() {
		t.Fatal("width <= 0 must render the same table as the 120-column fallback")
	}
}

func TestPrintTableRespectsWidth(t *testing.T) {
	rows := []session.Row{
		{
			Provider:  session.ProviderCodex,
			ID:        "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb",
			LastAt:    time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
			CWD:       "/work/codex",
			FirstUser: strings.Repeat("long message ", 40),
		},
	}

	var narrow, wide bytes.Buffer
	PrintTable(&narrow, 100, rows)
	PrintTable(&wide, 200, rows)

	narrowRow := strings.Split(strings.TrimRight(narrow.String(), "\n"), "\n")[1]
	wideRow := strings.Split(strings.TrimRight(wide.String(), "\n"), "\n")[1]
	if len(narrowRow) >= len(wideRow) {
		t.Fatalf("a wider terminal must show more preview text: narrow=%d wide=%d", len(narrowRow), len(wideRow))
	}
	if len(wideRow) > 200 {
		t.Fatalf("row exceeds requested width: %d > 200", len(wideRow))
	}
}
