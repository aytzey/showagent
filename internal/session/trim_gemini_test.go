package session

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTrimGeminiLegacyPreservesNativeMessageData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-fixture.json")
	row := Row{ID: "gemini-session", File: path}
	source := []byte(`{
		"sessionId":"gemini-session","projectHash":"project","kind":"main",
		"directories":["/sample"],"summary":"removed text in the generated summary",
		"memoryScratchpad":{"workflowSummary":"removed text in the generated scratchpad"},
		"messages":[
			{"id":"1","type":"user","content":"<session_context>injected"},
			{"id":"2","type":"user","content":[{"text":"keep question"}]},
			{"id":"3","type":"gemini","content":"keep answer","toolCalls":[{"id":"tool","args":{"value":9007199254740993}}]},
			{"id":"4","type":"user","content":"remove question"},
			{"id":"5","type":"gemini","content":"remove answer"}
		]
	}`)
	result, err := trimGemini(row, source, HandoffOptions{BeforeUser: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.keptUsers != 1 || result.removedUsers != 1 || result.keptTurns != 2 ||
		result.removedTurns != 2 || result.boundaryText != "remove question" {
		t.Fatalf("wrong trim summary: %+v", result)
	}
	for _, wanted := range []string{"9007199254740993", "toolCalls", "directories", "injected"} {
		if !bytes.Contains(result.replacement, []byte(wanted)) {
			t.Fatalf("lost retained native data %q", wanted)
		}
	}
	for _, unwanted := range []string{"remove question", "remove answer", "removed text", "memoryScratchpad"} {
		if bytes.Contains(result.replacement, []byte(unwanted)) {
			t.Fatalf("kept deleted data %q", unwanted)
		}
	}
	if err := os.WriteFile(path, result.replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, ok := parseGemini(path, "/sample")
	if !ok || parsed.ID != row.ID || parsed.LastUser != "keep question" {
		t.Fatalf("trimmed session did not rediscover with the same ID: %+v", parsed)
	}
}

func TestTrimGeminiLogReplaysNativeUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-fixture.jsonl")
	row := Row{ID: "gemini-session", File: path}
	source := []byte(`{"sessionId":"gemini-session","projectHash":"project","kind":"main"}
{"$set":{"messages":[{"id":"1","type":"user","content":"first"},{"id":"2","type":"gemini","content":"initial answer"},{"id":"3","type":"user","content":"rewound question"}]}}
{"$rewindTo":"3"}
{"id":"4","type":"user","content":"keep second question"}
{"id":"5","type":"gemini","content":"remove second answer"}
{"id":"2","type":"gemini","content":"latest first answer","tokens":{"total":10}}
{"$set":{"directories":["/sample"],"summary":"remove stale summary"}}
`)
	result, err := trimGemini(row, source, HandoffOptions{ThroughUser: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.keptUsers != 2 || result.removedUsers != 0 || result.keptTurns != 3 ||
		result.removedTurns != 1 || result.boundaryText != "keep second question" {
		t.Fatalf("wrong trim summary: %+v", result)
	}
	if err := os.WriteFile(path, result.replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	turns, err := geminiTranscript(path)
	if err != nil || len(turns) != 3 || turns[1].Text != "latest first answer" || turns[2].Text != "keep second question" {
		t.Fatalf("wrong native transcript: %v, %v", turns, err)
	}
	for _, unwanted := range []string{"rewound question", "remove second answer", "remove stale summary", "initial answer"} {
		if bytes.Contains(result.replacement, []byte(unwanted)) {
			t.Fatalf("kept deleted data %q", unwanted)
		}
	}
}

func TestTrimGeminiRejectsUnrecognizedLog(t *testing.T) {
	header := `{"sessionId":"gemini-session","projectHash":"project"}` + "\n"
	row := Row{ID: "gemini-session", File: "session-fixture.jsonl"}
	for _, test := range []struct {
		record string
		want   string
	}{
		{`{"$patch":{"removeIds":["first"]}}`, "message patches"},
		{`{"$rewindTo":"missing"}`, "unknown rewind target"},
		{`{"$unexpected":true}`, "unrecognized JSONL record"},
		{`!`, "malformed JSONL record"},
	} {
		result, err := trimGemini(row, []byte(header+test.record), HandoffOptions{ThroughUser: 1})
		if err == nil || !strings.Contains(err.Error(), test.want) || result.replacement != nil {
			t.Fatalf("record %s returned %v, %v", test.record, result, err)
		}
	}
}

func TestTrimGeminiStoreRoundTrip(t *testing.T) {
	const header = `"sessionId":"gemini-session","projectHash":"project","startTime":"2026-01-01T00:00:00Z"`
	const first = `{"id":"1","type":"user","content":"keep question"}`
	const answer = `{"id":"2","type":"gemini","content":"keep answer"}`
	const last = `{"id":"3","type":"user","content":"remove question"}`
	const reply = `{"id":"4","type":"gemini","content":"remove answer"}`
	for _, test := range []struct {
		name   string
		source string
	}{
		{"legacy.json", "{" + header + `,"messages":[` + strings.Join([]string{first, answer, last, reply}, ",") + "]}"},
		{"journal.jsonl", strings.Join([]string{"{" + header + "}", first, answer, last, reply}, "\n") + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GEMINI_CLI_HOME", t.TempDir())
			workspace := t.TempDir()
			project := filepath.Join(defaultGeminiHome(), "tmp", "isolated")
			chats := filepath.Join(project, "chats")
			if err := os.MkdirAll(chats, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, ".project_root"), []byte(workspace+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(chats, "session-"+test.name)
			if err := os.WriteFile(path, []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}

			assertTrimStoreRoundTrip(t, geminiProvider{}.Discover, workspace, "gemini-session", path,
				[]string{"gemini", "--resume", "gemini-session"}, "keep question", "keep answer")
		})
	}
}

func assertTrimStoreRoundTrip(t *testing.T, discover func() []Row, workspace, id, path string, command []string, question, answer string) {
	t.Helper()
	rows := discover()
	if len(rows) != 1 || rows[0].ID != id || rows[0].File != path || rows[0].CWD != workspace {
		t.Fatalf("wrong original session: %+v", rows)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanTrim(rows[0], HandoffOptions{BeforeUser: 2})
	if err != nil || plan.KeptTurns != 2 || plan.RemovedTurns != 2 ||
		plan.KeptUserMessages != 1 || plan.RemovedUserMessages != 1 {
		t.Fatalf("wrong trim preview: %+v, %v", plan, err)
	}
	if afterPreview, err := os.ReadFile(path); err != nil || !bytes.Equal(afterPreview, source) {
		t.Fatalf("preview changed source: %v", err)
	}
	applied, err := ApplyTrim(plan)
	if err != nil || applied.ID != id || applied.File != path || applied.CWD != workspace {
		t.Fatalf("trim changed session identity: %+v, %v", applied, err)
	}
	rows = discover()
	if len(rows) != 1 || rows[0].ID != id || rows[0].LastUser != question || rows[0].File != path {
		t.Fatalf("wrong rediscovered session: %+v", rows)
	}
	turns, err := Transcript(rows[0])
	expectedTurns := []Turn{{Role: "user", Text: question}, {Role: "assistant", Text: answer}}
	if err != nil || !slices.Equal(turns, expectedTurns) {
		t.Fatalf("wrong transcript: %+v, %v", turns, err)
	}
	messages, total, err := UserMessages(rows[0], 0, 0)
	if err != nil || len(messages) != 1 || messages[0].Number != 1 ||
		messages[0].Text != question || total != 2 {
		t.Fatalf("wrong numbered user messages: %+v, total %d, %v", messages, total, err)
	}
	recipe := RecipeFor(rows[0], ResumeOptions{})
	if recipe.ID != id || recipe.CWD != workspace || !slices.Equal(recipe.Command, command) {
		t.Fatalf("wrong resume recipe: %+v", recipe)
	}
}
