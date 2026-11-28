package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const trimPiHeader = `{"type":"session","version":3,"id":"pi-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/sample"}`
const trimPiFirst = `{"type":"message","id":"u1","parentId":null,"message":{"role":"user","content":"keep question"}}`
const trimPiReply = `{"type":"message","id":"a1","parentId":"u1","message":{"role":"assistant","content":[{"type":"text","text":"keep reply"}],"native":{"info":true}}}`
const trimPiLastUser = `{"type":"message","id":"u2","parentId":"a1","message":{"role":"user","content":"remove question"}}`
const trimPiLastReply = `{"type":"message","id":"a2","parentId":"u2","message":{"role":"assistant","content":"remove reply"}}`

func TestTrimPiKeepsNativePrefix(t *testing.T) {
	const lineEnding = "\r\n"
	retained := strings.Join([]string{trimPiHeader, trimPiFirst, trimPiReply}, lineEnding) + lineEnding
	source := []byte(retained + trimPiLastUser + lineEnding + trimPiLastReply)
	path := filepath.Join(t.TempDir(), "fixture.jsonl")
	row := Row{ID: "pi-session", File: path}

	result, err := trimPi(row, source, HandoffOptions{BeforeUser: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.replacement, []byte(retained)) || result.keptUsers != 1 ||
		result.keptTurns != 2 || result.removedUsers != 1 || result.removedTurns != 2 ||
		result.boundaryText != "remove question" {
		t.Fatalf("wrong prefix or summary: %+v", result)
	}
	if err := os.WriteFile(path, result.replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, ok := parsePi(path)
	if !ok || parsed.ID != row.ID || parsed.LastUser != "keep question" {
		t.Fatalf("trimmed session did not rediscover with the same ID: %+v", parsed)
	}
	turns, err := piTranscript(path)
	if err != nil || len(turns) != 2 || turns[1].Text != "keep reply" {
		t.Fatalf("wrong native transcript: %v, %v", turns, err)
	}
}

func TestTrimPiRejectsBranchesAndInvalidCutoffs(t *testing.T) {
	linear := []string{trimPiHeader, trimPiFirst, trimPiReply, trimPiLastUser}
	for _, test := range []struct {
		name    string
		lines   []string
		options HandoffOptions
		want    string
	}{
		{
			name:    "branched lineage",
			lines:   append(append([]string(nil), linear...), `{"type":"message","id":"branch","parentId":"u1","message":{"role":"user","content":"alternate"}}`),
			options: HandoffOptions{ThroughUser: 1}, want: "branches or a broken parent chain",
		},
		{name: "legacy version", lines: []string{strings.Replace(trimPiHeader, `"version":3`, `"version":1`, 1), trimPiFirst},
			options: HandoffOptions{ThroughUser: 1}, want: "version 3"},
		{name: "all history", lines: linear, options: HandoffOptions{BeforeUser: 1}, want: "keep at least one"},
		{name: "no suffix", lines: linear, options: HandoffOptions{ThroughUser: 2}, want: "no conversation messages"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(strings.Join(test.lines, "\n"))
			result, err := trimPi(Row{ID: "pi-session"}, source, test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) || result.replacement != nil {
				t.Fatalf("returned %v, %v", result, err)
			}
		})
	}
}

func TestTrimPiCustomStoreRoundTrip(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	settingsDir := filepath.Join(workspace, ".pi")
	customDir := filepath.Join(workspace, "custom-sessions")
	for _, directory := range []string{settingsDir, customDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"sessionDir":"custom-sessions"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(piSessionHeader{Type: "session", Version: 3, ID: "pi-session",
		Timestamp: "2026-01-01T00:00:00Z", CWD: workspace})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(customDir, "fixture.jsonl")
	source := strings.Join([]string{string(header), trimPiFirst, trimPiReply, trimPiLastUser, trimPiLastReply}, "\n")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	assertTrimStoreRoundTrip(t, piProvider{}.Discover, workspace, "pi-session", path,
		[]string{"pi", "--session", path}, "keep question", "keep reply")
}
