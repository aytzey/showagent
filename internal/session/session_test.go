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

func TestClaudeSubagentsAreIgnored(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", claudeHome)

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
	project := filepath.Join(root, "project")
	nested := filepath.Join(project, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", claudeHome)

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
