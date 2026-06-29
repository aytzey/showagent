package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverFindsCodexAndClaudeSessions(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))

	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "01", "rollout-2026-06-01T12-00-00-aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb.jsonl"), `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","cwd":"/work/codex"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first codex message"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
{"timestamp":"2026-06-01T09:03:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"last codex message password secret"}]}}
`)

	writeFile(t, filepath.Join(claudeHome, "projects", "-work-claude", "cccccccc-1111-2222-3333-dddddddddddd.jsonl"), `
{"type":"user","message":{"role":"user","content":"first claude message"},"uuid":"1","timestamp":"2026-06-02T10:00:00Z","cwd":"/work/claude","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]},"uuid":"2","timestamp":"2026-06-02T10:01:00Z","cwd":"/work/claude","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"last claude message"}]},"uuid":"3","timestamp":"2026-06-02T10:02:00Z","cwd":"/work/claude","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
`)

	rows := Discover()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Provider != ProviderClaude {
		t.Fatalf("newest row provider = %s, want claude", rows[0].Provider)
	}
	if rows[0].FirstUser != "first claude message" || rows[0].LastUser != "last claude message" {
		t.Fatalf("unexpected claude messages: %#v", rows[0])
	}
	if rows[1].Provider != ProviderCodex {
		t.Fatalf("second row provider = %s, want codex", rows[1].Provider)
	}
	if rows[1].FirstUser != "first codex message" {
		t.Fatalf("unexpected first codex message: %q", rows[1].FirstUser)
	}
	if rows[1].LastUser != "last codex message password [redacted]" {
		t.Fatalf("unexpected redacted codex last message: %q", rows[1].LastUser)
	}
}

func TestJCodeIsOptional(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "empty-claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "jcode"))
	t.Setenv("PATH", filepath.Join(root, "empty-bin"))

	writeFile(t, filepath.Join(root, "jcode", "sessions", "session_showagent_1_deadbeef.json"), `{
  "id": "session_showagent_1_deadbeef",
  "created_at": "2026-06-03T09:00:00Z",
  "updated_at": "2026-06-03T09:01:00Z",
  "working_dir": "/work/jcode",
  "messages": [{"id":"m1","role":"user","content":[{"type":"text","text":"hello"}],"timestamp":"2026-06-03T09:00:00Z"}]
}`)

	if rows := Discover(); len(rows) != 0 {
		t.Fatalf("jcode should be skipped when command is unavailable, got %d rows", len(rows))
	}
}

func TestDiscoverFindsJCodeSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "empty-claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "jcode"))
	withFakeCommand(t, "jcode")

	writeJCodeFixture(t, filepath.Join(root, "jcode", "sessions", "session_showagent_1_deadbeef.json"))

	rows := Discover()
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	row := rows[0]
	if row.Provider != ProviderJCode || row.ID != "session_showagent_1_deadbeef" {
		t.Fatalf("unexpected jcode row: %#v", row)
	}
	if row.CWD != "/work/jcode" || row.FirstUser != "first jcode" || row.LastUser != "last jcode" {
		t.Fatalf("unexpected jcode metadata: %#v", row)
	}
}

func TestClaudeSubagentsAreIgnored(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))

	writeFile(t, filepath.Join(claudeHome, "projects", "-work", "session", "subagents", "agent-a.jsonl"), `
{"type":"user","message":{"role":"user","content":"subagent"},"timestamp":"2026-06-02T10:00:00Z","cwd":"/work","sessionId":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"}
`)

	if rows := Discover(); len(rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(rows))
	}
}

func TestClaudeCommandNoiseIsIgnored(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))

	writeFile(t, filepath.Join(claudeHome, "projects", "-work", "cccccccc-1111-2222-3333-dddddddddddd.jsonl"), `
{"type":"user","message":{"role":"user","content":"<local-command-caveat>Caveat text</local-command-caveat>"},"timestamp":"2026-06-02T10:00:00Z","cwd":"/work","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
{"type":"user","message":{"role":"user","content":"<command-name>/model</command-name>"},"timestamp":"2026-06-02T10:01:00Z","cwd":"/work","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
{"type":"user","message":{"role":"user","content":"<local-command-stdout>Set model</local-command-stdout>"},"timestamp":"2026-06-02T10:02:00Z","cwd":"/work","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
{"type":"user","message":{"role":"user","content":"actual first"},"timestamp":"2026-06-02T10:03:00Z","cwd":"/work","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
{"type":"user","message":{"role":"user","content":"actual last"},"timestamp":"2026-06-02T10:04:00Z","cwd":"/work","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
`)

	rows := Discover()
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if rows[0].FirstUser != "actual first" || rows[0].LastUser != "actual last" {
		t.Fatalf("unexpected previews: %#v", rows[0])
	}
}

