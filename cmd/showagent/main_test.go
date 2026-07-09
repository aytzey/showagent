package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aytzey/showagent/internal/session"
)

const (
	codexID  = "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"
	claudeID = "cccccccc-1111-2222-3333-dddddddddddd"
)

// setFixtureHomes points every provider home at hermetic fixture directories
// containing one codex session and one claude session.
func setFixtureHomes(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))

	writeFixture(t, filepath.Join(codexHome, "sessions", "2026", "06", "01", "rollout-2026-06-01T12-00-00-"+codexID+".jsonl"), `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"`+codexID+`","cwd":"/work/codex"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first codex message"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"last codex message"}]}}
`)

	writeFixture(t, filepath.Join(claudeHome, "projects", "-work-claude", claudeID+".jsonl"), `
{"type":"user","message":{"role":"user","content":"first claude message"},"uuid":"1","timestamp":"2026-06-02T10:00:00Z","cwd":"/work/claude","sessionId":"`+claudeID+`"}
{"type":"user","message":{"role":"user","content":"last claude message"},"uuid":"2","timestamp":"2026-06-02T10:02:00Z","cwd":"/work/claude","sessionId":"`+claudeID+`"}
`)
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = run(args, &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestHelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		code, stdout, _ := runCLI(t, flag)
		if code != 0 {
			t.Fatalf("%s exit = %d, want 0", flag, code)
		}
		for _, want := range []string{"Usage:", "list", "resume", "convert", "info", "update", "setup", "CODEX_HOME", "CLAUDE_HOME", "JCODE_HOME", "--yolo", "--json", "--dry-run"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%s output missing %q:\n%s", flag, want, stdout)
			}
		}
	}
}

func TestVersionFlag(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "version"} {
		code, stdout, _ := runCLI(t, flag)
		if code != 0 {
			t.Fatalf("%s exit = %d, want 0", flag, code)
		}
		if !strings.HasPrefix(stdout, "showagent ") || strings.TrimSpace(stdout) == "showagent" {
			t.Fatalf("%s output = %q, want 'showagent <version>'", flag, stdout)
		}
	}
}

func TestVersionStringPrefersStampedVersion(t *testing.T) {
	saved := version
	defer func() { version = saved }()

	version = "v9.9.9"
	if got := versionString(); got != "v9.9.9" {
		t.Fatalf("versionString = %q, want stamped v9.9.9", got)
	}

	// Unstamped builds must still report something non-empty (build info or
	// the "dev" fallback).
	version = "dev"
	if got := versionString(); got == "" {
		t.Fatal("versionString must never be empty")
	}
}

func TestUnknownArgumentExitsTwo(t *testing.T) {
	code, _, stderr := runCLI(t, "bogus")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage:") || !strings.Contains(stderr, "showagent --help") {
		t.Fatalf("stderr missing usage hint:\n%s", stderr)
	}
}

func TestUnknownListFlagExitsTwo(t *testing.T) {
	code, _, stderr := runCLI(t, "list", "--nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--nope") {
		t.Fatalf("stderr should name the bad flag:\n%s", stderr)
	}
}

func TestListTableIncludesSessionID(t *testing.T) {
	setFixtureHomes(t)
	code, stdout, _ := runCLI(t, "list")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d lines:\n%s", len(lines), stdout)
	}
	if !strings.Contains(lines[0], "ID") || !strings.Contains(lines[0], "AGENT") {
		t.Fatalf("unexpected header: %q", lines[0])
	}
	if !strings.Contains(stdout, codexID) || !strings.Contains(stdout, claudeID) {
		t.Fatalf("table must contain full session ids:\n%s", stdout)
	}
}

func TestListJSONShape(t *testing.T) {
	setFixtureHomes(t)
	code, stdout, _ := runCLI(t, "list", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, stdout)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	// Newest first: the claude session was updated later.
	first := items[0]
	for _, key := range []string{"id", "provider", "workspace", "updated", "first_message", "last_message"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("item missing %q field: %#v", key, first)
		}
	}
	if first["id"] != claudeID || first["provider"] != "claude" || first["workspace"] != "/work/claude" {
		t.Fatalf("unexpected first item: %#v", first)
	}
	if first["first_message"] != "first claude message" || first["last_message"] != "last claude message" {
		t.Fatalf("unexpected messages: %#v", first)
	}
	updated, ok := first["updated"].(string)
	if !ok {
		t.Fatalf("updated is not a string: %#v", first["updated"])
	}
	if _, err := time.Parse(time.RFC3339, updated); err != nil {
		t.Fatalf("updated %q is not RFC3339: %v", updated, err)
	}
	if items[1]["id"] != codexID || items[1]["provider"] != "codex" {
		t.Fatalf("unexpected second item: %#v", items[1])
	}
}

func TestListJSONEmptyIsValidArray(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "empty-claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))

	code, stdout, _ := runCLI(t, "list", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		t.Fatalf("empty list output is not valid JSON: %v\n%s", err, stdout)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty array, got %d items", len(items))
	}
}

