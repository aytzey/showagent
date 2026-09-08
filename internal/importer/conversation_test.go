package importer

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseTextPreservesContentAndRecognizesExplicitRoles(t *testing.T) {
	input := "User: Merhaba\r\n  girintili\r\n\r\nAssistant:\r\nYanıt 🌿\r\nAssistant: devam\r\n"
	wantTime := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	conversation, err := ParseText(input, TextOptions{AcquiredAt: wantTime})
	if err != nil {
		t.Fatal(err)
	}
	if conversation.SchemaVersion != SchemaVersion || conversation.SourceKind != SourcePastedText {
		t.Fatalf("unexpected conversation metadata: %#v", conversation)
	}
	if !conversation.AcquiredAt.Equal(wantTime) {
		t.Fatalf("acquired_at = %v, want %v", conversation.AcquiredAt, wantTime)
	}
	if len(conversation.Messages) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(conversation.Messages), conversation.Messages)
	}
	want := []Message{
		{Role: RoleUser, Text: "Merhaba\n  girintili\n"},
		{Role: RoleAssistant, Text: "Yanıt 🌿"},
		{Role: RoleAssistant, Text: "devam\n"},
	}
	for i := range want {
		if conversation.Messages[i].Role != want[i].Role || conversation.Messages[i].Text != want[i].Text {
			t.Errorf("message[%d] = %#v, want %#v", i, conversation.Messages[i], want[i])
		}
	}
}

func TestParseTextDoesNotTreatFencedOrQuotedLabelsAsSpeakers(t *testing.T) {
	input := "User:\nHere is code:\n```text\nAssistant: literal\n```\n> Assistant: quoted\nAssistant:\nActual reply"

	conversation, err := ParseText(input, TextOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation.Messages) != 2 {
		t.Fatalf("messages = %d, want 2: %#v", len(conversation.Messages), conversation.Messages)
	}
	wantUser := "Here is code:\n```text\nAssistant: literal\n```\n> Assistant: quoted"
	if conversation.Messages[0].Text != wantUser {
		t.Fatalf("user text = %q, want %q", conversation.Messages[0].Text, wantUser)
	}
	if conversation.Messages[1] != (Message{Role: RoleAssistant, Text: "Actual reply"}) {
		t.Fatalf("assistant message = %#v", conversation.Messages[1])
	}
}

func TestParseTextAliasesAndAmbiguity(t *testing.T) {
	conversation, err := ParseText("preface\nHuman: question\nClaude: answer", TextOptions{})
	var ambiguity *AmbiguityError
	if !errors.As(err, &ambiguity) {
		t.Fatalf("error = %v, want AmbiguityError", err)
	}
	if ambiguity.UnassignedBlocks != 1 || len(conversation.Messages) != 3 {
		t.Fatalf("unexpected ambiguous parse: error=%#v conversation=%#v", ambiguity, conversation)
	}
	if conversation.Messages[1].Role != RoleUser || conversation.Messages[2].Role != RoleAssistant {
		t.Fatalf("aliases were not recognized: %#v", conversation.Messages)
	}

	note, err := ParseText("preface\nHuman: question\nClaude: answer", TextOptions{AsOneContextNote: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(note.Messages) != 1 || note.Messages[0].Role != RoleUser {
		t.Fatalf("one-context-note result = %#v", note.Messages)
	}
	wantNote := "[Imported conversation context from pasted text]\n\npreface\nHuman: question\nClaude: answer"
	if note.Messages[0].Text != wantNote {
		t.Fatalf("one-context-note text = %q, want %q", note.Messages[0].Text, wantNote)
	}
}

func TestParseTextRejectsEmptyInvalidUTF8AndOversize(t *testing.T) {
	if _, err := ParseText("  \n\t", TextOptions{}); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("empty error = %v", err)
	}
	if _, err := ParseText(string([]byte{0xff}), TextOptions{}); !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("UTF-8 error = %v", err)
	}
	if _, err := ParseText(strings.Repeat("x", MaxTextBytes+1), TextOptions{AsOneContextNote: true}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestParseJSONV1StrictAndPreservesText(t *testing.T) {
	data := []byte(`{
  "schema_version": 1,
  "source_kind": "transcript_file",
  "acquired_at": "2026-09-08T12:00:00Z",
  "title": "Plan",
  "messages": [
    {"source_id":"u1","role":"user","text":"line 1\r\n  line 2"},
    {"role":"assistant","text":"yanıt 🌿"}
  ],
  "warnings": [],
  "completeness": "visible_path"
}`)

	conversation, err := ParseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Messages[0].Text != "line 1\n  line 2" {
		t.Fatalf("JSON text = %q", conversation.Messages[0].Text)
	}
	if conversation.Title != "Plan" || conversation.Completeness != CompletenessVisiblePath {
		t.Fatalf("metadata not preserved: %#v", conversation)
	}
}

func TestParseJSONRejectsWrongVersionUnknownFieldsAndUnassignedRoles(t *testing.T) {
	tests := []struct {
		name string
		json string
		want error
	}{
		{"version", `{"schema_version":2,"source_kind":"pasted_text","acquired_at":"2026-09-08T12:00:00Z","messages":[{"role":"user","text":"x"}],"completeness":"unknown"}`, ErrUnsupportedSchema},
		{"unknown field", `{"schema_version":1,"source_kind":"pasted_text","acquired_at":"2026-09-08T12:00:00Z","messages":[{"role":"user","text":"x"}],"completeness":"unknown","secret":true}`, ErrInvalidJSON},
		{"trailing value", `{"schema_version":1,"source_kind":"pasted_text","acquired_at":"2026-09-08T12:00:00Z","messages":[{"role":"user","text":"x"}],"completeness":"unknown"} {}`, ErrInvalidJSON},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseJSON([]byte(test.json)); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	data := []byte(`{"schema_version":1,"source_kind":"pasted_text","acquired_at":"2026-09-08T12:00:00Z","messages":[{"role":"unassigned","text":"x"}],"completeness":"unknown"}`)
	_, err := ParseJSON(data)
	var ambiguity *AmbiguityError
	if !errors.As(err, &ambiguity) {
		t.Fatalf("error = %v, want AmbiguityError", err)
	}
}