func TestClaudeResumeUsesProjectBucketCWD(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	project := filepath.Join(root, "science_cards")
	nested := filepath.Join(project, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))

	sessionID := "cccccccc-1111-2222-3333-dddddddddddd"
	writeFile(t, filepath.Join(claudeHome, "projects", claudeProjectDir(project), sessionID+".jsonl"), `
{"type":"user","message":{"role":"user","content":"started in project"},"timestamp":"2026-06-02T10:00:00Z","cwd":"`+project+`","sessionId":"`+sessionID+`"}
{"type":"user","message":{"role":"user","content":"continued in nested dir"},"timestamp":"2026-06-02T10:01:00Z","cwd":"`+nested+`","sessionId":"`+sessionID+`"}
`)

	rows := Discover()
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if rows[0].CWD != nested {
		t.Fatalf("display cwd = %q, want latest cwd %q", rows[0].CWD, nested)
	}
	if rows[0].resumeCWD() != project {
		t.Fatalf("resume cwd = %q, want Claude project bucket cwd %q", rows[0].resumeCWD(), project)
	}
}

func TestClaudeProjectDirMatchesClaudeUnderscoreEncoding(t *testing.T) {
	got := claudeProjectDir("/home/aytzey/science_cards")
	want := "-home-aytzey-science-cards"
	if got != want {
		t.Fatalf("claudeProjectDir = %q, want %q", got, want)
	}
}

func TestCodexUsesLatestTurnContextCWD(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	claudeHome := filepath.Join(root, "claude")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))

	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "22", "rollout-2026-06-22T09-00-00-aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb.jsonl"), `
{"timestamp":"2026-06-22T09:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","cwd":"/home/aytug"}}
{"timestamp":"2026-06-22T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first codex message"}]}}
{"timestamp":"2026-06-22T09:02:00Z","type":"turn_context","payload":{"cwd":"`+project+`"}}
{"timestamp":"2026-06-22T09:03:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"last codex message"}]}}
`)

	rows := Discover()
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if rows[0].CWD != project {
		t.Fatalf("cwd = %q, want latest turn_context cwd %q", rows[0].CWD, project)
	}

	converted, err := Convert(rows[0], ProviderClaude, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if converted.CWD != project {
		t.Fatalf("converted cwd = %q, want %q", converted.CWD, project)
	}
	if !strings.Contains(converted.File, claudeProjectDir(project)) {
		t.Fatalf("converted path = %q, want project dir %q", converted.File, claudeProjectDir(project))
	}
}

func TestResumeCommandDangerousMode(t *testing.T) {
	codex := Row{Provider: ProviderCodex, ID: "codex-session"}
	if got := strings.Join(codex.ResumeCommand(ResumeOptions{}), " "); got != "codex resume codex-session" {
		t.Fatalf("codex normal command = %q", got)
	}
	if got := strings.Join(codex.ResumeCommand(ResumeOptions{Dangerous: true}), " "); got != "codex resume --dangerously-bypass-approvals-and-sandbox codex-session" {
		t.Fatalf("codex dangerous command = %q", got)
	}

	claude := Row{Provider: ProviderClaude, ID: "claude-session"}
	if got := strings.Join(claude.ResumeCommand(ResumeOptions{}), " "); got != "claude --resume claude-session" {
		t.Fatalf("claude normal command = %q", got)
	}
	if got := strings.Join(claude.ResumeCommand(ResumeOptions{Dangerous: true}), " "); got != "claude --dangerously-skip-permissions --resume claude-session" {
		t.Fatalf("claude dangerous command = %q", got)
	}

	jcode := Row{Provider: ProviderJCode, ID: "jcode-session"}
	if got := strings.Join(jcode.ResumeCommand(ResumeOptions{}), " "); got != "jcode --no-update --resume jcode-session" {
		t.Fatalf("jcode normal command = %q", got)
	}
	if got := strings.Join(jcode.ResumeCommand(ResumeOptions{Dangerous: true}), " "); got != "jcode --no-update --resume jcode-session" {
		t.Fatalf("jcode dangerous command = %q", got)
	}
}

func TestLaunchDirDoesNotFallbackToCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	dir, err := launchDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if dir != root {
		t.Fatalf("launchDir = %q, want %q", dir, root)
	}

	if dir, err := launchDir("(unknown cwd)"); err != nil || dir != "" {
		t.Fatalf("unknown cwd launchDir = %q, %v; want empty nil", dir, err)
	}

	missing := filepath.Join(root, "missing")
	if _, err := launchDir(missing); err == nil || !strings.Contains(err.Error(), "workspace not found") {
		t.Fatalf("missing launchDir err = %v, want workspace not found", err)
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := launchDir(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file launchDir err = %v, want not a directory", err)
	}
}

func TestConvertCodexToClaudeCreatesSessionFile(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))

	sourcePath := filepath.Join(root, "source-codex.jsonl")
	writeFile(t, sourcePath, `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","cwd":"/work/codex"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build the feature"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"implemented it"}]}}
{"timestamp":"2026-06-01T09:03:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"ship it"}]}}
`)

	converted, err := Convert(Row{
		Provider: ProviderCodex,
		ID:       "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb",
		CWD:      "/work/app",
		File:     sourcePath,
	}, ProviderClaude, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Provider != ProviderClaude {
		t.Fatalf("converted provider = %s, want claude", converted.Provider)
	}
	if converted.ID == "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb" {
		t.Fatal("converted session reused source id")
	}
	if _, err := os.Stat(converted.File); err != nil {
		t.Fatalf("converted file missing: %v", err)
	}
	if !strings.Contains(converted.File, filepath.Join(claudeHome, "projects", "-work-app")) {
		t.Fatalf("converted claude path = %q", converted.File)
	}
	content, err := os.ReadFile(converted.File)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"id":"msg_`) {
		t.Fatalf("converted claude assistant message id is not API-shaped:\n%s", content)
	}
	if !strings.Contains(string(content), `"requestId":"req_`) {
		t.Fatalf("converted claude request id is not API-shaped:\n%s", content)
	}
	if strings.Contains(string(content), `"id":"showagent-`) || strings.Contains(string(content), `"requestId":"showagent-`) {
		t.Fatalf("converted claude file contains old synthetic id format:\n%s", content)
	}

	turns, err := Transcript(converted)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 {
		t.Fatalf("converted turns = %d, want 3: %#v", len(turns), turns)
	}
	if turns[0] != (Turn{Role: "user", Text: "build the feature"}) || turns[1] != (Turn{Role: "assistant", Text: "implemented it"}) || turns[2] != (Turn{Role: "user", Text: "ship it"}) {
		t.Fatalf("unexpected converted turns: %#v", turns)
	}

	rows := discoverClaude(claudeHome)
	if len(rows) != 1 {
		t.Fatalf("discoverClaude rows = %d, want 1", len(rows))
	}
	if rows[0].FirstUser != "build the feature" || rows[0].LastUser != "ship it" {
		t.Fatalf("unexpected discovered converted row: %#v", rows[0])
	}
}

func TestConvertClaudeToCodexRespectsScope(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))

	sourcePath := filepath.Join(root, "source-claude.jsonl")
	writeFile(t, sourcePath, `
{"type":"user","message":{"role":"user","content":"first"},"timestamp":"2026-06-02T10:00:00Z","cwd":"/work/claude","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second"}]},"timestamp":"2026-06-02T10:01:00Z","cwd":"/work/claude","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
{"type":"user","message":{"role":"user","content":"third"},"timestamp":"2026-06-02T10:02:00Z","cwd":"/work/claude","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}
`)

	converted, err := Convert(Row{
		Provider: ProviderClaude,
		ID:       "cccccccc-1111-2222-3333-dddddddddddd",
		CWD:      "/work/claude",
		File:     sourcePath,
	}, ProviderCodex, HandoffOptions{MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Provider != ProviderCodex {
		t.Fatalf("converted provider = %s, want codex", converted.Provider)
	}
	if _, err := os.Stat(converted.File); err != nil {
		t.Fatalf("converted file missing: %v", err)
	}

	turns, err := Transcript(converted)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("converted turns = %d, want 2: %#v", len(turns), turns)
	}
	if turns[0] != (Turn{Role: "assistant", Text: "second"}) || turns[1] != (Turn{Role: "user", Text: "third"}) {
		t.Fatalf("unexpected converted turns: %#v", turns)
	}

	rows := discoverCodex(codexHome)
	if len(rows) != 1 {
		t.Fatalf("discoverCodex rows = %d, want 1", len(rows))
	}
	if rows[0].FirstUser != "third" || rows[0].LastUser != "third" {
		t.Fatalf("unexpected discovered converted row: %#v", rows[0])
	}
}

