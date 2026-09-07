package session

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteCodexConvertedKeepsModelAndDisplayHistory(t *testing.T) {
	tests := []struct {
		name  string
		turns []Turn
	}{
		{
			name: "conversation with formatted text",
			turns: []Turn{
				{Role: "user", Text: "Keep this decision: İstanbul & <clock>\n\n```go\n\tclock.Now()\n```"},
				{Role: "assistant", Text: "Use a fake clock.\n\tpassword = example-only"},
				{Role: "user", Text: "Which clock did we choose?"},
			},
		},
		{
			name: "scoped history starts with assistant",
			turns: []Turn{
				{Role: "assistant", Text: "Earlier answer"},
				{Role: "user", Text: "Continue from there"},
			},
		},
		{
			name: "consecutive messages retain order",
			turns: []Turn{
				{Role: "user", Text: "First request"},
				{Role: "user", Text: "One more constraint"},
				{Role: "assistant", Text: "Working on it"},
				{Role: "assistant", Text: "Finished"},
			},
		},
		{name: "user only", turns: []Turn{{Role: "user", Text: "Unanswered request"}}},
		{name: "assistant only", turns: []Turn{{Role: "assistant", Text: "Scoped answer"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
			converted, err := writeCodexConverted(Row{
				Provider: ProviderClaude,
				ID:       "source-session",
				CWD:      root,
			}, tt.turns)
			if err != nil {
				t.Fatal(err)
			}

			// response_item records remain the model-visible conversation. Adding
			// display events must not duplicate or change the transferable text.
			modelHistory, err := Transcript(converted)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(modelHistory, tt.turns) {
				t.Fatalf("model history = %#v, want %#v", modelHistory, tt.turns)
			}

			data, err := os.ReadFile(converted.File)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			var displayHistory []Turn
			for {
				var record struct {
					Type    string `json:"type"`
					Payload struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					} `json:"payload"`
				}
				if err := decoder.Decode(&record); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if record.Type != "event_msg" {
					continue
				}
				// Codex v0.153.3 ThreadHistoryBuilder handles these events for
				// display; ordinary response_item messages do not populate turns.
				// https://github.com/openai/codex/blob/rust-v0.153.3/codex-rs/app-server-protocol/src/protocol/thread_history.rs
				var role string
				switch record.Payload.Type {
				case "user_message":
					role = "user"
				case "agent_message":
					role = "assistant"
				default:
					t.Fatalf("unexpected imported runtime event %q", record.Payload.Type)
				}
				displayHistory = append(displayHistory, Turn{Role: role, Text: record.Payload.Message})
			}
			if !reflect.DeepEqual(displayHistory, tt.turns) {
				t.Fatalf("Codex display history = %#v, want %#v", displayHistory, tt.turns)
			}
		})
	}
}
