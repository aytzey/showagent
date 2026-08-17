package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// withFakeOpenCode installs a fake opencode CLI on PATH that serves fixtures
// from fixtureDir and logs every invocation to fixtureDir/calls.log:
//
//	db ...            prints fixtureDir/sessions.json
//	export <id>       prints fixtureDir/export.json
//	session delete    acknowledges the deletion
//	import <file>     copies the file to fixtureDir/imported.json and
//	                  acknowledges the session id it contains
func withFakeOpenCode(t *testing.T, fixtureDir string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
export PATH=/usr/bin:/bin
echo "$@" >> "` + fixtureDir + `/calls.log"
case "$1" in
db)
  cat "` + fixtureDir + `/sessions.json"
  ;;
export)
  echo "Exporting session: $2" >&2
  cat "` + fixtureDir + `/export.json"
  ;;
session)
  echo "Session $3 deleted"
  ;;
import)
  cp "$2" "` + fixtureDir + `/imported.json"
  id=$(sed -n 's/.*"id":"\(ses_[0-9A-Za-z]*\)".*/\1/p' "$2" | head -n 1)
  echo "Imported session: $id"
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

// opencodeFixtureHome creates a data home containing an opencode.db marker so
// the provider engages, and returns it after pointing OPENCODE_DATA_HOME at it.
func opencodeFixtureHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "opencode.db"), "not a real database; discovery goes through the CLI")
	t.Setenv("OPENCODE_DATA_HOME", home)
	return home
}

func setEmptyHomes(t *testing.T, root string) {
	t.Helper()
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "empty-claude"))
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))
	t.Setenv("GEMINI_CLI_HOME", filepath.Join(root, "empty-gemini"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "empty-pi"))
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
}

func TestDiscoverFindsOpenCodeSessions(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	home := opencodeFixtureHome(t)
	withFakeOpenCode(t, home)

	writeFile(t, filepath.Join(home, "sessions.json"), `[
  {
    "id": "ses_aaaaaaaaaaaaAAAAAAAAAAAAAA",
    "directory": "/work/opencode",
    "title": "Fix the retry queue",
    "created": 1751500800000,
    "updated": 1751504400000,
    "first_user": "first opencode message password hunter2",
    "last_user": "last opencode message"
  },
  {
    "id": "ses_bbbbbbbbbbbbBBBBBBBBBBBBBB",
    "directory": "",
    "title": "New session - 2026-07-01T09:00:00.000Z",
    "created": 1751360400000,
    "updated": 1751364000000,
    "first_user": null,
    "last_user": null
  }
]`)

	rows := Discover()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %#v", len(rows), rows)
	}

	first := rows[0]
	if first.Provider != ProviderOpenCode || first.ID != "ses_aaaaaaaaaaaaAAAAAAAAAAAAAA" {
		t.Fatalf("unexpected first row: %#v", first)
	}
	if first.CWD != "/work/opencode" || first.LaunchCWD != "/work/opencode" {
		t.Fatalf("unexpected cwd: %#v", first)
	}
	if first.FirstUser != "first opencode message password [redacted]" {
		t.Fatalf("first user preview not redacted: %q", first.FirstUser)
	}
	if first.LastUser != "last opencode message" {
		t.Fatalf("unexpected last user preview: %q", first.LastUser)
	}
	if !first.LastAt.Equal(time.UnixMilli(1751504400000)) {
		t.Fatalf("unexpected LastAt: %v", first.LastAt)
	}

	second := rows[1]
	if second.ID != "ses_bbbbbbbbbbbbBBBBBBBBBBBBBB" {
		t.Fatalf("unexpected second row: %#v", second)
	}
	if second.CWD != "(unknown cwd)" {
		t.Fatalf("empty directory should fall back to placeholder, got %q", second.CWD)
	}
	// Sessions without plain user text fall back to opencode's own title.
	if second.FirstUser != "New session - 2026-07-01T09:00:00.000Z" {
		t.Fatalf("unexpected title fallback: %q", second.FirstUser)
	}
}

func TestOpenCodeSkippedWithoutDatabase(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	home := t.TempDir()
	t.Setenv("OPENCODE_DATA_HOME", home)
	withFakeOpenCode(t, home)

	if rows := Discover(); len(rows) != 0 {
		t.Fatalf("expected no rows without an opencode database, got %d", len(rows))
	}
	// Without a database the CLI must never run, so showagent cannot make
	// opencode create one as a side effect.
	if _, err := os.Stat(filepath.Join(home, "calls.log")); !os.IsNotExist(err) {
		t.Fatalf("opencode CLI was invoked although no database exists: %v", err)
	}
}

