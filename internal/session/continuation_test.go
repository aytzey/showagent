package session

import (
	"os"
	"reflect"
	"slices"
	"strings"
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

func TestUserMessagesListsEveryMessageAndPagesOlderOnes(t *testing.T) {
	var turns []Turn
	for i := 0; i < 35; i++ {
		turns = append(turns, Turn{Role: "user", Text: "ask"}, Turn{Role: "assistant", Text: "answer"}, Turn{Role: "assistant", Text: "more"})
	}
	source := continuationSource(t, turns)

	all, total, err := UserMessages(source, 0, 0)
	if err != nil || len(all) != 35 || total != len(turns) {
		t.Fatalf("all = %d messages of %d turns, err=%v; want 35 of %d", len(all), total, err, len(turns))
	}
	for i, message := range all {
		number := 35 - i
		// A copy ending on user #n keeps the n-1 exchanges before it and the message.
		if message.Number != number || message.Turns != (number-1)*3+1 {
			t.Fatalf("message %d = %#v", i, message)
		}
	}

	page, total, err := UserMessages(source, 31, 10)
	if err != nil || len(page) != 10 || total != len(turns) || page[0].Number != 30 || page[9].Number != 21 {
		t.Fatalf("page below #31 = %#v, total=%d, err=%v", page, total, err)
	}
	oldest, _, err := UserMessages(source, 4, RecentUserMessageLimit)
	if err != nil || len(oldest) != 3 || oldest[0].Number != 3 || oldest[2].Number != 1 {
		t.Fatalf("page below #4 = %#v, err=%v", oldest, err)
	}
	// Nothing is older than the first message; the result stays a list for JSON.
	none, _, err := UserMessages(source, 1, 0)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("page below #1 = %#v, err=%v", none, err)
	}
	recent, err := RecentUserMessages(source)
	if err != nil || len(recent) != RecentUserMessageLimit || recent[0].Number != 35 {
		t.Fatalf("recent = %d messages, err=%v", len(recent), err)
	}
}

func TestContinuationStopsBeforeSelectedUser(t *testing.T) {
	turns := []Turn{
		{Role: "user", Text: "first question"},
		{Role: "assistant", Text: "first answer"},
		{Role: "user", Text: "second question"},
		{Role: "assistant", Text: "second answer"},
		{Role: "user", Text: "wrong turn"},
		{Role: "assistant", Text: "unrelated direction"},
	}
	source := continuationSource(t, turns)
	before, err := os.ReadFile(source.File)
	if err != nil {
		t.Fatal(err)
	}

	// Rewinding to "before user #3" keeps the reply to #2, which a through-user
	// cut cannot do without also keeping #3.
	options := HandoffOptions{BeforeUser: 3}
	for _, target := range []Provider{ProviderCodex, ProviderClaude, ProviderGemini} {
		preview, err := PreviewConversion(source, target, options)
		if err != nil || preview.Scope != "before user #3" || preview.TransferTurns != 4 || preview.LastUser != "second question" {
			t.Fatalf("%s preview = %#v, err=%v", target, preview, err)
		}
		copy, err := Convert(source, target, options)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Transcript(copy)
		if err != nil || !reflect.DeepEqual(got, turns[:4]) {
			t.Fatalf("%s copy = %#v, err=%v; want %#v", target, got, err, turns[:4])
		}
	}

	scoped := HandoffOptions{BeforeUser: 3, MaxTurns: 2}
	if scoped.Label() != "before user #3 · last 2" {
		t.Fatalf("label = %q", scoped.Label())
	}
	trimmed, err := Branch(source, scoped)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Transcript(trimmed)
	if err != nil || !reflect.DeepEqual(got, turns[2:4]) {
		t.Fatalf("scope must be applied after the cut: %#v, err=%v", got, err)
	}

	for name, test := range map[string]struct {
		options HandoffOptions
		want    string
	}{
		"nothing before the first message": {HandoffOptions{BeforeUser: 1}, "nothing comes before user message #1"},
		"missing message":                  {HandoffOptions{BeforeUser: 9}, "user message #9 is not in this session"},
		"negative number":                  {HandoffOptions{BeforeUser: -1}, "must be positive"},
		"both cuts":                        {HandoffOptions{ThroughUser: 2, BeforeUser: 3}, "not both"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PreviewBranch(source, test.options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("preview error = %v, want %q", err, test.want)
			}
			if _, err := Branch(source, test.options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("branch error = %v, want %q", err, test.want)
			}
		})
	}
	after, err := os.ReadFile(source.File)
	if err != nil || string(before) != string(after) {
		t.Fatalf("original session changed: %v", err)
	}
}

// A branch lands in the session's own agent on purpose; only a hand-off that
// names the source agent as its target is worth a warning.
func TestPreviewBranchDoesNotWarnAboutItsOwnAgent(t *testing.T) {
	source := continuationSource(t, []Turn{{Role: "user", Text: "ask"}, {Role: "assistant", Text: "answer"}})
	const warning = "target provider matches source; this creates a local branch/copy"

	branch, err := PreviewBranch(source, HandoffOptions{})
	if err != nil || branch.TargetProvider != ProviderCodex || branch.TransferTurns != 2 || slices.Contains(branch.Warnings, warning) {
		t.Fatalf("branch preview = %#v, err=%v", branch, err)
	}
	conversion, err := PreviewConversion(source, ProviderCodex, HandoffOptions{})
	if err != nil || len(conversion.Warnings) == 0 || conversion.Warnings[0] != warning {
		t.Fatalf("same-agent hand-off preview = %#v, err=%v", conversion, err)
	}
	if !slices.Equal(conversion.Warnings[1:], branch.Warnings) {
		t.Fatalf("previews disagree beyond the same-agent warning: %#v vs %#v", conversion.Warnings, branch.Warnings)
	}
}
