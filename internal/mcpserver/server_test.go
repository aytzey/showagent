package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aytzey/showagent/internal/session"
)

const (
	codexRateLimitID = "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb"
	codexDarkModeID  = "bbbbbbbb-2222-3333-4444-cccccccccccc"
	claudeWebhookID  = "cccccccc-1111-2222-3333-dddddddddddd"
)

// setFixtureHomes points every provider home at hermetic directories holding
// two codex sessions and one claude session with distinct workspaces and
// message texts, so filter tests can tell them apart.
func setFixtureHomes(t *testing.T) (codexHome, claudeHome string) {
	t.Helper()
	root := t.TempDir()
	codexHome = filepath.Join(root, "codex")
	claudeHome = filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))
	t.Setenv("OPENCODE_DATA_HOME", filepath.Join(root, "empty-opencode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "empty-pi"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")

	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "01", "rollout-2026-06-01T12-00-00-"+codexRateLimitID+".jsonl"), `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"`+codexRateLimitID+`","cwd":"/work/api-server"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"add rate limiting to the charges endpoint"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"added a token bucket"}]}}
{"timestamp":"2026-06-01T09:03:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"return Retry-After on 429"}]}}
{"timestamp":"2026-06-01T09:04:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done, header added"}]}}
{"timestamp":"2026-06-01T09:05:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"now mock the clock in the TTL test"}]}}
`)

	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "02", "rollout-2026-06-02T12-00-00-"+codexDarkModeID+".jsonl"), `
{"timestamp":"2026-06-02T09:00:00Z","type":"session_meta","payload":{"id":"`+codexDarkModeID+`","cwd":"/work/webapp"}}
{"timestamp":"2026-06-02T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"add dark mode toggle"}]}}
{"timestamp":"2026-06-02T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"toggle shipped"}]}}
`)

	writeFile(t, filepath.Join(claudeHome, "projects", "-work-api-server", claudeWebhookID+".jsonl"), `
{"type":"user","message":{"role":"user","content":"fix the webhook idempotency bug"},"uuid":"1","timestamp":"2026-06-03T10:00:00Z","cwd":"/work/api-server","sessionId":"`+claudeWebhookID+`"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"keyed on the provider event id"}]},"uuid":"2","timestamp":"2026-06-03T10:01:00Z","cwd":"/work/api-server","sessionId":"`+claudeWebhookID+`"}
{"type":"user","message":{"role":"user","content":"log dropped duplicates"},"uuid":"3","timestamp":"2026-06-03T10:02:00Z","cwd":"/work/api-server","sessionId":"`+claudeWebhookID+`"}
`)
	return codexHome, claudeHome
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTestSession connects an in-memory MCP client to the showagent server and
// returns the client session, so tests exercise the full tool dispatch path
// (schema validation included) rather than the handlers in isolation.
func newTestSession(t *testing.T, options ...Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := New("test", options...).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Wait() })

	client := mcp.NewClient(&mcp.Implementation{Name: "showagent-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

// callTool invokes name with args and decodes the structured output into T.
func callTool[T any](t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) T {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("call %s returned tool error: %s", name, resultText(result))
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal %s output: %v", name, err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s output %s: %v", name, raw, err)
	}
	return out
}

// callToolExpectError invokes name with args and returns the tool error text.
func callToolExpectError(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if !result.IsError {
		t.Fatalf("call %s succeeded, want tool error", name)
	}
	return resultText(result)
}

