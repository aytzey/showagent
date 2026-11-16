package mcpserver

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserMessagesAndContinuationTools(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)
	messages := callTool[userMessagesResult](t, cs, "get_user_messages", map[string]any{"id": codexRateLimitID})
	if messages.ID != codexRateLimitID || len(messages.Messages) != 3 || !messages.SecretsRedacted || messages.Warning == "" {
		t.Fatalf("user messages = %#v", messages)
	}
	if messages.Messages[0].Number != 3 || messages.Messages[1].Number != 2 || messages.Messages[1].Text != "return Retry-After on 429" {
		t.Fatalf("user message order/numbers = %#v", messages.Messages)
	}
	for _, tool := range []string{"branch_session", "convert_session"} {
		args := map[string]any{"id": codexRateLimitID, "through_user": 2}
		if tool == "convert_session" {
			args["target"] = "claude"
		}
		copy := callTool[newSessionResult](t, cs, tool, args)
		transcript := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": copy.ID})
		if transcript.TotalTurns != 3 || transcript.Turns[2].Role != "user" || transcript.Turns[2].Text != "return Retry-After on 429" {
			t.Fatalf("%s copied later turns: %#v", tool, transcript)
		}
		for _, number := range []int{-1, 99} {
			args["through_user"] = number
			if text := callToolExpectError(t, cs, tool, args); !strings.Contains(text, "user message") {
				t.Fatalf("%s invalid user #%d: %s", tool, number, text)
			}
		}
	}
	original := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": codexRateLimitID})
	if original.TotalTurns != 5 {
		t.Fatalf("source changed: %#v", original)
	}
}

func TestUserMessagesAreAlwaysRedactedAndAvailableReadOnly(t *testing.T) {
	codexHome, _ := setFixtureHomes(t)
	writeFile(t, codexHome+"/sessions/secret.jsonl", `
{"type":"session_meta","payload":{"id":"dddddddd-1111-2222-3333-eeeeeeeeeeee","cwd":"/work/test"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"password=synthetic-only\n  formatted\u001b[31m\u202e"}]}}
`)
	for _, options := range []Options{{ReadOnly: true}, {AllowSecrets: true}} {
		cs := newTestSession(t, options)
		result := callTool[userMessagesResult](t, cs, "get_user_messages", map[string]any{"id": "dddddddd-1111-2222-3333-eeeeeeeeeeee"})
		if !result.SecretsRedacted || len(result.Messages) != 1 || strings.Contains(result.Messages[0].Text, "synthetic-only") ||
			strings.ContainsAny(result.Messages[0].Text, "\x1b\u202e") || !strings.Contains(result.Messages[0].Text, "\n  formatted") {
			t.Fatalf("unsafe or damaged picker text: %#v", result)
		}
	}
}

// before_user is the rewind: the copy stops on the reply in front of the
// chosen message, so the new session can ask that message again differently.
func TestBeforeUserEndsTheCopyOnThePrecedingReply(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	preview := callTool[conversionPreviewResult](t, cs, "preview_conversion", map[string]any{"id": codexRateLimitID, "target": "claude", "before_user": 3})
	if preview.Scope != "before user #3" || preview.TransferTurns != 4 || preview.LastUser != "return Retry-After on 429" {
		t.Fatalf("preview = %#v, want the 4 turns in front of user #3", preview)
	}
	for _, tool := range []string{"branch_session", "convert_session"} {
		args := map[string]any{"id": codexRateLimitID, "before_user": 3}
		if tool == "convert_session" {
			args["target"] = "claude"
		}
		copied := callTool[newSessionResult](t, cs, tool, args)
		transcript := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": copied.ID})
		if transcript.TotalTurns != 4 || transcript.Turns[3].Role != "assistant" || transcript.Turns[3].Text != "done, header added" {
			t.Fatalf("%s should end on the reply before user #3: %#v", tool, transcript)
		}

		for _, tt := range []struct {
			scope map[string]any
			want  string
		}{
			{scope: map[string]any{"before_user": 1}, want: "nothing comes before user message #1"},
			{scope: map[string]any{"before_user": 99}, want: "user message #99"},
			{scope: map[string]any{"before_user": 3, "through_user": 2}, want: "cannot be combined"},
		} {
			args := map[string]any{"id": codexRateLimitID}
			if tool == "convert_session" {
				args["target"] = "claude"
			}
			for name, value := range tt.scope {
				args[name] = value
			}
			if text := callToolExpectError(t, cs, tool, args); !strings.Contains(text, tt.want) {
				t.Fatalf("%s %v: error = %q, want %q", tool, tt.scope, text, tt.want)
			}
		}
	}
	if text := callToolExpectError(t, cs, "preview_conversion", map[string]any{"id": codexRateLimitID, "target": "claude", "before_user": 2, "through_user": 2}); !strings.Contains(text, "cannot be combined") {
		t.Fatalf("preview with both ends: %q", text)
	}

	original := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": codexRateLimitID})
	if original.TotalTurns != 5 {
		t.Fatalf("source changed: %#v", original)
	}
	// Two copies were written; the rejected calls wrote nothing.
	if listed := callTool[listSessionsResult](t, cs, "list_sessions", nil); listed.Total != 5 {
		t.Fatalf("sessions = %d, want the 3 fixtures and 2 copies", listed.Total)
	}
}

