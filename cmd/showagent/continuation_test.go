package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// writeCodexConversation replaces the codex fixture with exchanges numbered
// from one: "ask N" from the user, "answer N" from the assistant.
func writeCodexConversation(t *testing.T, exchanges int) {
	t.Helper()
	var lines strings.Builder
	lines.WriteString(`{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"` + codexID + `","cwd":"/work/codex"}}` + "\n")
	for number := 1; number <= exchanges; number++ {
		fmt.Fprintf(&lines, `{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"ask %d"}]}}`+"\n", number)
		fmt.Fprintf(&lines, `{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer %d"}]}}`+"\n", number)
	}
	writeFixture(t, filepath.Join(os.Getenv("CODEX_HOME"), "sessions", "2026", "06", "01", "rollout-2026-06-01T12-00-00-"+codexID+".jsonl"), lines.String())
}

// newSession returns the one discovered session of provider that is not known.
func newSession(t *testing.T, provider session.Provider, known string) session.Row {
	t.Helper()
	var found []session.Row
	for _, row := range session.Discover() {
		if row.Provider == provider && row.ID != known {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		t.Fatalf("new %s sessions = %d, want 1", provider, len(found))
	}
	return found[0]
}

func TestMessagesAllListsEveryMessage(t *testing.T) {
	setFixtureHomes(t)
	// A short session is listed whole, so there is nothing to announce.
	if _, stdout, _ := runCLI(t, "messages", codexID); strings.Contains(stdout, "Showing the newest") {
		t.Fatalf("short listing claims to be cut:\n%s", stdout)
	}

	writeCodexConversation(t, 33)
	code, stdout, stderr := runCLI(t, "messages", codexID)
	if code != 0 {
		t.Fatalf("messages exit=%d: %s", code, stderr)
	}
	for _, want := range []string{"Showing the newest 30 of 33 user messages; add --all for the rest.", "[user #33]\nask 33\n", "[user #4]\n", "--through-user N", "--before-user N"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("default listing missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "[user #3]\n") {
		t.Fatalf("default listing should stop at 30 messages:\n%s", stdout)
	}

	code, stdout, stderr = runCLI(t, "messages", "--all", codexID)
	if code != 0 || !strings.Contains(stdout, "[user #1]\nask 1\n") || strings.Contains(stdout, "Showing the newest") {
		t.Fatalf("--all exit=%d: %s / %s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(t, "messages", codexID, "--json", "--all")
	var messages []session.UserMessage
	if err := json.Unmarshal([]byte(stdout), &messages); code != 0 || err != nil {
		t.Fatalf("--all --json exit=%d, err=%v: %s", code, err, stderr)
	}
	if len(messages) != 33 || messages[0].Number != 33 || messages[32].Number != 1 || messages[32].Text != "ask 1" {
		t.Fatalf("--all --json = %d messages, first %#v", len(messages), messages[0])
	}
}

func TestBranchCopiesInsideTheSessionsOwnAgent(t *testing.T) {
	setFixtureHomes(t)
	code, stdout, stderr := runCLI(t, "branch", claudeID, "--through-user", "1", "--dry-run")
	if code != 0 {
		t.Fatalf("dry-run exit=%d: %s", code, stderr)
	}
	for _, want := range []string{"branch preview", "source:    claude " + claudeID, "target:    claude", "scope:     through user #1 (1 transferable turns)", "last user: first claude message"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("preview missing %q:\n%s", want, stdout)
		}
	}
	// Staying in the same agent is what a branch is for, not a warning.
	if strings.Contains(stdout, "matches source") || strings.Contains(stdout, "conversion preview") {
		t.Fatalf("branch preview reads like a hand-off:\n%s", stdout)
	}
	if matches := claudeSessionFiles(t); len(matches) != 1 {
		t.Fatalf("dry-run wrote sessions: %v", matches)
	}

	code, stdout, stderr = runCLI(t, "branch", "--through-user", "1", claudeID)
	if code != 0 {
		t.Fatalf("branch exit=%d: %s", code, stderr)
	}
	branch := newSession(t, session.ProviderClaude, claudeID)
	for _, want := range []string{"branched claude " + claudeID + " -> claude " + branch.ID + "\n", "resume recipe", "claude --resume " + branch.ID} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("branch output missing %q:\n%s", want, stdout)
		}
	}
	turns, err := session.Transcript(branch)
	if err != nil || len(turns) != 1 || turns[0].Text != "first claude message" {
		t.Fatalf("branch transcript = %#v, err=%v", turns, err)
	}
	original, err := resolveSession(session.Discover(), claudeID)
	if err != nil {
		t.Fatal(err)
	}
	if turns, err := session.Transcript(original); err != nil || len(turns) != 2 {
		t.Fatalf("original session changed: %#v, err=%v", turns, err)
	}

	// The one-time hint belongs to the first hand-off; a branch leaves it unspent.
	if strings.Contains(stderr, session.StarHint) {
		t.Fatalf("branch printed the star hint: %q", stderr)
	}
	if _, _, stderr := runCLI(t, "convert", codexID, "--to", "claude"); !strings.Contains(stderr, session.StarHint) {
		t.Fatalf("first hand-off after a branch should still print the star hint; stderr=%q", stderr)
	}
}

func TestBranchBeforeUserKeepsThePrecedingReply(t *testing.T) {
	setFixtureHomes(t)
	writeCodexConversation(t, 3)

	code, stdout, stderr := runCLI(t, "branch", codexID, "--before-user", "3", "--dry-run")
	if code != 0 || !strings.Contains(stdout, "scope:     before user #3 (4 transferable turns)") || !strings.Contains(stdout, "last user: ask 2") {
		t.Fatalf("dry-run exit=%d: %s / %s", code, stdout, stderr)
	}
	if code, _, stderr := runCLI(t, "branch", codexID, "--before-user", "3"); code != 0 {
		t.Fatalf("branch exit=%d: %s", code, stderr)
	}
	turns, err := session.Transcript(newSession(t, session.ProviderCodex, codexID))
	if err != nil || len(turns) != 4 || turns[2].Text != "ask 2" || turns[3].Role != "assistant" || turns[3].Text != "answer 2" {
		t.Fatalf("branch should end on the reply before user #3: %#v, err=%v", turns, err)
	}

	// The same cut hands off to another agent.
	code, stdout, stderr = runCLI(t, "convert", codexID, "--to", "claude", "--before-user", "2", "--scope", "last:1", "--dry-run")
	if code != 0 || !strings.Contains(stdout, "scope:     before user #2 · last 1 (1 transferable turns)") {
		t.Fatalf("convert dry-run exit=%d: %s / %s", code, stdout, stderr)
	}

	for _, test := range []struct{ number, want string }{
		{"1", "nothing comes before user message #1"},
		{"9", "user message #9 is not in this session"},
	} {
		code, stdout, stderr := runCLI(t, "branch", codexID, "--before-user", test.number)
		if code != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
			t.Fatalf("--before-user %s = %d, %q / %q; want exit 1 containing %q", test.number, code, stdout, stderr, test.want)
		}
	}
}

func TestBranchOpenLaunchesTheBranch(t *testing.T) {
	setFixtureHomes(t)
	workspace := useRealCodexWorkspace(t)
	withFakeCLI(t, "codex")
	calls := stubResume(t, nil)

	code, stdout, stderr := runCLI(t, "branch", codexID, "--open", "--yolo")
	if code != 0 || len(*calls) != 1 {
		t.Fatalf("exit = %d, launches = %d; want 0 and 1; stderr=%s", code, len(*calls), stderr)
	}
	opened := (*calls)[0]
	if opened.row.Provider != session.ProviderCodex || opened.row.ID == "" || opened.row.ID == codexID || opened.row.CWD != workspace || !opened.options.Dangerous {
		t.Fatalf("opened %#v, want the new codex branch in %s", opened, workspace)
	}
	for _, want := range []string{"branched codex " + codexID + " -> codex " + opened.row.ID + "\n", "opening: codex "} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "resume recipe") {
		t.Fatalf("--open should not print the recipe it is about to run:\n%s", stdout)
	}

	// Without its CLI the branch could not be opened, so nothing is written.
	t.Setenv("PATH", t.TempDir())
	code, stdout, stderr = runCLI(t, "branch", claudeID, "--open")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "cannot open the session in claude") || !strings.Contains(stderr, "nothing was written") {
		t.Fatalf("refusal = %d, %q / %q", code, stdout, stderr)
	}
	if matches := claudeSessionFiles(t); len(matches) != 1 {
		t.Fatalf("refusal wrote a session: %v", matches)
	}
}

func TestBranchRejectsMalformedArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing id", args: []string{"branch"}, want: "branch needs a session id"},
		{name: "another agent", args: []string{"branch", codexID, "--to", "claude"}, want: "use 'showagent convert --to <provider>'"},
		{name: "missing through-user value", args: []string{"branch", codexID, "--through-user"}, want: "branch --through-user needs a positive user message number"},
		{name: "zero before-user", args: []string{"branch", codexID, "--before-user", "0"}, want: "branch --before-user needs a positive user message number"},
		{name: "before-user not a number", args: []string{"branch", codexID, "--before-user", "two"}, want: "branch --before-user needs a positive user message number"},
		{name: "both cuts", args: []string{"branch", codexID, "--through-user", "1", "--before-user", "2"}, want: "branch --through-user and --before-user cannot be combined"},
		{name: "both cuts on convert", args: []string{"convert", codexID, "--to", "claude", "--before-user", "2", "--through-user", "1"}, want: "convert --through-user and --before-user cannot be combined"},
		{name: "missing scope", args: []string{"branch", codexID, "--scope"}, want: "branch --scope needs a value"},
		{name: "unknown flag", args: []string{"branch", codexID, "--wat"}, want: "unknown branch argument"},
		{name: "two ids", args: []string{"branch", codexID, "other"}, want: "unexpected branch argument"},
		{name: "open with dry run", args: []string{"branch", codexID, "--open", "--dry-run"}, want: "branch --open and --dry-run cannot be combined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, tt.args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, tt.want) {
				t.Fatalf("run(%v) = %d, %q; want exit 2 containing %q", tt.args, code, stderr, tt.want)
			}
		})
	}
}
