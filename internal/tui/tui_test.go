package tui

import (
	"testing"
	"time"

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
