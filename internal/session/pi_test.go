package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const piSessionID = "11111111-2222-4333-8444-555555555555"

func TestDiscoverPiUsesOnlyTheActiveBranch(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	path := filepath.Join(agentDir, "sessions", "--work-pi--", "2026-07-10T10-00-00-000Z_"+piSessionID+".jsonl")
	writeFile(t, path, `
{"type":"session","version":3,"id":"`+piSessionID+`","timestamp":"2026-07-10T10:00:00.000Z","cwd":"/work/pi"}
{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-07-10T10:01:00.000Z","message":{"role":"user","content":"first pi question","timestamp":1783677660000}}
{"type":"message","id":"00000002","parentId":"00000001","timestamp":"2026-07-10T10:02:00.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private reasoning"},{"type":"text","text":"pi answer"}],"api":"openai-completions","provider":"opencode-go","model":"glm-5.2","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1783677720000}}
{"type":"message","id":"00000003","parentId":"00000002","timestamp":"2026-07-10T10:03:00.000Z","message":{"role":"user","content":"abandoned branch","timestamp":1783677780000}}
{"type":"message","id":"00000004","parentId":"00000002","timestamp":"2026-07-10T10:04:00.000Z","message":{"role":"user","content":[{"type":"text","text":"active\nbranch sk-test-12345678901234567890"}],"timestamp":1783677840000}}
`)

	rows := discoverPi(defaultPiSessionRoot())
	if len(rows) != 1 {
		t.Fatalf("rows = %#v, want one Pi session", rows)
	}
	row := rows[0]
	if row.Provider != ProviderPi || row.ID != piSessionID || row.CWD != "/work/pi" || row.File != path {
		t.Fatalf("row = %#v", row)
	}
	if row.FirstUser != "first pi question" || row.LastUser != "active branch [redacted-secret]" {
		t.Fatalf("previews = %q / %q", row.FirstUser, row.LastUser)
	}
	wantTime := time.Date(2026, 7, 10, 10, 4, 0, 0, time.UTC)
	if !row.LastAt.Equal(wantTime) {
		t.Fatalf("LastAt = %s, want %s", row.LastAt, wantTime)
	}

	turns, err := piTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	wantTurns := []Turn{
		{Role: "user", Text: "first pi question"},
		{Role: "assistant", Text: "pi answer"},
		{Role: "user", Text: "active\nbranch sk-test-12345678901234567890"},
	}
	if !reflect.DeepEqual(turns, wantTurns) {
		t.Fatalf("turns = %#v, want %#v", turns, wantTurns)
	}
}

func TestDiscoverPiReadsLegacyLinearSession(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	path := filepath.Join(agentDir, "sessions", "--work-legacy--", "legacy.jsonl")
	writeFile(t, path, `
{"type":"session","version":1,"id":"`+piSessionID+`","timestamp":"2026-07-10T09:00:00Z","cwd":"/work/legacy"}
{"type":"message","timestamp":"2026-07-10T09:01:00Z","message":{"role":"user","content":"legacy first"}}
{"type":"message","timestamp":"2026-07-10T09:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"legacy answer"}]}}
{"type":"message","timestamp":"2026-07-10T09:03:00Z","message":{"role":"user","content":"legacy last"}}
`)

	rows := discoverPi(defaultPiSessionRoot())
	if len(rows) != 1 || rows[0].FirstUser != "legacy first" || rows[0].LastUser != "legacy last" {
		t.Fatalf("legacy rows = %#v", rows)
	}
	turns, err := piTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{{Role: "user", Text: "legacy first"}, {Role: "assistant", Text: "legacy answer"}, {Role: "user", Text: "legacy last"}}
	if !reflect.DeepEqual(turns, want) {
		t.Fatalf("legacy turns = %#v, want %#v", turns, want)
	}
}

func TestDiscoverPiSkipsMalformedFilesAndLines(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	dir := filepath.Join(agentDir, "sessions", "--work-corrupt--")
	writeFile(t, filepath.Join(dir, "invalid-header.jsonl"), "not json\n"+`{"type":"session","version":3,"id":"bad","cwd":"/work/corrupt"}`)
	validPath := filepath.Join(dir, "valid-with-corrupt-line.jsonl")
	writeFile(t, validPath, `
{"type":"session","version":3,"id":"`+piSessionID+`","timestamp":"2026-07-10T09:00:00Z","cwd":"/work/corrupt"}
not json
{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-07-10T09:01:00Z","message":{"role":"user","content":"survives corruption"}}
`)

	rows := discoverPi(defaultPiSessionRoot())
	if len(rows) != 1 || rows[0].File != validPath || rows[0].FirstUser != "survives corruption" {
		t.Fatalf("malformed-file discovery rows = %#v", rows)
	}
}

func TestPiProviderResumeCompoundAndDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pi session.jsonl")
	writeFile(t, path, "session")
	row := Row{Provider: ProviderPi, ID: piSessionID, CWD: "/work/pi", File: path}
	provider := piProvider{}

	wantResume := []string{"pi", "--session", path}
	for _, options := range []ResumeOptions{{}, {Dangerous: true}} {
		if got := provider.ResumeArgs(row, options); !reflect.DeepEqual(got, wantResume) {
			t.Fatalf("resume(%#v) = %v, want %v", options, got, wantResume)
		}
	}
	wantCompound := append(append([]string{}, wantResume...), "capture durable learnings")
	if got := provider.CompoundArgs(row, ResumeOptions{}, "capture durable learnings"); !reflect.DeepEqual(got, wantCompound) {
		t.Fatalf("compound = %v, want %v", got, wantCompound)
	}
	if err := provider.Delete(row); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Pi session still exists after delete: %v", err)
	}
	if err := provider.Delete(Row{Provider: ProviderPi}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("delete without file error = %v", err)
	}
	if err := provider.Delete(Row{Provider: ProviderPi, File: filepath.Join(t.TempDir(), "session.json")}); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("delete non-JSONL error = %v", err)
	}
	if _, err := writePiConverted(Row{Provider: ProviderCodex, CWD: "(unknown cwd)"}, []Turn{{Role: "user", Text: "hello"}}); err == nil || !strings.Contains(err.Error(), "known workspace") {
		t.Fatalf("conversion with unknown cwd error = %v", err)
	}
}