func TestConvertJCodeToCodexRespectsScope(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "empty-claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "jcode"))
	withFakeCommand(t, "jcode")

	sourcePath := filepath.Join(root, "jcode", "sessions", "session_showagent_1_deadbeef.json")
	writeJCodeFixture(t, sourcePath)

	converted, err := Convert(Row{
		Provider: ProviderJCode,
		ID:       "session_showagent_1_deadbeef",
		CWD:      "/work/jcode",
		File:     sourcePath,
	}, ProviderCodex, HandoffOptions{MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Provider != ProviderCodex {
		t.Fatalf("converted provider = %s, want codex", converted.Provider)
	}

	turns, err := Transcript(converted)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("converted turns = %d, want 2: %#v", len(turns), turns)
	}
	if turns[0] != (Turn{Role: "assistant", Text: "jcode reply"}) || turns[1] != (Turn{Role: "user", Text: "last jcode"}) {
		t.Fatalf("unexpected turns: %#v", turns)
	}
	if rows := discoverCodex(codexHome); len(rows) != 1 {
		t.Fatalf("discoverCodex rows = %d, want 1", len(rows))
	}
}

func TestConvertCodexToJCodeCreatesSessionFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "empty-claude"))
	jcodeHome := filepath.Join(root, "jcode")
	t.Setenv("JCODE_HOME", jcodeHome)
	withFakeCommand(t, "jcode")
	writeFile(t, filepath.Join(jcodeHome, "config.toml"), `[provider]
default_provider = "claude"
`)

	sourcePath := filepath.Join(root, "source-codex.jsonl")
	writeFile(t, sourcePath, `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","cwd":"/work/codex"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build jcode handoff"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"jcode handoff ready"}]}}
`)

	converted, err := Convert(Row{
		Provider: ProviderCodex,
		ID:       "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb",
		CWD:      "/work/codex",
		File:     sourcePath,
	}, ProviderJCode, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Provider != ProviderJCode {
		t.Fatalf("converted provider = %s, want jcode", converted.Provider)
	}
	if !strings.HasPrefix(converted.ID, "session_showagent_") {
		t.Fatalf("converted id = %q, want jcode-shaped session id", converted.ID)
	}
	content, err := os.ReadFile(converted.File)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"provider_key": "claude"`, `"working_dir": "/work/codex"`, `"display_role": "system"`, `"build jcode handoff"`, `"jcode handoff ready"`} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("converted jcode file missing %q:\n%s", want, content)
		}
	}

	turns, err := Transcript(converted)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].Text != "build jcode handoff" || turns[1].Text != "jcode handoff ready" {
		t.Fatalf("unexpected converted turns: %#v", turns)
	}
}

func TestDeleteClaudeSessionRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	writeFile(t, path, `{"type":"user","message":{"role":"user","content":"hello"},"timestamp":"2026-06-02T10:00:00Z","cwd":"/work","sessionId":"cccccccc-1111-2222-3333-dddddddddddd"}`)

	if err := Delete(Row{Provider: ProviderClaude, ID: "cccccccc-1111-2222-3333-dddddddddddd", File: path}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, stat err=%v", err)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJCodeFixture(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, `{
  "id": "session_showagent_1_deadbeef",
  "parent_id": null,
  "title": null,
  "created_at": "2026-06-03T09:00:00Z",
  "updated_at": "2026-06-03T09:04:00Z",
  "messages": [
    {"id":"m0","role":"user","display_role":"system","content":[{"type":"text","text":"<system-reminder>noise</system-reminder>"}],"timestamp":"2026-06-03T09:00:00Z"},
    {"id":"m1","role":"user","content":[{"type":"text","text":"first jcode"}],"timestamp":"2026-06-03T09:01:00Z"},
    {"id":"m2","role":"assistant","content":[{"type":"text","text":"jcode reply"}],"timestamp":"2026-06-03T09:02:00Z"},
    {"id":"m3","role":"user","content":[{"type":"text","text":"last jcode"}],"timestamp":"2026-06-03T09:03:00Z"}
  ],
  "provider_key": "openai",
  "model": "gpt-5.5",
  "working_dir": "/work/jcode",
  "short_name": "showagent",
  "status": "Closed",
  "saved": false
}`)
}

func withFakeCommand(t *testing.T, name string) {
	t.Helper()
	bin := t.TempDir()
	path := filepath.Join(bin, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestCompoundCommand(t *testing.T) {
	prompt := "do the compound pass"

	codex := Row{Provider: ProviderCodex, ID: "cid"}
	got := codex.CompoundCommand(ResumeOptions{}, prompt)
	want := []string{"codex", "resume", "cid", prompt}
	if strings.Join(got, "\x1f") != strings.Join(want, "\x1f") {
		t.Fatalf("codex compound = %v, want %v", got, want)
	}
	gotYolo := codex.CompoundCommand(ResumeOptions{Dangerous: true}, prompt)
	wantYolo := []string{"codex", "resume", "--dangerously-bypass-approvals-and-sandbox", "cid", prompt}
	if strings.Join(gotYolo, "\x1f") != strings.Join(wantYolo, "\x1f") {
		t.Fatalf("codex yolo compound = %v, want %v", gotYolo, wantYolo)
	}

	claude := Row{Provider: ProviderClaude, ID: "clid"}
	gotC := claude.CompoundCommand(ResumeOptions{Dangerous: true}, prompt)
	wantC := []string{"claude", "--dangerously-skip-permissions", "--resume", "clid", prompt}
	if strings.Join(gotC, "\x1f") != strings.Join(wantC, "\x1f") {
		t.Fatalf("claude yolo compound = %v, want %v", gotC, wantC)
	}

	jcode := Row{Provider: ProviderJCode, ID: "jid"}
	gotJ := jcode.CompoundCommand(ResumeOptions{Dangerous: true}, prompt)
	wantJ := []string{"jcode", "run", "--no-update", "--resume", "jid", prompt}
	if strings.Join(gotJ, "\x1f") != strings.Join(wantJ, "\x1f") {
		t.Fatalf("jcode compound = %v, want %v", gotJ, wantJ)
	}
}

func TestProjectLearningsDirIsPerProject(t *testing.T) {
	t.Setenv("SHOWAGENT_LEARNINGS_DIR", "/tmp/base")
	a := ProjectLearningsDir("/home/u/proj-a")
	b := ProjectLearningsDir("/home/u/proj-b")
	if a == b {
		t.Fatalf("different projects must map to different dirs: %q == %q", a, b)
	}
	if !strings.HasPrefix(a, "/tmp/base") || !strings.HasPrefix(b, "/tmp/base") {
		t.Fatalf("dirs should sit under the override base: %q %q", a, b)
	}
	// Same project is stable (shared across codex/claude).
	if ProjectLearningsDir("/home/u/proj-a") != a {
		t.Fatal("same project must map to the same dir")
	}
}

func TestCompoundPromptMentionsSharedDir(t *testing.T) {
	p := compoundPrompt("/tmp/base/-home-u-proj-a")
	for _, want := range []string{"/tmp/base/-home-u-proj-a", "Codex", "Claude", "compound-engineering"} {
		if !strings.Contains(p, want) {
			t.Fatalf("compound prompt missing %q", want)
		}
	}
}

func TestProjectLearningsDirRejectsTraversal(t *testing.T) {
	base := "/tmp/base"
	t.Setenv("SHOWAGENT_LEARNINGS_DIR", base)
	// The resolved learnings dir must always stay inside the base, no matter how
	// hostile the session's cwd is.
	for _, cwd := range []string{"..", "../..", ".", "../../etc", "/a/../../b"} {
		got := filepath.Clean(ProjectLearningsDir(cwd))
		if got != base && !strings.HasPrefix(got, base+string(filepath.Separator)) {
			t.Fatalf("cwd %q escaped base: %q", cwd, got)
		}
	}
	// The bare ".." must collapse to the safe fallback rather than the parent.
	if got := ProjectLearningsDir(".."); got != filepath.Join(base, "-unknown-cwd") {
		t.Fatalf("cwd %q = %q, want fallback", "..", got)
	}
}
