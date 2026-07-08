package session

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// geminiFixtureHome pins every provider to an empty store, points
// GEMINI_CLI_HOME at a fresh root, and returns the .gemini dir inside it
// (GEMINI_CLI_HOME replaces the home directory, mirroring gemini-cli).
func geminiFixtureHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	setEmptyHomes(t, root)
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", root)
	return filepath.Join(root, ".gemini")
}

func TestDiscoverFindsGeminiSessions(t *testing.T) {
	home := geminiFixtureHome(t)

	// Current format: a slug project dir owning a .project_root marker and a
	// JSONL log with a metadata line, messages, and a $set metadata update.
	writeFile(t, filepath.Join(home, "tmp", "myapp", ".project_root"), "/work/gemini\n")
	writeFile(t, filepath.Join(home, "tmp", "myapp", "chats", "session-2026-06-03T10-00-eeeeeeee.jsonl"), `
{"sessionId":"eeeeeeee-1111-2222-3333-ffffffffffff","projectHash":"hash","startTime":"2026-06-03T10:00:00.000Z","lastUpdated":"2026-06-03T10:00:00.000Z"}
{"id":"m1","timestamp":"2026-06-03T10:00:01.000Z","type":"user","content":"first gemini message password hunter2"}
{"id":"m2","timestamp":"2026-06-03T10:00:02.000Z","type":"gemini","content":[{"text":"on it"}]}
{"id":"m3","timestamp":"2026-06-03T10:00:03.000Z","type":"user","content":[{"text":"last gemini message"}]}
{"$set":{"lastUpdated":"2026-06-03T10:05:00.000Z"}}
`)

	// Legacy format: a sha256-named project dir with a pretty-printed .json
	// document and no ownership marker, so its workspace is unknown.
	legacyDir := "435cb0f908c41d491ce19333227478c469d395dfad3327c9bcd1aa96c12e4c5c"
	writeFile(t, filepath.Join(home, "tmp", legacyDir, "chats", "session-2026-06-02T09-00-abcd1234.json"), `{
  "sessionId": "abcd1234-1111-2222-3333-000000000000",
  "projectHash": "`+legacyDir+`",
  "startTime": "2026-06-02T09:00:00.000Z",
  "lastUpdated": "2026-06-02T09:30:00.000Z",
  "messages": [
    {"id": "l1", "timestamp": "2026-06-02T09:00:01.000Z", "type": "user", "content": "legacy question"},
    {"id": "l2", "timestamp": "2026-06-02T09:00:02.000Z", "type": "gemini", "content": [{"text": "legacy answer"}]}
  ]
}`)
	// Per-project log files next to chats/ must not be mistaken for sessions.
	writeFile(t, filepath.Join(home, "tmp", legacyDir, "logs.json"), `[]`)

	rows := Discover()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %#v", len(rows), rows)
	}

	first := rows[0]
	if first.Provider != ProviderGemini || first.ID != "eeeeeeee-1111-2222-3333-ffffffffffff" {
		t.Fatalf("unexpected first row: %#v", first)
	}
	if first.CWD != "/work/gemini" || first.LaunchCWD != "/work/gemini" {
		t.Fatalf("marker cwd not resolved: %#v", first)
	}
	if first.FirstUser != "first gemini message password [redacted]" {
		t.Fatalf("first user preview not redacted: %q", first.FirstUser)
	}
	if first.LastUser != "last gemini message" {
		t.Fatalf("unexpected last user preview: %q", first.LastUser)
	}
	// The $set update must win over the metadata line's lastUpdated.
	want := time.Date(2026, 6, 3, 10, 5, 0, 0, time.UTC)
	if !first.LastAt.Equal(want) {
		t.Fatalf("LastAt = %v, want %v", first.LastAt, want)
	}

	second := rows[1]
	if second.ID != "abcd1234-1111-2222-3333-000000000000" {
		t.Fatalf("unexpected second row: %#v", second)
	}
	if second.CWD != "(unknown cwd)" {
		t.Fatalf("legacy hash dir should have unknown cwd: %#v", second)
	}
	if second.FirstUser != "legacy question" {
		t.Fatalf("unexpected legacy preview: %q", second.FirstUser)
	}
}

func TestGeminiCWDFromProjectsRegistry(t *testing.T) {
	home := geminiFixtureHome(t)

	writeFile(t, filepath.Join(home, "projects.json"), `{"projects":{"/work/registry":"registered"}}`)
	writeFile(t, filepath.Join(home, "tmp", "registered", "chats", "session-2026-06-03T10-00-11111111.jsonl"), `
{"sessionId":"11111111-1111-2222-3333-222222222222","projectHash":"hash","startTime":"2026-06-03T10:00:00.000Z","lastUpdated":"2026-06-03T10:00:00.000Z"}
{"id":"m1","timestamp":"2026-06-03T10:00:01.000Z","type":"user","content":"registry question"}
`)

	rows := Discover()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].CWD != "/work/registry" {
		t.Fatalf("projects.json cwd not resolved: %#v", rows[0])
	}
}