// A long session is read 30 messages at a time, so every message stays
// reachable as a branch point without flooding the caller's context.
func TestUserMessagesPageBackThroughALongSession(t *testing.T) {
	codexHome, _ := setFixtureHomes(t)
	const id = "eeeeeeee-1111-2222-3333-ffffffffffff"
	var lines strings.Builder
	fmt.Fprintf(&lines, `{"timestamp":"2026-06-05T09:00:00Z","type":"session_meta","payload":{"id":"%s","cwd":"/work/long"}}`+"\n", id)
	for number := 1; number <= 65; number++ {
		fmt.Fprintf(&lines, `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"ask %d"}]}}`+"\n", number)
		fmt.Fprintf(&lines, `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer %d"}]}}`+"\n", number)
	}
	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "05", "rollout-2026-06-05T12-00-00-"+id+".jsonl"), lines.String())
	cs := newTestSession(t, Options{ReadOnly: true})

	olderThan, next := 0, 65
	for _, page := range []struct {
		count    int
		hasOlder bool
	}{{30, true}, {30, true}, {5, false}} {
		args := map[string]any{"id": id}
		if olderThan > 0 {
			args["older_than"] = olderThan
		}
		result := callTool[userMessagesResult](t, cs, "get_user_messages", args)
		if len(result.Messages) != page.count || result.HasOlder != page.hasOlder {
			t.Fatalf("older_than %d: %d messages, has_older %v; want %d, %v", olderThan, len(result.Messages), result.HasOlder, page.count, page.hasOlder)
		}
		for _, message := range result.Messages {
			if message.Number != next || message.Text != fmt.Sprintf("ask %d", next) {
				t.Fatalf("older_than %d: got #%d %q, want #%d", olderThan, message.Number, message.Text, next)
			}
			next--
		}
		olderThan = result.Messages[len(result.Messages)-1].Number
	}
	if next != 0 {
		t.Fatalf("pages stopped at #%d, want every message", next+1)
	}

	end := callTool[userMessagesResult](t, cs, "get_user_messages", map[string]any{"id": id, "older_than": 1})
	if end.Messages == nil || len(end.Messages) != 0 || end.HasOlder {
		t.Fatalf("past the first message = %#v, want an empty page", end)
	}
	if text := callToolExpectError(t, cs, "get_user_messages", map[string]any{"id": id, "older_than": -1}); !strings.Contains(text, "older_than must not be negative") {
		t.Fatalf("negative older_than: %q", text)
	}

	// A number from a later page is a valid branch point.
	preview := callTool[conversionPreviewResult](t, cs, "preview_conversion", map[string]any{"id": id, "target": "claude", "before_user": 4})
	if preview.TransferTurns != 6 || preview.LastUser != "ask 3" {
		t.Fatalf("preview before user #4 = %#v, want 6 turns ending after ask 3", preview)
	}
}
