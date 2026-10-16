package session

import (
	"encoding/json"
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

// withFakeAgy installs an agy stub that records its argv and stdin, writes a
// transcript for the conversation it reports, and answers with resultJSON.
func withFakeAgy(t *testing.T, home, id, resultJSON string) string {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(bin, "record")
	// Like agy, exit non-zero when the turn failed.
	exitCode := "0"
	if strings.Contains(resultJSON, `"ERROR"`) {
		exitCode = "1"
	}
	transcript := filepath.Join(home, "antigravity-cli", "brain", id, ".system_generated", "logs", "transcript_full.jsonl")
	script := `#!/bin/sh
export PATH=/usr/bin:/bin
pwd > "` + record + `.cwd"
echo "$@" > "` + record + `.args"
cat > "` + record + `.stdin"
mkdir -p "` + filepath.Dir(transcript) + `"
printf '%s\n' '{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-10-01T09:00:00Z","content":"<USER_REQUEST>\nhand-off\n</USER_REQUEST>"}' '{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-01T09:00:01Z","content":"ok"}' > "` + transcript + `"
echo '{"event":"init","conversation_id":"` + id + `"}'
echo '` + resultJSON + `'
exit ` + exitCode + `
`
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return record
}

func TestConvertIntoAntigravityRunsAgy(t *testing.T) {
	home := antigravityFixtureHome(t)
	workspace := t.TempDir()
	const id = "6f1d2c3b-0000-4000-8000-000000000020"
	record := withFakeAgy(t, home, id, `{"event":"result","result":{"conversation_id":"`+id+`","status":"SUCCESS","response":"ok"}}`)

	source := Row{Provider: ProviderCodex, ID: "codex-1", CWD: workspace, LaunchCWD: workspace}
	turns := []Turn{
		{Role: "user", Text: "fix it\n\n```go\n\tx := 1\n```"},
		{Role: "assistant", Text: "Türkçe answer"},
		{Role: "user", Text: "ship it"},
	}
	row, err := writeAntigravityConverted(source, turns)
	if err != nil {
		t.Fatal(err)
	}
	wantFile := filepath.Join(home, "antigravity-cli", "brain", id, ".system_generated", "logs", "transcript_full.jsonl")
	if row.Provider != ProviderAntigravity || row.ID != id || row.CWD != workspace || row.File != wantFile {
		t.Fatalf("row = %#v", row)
	}
	if row.FirstUser != "fix it ```go x := 1 ```" && !strings.HasPrefix(row.FirstUser, "fix it") {
		t.Fatalf("first user = %q", row.FirstUser)
	}
	if row.LastUser != "ship it" {
		t.Fatalf("last user = %q", row.LastUser)
	}

	args, _ := os.ReadFile(record + ".args")
	if got := strings.TrimSpace(string(args)); got != "--input-format stream-json --output-format stream-json --disable-slash-commands --print=" {
		t.Fatalf("agy args = %q", got)
	}
	cwd, _ := os.ReadFile(record + ".cwd")
	if resolved, _ := filepath.EvalSymlinks(workspace); strings.TrimSpace(string(cwd)) != resolved && strings.TrimSpace(string(cwd)) != workspace {
		t.Fatalf("agy ran in %q, want %q", cwd, workspace)
	}
	stdin, _ := os.ReadFile(record + ".stdin")
	lines := strings.Split(strings.TrimRight(string(stdin), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdin must be exactly one NDJSON message, got %q", stdin)
	}
	var message struct {
		Event   string `json:"event"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &message); err != nil || message.Event != "user" {
		t.Fatalf("stdin message = %q, %v", lines[0], err)
	}
	for _, want := range []string{
		"started in Codex",
		"Workspace: " + workspace,
		"do not call any tools",
		"<message role=\"user\">\nfix it\n\n```go\n\tx := 1\n```\n</message>",
		"<message role=\"assistant\">\nTürkçe answer\n</message>",
		"<message role=\"user\">\nship it\n</message>\n</conversation_history>",
	} {
		if !strings.Contains(message.Message.Content, want) {
			t.Fatalf("hand-off prompt missing %q:\n%s", want, message.Message.Content)
		}
	}

	// The new conversation is immediately discoverable and resumable.
	writeFile(t, filepath.Join(home, "antigravity-cli", "conversations", id+".db"), "sqlite")
	for _, discovered := range Discover() {
		if discovered.ID == id && !discovered.HandoffOnly {
			return
		}
	}
	t.Fatal("converted conversation is not discoverable")
}

func TestConvertIntoAntigravityReportsAgyErrors(t *testing.T) {
	home := antigravityFixtureHome(t)
	workspace := t.TempDir()
	const id = "6f1d2c3b-0000-4000-8000-000000000021"
	turns := []Turn{{Role: "user", Text: "q"}, {Role: "assistant", Text: "a"}}

	withFakeAgy(t, home, id, `{"event":"result","result":{"conversation_id":"`+id+`","status":"ERROR","error":"quota exhausted"}}`)
	if _, err := writeAntigravityConverted(Row{CWD: workspace}, turns); err == nil || !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("error result err = %v", err)
	}

	withFakeAgy(t, home, id, `not json`)
	if _, err := writeAntigravityConverted(Row{CWD: workspace}, turns); err == nil || !strings.Contains(err.Error(), "without reporting a conversation") {
		t.Fatalf("missing result err = %v", err)
	}

	for _, bad := range []string{"../../etc", "../" + id, id + "/x"} {
		withFakeAgy(t, home, id, `{"event":"result","result":{"conversation_id":"`+bad+`","status":"SUCCESS"}}`)
		if _, err := writeAntigravityConverted(Row{CWD: workspace}, turns); err == nil || !strings.Contains(err.Error(), "unexpected conversation id") {
			t.Fatalf("bad id %q err = %v", bad, err)
		}
	}

	if _, err := writeAntigravityConverted(Row{CWD: "(unknown cwd)"}, turns); err == nil || !strings.Contains(err.Error(), "known workspace") {
		t.Fatalf("unknown workspace err = %v", err)
	}
	if _, err := writeAntigravityConverted(Row{CWD: filepath.Join(workspace, "gone")}, turns); err == nil || !strings.Contains(err.Error(), "workspace not found") {
		t.Fatalf("missing workspace err = %v", err)
	}
}

func TestAntigravityDeleteIsLeftToAgy(t *testing.T) {
	err := antigravityProvider{}.Delete(Row{Provider: ProviderAntigravity, ID: "id"})
	if err == nil || !strings.Contains(err.Error(), "/resume") {
		t.Fatalf("delete err = %v", err)
	}
	if !WritesThroughCLI(ProviderAntigravity) || WritesThroughCLI(ProviderClaude) {
		t.Fatal("Antigravity conversion must be reported as CLI-backed")
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

func TestConvertRoundTripsThroughAntigravity(t *testing.T) {
	home := antigravityFixtureHome(t)
	workspace := t.TempDir()
	const id = "6f1d2c3b-0000-4000-8000-000000000030"
	withFakeAgy(t, home, id, `{"event":"result","result":{"conversation_id":"`+id+`","status":"SUCCESS"}}`)

	source := codexSourceRow(t, t.TempDir())
	source.CWD = workspace
	converted, err := Convert(source, ProviderAntigravity, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Provider != ProviderAntigravity || converted.ID != id {
		t.Fatalf("converted = %#v", converted)
	}
	// Reading it back goes through the transcript agy wrote.
	turns, err := Transcript(converted)
	if err != nil || len(turns) != 2 || turns[0].Text != "hand-off" || turns[1].Text != "ok" {
		t.Fatalf("transcript = %#v, %v", turns, err)
	}
	// And it can be handed onward, here to Claude.
	t.Setenv("CLAUDE_HOME", filepath.Join(t.TempDir(), ".claude"))
	claude, err := Convert(converted, ProviderClaude, HandoffOptions{})
	if err != nil || claude.Provider != ProviderClaude {
		t.Fatalf("antigravity -> claude = %#v, %v", claude, err)
	}
	if got := strings.Join(converted.ResumeCommand(ResumeOptions{}), " "); got != "agy --conversation "+id {
		t.Fatalf("resume = %q", got)
	}
	if name := (antigravityProvider{}).CommandName(); name != "agy" {
		t.Fatalf("command = %q", name)
	}
}

func TestAntigravityPreviewDisclosesModelTurn(t *testing.T) {
	antigravityFixtureHome(t)
	source := codexSourceRow(t, t.TempDir())
	preview, err := PreviewConversion(source, ProviderAntigravity, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(preview.Warnings, "; "), "model turn") {
		t.Fatalf("preview warnings = %#v, want the agy model-turn disclosure", preview.Warnings)
	}
	local, err := PreviewConversion(source, ProviderClaude, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(local.Warnings, "; "), "model turn") {
		t.Fatalf("local target must not claim a model turn: %#v", local.Warnings)
	}
}
