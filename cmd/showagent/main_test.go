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
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "empty-pi"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")

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
		for _, want := range []string{"Usage:", "list", "transcript", "resume", "convert", "info", "mcp", "update", "setup", "CODEX_HOME", "CLAUDE_HOME", "JCODE_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "--yolo", "--json", "--max-turns", "--dry-run", "--read-only", "--allow-secrets"} {
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

func TestMCPRejectsArguments(t *testing.T) {
	code, _, stderr := runCLI(t, "mcp", "--http")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown mcp argument") {
		t.Fatalf("stderr missing mcp usage hint:\n%s", stderr)
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

func TestDefaultNonTerminalPrintsTable(t *testing.T) {
	setFixtureHomes(t)
	var stdout, stderr bytes.Buffer
	if code := runDefault(&stdout, &stderr); code != 0 {
		t.Fatalf("runDefault exit = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), codexID) || !strings.Contains(stdout.String(), claudeID) {
		t.Fatalf("default non-terminal output missing sessions:\n%s", stdout.String())
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

func TestListJSONPreviewIsBoundedAndRedacted(t *testing.T) {
	input := "api_key=super-secret " + strings.Repeat("界", maxListPreviewRunes)
	got := listJSONPreview(input)
	if strings.Contains(got, "super-secret") {
		t.Fatalf("preview leaked a secret: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("long preview was not truncated: %q", got)
	}
	if count := len([]rune(got)); count != maxListPreviewRunes+1 {
		t.Fatalf("preview rune count = %d, want %d", count, maxListPreviewRunes+1)
	}
}

func TestListJSONEmptyIsValidArray(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "empty-claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "empty-pi"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")

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

func TestTranscriptJSONRedactsAndTruncates(t *testing.T) {
	setFixtureHomes(t)
	path := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "06", "01", "rollout-2026-06-01T12-00-00-"+codexID+".jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("\n" + `{"timestamp":"2026-06-01T09:03:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"password=hunter2\u001b[31m\u202e"}]}}` + "\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append fixture: write=%v close=%v", writeErr, closeErr)
	}

	code, stdout, stderr := runCLI(t, "transcript", codexID, "--max-turns", "1", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	var result transcriptResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout)
	}
	if result.ID != codexID || result.Provider != "codex" || result.Workspace != "/work/codex" {
		t.Fatalf("unexpected metadata: %#v", result)
	}
	if result.TotalTurns != 3 || !result.Truncated || !result.SecretsRedacted || len(result.Turns) != 1 {
		t.Fatalf("unexpected transcript bounds: %#v", result)
	}
	if strings.Contains(result.Turns[0].Text, "hunter2") || !strings.Contains(strings.ToLower(result.Turns[0].Text), "[redacted]") {
		t.Fatalf("secret was not redacted: %#v", result.Turns[0])
	}
	if strings.Contains(result.Turns[0].Text, "\x1b") || strings.Contains(result.Turns[0].Text, "\u202e") {
		t.Fatalf("terminal controls were not removed: %#v", result.Turns[0])
	}
	if !strings.Contains(result.Warning, "untrusted") {
		t.Fatalf("missing trust-boundary warning: %#v", result)
	}
}

func TestTranscriptRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"transcript"}, want: "needs a session id"},
		{args: []string{"transcript", codexID, "--max-turns"}, want: "positive integer"},
		{args: []string{"transcript", codexID, "--max-turns", "0"}, want: "positive integer"},
		{args: []string{"transcript", codexID, "--wat"}, want: "unknown transcript argument"},
		{args: []string{"transcript", codexID, claudeID}, want: "unexpected transcript argument"},
	}
	for _, tt := range tests {
		code, _, stderr := runCLI(t, tt.args...)
		if code != 2 || !strings.Contains(stderr, tt.want) {
			t.Fatalf("run(%v) = %d, %q; want exit 2 containing %q", tt.args, code, stderr, tt.want)
		}
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

func TestResumeExistingSessionReportsMissingWorkspace(t *testing.T) {
	setFixtureHomes(t)
	code, _, stderr := runCLI(t, "resume", codexID)
	if code != 1 || !strings.Contains(stderr, "workspace not found") {
		t.Fatalf("resume missing workspace = %d, %q", code, stderr)
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

func TestInfoRejectsInvalidArgumentsAndUnknownSessions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "missing id", args: []string{"info"}, code: 2, want: "needs a session id"},
		{name: "unknown flag", args: []string{"info", "--json"}, code: 2, want: "unknown info argument"},
		{name: "two ids", args: []string{"info", "one", "two"}, code: 2, want: "unexpected info argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, tt.args...)
			if code != tt.code || !strings.Contains(stderr, tt.want) {
				t.Fatalf("run(%v) = %d, %q; want %d containing %q", tt.args, code, stderr, tt.code, tt.want)
			}
		})
	}

	setFixtureHomes(t)
	code, _, stderr := runCLI(t, "info", "not-a-session")
	if code != 1 || !strings.Contains(stderr, "showagent list") {
		t.Fatalf("unknown session = %d, %q; want exit 1 with list hint", code, stderr)
	}
}

func TestConvertRejectsMalformedArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing id", args: []string{"convert", "--to", "claude"}, want: "needs a session id"},
		{name: "missing target flag value", args: []string{"convert", codexID, "--to"}, want: "--to needs a provider"},
		{name: "missing target", args: []string{"convert", codexID}, want: "needs --to"},
		{name: "missing scope", args: []string{"convert", codexID, "--to", "claude", "--scope"}, want: "--scope needs a value"},
		{name: "invalid scope", args: []string{"convert", codexID, "--to", "claude", "--scope", "last:0"}, want: "scope must be"},
		{name: "unknown flag", args: []string{"convert", codexID, "--to", "claude", "--wat"}, want: "unknown convert argument"},
		{name: "two ids", args: []string{"convert", codexID, "other", "--to", "claude"}, want: "unexpected convert argument"},
		{name: "unknown provider", args: []string{"convert", codexID, "--to", "mystery"}, want: "unsupported provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, tt.args...)
			if code != 2 || !strings.Contains(stderr, tt.want) {
				t.Fatalf("run(%v) = %d, %q; want exit 2 containing %q", tt.args, code, stderr, tt.want)
			}
		})
	}
}

func TestSetupRejectsArguments(t *testing.T) {
	code, _, stderr := runCLI(t, "setup", "extra")
	if code != 2 || !strings.Contains(stderr, "setup takes no arguments") {
		t.Fatalf("setup extra argument = %d, %q", code, stderr)
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

func TestConvertWritesTargetAndPrintsRecipe(t *testing.T) {
	setFixtureHomes(t)
	code, stdout, stderr := runCLI(t, "convert", codexID, "--to", "claude", "--scope", "last:1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	for _, want := range []string{"converted codex " + codexID + " -> claude", "resume recipe", "claude --resume"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("conversion output missing %q:\n%s", want, stdout)
		}
	}
	matches, err := filepath.Glob(filepath.Join(os.Getenv("CLAUDE_HOME"), "projects", "*", "*.jsonl"))
	if err != nil || len(matches) != 2 {
		t.Fatalf("converted claude files = %v, %v; want original + copy", matches, err)
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

func TestSetupReportsUnavailableCLIs(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	code, stdout, stderr := runCLI(t, "setup")
	if code != 0 {
		t.Fatalf("setup exit = %d; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "codex: not found") || !strings.Contains(stdout, "claude: not found") {
		t.Fatalf("setup output = %q", stdout)
	}
}

func TestSetupReportsCLICommandFailure(t *testing.T) {
	bin := t.TempDir()
	writeFixture(t, filepath.Join(bin, "codex"), "#!/bin/sh\necho setup-broke >&2\nexit 7\n")
	if err := os.Chmod(filepath.Join(bin, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	code, _, stderr := runCLI(t, "setup")
	if code != 1 || !strings.Contains(stderr, "setup-broke") {
		t.Fatalf("setup command failure = %d, %q", code, stderr)
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
		{"v1.0.0", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.1", "v1.0.0", false},
		{"v1.0", "v0.9.9", false},
		{"v1.0.0.1", "v1.0.0", false},
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
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "empty-pi"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")

	code, _, stderr := runCLI(t, "list")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	for _, want := range []string{
		"no supported local sessions found",
		filepath.Join(root, "empty-codex", "sessions"),
		filepath.Join(root, "empty-claude", "projects"),
		"CODEX_HOME", "CLAUDE_HOME", "JCODE_HOME", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR",
		"start a conversation with a supported agent",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}
