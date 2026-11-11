package mcpserver

import (
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
