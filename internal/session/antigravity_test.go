package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// antigravityFixtureHome pins every provider to an empty store and returns a
// fresh ANTIGRAVITY_HOME (the directory that holds antigravity-cli/ etc.).
func antigravityFixtureHome(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	setEmptyHomes(t, root)
	home := filepath.Join(root, ".gemini")
	t.Setenv("ANTIGRAVITY_HOME", home)
	return home
}

func antigravityLine(t *testing.T, index int, source, kind, createdAt, content string) string {
	t.Helper()
	record := map[string]any{
		"step_index": index,
		"source":     source,
		"type":       kind,
		"status":     "DONE",
		"created_at": createdAt,
	}
	if content != "" {
		record["content"] = content
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func writeAntigravityTranscript(t *testing.T, home, surface, id, file string, lines ...string) string {
	t.Helper()
	path := filepath.Join(home, surface, "brain", id, ".system_generated", "logs", file)
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	return path
}

func TestDiscoverFindsAntigravityCLIConversations(t *testing.T) {
	home := antigravityFixtureHome(t)
	const id = "6f1d2c3b-0000-4000-8000-000000000001"
	path := writeAntigravityTranscript(t, home, "antigravity-cli", id, "transcript_full.jsonl",
		antigravityLine(t, 0, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T09:00:00Z",
			"<USER_REQUEST>\nfix the flaky retry test password hunter2\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\ncursor at foo.go:12\n</ADDITIONAL_METADATA>"),
		antigravityLine(t, 1, "SYSTEM", "CONVERSATION_HISTORY", "2026-10-01T09:00:00Z", ""),
		antigravityLine(t, 2, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:05Z", ""),
		antigravityLine(t, 3, "MODEL", "RUN_COMMAND", "2026-10-01T09:00:06Z", "go test ./..."),
		antigravityLine(t, 4, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:09Z", "Use a fake clock."),
		antigravityLine(t, 5, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T09:05:00Z", "<USER_REQUEST>\nnow ship it\n</USER_REQUEST>"),
	)
	writeFile(t, filepath.Join(home, "antigravity-cli", "conversations", id+".db"), "sqlite")
	writeFile(t, filepath.Join(home, "antigravity-cli", "history.jsonl"),
		`{"display":"fix the flaky retry test","timestamp":1759309200000,"workspace":"/work/old","conversationId":"`+id+`"}`+"\n"+
			`{"display":"/model","timestamp":1759309250000,"workspace":"/work/old","type":"slash_command"}`+"\n"+
			`{"display":"now ship it","timestamp":1759309500000,"workspace":"/work/agy","conversationId":"`+id+`"}`+"\n")

	rows := Discover()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %#v", rows)
	}
	row := rows[0]
	if row.Provider != ProviderAntigravity || row.ID != id || row.File != path {
		t.Fatalf("row = %#v", row)
	}
	if row.CWD != "/work/agy" || row.LaunchCWD != "/work/agy" {
		t.Fatalf("workspace = %q/%q, want the latest history workspace", row.CWD, row.LaunchCWD)
	}
	if row.HandoffOnly {
		t.Fatal("a CLI conversation with its step database must be natively resumable")
	}
	if !strings.HasPrefix(row.FirstUser, "fix the flaky retry test password") {
		t.Fatalf("first user = %q", row.FirstUser)
	}
	if strings.Contains(row.FirstUser, "hunter2") || strings.Contains(row.FirstUser, "USER_REQUEST") || strings.Contains(row.FirstUser, "cursor at") {
		t.Fatalf("preview leaked wrapper, metadata or secret: %q", row.FirstUser)
	}
	if row.LastUser != "now ship it" {
		t.Fatalf("last user = %q", row.LastUser)
	}
	if got := row.LastAt.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-10-01T09:05:00Z" {
		t.Fatalf("last at = %s", got)
	}

	got := strings.Join(row.ResumeCommand(ResumeOptions{}), " ")
	if got != "agy --conversation "+id {
		t.Fatalf("resume = %q", got)
	}
	got = strings.Join(row.ResumeCommand(ResumeOptions{Dangerous: true}), " ")
	if got != "agy --dangerously-skip-permissions --conversation "+id {
		t.Fatalf("dangerous resume = %q", got)
	}
}

func TestAntigravityTranscriptKeepsConversationOnly(t *testing.T) {
	home := antigravityFixtureHome(t)
	const id = "6f1d2c3b-0000-4000-8000-000000000002"
	path := writeAntigravityTranscript(t, home, "antigravity-cli", id, "transcript_full.jsonl",
		antigravityLine(t, 0, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T09:00:00Z", "<USER_REQUEST>\nexplain\n\n```go\n\tx := 1\n```\n</USER_REQUEST>"),
		antigravityLine(t, 1, "SYSTEM", "CONVERSATION_HISTORY", "2026-10-01T09:00:00Z", "older summary"),
		antigravityLine(t, 2, "MODEL", "VIEW_FILE", "2026-10-01T09:00:01Z", "package main"),
		antigravityLine(t, 3, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:02Z", "Türkçe answer\n  indented"),
		`not json`,
	)

	turns, err := antigravityTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{
		{Role: "user", Text: "explain\n\n```go\n\tx := 1\n```"},
		{Role: "assistant", Text: "Türkçe answer\n  indented"},
	}
	if len(turns) != len(want) {
		t.Fatalf("turns = %#v", turns)
	}
	for index := range want {
		if turns[index] != want[index] {
			t.Fatalf("turn %d = %#v, want %#v", index, turns[index], want[index])
		}
	}
}

func TestAntigravityUserTextKeepsQuotedDelimiters(t *testing.T) {
	for content, want := range map[string]string{
		"<USER_REQUEST>\nplain\n</USER_REQUEST>":                                                                                   "plain",
		"<USER_REQUEST>\nplain\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nt\n</ADDITIONAL_METADATA>":                                 "plain",
		"<USER_REQUEST>\nwhy does </USER_REQUEST> end early?\n</USER_REQUEST>\n<USER_SETTINGS_CHANGE>\nx\n</USER_SETTINGS_CHANGE>": "why does </USER_REQUEST> end early?",
		"<USER_REQUEST>\n  indented first line\n</USER_REQUEST>":                                                                   "  indented first line",
		"no wrapper at all":            "no wrapper at all",
		"<USER_REQUEST>\nunterminated": "<USER_REQUEST>\nunterminated",
	} {
		if got := antigravityUserText(content); got != want {
			t.Fatalf("antigravityUserText(%q) = %q, want %q", content, got, want)
		}
	}
}

func TestAntigravityPrefersFullTranscriptAndFallsBack(t *testing.T) {
	home := antigravityFixtureHome(t)
	const full = "6f1d2c3b-0000-4000-8000-000000000003"
	const short = "6f1d2c3b-0000-4000-8000-000000000004"
	writeAntigravityTranscript(t, home, "antigravity-cli", full, "transcript.jsonl",
		antigravityLine(t, 0, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T09:00:00Z", "<USER_REQUEST>\nq\n</USER_REQUEST>"),
		antigravityLine(t, 1, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:01Z", "TRUNCATED"))
	fullPath := writeAntigravityTranscript(t, home, "antigravity-cli", full, "transcript_full.jsonl",
		antigravityLine(t, 0, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T09:00:00Z", "<USER_REQUEST>\nq\n</USER_REQUEST>"),
		antigravityLine(t, 1, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:01Z", "COMPLETE"))
	writeAntigravityTranscript(t, home, "antigravity-cli", short, "transcript_full.jsonl",
		antigravityLine(t, 0, "SYSTEM", "CONVERSATION_HISTORY", "2026-10-01T09:00:00Z", ""))
	shortPath := writeAntigravityTranscript(t, home, "antigravity-cli", short, "transcript.jsonl",
		antigravityLine(t, 0, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T08:00:00Z", "<USER_REQUEST>\nkept\n</USER_REQUEST>"))

	byID := map[string]Row{}
	for _, row := range Discover() {
		byID[row.ID] = row
	}
	if byID[full].File != fullPath || byID[short].File != shortPath {
		t.Fatalf("rows = %#v", byID)
	}
	// Neither conversation has a step database, so agy cannot reopen them.
	if !byID[full].HandoffOnly || byID[full].CWD != "(unknown cwd)" {
		t.Fatalf("row without a step database = %#v", byID[full])
	}
}

func TestAntigravityIDEConversationsAreHandoffSources(t *testing.T) {
	home := antigravityFixtureHome(t)
	const shared = "6f1d2c3b-0000-4000-8000-000000000005"
	const ide = "6f1d2c3b-0000-4000-8000-000000000006"
	const legacy = "6f1d2c3b-0000-4000-8000-000000000007"
	conversation := []string{
		antigravityLine(t, 0, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T09:00:00Z", "<USER_REQUEST>\nfrom the editor\n</USER_REQUEST>"),
		antigravityLine(t, 1, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:01Z", "done"),
	}
	cliPath := writeAntigravityTranscript(t, home, "antigravity-cli", shared, "transcript_full.jsonl", conversation...)
	writeFile(t, filepath.Join(home, "antigravity-cli", "conversations", shared+".db"), "sqlite")
	writeAntigravityTranscript(t, home, "antigravity-ide", shared, "transcript_full.jsonl", conversation...)
	writeAntigravityTranscript(t, home, "antigravity-ide", ide, "transcript_full.jsonl", conversation...)
	writeAntigravityTranscript(t, home, "antigravity", legacy, "transcript.jsonl", conversation...)
	// A brain directory with only planning artifacts is not a conversation.
	writeFile(t, filepath.Join(home, "antigravity", "brain", "6f1d2c3b-0000-4000-8000-000000000008", "task.md"), "- [ ] plan")

	rows := Discover()
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %#v", rows)
	}
	for _, row := range rows {
		switch row.ID {
		case shared:
			if row.HandoffOnly || row.File != cliPath {
				t.Fatalf("the CLI copy of a shared id must win: %#v", row)
			}
		case ide, legacy:
			if !row.HandoffOnly || row.CWD != "(unknown cwd)" {
				t.Fatalf("IDE conversation must be a hand-off source: %#v", row)
			}
			if row.ResumeCommand(ResumeOptions{}) != nil {
				t.Fatalf("IDE conversation must not offer agy resume: %#v", row)
			}
		default:
			t.Fatalf("unexpected row %#v", row)
		}
	}

	targets := antigravityProvider{}.ScanTargets()
	if len(targets) != 3 {
		t.Fatalf("scan targets = %#v", targets)
	}
}

func TestAntigravitySkipsEmptyConversations(t *testing.T) {
	home := antigravityFixtureHome(t)
	writeAntigravityTranscript(t, home, "antigravity-cli", "6f1d2c3b-0000-4000-8000-000000000009", "transcript_full.jsonl",
		antigravityLine(t, 0, "SYSTEM", "CONVERSATION_HISTORY", "2026-10-01T09:00:00Z", ""),
		antigravityLine(t, 1, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:01Z", ""))
	if err := os.MkdirAll(filepath.Join(home, "antigravity-cli", "brain", "not-a-conversation"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rows := Discover(); len(rows) != 0 {
		t.Fatalf("expected no rows, got %#v", rows)
	}
}

// protoField encodes one length-delimited protobuf field, enough to build
// agy's summary index in tests without a protobuf dependency.
func protoField(number int, value []byte) []byte {
	encoded := appendProtoVarint(nil, uint64(number)<<3|2)
	encoded = appendProtoVarint(encoded, uint64(len(value)))
	return append(encoded, value...)
}

func appendProtoVarint(data []byte, value uint64) []byte {
	for value >= 0x80 {
		data = append(data, byte(value)|0x80)
		value >>= 7
	}
	return append(data, byte(value))
}

// antigravitySummaryIndex builds a CascadeTrajectorySummaries message mapping
// each conversation id to one workspace URI.
func antigravitySummaryIndex(workspaces map[string]string) []byte {
	var message []byte
	for id, uri := range workspaces {
		metadata := protoField(1, []byte(uri))
		// A varint step count before the workspaces must be skipped cleanly.
		summary := append(protoField(1, []byte("title")), 0x10, 0x05)
		summary = append(summary, protoField(9, metadata)...)
		entry := append(protoField(1, []byte(id)), protoField(2, summary)...)
		message = append(message, protoField(1, entry)...)
	}
	return message
}

func TestAntigravityWorkspaceSources(t *testing.T) {
	home := antigravityFixtureHome(t)
	cli := filepath.Join(home, "antigravity-cli")
	const indexed = "6f1d2c3b-0000-4000-8000-000000000010"
	const cached = "6f1d2c3b-0000-4000-8000-000000000011"
	const ide = "6f1d2c3b-0000-4000-8000-000000000012"
	conversation := []string{
		antigravityLine(t, 0, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T09:00:00Z", "<USER_REQUEST>\nq\n</USER_REQUEST>"),
		antigravityLine(t, 1, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:01Z", "a"),
	}
	for _, id := range []string{indexed, cached} {
		writeAntigravityTranscript(t, home, "antigravity-cli", id, "transcript_full.jsonl", conversation...)
		writeFile(t, filepath.Join(cli, "conversations", id+".db"), "sqlite")
	}
	writeAntigravityTranscript(t, home, "antigravity-ide", ide, "transcript_full.jsonl", conversation...)

	writeFile(t, filepath.Join(cli, "jetbox_summaries_proto.pb"), string(antigravitySummaryIndex(map[string]string{
		indexed: "file:///work/indexed%20space",
	})))
	// The weaker fallbacks disagree with the index; the index must win.
	writeFile(t, filepath.Join(cli, "cache", "last_conversations.json"), `{"/work/stale":"`+indexed+`","/work/cached":"`+cached+`"}`)
	writeFile(t, filepath.Join(cli, "cache", "conversation_metadata.json"),
		`{"conversations":{"`+ide+`":{"summary":{"ID":"`+ide+`","WorkspaceURIs":["file:///work/ide"]}}}}`)

	byID := map[string]Row{}
	for _, row := range Discover() {
		byID[row.ID] = row
	}
	if got := byID[indexed].CWD; got != filepath.FromSlash("/work/indexed space") {
		t.Fatalf("indexed workspace = %q", got)
	}
	if got := byID[cached].CWD; got != "/work/cached" {
		t.Fatalf("cached workspace = %q", got)
	}
	if row := byID[ide]; row.CWD != filepath.FromSlash("/work/ide") || !row.HandoffOnly {
		t.Fatalf("IDE row = %#v", row)
	}
}

func TestAntigravitySummaryIndexRejectsMalformedInput(t *testing.T) {
	valid := antigravitySummaryIndex(map[string]string{"id": "file:///work"})
	for _, message := range [][]byte{
		valid[:len(valid)-3],
		{0x0a, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01},
		{0x0b},
		protoField(1, protoField(1, []byte("id"))),
		antigravitySummaryIndex(map[string]string{"id": "https://example.com/work"}),
	} {
		if got := antigravitySummaryWorkspaces(message); len(got) != 0 {
			t.Fatalf("malformed index %x decoded to %#v", message, got)
		}
	}
	if got := antigravitySummaryWorkspaces(valid); got["id"] != filepath.FromSlash("/work") {
		t.Fatalf("valid index = %#v", got)
	}
}

func TestAntigravityIsAHandoffSourceOnly(t *testing.T) {
	home := antigravityFixtureHome(t)
	workspace := t.TempDir()
	const id = "6f1d2c3b-0000-4000-8000-000000000030"
	writeAntigravityTranscript(t, home, "antigravity-cli", id, "transcript_full.jsonl",
		antigravityLine(t, 0, "USER_EXPLICIT", "USER_INPUT", "2026-10-01T09:00:00Z", "<USER_REQUEST>\ncarry this over\n</USER_REQUEST>"),
		antigravityLine(t, 1, "MODEL", "PLANNER_RESPONSE", "2026-10-01T09:00:01Z", "carried"))
	writeFile(t, filepath.Join(home, "antigravity-cli", "conversations", id+".db"), "sqlite")
	writeFile(t, filepath.Join(home, "antigravity-cli", "cache", "last_conversations.json"), `{"`+workspace+`":"`+id+`"}`)
	// No agy on PATH: nothing here may try to run it.
	t.Setenv("PATH", t.TempDir())

	rows := Discover()
	if len(rows) != 1 {
		t.Fatalf("rows = %#v", rows)
	}
	agyRow := rows[0]

	// Out of Antigravity works like any other source.
	t.Setenv("CLAUDE_HOME", filepath.Join(t.TempDir(), ".claude"))
	claude, err := Convert(agyRow, ProviderClaude, HandoffOptions{})
	if err != nil || claude.Provider != ProviderClaude || claude.CWD != workspace {
		t.Fatalf("antigravity -> claude = %#v, %v", claude, err)
	}
	turns, err := Transcript(claude)
	if err != nil || len(turns) != 2 || turns[0].Text != "carry this over" || turns[1].Text != "carried" {
		t.Fatalf("converted transcript = %#v, %v", turns, err)
	}

	// Into Antigravity is refused everywhere, before anything is read or run.
	source := codexSourceRow(t, t.TempDir())
	if AcceptsConversion(ProviderAntigravity) || !AcceptsConversion(ProviderClaude) {
		t.Fatal("conversion targets misreported")
	}
	if _, err := Convert(source, ProviderAntigravity, HandoffOptions{}); !errors.Is(err, errAntigravityTarget) {
		t.Fatalf("convert into antigravity err = %v", err)
	}
	if _, err := PreviewConversion(source, ProviderAntigravity, HandoffOptions{}); !errors.Is(err, errAntigravityTarget) {
		t.Fatalf("preview into antigravity err = %v", err)
	}
	if _, err := Branch(agyRow); !errors.Is(err, errAntigravityTarget) {
		t.Fatalf("branch antigravity err = %v", err)
	}
	if capability := ImportCapabilities(ProviderAntigravity); capability.CanCreate || capability.CanAppendContext || capability.Reason == "" {
		t.Fatalf("antigravity import capability = %#v", capability)
	}
	if err := ValidateCompound(source, ProviderAntigravity); !errors.Is(err, errAntigravityTarget) {
		t.Fatalf("compound into antigravity err = %v", err)
	}
	if got := strings.Join(agyRow.ResumeCommand(ResumeOptions{}), " "); got != "agy --conversation "+id {
		t.Fatalf("resume = %q", got)
	}
	if name := (antigravityProvider{}).CommandName(); name != "agy" {
		t.Fatalf("command = %q", name)
	}
	if _, err := (antigravityProvider{}).WriteConverted(source, turns); !errors.Is(err, errAntigravityTarget) {
		t.Fatalf("direct write err = %v", err)
	}
}

func TestAntigravityDeleteIsLeftToAgy(t *testing.T) {
	err := antigravityProvider{}.Delete(Row{Provider: ProviderAntigravity, ID: "id"})
	if err == nil || !strings.Contains(err.Error(), "/resume") {
		t.Fatalf("delete err = %v", err)
	}
}

func TestFileURIPath(t *testing.T) {
	for input, want := range map[string]string{
		"file:///work/app":          filepath.FromSlash("/work/app"),
		"file://localhost/work/app": filepath.FromSlash("/work/app"),
		"file:///work/a%20b":        filepath.FromSlash("/work/a b"),
		"file://server/share":       "",
		"https://example.com/x":     "",
		"relative/path":             "",
	} {
		if got := fileURIPath(input); got != want {
			t.Fatalf("fileURIPath(%q) = %q, want %q", input, got, want)
		}
	}
}