func TestWritePiConvertedCreatesPrivateNativeSession(t *testing.T) {
	agentDir := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace with spaces")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	turns := []Turn{
		{Role: "user", Text: "first converted prompt"},
		{Role: "assistant", Text: "converted answer"},
		{Role: "user", Text: "last converted prompt"},
	}

	row, err := writePiConverted(Row{Provider: ProviderCodex, CWD: workspace}, turns)
	if err != nil {
		t.Fatal(err)
	}
	if row.Provider != ProviderPi || row.ID == "" || row.CWD != workspace {
		t.Fatalf("row = %#v", row)
	}
	if row.FirstUser != "first converted prompt" || row.LastUser != "last converted prompt" {
		t.Fatalf("previews = %q / %q", row.FirstUser, row.LastUser)
	}
	if filepath.Dir(row.File) != defaultPiSessionDir(workspace) {
		t.Fatalf("session dir = %q, want %q", filepath.Dir(row.File), defaultPiSessionDir(workspace))
	}
	info, err := os.Stat(row.File)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}

	file, err := os.Open(row.File)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("missing Pi session header")
	}
	var header struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		ID      string `json:"id"`
		CWD     string `json:"cwd"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		t.Fatal(err)
	}
	if header.Type != "session" || header.Version != 3 || header.ID != row.ID || header.CWD != workspace {
		t.Fatalf("header = %#v", header)
	}

	gotTurns, err := piTranscript(row.File)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotTurns, turns) {
		t.Fatalf("round-trip turns = %#v, want %#v", gotTurns, turns)
	}
}

func TestPiBranchCreatesIndependentTransferableCopy(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	turns := []Turn{{Role: "user", Text: "branch this"}, {Role: "assistant", Text: "branch ready"}}
	source, err := writePiConverted(Row{Provider: ProviderCodex, CWD: "/work/pi-branch"}, turns)
	if err != nil {
		t.Fatal(err)
	}
	branched, err := Branch(source)
	if err != nil {
		t.Fatal(err)
	}
	if branched.ID == source.ID || branched.File == source.File {
		t.Fatalf("branch did not create an independent session: source=%#v branch=%#v", source, branched)
	}
	if _, err := os.Stat(source.File); err != nil {
		t.Fatalf("branch modified the original: %v", err)
	}
	got, err := piTranscript(branched.File)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, turns) {
		t.Fatalf("branch turns = %#v, want %#v", got, turns)
	}
}

func TestPiSessionRootHonorsSettingsAndEnvironmentPrecedence(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	settingsRoot := filepath.Join(t.TempDir(), "settings-sessions")
	settings, err := json.Marshal(map[string]string{"sessionDir": settingsRoot})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(agentDir, "settings.json"), string(settings))

	if got := defaultPiSessionRoot(); got != settingsRoot {
		t.Fatalf("settings session root = %q", got)
	}

	envRoot := filepath.Join(t.TempDir(), "env-sessions")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", envRoot)
	if got := defaultPiSessionRoot(); got != envRoot {
		t.Fatalf("env session root = %q, want %q", got, envRoot)
	}

	row, err := writePiConverted(Row{Provider: ProviderClaude, CWD: "/work/pi"}, []Turn{{Role: "user", Text: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(row.File) != envRoot {
		t.Fatalf("custom session file = %q, want directory %q", row.File, envRoot)
	}
	if !strings.HasSuffix(row.File, "_"+row.ID+".jsonl") {
		t.Fatalf("unexpected Pi session filename %q", row.File)
	}
}

func TestPiSessionRootHonorsProjectSettingsForConversion(t *testing.T) {
	agentDir := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	writeFile(t, filepath.Join(agentDir, "settings.json"), `{"sessionDir":"global-sessions"}`)
	writeFile(t, filepath.Join(workspace, ".pi", "settings.json"), `{"sessionDir":".pi/project-sessions"}`)
	t.Chdir(workspace)

	want := filepath.Join(workspace, ".pi", "project-sessions")
	if got := defaultPiSessionDir(workspace); got != want {
		t.Fatalf("project session dir = %q, want %q", got, want)
	}
	nativePath := filepath.Join(want, "native.jsonl")
	writeFile(t, nativePath, `
{"type":"session","version":3,"id":"`+piSessionID+`","timestamp":"2026-07-10T10:00:00Z","cwd":"`+workspace+`"}
{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-07-10T10:01:00Z","message":{"role":"user","content":"project session"}}
`)
	rows := piProvider{}.Discover()
	if len(rows) != 1 || rows[0].File != nativePath {
		t.Fatalf("project discovery rows = %#v, want %q", rows, nativePath)
	}
	row, err := writePiConverted(Row{Provider: ProviderCodex, CWD: workspace}, []Turn{{Role: "user", Text: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(row.File) != want {
		t.Fatalf("converted file = %q, want directory %q", row.File, want)
	}
}