func TestOpenCodeSkippedWithoutCLI(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	opencodeFixtureHome(t)
	t.Setenv("PATH", filepath.Join(root, "empty-bin"))

	if rows := Discover(); len(rows) != 0 {
		t.Fatalf("expected no rows without the opencode CLI, got %d", len(rows))
	}
}

func TestOpenCodeResumeAndCompoundCommands(t *testing.T) {
	row := Row{Provider: ProviderOpenCode, ID: "ses_x"}

	if got := strings.Join(row.ResumeCommand(ResumeOptions{}), " "); got != "opencode --session ses_x" {
		t.Fatalf("normal resume = %q", got)
	}
	if got := strings.Join(row.ResumeCommand(ResumeOptions{Dangerous: true}), " "); got != "opencode --auto --session ses_x" {
		t.Fatalf("dangerous resume = %q", got)
	}
	got := row.CompoundCommand(ResumeOptions{}, "do the pass")
	want := []string{"opencode", "--session", "ses_x", "--prompt", "do the pass"}
	if strings.Join(got, "\x1f") != strings.Join(want, "\x1f") {
		t.Fatalf("compound = %v, want %v", got, want)
	}
}

func TestCompoundAgentsFollowRegistryOrder(t *testing.T) {
	agents := CompoundAgents()
	want := []Provider{ProviderCodex, ProviderClaude, ProviderJCode, ProviderOpenCode, ProviderGemini, ProviderPi}
	if len(agents) != len(want) {
		t.Fatalf("agents = %v, want %v", agents, want)
	}
	for index, provider := range want {
		if agents[index] != provider {
			t.Fatalf("agents = %v, want %v", agents, want)
		}
	}
}

