package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aytzey/showagent/internal/session"
)

func TestMessagesCommand(t *testing.T) {
	setFixtureHomes(t)
	code, stdout, stderr := runCLI(t, "messages", codexID, "--json")
	if code != 0 {
		t.Fatalf("messages exit=%d: %s", code, stderr)
	}
	var messages []session.UserMessage
	if err := json.Unmarshal([]byte(stdout), &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Number != 2 || messages[0].Text != "last codex message" || messages[1].Number != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	code, stdout, stderr = runCLI(t, "messages", codexID)
	if code != 0 || !strings.Contains(stdout, "[user #2]") || !strings.Contains(stdout, "Untrusted") {
		t.Fatalf("messages text exit=%d: %s / %s", code, stdout, stderr)
	}
	for _, args := range [][]string{{"messages"}, {"messages", "--bad"}, {"messages", codexID, "extra"}} {
		if code, _, _ := runCLI(t, args...); code != 2 {
			t.Fatalf("%v exit=%d, want usage error", args, code)
		}
	}
	if code, _, _ := runCLI(t, "messages", "missing"); code != 1 {
		t.Fatalf("missing session exit=%d", code)
	}
}

func TestConvertThroughUserValidationAndFlagOrder(t *testing.T) {
	setFixtureHomes(t)
	for _, value := range []string{"", "0", "-1", "not-a-number"} {
		args := []string{"convert", codexID, "--to", "claude", "--through-user"}
		if value != "" {
			args = append(args, value)
		}
		if code, _, _ := runCLI(t, args...); code != 2 {
			t.Fatalf("through-user %q exit=%d", value, code)
		}
	}
	for _, flags := range [][]string{{"--through-user", "1", "--scope", "last:1"}, {"--scope", "last:1", "--through-user", "1"}} {
		args := append([]string{"convert", codexID, "--to", "claude", "--dry-run"}, flags...)
		code, stdout, stderr := runCLI(t, args...)
		if code != 0 || !strings.Contains(stdout, "through user #1") || !strings.Contains(stdout, "last user: first codex message") {
			t.Fatalf("flag order changed selection: exit=%d, %s / %s", code, stdout, stderr)
		}
	}
	if code, _, _ := runCLI(t, "convert", codexID, "--to", "claude", "--through-user", "99"); code != 1 {
		t.Fatalf("missing user message exit=%d", code)
	}
}
