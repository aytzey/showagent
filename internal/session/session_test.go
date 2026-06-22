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

func TestForkCommandDangerousMode(t *testing.T) {
	codex := Row{Provider: ProviderCodex, ID: "codex-session"}
	if got := strings.Join(codex.ForkCommand(ResumeOptions{Dangerous: true}), " "); got != "codex fork --dangerously-bypass-approvals-and-sandbox codex-session" {
		t.Fatalf("codex fork command = %q", got)
	}

	claude := Row{Provider: ProviderClaude, ID: "claude-session"}
	if got := strings.Join(claude.ForkCommand(ResumeOptions{Dangerous: true}), " "); got != "claude --dangerously-skip-permissions --fork-session --resume claude-session" {
		t.Fatalf("claude fork command = %q", got)
	}
}

func TestStartCommandDangerousMode(t *testing.T) {
	if got := strings.Join(startCommand(ProviderCodex, "continue here", ResumeOptions{Dangerous: true}), " "); got != "codex --dangerously-bypass-approvals-and-sandbox continue here" {
		t.Fatalf("codex handoff command = %q", got)
	}
	if got := strings.Join(startCommand(ProviderClaude, "continue here", ResumeOptions{Dangerous: true}), " "); got != "claude --dangerously-skip-permissions continue here" {
		t.Fatalf("claude handoff command = %q", got)
	}
}

func TestHandoffPromptIncludesTranscript(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	writeFile(t, path, `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","cwd":"/work/codex"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build the feature"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"implemented it"}]}}
`)

	prompt, err := HandoffPrompt(Row{
		Provider: ProviderCodex,
		ID:       "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb",
		CWD:      "/work/codex",
		File:     path,
	}, ProviderClaude, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"taking over", "Transfer scope: all", "Transcript, oldest to newest", "Source session: aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb", "[USER]\nbuild the feature", "[ASSISTANT]\nimplemented it"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("handoff prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestHandoffPromptCanLimitTransferredTurns(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	writeFile(t, path, `
{"timestamp":"2026-06-01T09:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"third"}]}}
`)

	prompt, err := HandoffPrompt(Row{Provider: ProviderCodex, ID: "id", CWD: "/work", File: path}, ProviderClaude, HandoffOptions{MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "first") {
		t.Fatalf("limited handoff included old turn:\n%s", prompt)
	}
	for _, want := range []string{"Transfer scope: last 2", "Transcript excerpt", "[ASSISTANT]\nsecond", "[USER]\nthird"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("limited handoff missing %q:\n%s", want, prompt)
		}
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