func TestOpenCodeDeleteInvokesCLI(t *testing.T) {
	home := opencodeFixtureHome(t)
	withFakeOpenCode(t, home)

	if err := Delete(Row{Provider: ProviderOpenCode, ID: "ses_x", CWD: "(unknown cwd)"}); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(filepath.Join(home, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "session delete ses_x") {
		t.Fatalf("delete did not call the opencode CLI: %q", log)
	}
}

func TestConvertOpenCodeToClaude(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	t.Setenv("CODEX_HOME", filepath.Join(root, "empty-codex"))
	t.Setenv("CLAUDE_HOME", claudeHome)
	t.Setenv("JCODE_HOME", filepath.Join(root, "empty-jcode"))
	home := opencodeFixtureHome(t)
	withFakeOpenCode(t, home)

	writeFile(t, filepath.Join(home, "export.json"), `{
  "info": {"id": "ses_source", "directory": "/work/opencode", "title": "Fix the retry queue"},
  "messages": [
    {"info": {"id": "msg_1", "role": "user"}, "parts": [
      {"id": "prt_0", "type": "text", "text": "<system-reminder>injected context</system-reminder>", "synthetic": true},
      {"id": "prt_1", "type": "text", "text": "first opencode message"}
    ]},
    {"info": {"id": "msg_2", "role": "assistant"}, "parts": [
      {"id": "prt_2", "type": "step-start"},
      {"id": "prt_3", "type": "text", "text": "assistant reply"}
    ]},
    {"info": {"id": "msg_3", "role": "user"}, "parts": [
      {"id": "prt_4", "type": "tool"}
    ]},
    {"info": {"id": "msg_4", "role": "user"}, "parts": [
      {"id": "prt_5", "type": "text", "text": "last opencode message"}
    ]}
  ]
}`)

	source := Row{Provider: ProviderOpenCode, ID: "ses_source", CWD: "/work/opencode"}
	converted, err := Convert(source, ProviderClaude, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Provider != ProviderClaude {
		t.Fatalf("converted provider = %s, want claude", converted.Provider)
	}
	if _, err := os.Stat(converted.File); err != nil {
		t.Fatalf("converted file missing: %v", err)
	}

	turns, err := Transcript(converted)
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{
		{Role: "user", Text: "first opencode message"},
		{Role: "assistant", Text: "assistant reply"},
		{Role: "user", Text: "last opencode message"},
	}
	if len(turns) != len(want) {
		t.Fatalf("converted turns = %#v, want %#v", turns, want)
	}
	for index, turn := range want {
		if turns[index] != turn {
			t.Fatalf("turn %d = %#v, want %#v", index, turns[index], turn)
		}
	}
}

func TestConvertCodexToOpenCode(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	home := opencodeFixtureHome(t)
	withFakeOpenCode(t, home)

	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(root, "source-codex.jsonl")
	writeFile(t, sourcePath, `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","cwd":"`+workspace+`"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build the opencode handoff"}]}}
{"timestamp":"2026-06-01T09:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"handoff ready"}]}}
`)

	converted, err := Convert(Row{
		Provider: ProviderCodex,
		ID:       "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb",
		CWD:      workspace,
		File:     sourcePath,
	}, ProviderOpenCode, HandoffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Provider != ProviderOpenCode {
		t.Fatalf("converted provider = %s, want opencode", converted.Provider)
	}
	if !regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`).MatchString(converted.ID) {
		t.Fatalf("converted id = %q, want opencode-shaped session id", converted.ID)
	}
	if converted.CWD != workspace || converted.LaunchCWD != workspace {
		t.Fatalf("unexpected converted cwd: %#v", converted)
	}
	if converted.FirstUser != "build the opencode handoff" {
		t.Fatalf("unexpected first user preview: %q", converted.FirstUser)
	}

	// The payload handed to `opencode import` must be a valid export file:
	// session info plus messages with the schema-required fields.
	content, err := os.ReadFile(filepath.Join(home, "imported.json"))
	if err != nil {
		t.Fatalf("import was not invoked with a payload file: %v", err)
	}
	var payload struct {
		Info struct {
			ID        string `json:"id"`
			Slug      string `json:"slug"`
			Directory string `json:"directory"`
			Title     string `json:"title"`
			Version   string `json:"version"`
			Time      struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			} `json:"time"`
		} `json:"info"`
		Messages []struct {
			Info struct {
				ID        string `json:"id"`
				SessionID string `json:"sessionID"`
				Role      string `json:"role"`
				ParentID  string `json:"parentID"`
				Agent     string `json:"agent"`
			} `json:"info"`
			Parts []struct {
				ID        string `json:"id"`
				SessionID string `json:"sessionID"`
				MessageID string `json:"messageID"`
				Type      string `json:"type"`
				Text      string `json:"text"`
			} `json:"parts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("imported payload is not valid JSON: %v", err)
	}
	if payload.Info.ID != converted.ID {
		t.Fatalf("payload session id = %q, want %q", payload.Info.ID, converted.ID)
	}
	if payload.Info.Directory != workspace || payload.Info.Version == "" || payload.Info.Slug == "" {
		t.Fatalf("payload info incomplete: %+v", payload.Info)
	}
	if payload.Info.Time.Created == 0 || payload.Info.Time.Updated < payload.Info.Time.Created {
		t.Fatalf("payload timestamps invalid: %+v", payload.Info.Time)
	}
	if len(payload.Messages) != 2 {
		t.Fatalf("payload messages = %d, want 2", len(payload.Messages))
	}
	user, assistant := payload.Messages[0], payload.Messages[1]
	if user.Info.Role != "user" || !strings.HasPrefix(user.Info.ID, "msg_") || user.Info.SessionID != converted.ID {
		t.Fatalf("unexpected user message info: %+v", user.Info)
	}
	if assistant.Info.Role != "assistant" || assistant.Info.ParentID != user.Info.ID || assistant.Info.Agent == "" {
		t.Fatalf("unexpected assistant message info: %+v", assistant.Info)
	}
	if len(user.Parts) != 1 || user.Parts[0].Type != "text" || user.Parts[0].Text != "build the opencode handoff" {
		t.Fatalf("unexpected user parts: %+v", user.Parts)
	}
	if user.Parts[0].MessageID != user.Info.ID || user.Parts[0].SessionID != converted.ID || !strings.HasPrefix(user.Parts[0].ID, "prt_") {
		t.Fatalf("user part ids inconsistent: %+v", user.Parts[0])
	}
	if len(assistant.Parts) != 1 || assistant.Parts[0].Text != "handoff ready" {
		t.Fatalf("unexpected assistant parts: %+v", assistant.Parts)
	}
}

func TestConvertToOpenCodeNeedsWorkspace(t *testing.T) {
	root := t.TempDir()
	setEmptyHomes(t, root)
	home := opencodeFixtureHome(t)
	withFakeOpenCode(t, home)

	sourcePath := filepath.Join(root, "source-codex.jsonl")
	writeFile(t, sourcePath, `
{"timestamp":"2026-06-01T09:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb","cwd":"/gone"}}
{"timestamp":"2026-06-01T09:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
`)

	_, err := Convert(Row{
		Provider: ProviderCodex,
		ID:       "aaaaaaaa-1111-2222-3333-bbbbbbbbbbbb",
		CWD:      filepath.Join(root, "missing-workspace"),
		File:     sourcePath,
	}, ProviderOpenCode, HandoffOptions{})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("conversion into opencode without a workspace = %v, want workspace error", err)
	}
}

func TestDecodeOpenCodeJSONIgnoresBracketedBannerText(t *testing.T) {
	var entries []opencodeSessionEntry
	output := []byte("notice: [beta] opencode db output follows\n[{\"id\":\"ses_1\",\"created\":1,\"updated\":2}]\nupgrade notice after json\n")
	if err := decodeOpenCodeJSON(output, &entries); err != nil {
		t.Fatalf("decodeOpenCodeJSON failed: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "ses_1" {
		t.Fatalf("entries = %#v, want ses_1", entries)
	}
}

func TestCappedBufferRejectsOversizedOutput(t *testing.T) {
	buffer := cappedBuffer{max: 5}
	if n, err := buffer.Write([]byte("abc")); err != nil || n != 3 {
		t.Fatalf("first write = %d, %v", n, err)
	}
	if n, err := buffer.Write([]byte("def")); err == nil || n != 2 {
		t.Fatalf("overflow write = %d, %v; want 2 and error", n, err)
	}
	if got := buffer.String(); got != "abcde" {
		t.Fatalf("buffer = %q, want abcde", got)
	}
}

func TestOpenCodeIDShape(t *testing.T) {
	at := time.UnixMilli(1751504400000)
	ascending, err := opencodeID("msg", false, at)
	if err != nil {
		t.Fatal(err)
	}
	descending, err := opencodeID("ses", true, at)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$`).MatchString(ascending) {
		t.Fatalf("ascending id = %q", ascending)
	}
	if !regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`).MatchString(descending) {
		t.Fatalf("descending id = %q", descending)
	}

	// Ascending ids must sort chronologically, descending ids the other way,
	// matching opencode's identifier scheme.
	later := at.Add(time.Second)
	ascendingLater, err := opencodeID("msg", false, later)
	if err != nil {
		t.Fatal(err)
	}
	descendingLater, err := opencodeID("ses", true, later)
	if err != nil {
		t.Fatal(err)
	}
	if ascending[:16] >= ascendingLater[:16] {
		t.Fatalf("ascending ids out of order: %q then %q", ascending, ascendingLater)
	}
	if descending[:16] <= descendingLater[:16] {
		t.Fatalf("descending ids out of order: %q then %q", descending, descendingLater)
	}
}

// TestRunOpenCodeDrainsLargeOutput is the regression test for issue #10:
// the previous runOpenCode used command.Stdout = &buf; command.Run(), which
// silently truncated outputs that crossed the Linux pipe-buffer threshold
// (~64 KiB) because os/exec's internal copy goroutine could lose the tail
// of the pipe after the child exited. The fix drains stdout via StdoutPipe
// + io.Copy and waits on both pipes before returning. The fake `opencode`
// writes 96 KiB ending in a marker, comfortably above the pipe threshold;
// a partial drain drops the marker.
func TestRunOpenCodeDrainsLargeOutput(t *testing.T) {
	const tail = "<<<SHOWAGENT_TAIL>>>"
	pad := strings.Repeat("a", 96<<10)

	bin := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
export PATH=/usr/bin:/bin
if [ "$1" = "db" ]; then
  head -c %d /dev/zero | tr '\0' 'a'
  printf '%%s' %q
fi
`, len(pad), tail)
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	out, err := runOpenCode("", "db", "--format", "json", "select 1")
	if err != nil {
		t.Fatalf("runOpenCode failed: %v", err)
	}
	want := len(pad) + len(tail)
	if len(out) != want {
		t.Fatalf("captured %d bytes, want %d", len(out), want)
	}
	if !strings.HasSuffix(string(out), tail) {
		t.Fatalf("tail marker missing; last 32 bytes: %q", string(out[len(out)-32:]))
	}
}