func resultText(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func sessionIDs(result listSessionsResult) []string {
	ids := make([]string, 0, len(result.Sessions))
	for _, s := range result.Sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestListSessionsReturnsAllNewestFirst(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	result := callTool[listSessionsResult](t, cs, "list_sessions", nil)
	if result.Total != 3 || len(result.Sessions) != 3 {
		t.Fatalf("total = %d, sessions = %d, want 3/3: %v", result.Total, len(result.Sessions), sessionIDs(result))
	}
	want := []string{claudeWebhookID, codexDarkModeID, codexRateLimitID}
	if got := sessionIDs(result); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want newest first %v", got, want)
	}
	first := result.Sessions[0]
	if first.Provider != "claude" || first.Workspace != "/work/api-server" {
		t.Fatalf("unexpected first session: %#v", first)
	}
	if first.Updated != "2026-06-03T10:02:00Z" {
		t.Fatalf("updated = %q, want RFC3339 last-activity time", first.Updated)
	}
	if first.FirstMessage != "fix the webhook idempotency bug" || first.LastMessage != "log dropped duplicates" {
		t.Fatalf("unexpected previews: %#v", first)
	}
}

func TestListSessionsFilters(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	byProvider := callTool[listSessionsResult](t, cs, "list_sessions", map[string]any{"provider": "claude"})
	if len(byProvider.Sessions) != 1 || byProvider.Sessions[0].ID != claudeWebhookID {
		t.Fatalf("provider filter = %v, want only claude session", sessionIDs(byProvider))
	}

	byWorkspace := callTool[listSessionsResult](t, cs, "list_sessions", map[string]any{"workspace": "API-SERVER"})
	if len(byWorkspace.Sessions) != 2 {
		t.Fatalf("workspace filter = %v, want both api-server sessions", sessionIDs(byWorkspace))
	}

	// The free-text query matches first/last user messages, not just paths.
	byQuery := callTool[listSessionsResult](t, cs, "list_sessions", map[string]any{"query": "Rate Limiting"})
	if len(byQuery.Sessions) != 1 || byQuery.Sessions[0].ID != codexRateLimitID {
		t.Fatalf("query filter = %v, want only the rate-limit session", sessionIDs(byQuery))
	}
	byLastMessage := callTool[listSessionsResult](t, cs, "list_sessions", map[string]any{"query": "mock the clock"})
	if len(byLastMessage.Sessions) != 1 || byLastMessage.Sessions[0].ID != codexRateLimitID {
		t.Fatalf("last-message query = %v, want only the rate-limit session", sessionIDs(byLastMessage))
	}

	combined := callTool[listSessionsResult](t, cs, "list_sessions", map[string]any{"provider": "codex", "workspace": "api-server"})
	if len(combined.Sessions) != 1 || combined.Sessions[0].ID != codexRateLimitID {
		t.Fatalf("combined filter = %v, want only the codex api-server session", sessionIDs(combined))
	}

	none := callTool[listSessionsResult](t, cs, "list_sessions", map[string]any{"query": "no such topic anywhere"})
	if none.Total != 0 || len(none.Sessions) != 0 {
		t.Fatalf("no-match query = %v (total %d), want empty", sessionIDs(none), none.Total)
	}
}

func TestListSessionsLimitKeepsTotal(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	result := callTool[listSessionsResult](t, cs, "list_sessions", map[string]any{"limit": 1})
	if len(result.Sessions) != 1 || result.Sessions[0].ID != claudeWebhookID {
		t.Fatalf("limited sessions = %v, want just the newest", sessionIDs(result))
	}
	if result.Total != 3 {
		t.Fatalf("total = %d, want 3 so callers know more matched", result.Total)
	}
}

func TestListSessionsRejectsUnknownProvider(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	text := callToolExpectError(t, cs, "list_sessions", map[string]any{"provider": "cursor"})
	if !strings.Contains(text, "unsupported provider") || !strings.Contains(text, "codex") {
		t.Fatalf("error %q should name the supported providers", text)
	}
}

func TestGetTranscriptTruncatesFromTheEnd(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	full := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": codexRateLimitID})
	if full.TotalTurns != 5 || len(full.Turns) != 5 || full.Truncated {
		t.Fatalf("full transcript = %d/%d truncated=%v, want 5/5 untruncated", len(full.Turns), full.TotalTurns, full.Truncated)
	}
	if full.Provider != "codex" || full.Workspace != "/work/api-server" {
		t.Fatalf("unexpected transcript metadata: %#v", full)
	}
	if !full.SecretsRedacted || full.Warning == "" {
		t.Fatalf("transcript must declare redaction and trust boundary: %#v", full)
	}
	if full.Turns[0].Role != "user" || full.Turns[0].Text != "add rate limiting to the charges endpoint" {
		t.Fatalf("unexpected first turn: %#v", full.Turns[0])
	}

	tail := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": codexRateLimitID, "max_turns": 2})
	if tail.TotalTurns != 5 || !tail.Truncated || len(tail.Turns) != 2 {
		t.Fatalf("tail transcript = %d/%d truncated=%v, want the last 2 of 5", len(tail.Turns), tail.TotalTurns, tail.Truncated)
	}
	if tail.Turns[0] != (transcriptTurn{Role: "assistant", Text: "done, header added"}) ||
		tail.Turns[1] != (transcriptTurn{Role: "user", Text: "now mock the clock in the TTL test"}) {
		t.Fatalf("tail must keep the most recent turns in order: %#v", tail.Turns)
	}

	latest := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": "latest"})
	if latest.ID != claudeWebhookID {
		t.Fatalf("latest transcript id = %q, want newest session %q", latest.ID, claudeWebhookID)
	}
}

func TestGetTranscriptRedactsSecretsUnlessExplicitlyIncluded(t *testing.T) {
	codexHome, _ := setFixtureHomes(t)
	const id = "dddddddd-1111-2222-3333-eeeeeeeeeeee"
	googleLikeKey := "AI" + "za1234567890abcdefghijklmnop"
	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "04", "rollout-"+id+".jsonl"), `
{"timestamp":"2026-06-04T09:00:00Z","type":"session_meta","payload":{"id":"`+id+`","cwd":"/work/secrets"}}
{"timestamp":"2026-06-04T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"password hunter2\napi_key=`+googleLikeKey+`"}]}}
`)
	cs := newTestSession(t)

	redacted := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": id})
	if !redacted.SecretsRedacted || strings.Contains(redacted.Turns[0].Text, "hunter2") || strings.Contains(redacted.Turns[0].Text, "AIza") {
		t.Fatalf("default transcript leaked secrets: %#v", redacted)
	}

	verbatimCS := newTestSession(t, Options{AllowSecrets: true})
	verbatim := callTool[transcriptResult](t, verbatimCS, "get_transcript", map[string]any{"id": id})
	if verbatim.SecretsRedacted || !strings.Contains(verbatim.Turns[0].Text, "hunter2") || !strings.Contains(verbatim.Turns[0].Text, "AIza") {
		t.Fatalf("explicit verbatim transcript did not preserve values: %#v", verbatim)
	}
	if !strings.Contains(verbatim.Turns[0].Text, "\n") {
		t.Fatalf("transcript lost formatting: %q", verbatim.Turns[0].Text)
	}
}