func TestGeminiSkipsSubagentAndContentlessSessions(t *testing.T) {
	home := geminiFixtureHome(t)
	chats := filepath.Join(home, "tmp", "myapp", "chats")

	// Subagent sessions are hidden from resume flows.
	writeFile(t, filepath.Join(chats, "session-2026-06-03T10-00-aaaaaaaa.jsonl"), `
{"sessionId":"aaaaaaaa-2222-3333-4444-555555555555","projectHash":"hash","startTime":"2026-06-03T10:00:00.000Z","lastUpdated":"2026-06-03T10:00:00.000Z","kind":"subagent"}
{"id":"m1","timestamp":"2026-06-03T10:00:01.000Z","type":"user","content":"subagent task"}
`)
	// Nested subagent transcripts live in a per-parent subdirectory.
	writeFile(t, filepath.Join(chats, "aaaaaaaa-2222-3333-4444-555555555555", "bbbbbbbb.jsonl"), `
{"sessionId":"bbbbbbbb-2222-3333-4444-555555555555","projectHash":"hash","startTime":"2026-06-03T10:00:00.000Z","lastUpdated":"2026-06-03T10:00:00.000Z","kind":"subagent"}
`)
	// Slash-command-only sessions have nothing to resume.
	writeFile(t, filepath.Join(chats, "session-2026-06-03T11-00-cccccccc.jsonl"), `
{"sessionId":"cccccccc-2222-3333-4444-555555555555","projectHash":"hash","startTime":"2026-06-03T11:00:00.000Z","lastUpdated":"2026-06-03T11:00:00.000Z"}
{"id":"m1","timestamp":"2026-06-03T11:00:01.000Z","type":"user","content":"/stats"}
{"id":"m2","timestamp":"2026-06-03T11:00:02.000Z","type":"info","content":"session started"}
`)

	if rows := Discover(); len(rows) != 0 {
		t.Fatalf("expected no rows, got %d: %#v", len(rows), rows)
	}
}

func TestGeminiTranscriptReplaysRewindsAndCheckpoints(t *testing.T) {
	home := geminiFixtureHome(t)
	path := filepath.Join(home, "tmp", "myapp", "chats", "session-2026-06-03T10-00-dddddddd.jsonl")
	writeFile(t, path, `
{"sessionId":"dddddddd-1111-2222-3333-eeeeeeeeeeee","projectHash":"hash","startTime":"2026-06-03T10:00:00.000Z","lastUpdated":"2026-06-03T10:00:00.000Z"}
{"id":"m1","timestamp":"2026-06-03T10:00:01.000Z","type":"user","content":"first question"}
{"id":"m2","timestamp":"2026-06-03T10:00:02.000Z","type":"gemini","content":[{"thought":true,"text":"hidden reasoning"},{"text":"first answer"}]}
{"id":"m3","timestamp":"2026-06-03T10:00:03.000Z","type":"user","content":"abandoned question"}
{"id":"m4","timestamp":"2026-06-03T10:00:04.000Z","type":"gemini","content":"abandoned answer"}
{"$rewindTo":"m3"}
{"id":"m5","timestamp":"2026-06-03T10:00:05.000Z","type":"user","content":"second question"}
{"id":"m2","timestamp":"2026-06-03T10:00:06.000Z","type":"gemini","content":[{"text":"first answer, edited in place"}]}
{"id":"m6","timestamp":"2026-06-03T10:00:07.000Z","type":"gemini","content":"second answer"}
`)

	turns, err := Transcript(Row{Provider: ProviderGemini, ID: "dddddddd-1111-2222-3333-eeeeeeeeeeee", File: path})
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{
		{Role: "user", Text: "first question"},
		{Role: "assistant", Text: "first answer, edited in place"},
		{Role: "user", Text: "second question"},
		{Role: "assistant", Text: "second answer"},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %#v, want %#v", turns, want)
	}
	for index, turn := range want {
		if turns[index] != turn {
			t.Fatalf("turn %d = %#v, want %#v", index, turns[index], turn)
		}
	}
}

func TestGeminiResumeAndCompoundArgs(t *testing.T) {
	row := Row{Provider: ProviderGemini, ID: "eeeeeeee-1111-2222-3333-ffffffffffff"}

	got := row.ResumeCommand(ResumeOptions{})
	want := []string{"gemini", "--resume", row.ID}
	if strings.Join(got, "\x1f") != strings.Join(want, "\x1f") {
		t.Fatalf("resume = %v, want %v", got, want)
	}

	got = row.ResumeCommand(ResumeOptions{Dangerous: true})
	want = []string{"gemini", "--yolo", "--resume", row.ID}
	if strings.Join(got, "\x1f") != strings.Join(want, "\x1f") {
		t.Fatalf("yolo resume = %v, want %v", got, want)
	}

	got = geminiProvider{}.CompoundArgs(row, ResumeOptions{}, "run the compound pass")
	want = []string{"gemini", "--resume", row.ID, "--prompt-interactive", "run the compound pass"}
	if strings.Join(got, "\x1f") != strings.Join(want, "\x1f") {
		t.Fatalf("compound = %v, want %v", got, want)
	}
}

