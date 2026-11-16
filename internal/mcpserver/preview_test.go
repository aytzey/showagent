package mcpserver

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewConversionReportsTheHandoffWithoutWriting(t *testing.T) {
	_, claudeHome := setFixtureHomes(t)
	// The preview is a read: it must also be there when writes are switched off.
	for _, options := range []Options{{}, {ReadOnly: true}} {
		cs := newTestSession(t, options)

		preview := callTool[conversionPreviewResult](t, cs, "preview_conversion", map[string]any{"id": codexRateLimitID, "target": "claude"})
		if preview.SourceID != codexRateLimitID || preview.SourceProvider != "codex" || preview.Target != "claude" {
			t.Fatalf("preview names the wrong hand-off: %#v", preview)
		}
		if preview.Workspace != "/work/api-server" || preview.Scope != "all" || preview.TransferTurns != 5 {
			t.Fatalf("preview scope = %#v, want all 5 turns of /work/api-server", preview)
		}
		if preview.LastUser != "now mock the clock in the TTL test" {
			t.Fatalf("last user = %q, want where the target would pick up", preview.LastUser)
		}
		if len(preview.Dropped) == 0 || !strings.Contains(strings.Join(preview.Dropped, "\n"), "tool calls") {
			t.Fatalf("preview should say what a hand-off leaves behind: %#v", preview.Dropped)
		}
		if preview.Warnings == nil || len(preview.Warnings) != 0 {
			t.Fatalf("warnings = %#v, want an empty list", preview.Warnings)
		}
		if !preview.SecretsRedacted || !strings.Contains(preview.Warning, "untrusted") || !strings.Contains(preview.Warning, "Nothing was written") {
			t.Fatalf("preview must declare redaction and its trust boundary: %#v", preview)
		}
	}

	matches, err := filepath.Glob(filepath.Join(claudeHome, "projects", "*", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("preview wrote a session: %v", matches)
	}
	listed := callTool[listSessionsResult](t, newTestSession(t), "list_sessions", nil)
	if listed.Total != 3 {
		t.Fatalf("sessions after previews = %d, want the 3 fixtures", listed.Total)
	}
}

func TestPreviewConversionMatchesTheScopeThatWouldBeWritten(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	tests := []struct {
		name      string
		scope     map[string]any
		wantScope string
		wantTurns int
		wantLast  string
	}{
		{name: "through a user message", scope: map[string]any{"through_user": 2}, wantScope: "through user #2", wantTurns: 3, wantLast: "return Retry-After on 429"},
		{name: "most recent turns", scope: map[string]any{"max_turns": 2}, wantScope: "last 2", wantTurns: 2, wantLast: "now mock the clock in the TTL test"},
		{name: "both", scope: map[string]any{"through_user": 2, "max_turns": 1}, wantScope: "through user #2 · last 1", wantTurns: 1, wantLast: "return Retry-After on 429"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"id": codexRateLimitID, "target": "claude"}
			for name, value := range tt.scope {
				args[name] = value
			}
			preview := callTool[conversionPreviewResult](t, cs, "preview_conversion", args)
			if preview.Scope != tt.wantScope || preview.TransferTurns != tt.wantTurns || preview.LastUser != tt.wantLast {
				t.Fatalf("preview = %q, %d turns, last %q; want %q, %d turns, last %q",
					preview.Scope, preview.TransferTurns, preview.LastUser, tt.wantScope, tt.wantTurns, tt.wantLast)
			}

			// What the preview promised is what convert_session writes.
			converted := callTool[newSessionResult](t, cs, "convert_session", args)
			transcript := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": converted.ID})
			if transcript.TotalTurns != tt.wantTurns {
				t.Fatalf("converted turns = %d, preview promised %d", transcript.TotalTurns, tt.wantTurns)
			}
		})
	}
}

func TestBranchSessionHonorsMaxTurns(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	branched := callTool[newSessionResult](t, cs, "branch_session", map[string]any{"id": codexRateLimitID, "max_turns": 2})
	transcript := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": branched.ID})
	if transcript.TotalTurns != 2 || transcript.Turns[1].Text != "now mock the clock in the TTL test" {
		t.Fatalf("branch should keep the 2 most recent turns: %#v", transcript.Turns)
	}

	// A branch is previewed by naming the session's own agent as the target.
	preview := callTool[conversionPreviewResult](t, cs, "preview_conversion", map[string]any{"id": codexRateLimitID, "target": "codex", "max_turns": 2})
	if preview.TransferTurns != 2 || !strings.Contains(strings.Join(preview.Warnings, "\n"), "branch/copy") {
		t.Fatalf("same-agent preview = %#v, want 2 turns and the branch note", preview)
	}
}

func TestPreviewConversionRedactsWhatItReturns(t *testing.T) {
	codexHome, _ := setFixtureHomes(t)
	const id = "dddddddd-1111-2222-3333-eeeeeeeeeeee"
	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "04", "rollout-"+id+".jsonl"), `
{"timestamp":"2026-06-04T09:00:00Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/work/API_KEY=workspace-secret\u001b[31m\u202e"}}
{"timestamp":"2026-06-04T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"deploy with password=synthetic-only\u001b[31m\u202e"}]}}
`)
	// --allow-secrets widens get_transcript only; a preview stays redacted.
	for _, options := range []Options{{}, {AllowSecrets: true}} {
		cs := newTestSession(t, options)
		preview := callTool[conversionPreviewResult](t, cs, "preview_conversion", map[string]any{"id": id, "target": "claude"})
		returned := preview.Workspace + "\n" + preview.LastUser
		if strings.Contains(returned, "workspace-secret") || strings.Contains(returned, "synthetic-only") || strings.ContainsAny(returned, "\x1b\u202e") {
			t.Fatalf("preview leaked a secret or control character: %#v", preview)
		}
		if !strings.Contains(preview.LastUser, "deploy with") || !preview.SecretsRedacted {
			t.Fatalf("preview should keep the readable part of the message: %#v", preview)
		}
	}
}

func TestHandoffToolsRejectInvalidScopeAndTarget(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	for _, tool := range []string{"preview_conversion", "convert_session", "branch_session"} {
		args := map[string]any{"id": codexRateLimitID, "max_turns": -1}
		if tool != "branch_session" {
			args["target"] = "claude"
		}
		if text := callToolExpectError(t, cs, tool, args); !strings.Contains(text, "max_turns must not be negative") {
			t.Fatalf("%s negative max_turns error = %q", tool, text)
		}
	}

	for _, tt := range []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "unknown target", args: map[string]any{"id": codexRateLimitID, "target": "cursor"}, want: "unsupported provider"},
		{name: "source-only target", args: map[string]any{"id": codexRateLimitID, "target": "antigravity"}, want: "antigravity"},
		{name: "user message out of range", args: map[string]any{"id": codexRateLimitID, "target": "claude", "through_user": 99}, want: "user message #99"},
		{name: "unknown session", args: map[string]any{"id": "nope", "target": "claude"}, want: "list_sessions"},
	} {
		if text := callToolExpectError(t, cs, "preview_conversion", tt.args); !strings.Contains(text, tt.want) {
			t.Fatalf("%s: error = %q, want %q", tt.name, text, tt.want)
		}
	}

	listed := callTool[listSessionsResult](t, cs, "list_sessions", nil)
	if listed.Total != 3 {
		t.Fatalf("rejected calls wrote sessions: total = %d, want 3", listed.Total)
	}
}