func TestGetTranscriptEnforcesHardTurnLimit(t *testing.T) {
	codexHome, _ := setFixtureHomes(t)
	const id = "eeeeeeee-1111-2222-3333-ffffffffffff"
	var lines strings.Builder
	lines.WriteString(`{"timestamp":"2026-06-05T09:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"/work/large"}}` + "\n")
	for i := 0; i < maxTranscriptTurns+25; i++ {
		_, _ = fmt.Fprintf(&lines, `{"timestamp":"2026-06-05T09:%02d:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"turn %d"}]}}`+"\n", i%60, i)
	}
	writeFile(t, filepath.Join(codexHome, "sessions", "2026", "06", "05", "rollout-"+id+".jsonl"), lines.String())
	cs := newTestSession(t)

	result := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": id, "max_turns": 1_000_000})
	if result.TotalTurns != maxTranscriptTurns+25 || len(result.Turns) != maxTranscriptTurns || !result.Truncated {
		t.Fatalf("hard limit result = %d/%d truncated=%v", len(result.Turns), result.TotalTurns, result.Truncated)
	}
}

func TestGetTranscriptUnknownID(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	text := callToolExpectError(t, cs, "get_transcript", map[string]any{"id": "nope"})
	if !strings.Contains(text, "not found") || !strings.Contains(text, "list_sessions") {
		t.Fatalf("error %q should point the caller at list_sessions", text)
	}
}

