package session

import (
	"os"
	"reflect"
	"testing"
)

func continuationSource(t *testing.T, turns []Turn) Row {
	t.Helper()
	setEmptyHomes(t, t.TempDir())
	row, err := writeCodexConverted(Row{CWD: t.TempDir()}, turns)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestRecentUserMessagesCountsUsersAndKeepsAbsoluteNumbers(t *testing.T) {
	turns := []Turn{{Role: "user", Text: "# AGENTS.md instructions\nnot a conversation prompt"}}
	for i := 0; i < 35; i++ {
		turns = append(turns, Turn{Role: "user", Text: "same text\n  indentation"})
		for j := 0; j < 4; j++ {
			turns = append(turns, Turn{Role: "assistant", Text: "commentary"})
		}
	}
	source := continuationSource(t, turns)
	messages, err := RecentUserMessages(source)
	if err != nil || len(messages) != 30 {
		t.Fatalf("messages = %d, err=%v; want last 30 users", len(messages), err)
	}
	for i, message := range messages {
		if message.Number != 35-i || message.Text != "same text\n  indentation" {
			t.Fatalf("message %d = %#v", i, message)
		}
	}
	// An active session may append while the chooser is open.
	file, err := os.OpenFile(source.File, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"new direction\"}]}}\n")
	if closeErr := file.Close(); err != nil || closeErr != nil {
		t.Fatalf("append = %v, close = %v", err, closeErr)
	}
	branched, err := Branch(source, HandoffOptions{ThroughUser: messages[0].Number})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Transcript(branched)
	if err != nil || len(got) != 171 || got[len(got)-1].Role != "user" || got[len(got)-1].Text == "new direction" {
		t.Fatalf("continuation point shifted after append: %d turns, err=%v", len(got), err)
	}
}

func TestContinuationCopiesOnlyThroughSelectedUserAcrossTargets(t *testing.T) {
	turns := []Turn{
		{Role: "user", Text: "first question"},
		{Role: "assistant", Text: "first answer\n```go\n  work()\n```"},
		{Role: "user", Text: "same request Türkçe"},
		{Role: "assistant", Text: "later answer"},
		{Role: "user", Text: "same request Türkçe"},
		{Role: "assistant", Text: "unrelated direction"},
	}
	source := continuationSource(t, turns)
	before, err := os.ReadFile(source.File)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []Provider{ProviderCodex, ProviderClaude, ProviderPi, ProviderGemini, ProviderJCode} {
		t.Run(string(target), func(t *testing.T) {
			options := HandoffOptions{ThroughUser: 2}
			preview, err := PreviewConversion(source, target, options)
			if err != nil || preview.TransferTurns != 3 || preview.LastUser != "same request Türkçe" {
				t.Fatalf("preview = %#v, err=%v", preview, err)
			}
			copy, err := Convert(source, target, options)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Transcript(copy)
			if err != nil || !reflect.DeepEqual(got, turns[:3]) {
				t.Fatalf("copy = %#v, err=%v; want %#v", got, err, turns[:3])
			}
		})
	}
	full, err := Branch(source, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Transcript(full)
	if err != nil || !reflect.DeepEqual(got, turns) {
		t.Fatalf("default full copy changed: %#v, err=%v", got, err)
	}
	trimmed, err := Convert(source, ProviderCodex, HandoffOptions{ThroughUser: 2, MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	got, err = Transcript(trimmed)
	if err != nil || !reflect.DeepEqual(got, turns[1:3]) {
		t.Fatalf("scope must be applied after cutoff: %#v, err=%v", got, err)
	}
	after, err := os.ReadFile(source.File)
	if err != nil || string(before) != string(after) {
		t.Fatalf("original session changed: %v", err)
	}
}

func TestContinuationRejectsMissingUserWithoutWriting(t *testing.T) {
	source := continuationSource(t, []Turn{{Role: "assistant", Text: "assistant only"}})
	messages, err := RecentUserMessages(source)
	if err != nil || len(messages) != 0 {
		t.Fatalf("assistant-only choices = %#v, err=%v", messages, err)
	}
	for _, number := range []int{-1, 1, 99} {
		options := HandoffOptions{ThroughUser: number}
		if _, err := PreviewConversion(source, ProviderClaude, options); err == nil {
			t.Fatalf("preview accepted user #%d", number)
		}
		if _, err := Convert(source, ProviderClaude, options); err == nil {
			t.Fatalf("conversion accepted user #%d", number)
		}
	}
	if _, err := os.Stat(defaultClaudeHome()); !os.IsNotExist(err) {
		t.Fatalf("invalid selection created target state: %v", err)
	}
}