func TestResolveSession(t *testing.T) {
	rows := []session.Row{
		{Provider: session.ProviderClaude, ID: claudeID},
		{Provider: session.ProviderCodex, ID: codexID},
	}

	row, err := resolveSession(rows, "latest")
	if err != nil || row.ID != claudeID {
		t.Fatalf("latest = %#v, %v; want first row", row, err)
	}

	row, err = resolveSession(rows, codexID)
	if err != nil || row.Provider != session.ProviderCodex {
		t.Fatalf("by id = %#v, %v; want codex row", row, err)
	}

	if _, err := resolveSession(rows, "missing-id"); err == nil || !strings.Contains(err.Error(), "showagent list") {
		t.Fatalf("unknown id err = %v, want hint to run 'showagent list'", err)
	}

	if _, err := resolveSession(nil, "latest"); err == nil {
		t.Fatal("latest with no sessions must fail")
	}
}

func TestResumeUnknownIDExitsOne(t *testing.T) {
	setFixtureHomes(t)
	code, _, stderr := runCLI(t, "resume", "not-a-session")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "not-a-session") || !strings.Contains(stderr, "showagent list") {
		t.Fatalf("stderr should name the id and point at 'showagent list':\n%s", stderr)
	}
}

func TestResumeWithoutIDExitsTwo(t *testing.T) {
	code, _, stderr := runCLI(t, "resume")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Fatalf("stderr missing usage:\n%s", stderr)
	}

	code, _, _ = runCLI(t, "resume", "id-one", "id-two")
	if code != 2 {
		t.Fatalf("two ids exit = %d, want 2", code)
	}
}

func TestConvertDryRunPrintsPreviewWithoutWriting(t *testing.T) {
	setFixtureHomes(t)
	code, stdout, stderr := runCLI(t, "convert", codexID, "--to", "claude", "--scope", "last:1", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	for _, want := range []string{
		"conversion preview",
		"source:    codex " + codexID,
		"target:    claude",
		"scope:     last 1 (1 transferable turns)",
		"last user: last codex message",
		"tool calls and tool results",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("preview missing %q:\n%s", want, stdout)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(os.Getenv("CLAUDE_HOME"), "projects", "*", "*.jsonl")); len(matches) != 1 {
		t.Fatalf("dry-run should not create a converted claude file, matches=%v", matches)
	}
}

func TestInfoPrintsResumeRecipe(t *testing.T) {
	setFixtureHomes(t)
	code, stdout, stderr := runCLI(t, "info", claudeID)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	for _, want := range []string{
		"resume recipe",
		"provider: claude",
		"session:  " + claudeID,
		"command:  claude --resume " + claudeID,
		"storage:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("recipe missing %q:\n%s", want, stdout)
		}
	}
}

func TestParseHandoffScope(t *testing.T) {
	tests := []struct {
		value string
		want  int
		ok    bool
	}{
		{"all", 0, true},
		{"", 0, true},
		{"last:3", 3, true},
		{"last-4", 4, true},
		{"3", 0, false},
		{"last:3x", 0, false},
		{"last:0", 0, false},
		{"last:-1", 0, false},
	}

	for _, tt := range tests {
		got, err := parseHandoffScope(tt.value)
		if tt.ok {
			if err != nil {
				t.Fatalf("parseHandoffScope(%q) err = %v, want nil", tt.value, err)
			}
			if got.MaxTurns != tt.want {
				t.Fatalf("parseHandoffScope(%q).MaxTurns = %d, want %d", tt.value, got.MaxTurns, tt.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("parseHandoffScope(%q) err = nil, want error", tt.value)
		}
	}
}

func TestUpdateCheckReportsNewerRelease(t *testing.T) {
	savedURL := latestReleaseURL
	savedVersion := version
	defer func() {
		latestReleaseURL = savedURL
		version = savedVersion
	}()

	version = "v0.7.0"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.8.0"}`))
	}))
	defer server.Close()
	latestReleaseURL = server.URL

	code, stdout, stderr := runCLI(t, "update", "--check")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "showagent v0.8.0 is available (current v0.7.0)") {
		t.Fatalf("unexpected update check output:\n%s", stdout)
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v0.8.0", "v0.7.0", true},
		{"v0.7.1", "v0.7.1", false},
		{"v1.0.0", "v0.9.9", true},
		{"not-a-version", "v0.7.0", false},
		{"v0.8.0", "dev", true},
	}
	for _, tt := range tests {
		if got := isNewerVersion(tt.candidate, tt.current); got != tt.want {
			t.Fatalf("isNewerVersion(%q, %q) = %v, want %v", tt.candidate, tt.current, got, tt.want)
		}
	}
}

func TestTerminalWidthFallsBackForNonTerminal(t *testing.T) {
	var buf bytes.Buffer
	if got := terminalWidth(&buf); got != 120 {
		t.Fatalf("non-file width = %d, want 120", got)
	}

	file, err := os.CreateTemp(t.TempDir(), "not-a-tty")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if got := terminalWidth(file); got != 120 {
		t.Fatalf("regular-file width = %d, want 120", got)
	}
}

func TestListEmptyExplainsScannedDirs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "empty-claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))

	code, _, stderr := runCLI(t, "list")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	for _, want := range []string{
		"no supported local sessions found",
		filepath.Join(root, "empty-codex", "sessions"),
		filepath.Join(root, "empty-claude", "projects"),
		"CODEX_HOME", "CLAUDE_HOME", "JCODE_HOME",
		"start a conversation with a supported agent",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}