func TestBranchSessionWritesCopy(t *testing.T) {
	codexHome, _ := setFixtureHomes(t)
	cs := newTestSession(t)

	branched := callTool[newSessionResult](t, cs, "branch_session", map[string]any{"id": codexRateLimitID})
	if branched.Provider != "codex" {
		t.Fatalf("branched provider = %q, want codex", branched.Provider)
	}
	if branched.ID == codexRateLimitID || branched.ID == "" {
		t.Fatalf("branched id = %q, want a fresh id", branched.ID)
	}
	if !strings.HasPrefix(branched.File, codexHome) {
		t.Fatalf("branched file = %q, want under %q", branched.File, codexHome)
	}
	if _, err := os.Stat(branched.File); err != nil {
		t.Fatalf("branched file missing: %v", err)
	}
	if branched.ResumeCommand != "codex resume "+branched.ID {
		t.Fatalf("resume command = %q", branched.ResumeCommand)
	}
	if branched.CWD != "/work/api-server" {
		t.Fatalf("branched cwd = %q, want the source workspace", branched.CWD)
	}

	// The copy carries the full transcript and the original is untouched.
	copyTranscript := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": branched.ID})
	if copyTranscript.TotalTurns != 5 {
		t.Fatalf("branched transcript turns = %d, want 5", copyTranscript.TotalTurns)
	}
	original := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": codexRateLimitID})
	if original.TotalTurns != 5 {
		t.Fatalf("original transcript turns = %d, want 5", original.TotalTurns)
	}
}

func TestConvertSessionWritesTargetFormat(t *testing.T) {
	_, claudeHome := setFixtureHomes(t)
	cs := newTestSession(t)

	converted := callTool[newSessionResult](t, cs, "convert_session", map[string]any{"id": codexRateLimitID, "target": "claude"})
	if converted.Provider != "claude" {
		t.Fatalf("converted provider = %q, want claude", converted.Provider)
	}
	if converted.ID == codexRateLimitID || converted.ID == "" {
		t.Fatalf("converted id = %q, want a fresh id", converted.ID)
	}
	if !strings.HasPrefix(converted.File, filepath.Join(claudeHome, "projects")) {
		t.Fatalf("converted file = %q, want under the claude projects dir", converted.File)
	}
	if _, err := os.Stat(converted.File); err != nil {
		t.Fatalf("converted file missing: %v", err)
	}
	if converted.ResumeCommand != "claude --resume "+converted.ID {
		t.Fatalf("resume command = %q", converted.ResumeCommand)
	}

	// The converted session must be readable back through its new provider.
	transcript := callTool[transcriptResult](t, cs, "get_transcript", map[string]any{"id": converted.ID})
	if transcript.Provider != "claude" || transcript.TotalTurns != 5 {
		t.Fatalf("converted transcript = %s/%d turns, want claude/5", transcript.Provider, transcript.TotalTurns)
	}
	if transcript.Turns[len(transcript.Turns)-1].Text != "now mock the clock in the TTL test" {
		t.Fatalf("converted transcript lost the last turn: %#v", transcript.Turns)
	}
}

func TestConvertSessionRejectsUnknownTarget(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	text := callToolExpectError(t, cs, "convert_session", map[string]any{"id": codexRateLimitID, "target": "cursor"})
	if !strings.Contains(text, "unsupported provider") {
		t.Fatalf("error = %q, want unsupported provider", text)
	}
}

