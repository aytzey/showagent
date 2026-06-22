package session

import (
	"os"
	"path/filepath"
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

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
