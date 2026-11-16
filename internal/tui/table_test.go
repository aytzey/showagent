package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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

// TestPrintTableKeepsLongAgentNamesAligned: an agent name longer than the
// picker's badge column must print in full without pushing its row out of line.
func TestPrintTableKeepsLongAgentNamesAligned(t *testing.T) {
	at := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	rows := []session.Row{
		{Provider: session.ProviderAntigravity, ID: "aaaaaaaa", LastAt: at, CWD: "/work/a", FirstUser: "first antigravity message"},
		{Provider: session.ProviderPi, ID: "bbbbbbbb", LastAt: at, CWD: "/work/b", FirstUser: "first pi message"},
	}

	var buf bytes.Buffer
	PrintTable(&buf, 120, rows)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d lines:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[1], " antigravity ") {
		t.Fatalf("agent name must be printed in full:\n%s", buf.String())
	}
	for _, column := range []struct{ header, first, second string }{
		{"UPDATED", "2026-", "2026-"},
		{"WORKSPACE", "/work/a", "/work/b"},
		{"USER MESSAGE", "first antigravity", "first pi"},
	} {
		want := strings.Index(lines[0], column.header)
		if got := strings.Index(lines[1], column.first); got != want {
			t.Fatalf("%s column starts at %d in row 1, want %d:\n%s", column.header, got, want, buf.String())
		}
		if got := strings.Index(lines[2], column.second); got != want {
			t.Fatalf("%s column starts at %d in row 2, want %d:\n%s", column.header, got, want, buf.String())
		}
	}
}

// TestProviderBadgeLabels: every registered agent gets a readable badge in the
// fixed-width column, and a name that does not fit is cut, never ellipsized.
func TestProviderBadgeLabels(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	th := newTheme(true)
	want := map[session.Provider]string{
		session.ProviderCodex:       " CODEX  ",
		session.ProviderClaude:      " CLAUDE ",
		session.ProviderJCode:       " JCODE  ",
		session.ProviderOpenCode:    "OPENCODE",
		session.ProviderMiMo:        "MIMOCODE",
		session.ProviderGemini:      " GEMINI ",
		session.ProviderPi:          "   PI   ",
		session.ProviderAntigravity: "ANTIGRAV",
	}
	for _, provider := range session.ProviderOrder() {
		label, ok := want[provider]
		if !ok {
			t.Fatalf("no expected badge label for %q: add one to this test", provider)
		}
		if got := providerPlainLabel(th, string(provider), tableProviderWidth); got != label {
			t.Errorf("%s badge label = %q, want %q", provider, got, label)
		}
		if got := ansi.Strip(providerBadge(th, string(provider), tableProviderWidth)); got != label {
			t.Errorf("%s rendered badge = %q, want %q", provider, got, label)
		}
		for width := 1; width <= 14; width++ {
			got := providerPlainLabel(th, string(provider), width)
			if lipgloss.Width(got) != width || strings.Contains(got, ".") {
				t.Errorf("%s badge at width %d = %q", provider, width, got)
			}
		}
	}
}

// TestProviderBadgeLabelsWithoutColor: with NO_COLOR a badge is plain bold
// text, so it lines up on the left like every other column.
func TestProviderBadgeLabelsWithoutColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	th := newTheme(true)
	for provider, want := range map[session.Provider]string{
		session.ProviderCodex:       "CODEX   ",
		session.ProviderPi:          "PI      ",
		session.ProviderOpenCode:    "OPENCODE",
		session.ProviderAntigravity: "ANTIGRAV",
	} {
		if got := providerPlainLabel(th, string(provider), tableProviderWidth); got != want {
			t.Errorf("%s plain badge label = %q, want %q", provider, got, want)
		}
	}
}

// TestEveryProviderHasItsOwnBadgeColor: a badge color identifies the agent, so
// no two registered agents may share one on either background.
func TestEveryProviderHasItsOwnBadgeColor(t *testing.T) {
	light, dark := map[string]session.Provider{}, map[string]session.Provider{}
	for _, provider := range session.ProviderOrder() {
		colors, ok := providerAccents[provider]
		if !ok {
			t.Errorf("%s has no badge color in providerAccents", provider)
			continue
		}
		if other, taken := light[colors.lightBg]; taken {
			t.Errorf("%s and %s share the light badge color %s", provider, other, colors.lightBg)
		}
		if other, taken := dark[colors.darkBg]; taken {
			t.Errorf("%s and %s share the dark badge color %s", provider, other, colors.darkBg)
		}
		light[colors.lightBg], dark[colors.darkBg] = provider, provider
	}
}

func TestPrintTableSanitizesUntrustedMetadata(t *testing.T) {
	rows := []session.Row{{
		Provider:  session.ProviderCodex,
		ID:        "safe-id\x1b]52;c;Y2xpcGJvYXJk\x07",
		LastAt:    time.Now(),
		CWD:       "/work\nforged-row\u202e",
		FirstUser: "hello\x1b[31m red",
	}}
	var output bytes.Buffer
	PrintTable(&output, 120, rows)
	got := output.String()
	if strings.ContainsAny(got, "\x1b\x07") || strings.ContainsRune(got, '\u202e') || strings.Contains(got, "clipboard") {
		t.Fatalf("plain table retained terminal controls: %q", got)
	}
	if lines := strings.Split(strings.TrimSpace(got), "\n"); len(lines) != 2 {
		t.Fatalf("metadata injected a forged row: %q", got)
	}
}