func TestResumeCommandReturnsCommandWithoutExecuting(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	result := callTool[resumeCommandResult](t, cs, "resume_command", map[string]any{"id": claudeWebhookID})
	if result.Command != "claude --resume "+claudeWebhookID {
		t.Fatalf("command = %q", result.Command)
	}
	if result.Provider != "claude" || result.CWD != "/work/api-server" {
		t.Fatalf("unexpected recipe: %#v", result)
	}
	if result.File == "" {
		t.Fatalf("resume_command should report where the session is stored")
	}
}

// TestNoDeleteToolExposed pins the deliberate decision that the MCP surface
// cannot destroy sessions: deletion stays behind the TUI's two-press confirm.
func TestNoDeleteToolExposed(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"list_sessions":   true,
		"get_transcript":  true,
		"branch_session":  true,
		"convert_session": true,
		"resume_command":  true,
	}
	if len(tools.Tools) != len(want) {
		t.Fatalf("tool count = %d, want %d", len(tools.Tools), len(want))
	}
	for _, tool := range tools.Tools {
		if !want[tool.Name] {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		if strings.Contains(tool.Name, "delete") || strings.Contains(tool.Name, "remove") {
			t.Fatalf("destructive tool %q must not be exposed over MCP", tool.Name)
		}
	}
}

func TestProviderToolSchemasAdvertiseRegistry(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "list_sessions" && tool.Name != "convert_session" {
			continue
		}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		for _, provider := range session.ProviderNames() {
			if !strings.Contains(string(data), provider) {
				t.Fatalf("%s schema does not advertise provider %q: %s", tool.Name, provider, data)
			}
		}
	}
}

func TestReadOnlyServerOmitsWriteTools(t *testing.T) {
	setFixtureHomes(t)
	cs := newTestSession(t, Options{ReadOnly: true})

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"list_sessions": true, "get_transcript": true, "resume_command": true}
	if len(tools.Tools) != len(want) {
		t.Fatalf("read-only tool count = %d, want %d", len(tools.Tools), len(want))
	}
	for _, tool := range tools.Tools {
		if !want[tool.Name] {
			t.Fatalf("read-only server exposed %q", tool.Name)
		}
	}
}

func TestResolveRowLatestAndMissing(t *testing.T) {
	setFixtureHomes(t)

	latest, err := resolveRow("latest")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != claudeWebhookID {
		t.Fatalf("latest = %q, want %q", latest.ID, claudeWebhookID)
	}
	if _, err := resolveRow("missing-id"); err == nil {
		t.Fatal("missing id must error")
	}

	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_HOME", t.TempDir())
	if _, err := resolveRow("latest"); err == nil || !strings.Contains(err.Error(), "no local agent sessions") {
		t.Fatalf("empty store err = %v, want 'no local agent sessions'", err)
	}
}

func TestMatchesFiltersWorkspaceAndQuery(t *testing.T) {
	row := session.Row{
		Provider:  session.ProviderCodex,
		CWD:       "/home/dev/Api-Server",
		FirstUser: "add rate limiting",
		LastUser:  "mock the clock",
	}
	if !matchesFilters(row, "", "api-server", "") {
		t.Fatal("workspace match must be case-insensitive")
	}
	if !matchesFilters(row, "", "", "API-SERVER") {
		t.Fatal("query must match the workspace path")
	}
	if !matchesFilters(row, session.ProviderCodex, "api", "clock") {
		t.Fatal("all filters together must match")
	}
	if matchesFilters(row, session.ProviderClaude, "", "") {
		t.Fatal("provider mismatch must exclude the row")
	}
	if matchesFilters(row, "", "webapp", "") {
		t.Fatal("workspace mismatch must exclude the row")
	}
	if matchesFilters(row, "", "", "dark mode") {
		t.Fatal("query mismatch must exclude the row")
	}
}