func TestGeminiDeleteRemovesSessionFile(t *testing.T) {
	home := geminiFixtureHome(t)
	path := filepath.Join(home, "tmp", "myapp", "chats", "session-2026-06-03T10-00-99999999.jsonl")
	writeFile(t, path, `{"sessionId":"99999999-1111-2222-3333-aaaaaaaaaaaa","projectHash":"hash"}`)

	if err := Delete(Row{Provider: ProviderGemini, ID: "9", File: path}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file still exists: %v", err)
	}

	if err := Delete(Row{Provider: ProviderGemini, ID: "9"}); err == nil {
		t.Fatal("delete without a file path should fail")
	}
}

func TestConvertCodexToGeminiRoundTrips(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", root)

	source := codexSourceRow(t, root)
	converted, err := Convert(source, ProviderGemini, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if converted.Provider != ProviderGemini || converted.CWD != source.CWD {
		t.Fatalf("unexpected converted row: %#v", converted)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(converted.ID) {
		t.Fatalf("converted session id is not a UUID: %q", converted.ID)
	}

	// Without a marker or registry entry the file must land in the legacy
	// sha256(cwd) project dir, which every gemini-cli release can read.
	wantDir := filepath.Join(root, ".gemini", "tmp", geminiProjectHash(source.CWD), "chats")
	if filepath.Dir(converted.File) != wantDir {
		t.Fatalf("converted file in %s, want %s", filepath.Dir(converted.File), wantDir)
	}
	base := filepath.Base(converted.File)
	if !strings.HasPrefix(base, "session-") || !strings.HasSuffix(base, "-"+converted.ID[:8]+".json") {
		t.Fatalf("unexpected converted file name: %q", base)
	}

	content, err := os.ReadFile(converted.File)
	if err != nil {
		t.Fatal(err)
	}
	// A single-line document parses both through gemini-cli's line-based
	// reader and through legacy whole-file fallbacks.
	if lines := strings.Count(strings.TrimSpace(string(content)), "\n"); lines != 0 {
		t.Fatalf("converted session should be a single JSON line, has %d extra", lines)
	}

	// The converted session must round-trip through gemini discovery and
	// yield the source transcript unchanged.
	rows := Discover()
	var found *Row
	for index := range rows {
		if rows[index].Provider == ProviderGemini && rows[index].ID == converted.ID {
			found = &rows[index]
			break
		}
	}
	if found == nil {
		t.Fatalf("converted session not discovered: %#v", rows)
	}
	if found.FirstUser != "build the feature" {
		t.Fatalf("unexpected discovered preview: %q", found.FirstUser)
	}

	turns, err := Transcript(*found)
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{
		{Role: "user", Text: "build the feature"},
		{Role: "assistant", Text: "implemented it"},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %#v, want %#v", turns, want)
	}
	for index, turn := range want {
		if turns[index] != turn {
			t.Fatalf("turn %d = %#v, want %#v", index, turns[index], turn)
		}
	}
}

func TestConvertToGeminiPrefersMarkedProjectDir(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", root)
	home := filepath.Join(root, ".gemini")

	writeFile(t, filepath.Join(home, "tmp", "app", ".project_root"), "/work/app")

	source := codexSourceRow(t, root)
	converted, err := Convert(source, ProviderGemini, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(home, "tmp", "app", "chats")
	if filepath.Dir(converted.File) != wantDir {
		t.Fatalf("converted file in %s, want marked dir %s", filepath.Dir(converted.File), wantDir)
	}
}

func TestConvertToGeminiNeedsWorkspace(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", root)

	source := codexSourceRow(t, root)
	source.CWD = "(unknown cwd)"
	if _, err := Convert(source, ProviderGemini, HandoffOptions{}); err == nil {
		t.Fatal("conversion without a workspace should fail")
	}
}

func TestConvertGeminiToClaude(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", root)
	claudeHome := filepath.Join(root, "claude-target")
	t.Setenv("CLAUDE_HOME", claudeHome)
	home := filepath.Join(root, ".gemini")

	path := filepath.Join(home, "tmp", "myapp", "chats", "session-2026-06-03T10-00-eeeeeeee.jsonl")
	writeFile(t, path, `
{"sessionId":"eeeeeeee-1111-2222-3333-ffffffffffff","projectHash":"hash","startTime":"2026-06-03T10:00:00.000Z","lastUpdated":"2026-06-03T10:00:00.000Z"}
{"id":"m1","timestamp":"2026-06-03T10:00:01.000Z","type":"user","content":"gemini question"}
{"id":"m2","timestamp":"2026-06-03T10:00:02.000Z","type":"gemini","content":[{"text":"gemini answer"}]}
`)
	source := Row{
		Provider: ProviderGemini,
		ID:       "eeeeeeee-1111-2222-3333-ffffffffffff",
		CWD:      "/work/gemini",
		File:     path,
	}

	converted, err := Convert(source, ProviderClaude, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Provider != ProviderClaude {
		t.Fatalf("unexpected converted row: %#v", converted)
	}
	turns, err := Transcript(converted)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].Text != "gemini question" || turns[1].Text != "gemini answer" {
		t.Fatalf("unexpected converted transcript: %#v", turns)
	}
}
