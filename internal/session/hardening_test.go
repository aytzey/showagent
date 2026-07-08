package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codexSourceFixture = `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","cwd":"/work/codex"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build the feature"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"implemented it"}]}}
`

func codexSourceRow(t *testing.T, root string) Row {
	t.Helper()
	sourcePath := filepath.Join(root, "source-codex.jsonl")
	writeFile(t, sourcePath, codexSourceFixture)
	return Row{
		Provider: ProviderCodex,
		ID:       "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb",
		CWD:      "/work/app",
		File:     sourcePath,
	}
}

func makeReadOnlyDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

func dirEntries(t *testing.T, path string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestConvertWriteFailureLeavesNoPartialFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only directories do not block root")
	}

	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	jcodeHome := filepath.Join(root, "jcode")
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", jcodeHome)
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))

	source := codexSourceRow(t, root)

	claudeTarget := filepath.Join(claudeHome, "projects", "-work-app")
	makeReadOnlyDir(t, claudeTarget)
	if _, err := Convert(source, ProviderClaude, HandoffOptions{}); err == nil {
		t.Fatal("expected claude conversion into read-only directory to fail")
	}
	if entries := dirEntries(t, claudeTarget); len(entries) != 0 {
		t.Fatalf("failed claude conversion left files behind: %v", entries)
	}

	jcodeTarget := filepath.Join(jcodeHome, "sessions")
	makeReadOnlyDir(t, jcodeTarget)
	if _, err := Convert(source, ProviderJCode, HandoffOptions{}); err == nil {
		t.Fatal("expected jcode conversion into read-only directory to fail")
	}
	if entries := dirEntries(t, jcodeTarget); len(entries) != 0 {
		t.Fatalf("failed jcode conversion left files behind: %v", entries)
	}
}

func TestConvertLeavesNoTempFileOnSuccess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "jcode"))
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))

	source := codexSourceRow(t, root)
	converted, err := Convert(source, ProviderClaude, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}

	entries := dirEntries(t, filepath.Dir(converted.File))
	if len(entries) != 1 {
		t.Fatalf("expected only the converted file, got %v", entries)
	}
	if strings.Contains(entries[0].Name(), ".tmp-") {
		t.Fatalf("temp file was not renamed into place: %s", entries[0].Name())
	}
}

func TestReverseLinesOrderAndEarlyStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lines.jsonl")
	writeFile(t, path, "one\ntwo\n\nthree\n")

	var lines []string
	if err := reverseLines(path, func(line string) bool {
		lines = append(lines, line)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, ",") != "three,two,one" {
		t.Fatalf("unexpected reverse order: %v", lines)
	}

	lines = nil
	if err := reverseLines(path, func(line string) bool {
		lines = append(lines, line)
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, ",") != "three" {
		t.Fatalf("expected early stop after one line, got %v", lines)
	}
}

func TestReverseLinesHandlesLargeSingleLine(t *testing.T) {
	// 10MB without a newline fits within scanBufferMax and must come back
	// as one intact line without quadratic buffering.
	payload := strings.Repeat("a", 10*1024*1024)
	path := filepath.Join(t.TempDir(), "one-line.jsonl")
	writeFile(t, path, payload)

	calls := 0
	if err := reverseLines(path, func(line string) bool {
		calls++
		if line != payload {
			t.Fatalf("single line came back altered: len=%d want %d", len(line), len(payload))
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected one line, got %d", calls)
	}
}

func TestReverseLinesTruncatesOversizedLine(t *testing.T) {
	// A line beyond scanBufferMax is degraded to its trailing bytes instead
	// of buffering the whole file; earlier lines still come through intact.
	oversized := strings.Repeat("b", scanBufferMax+512*1024)
	path := filepath.Join(t.TempDir(), "oversized.jsonl")
	writeFile(t, path, "first line\n"+oversized)

	var lines []string
	if err := reverseLines(path, func(line string) bool {
		lines = append(lines, line)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if len(lines[0]) == 0 || len(lines[0]) > scanBufferMax {
		t.Fatalf("oversized line length = %d, want 1..%d", len(lines[0]), scanBufferMax)
	}
	if strings.Trim(lines[0], "b") != "" {
		t.Fatal("truncated line should contain only the oversized payload's tail")
	}
	if lines[1] != "first line" {
		t.Fatalf("line before the oversized one = %q, want %q", lines[1], "first line")
	}
}

func TestDiscoverySkipsCorruptFilesWithoutCrashing(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))

	// Valid sessions that must survive discovery.
	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "01", "rollout-2026-06-01T12-00-00-aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb.jsonl"), codexSourceFixture)
	writeFile(t, filepath.Join(claudeHome, "projects", "-work-claude", "cccccccc-1111-2222-3333-dddddddddddd.jsonl"), `
{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"2026-06-02T10:00:00Z","cwd":"/work/claude","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
`)

	// Corrupt JSONL: binary junk, truncated JSON, and lines that exceed the
	// scanner buffer. None of these may crash discovery, and files whose
	// scan fails must be skipped.
	oversized := strings.Repeat("x", scanBufferMax+1)
	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "02", "rollout-2026-06-02T12-00-00-eeeeeeee-1111-2222-3333-ffffffffffff.jsonl"), oversized)
	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "03", "garbage.jsonl"), "\x00\x01\x02 not json\n{\"type\":\"session_meta\",\"payload\":{\"id\":")
	writeFile(t, filepath.Join(claudeHome, "projects", "-work-claude", "99999999-1111-2222-3333-999999999999.jsonl"), oversized)

	rows := Discover()
	ids := make(map[string]bool, len(rows))
	for _, row := range rows {
		ids[row.ID] = true
	}
	if !ids["aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"] {
		t.Fatalf("valid codex session missing from discovery: %#v", rows)
	}
	if !ids["cccccccc-1111-2222-3333-dddddddddddd"] {
		t.Fatalf("valid claude session missing from discovery: %#v", rows)
	}
	if ids["eeeeeeee-1111-2222-3333-ffffffffffff"] {
		t.Fatal("codex file with unscannable line should be skipped")
	}
	if ids["99999999-1111-2222-3333-999999999999"] {
		t.Fatal("claude file with unscannable line should be skipped")
	}
}
