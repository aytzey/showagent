package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newClaudeTrimFixture(t *testing.T) (Row, []byte) {
	t.Helper()
	t.Setenv("CLAUDE_HOME", t.TempDir())
	cwd := t.TempDir()
	row, err := writeClaudeConverted(Row{CWD: cwd}, []Turn{
		{Role: "user", Text: "retain first user"},
		{Role: "assistant", Text: "retain first answer"},
		{Role: "user", Text: "remove this password=synthetic-secret"},
		{Role: "assistant", Text: "remove last answer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(row.File)
	if err != nil {
		t.Fatal(err)
	}
	return row, original
}

func TestTrimClaudePreservesNativeIdentityAndIndex(t *testing.T) {
	row, original := newClaudeTrimFixture(t)
	var title bytes.Buffer
	_ = json.NewEncoder(&title).Encode(map[string]string{"type": "custom-title", "sessionId": row.ID, "customTitle": "Saved project title"})
	original = append(original, title.Bytes()...)
	if err := os.WriteFile(row.File, original, 0o600); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(filepath.Dir(row.File), "sessions-index.json")
	index, _ := json.Marshal(map[string]any{
		"version":  1,
		"topLevel": "keep",
		"entries": []map[string]any{
			{"sessionId": row.ID, "fullPath": row.File, "messageCount": 4, "fileMtime": 1,
				"customTitle": "Saved project title", "summary": "remove last answer", "futureField": true},
			{"sessionId": "second", "summary": "leave me"},
		},
	})
	if err := os.WriteFile(indexPath, index, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanTrim(row, HandoffOptions{BeforeUser: 2})
	if err != nil {
		t.Fatal(err)
	}
	if plan.KeptTurns != 2 || plan.RemovedTurns != 2 || plan.KeptUserMessages != 1 || plan.RemovedUserMessages != 1 ||
		plan.Boundary.Number != 2 || strings.Contains(plan.Boundary.Text, "synthetic-secret") {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	beforeApply, _ := os.ReadFile(row.File)
	if !bytes.Equal(beforeApply, original) {
		t.Fatal("preview changed the source")
	}
	updated, err := ApplyTrim(plan)
	if err != nil {
		t.Fatal(err)
	}
	if updated.File != row.File || updated.ID != row.ID || updated.LastUser != "retain first user" {
		t.Fatalf("unexpected updated row: %+v", updated)
	}
	trimmed, _ := os.ReadFile(row.File)
	lines := bytes.SplitAfter(original, []byte("\n"))
	want := append(bytes.Join(lines[:3], nil), title.Bytes()...)
	if !bytes.Equal(trimmed, want) {
		t.Fatalf("expected unmodified retained records and the session title, got %s", trimmed)
	}
	var postIndex struct {
		TopLevel string           `json:"topLevel"`
		Entries  []map[string]any `json:"entries"`
	}
	newIndex, _ := os.ReadFile(indexPath)
	if json.Unmarshal(newIndex, &postIndex) != nil || postIndex.TopLevel != "keep" || len(postIndex.Entries) != 2 {
		t.Fatalf("invalid refreshed index: %s", newIndex)
	}
	entry := postIndex.Entries[0]
	if entry["sessionId"] != row.ID || entry["fullPath"] != row.File || entry["messageCount"] != float64(2) ||
		entry["customTitle"] != "Saved project title" || entry["futureField"] != true || entry["summary"] != nil ||
		postIndex.Entries[1]["summary"] != "leave me" {
		t.Fatalf("unexpected refreshed index: %s", newIndex)
	}
}

func TestTrimRejectsChangedSourceAndInactiveBoundary(t *testing.T) {
	row, original := newClaudeTrimFixture(t)
	for _, options := range []HandoffOptions{{}, {BeforeUser: 1}, {BeforeUser: 3}, {BeforeUser: 2, ThroughUser: 1}} {
		if _, err := PlanTrim(row, options); err == nil {
			t.Fatalf("accepted invalid cutoff %+v", options)
		}
	}
	plan, err := PlanTrim(row, HandoffOptions{ThroughUser: 1})
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(original, []byte("last answer"), []byte("next answer"), 1)
	info, err := os.Stat(row.File)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(row.File, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(row.File, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyTrim(plan); !errors.Is(err, ErrTrimChanged) {
		t.Fatalf("trim accepted a stale snapshot: %v", err)
	}
	actual, _ := os.ReadFile(row.File)
	if !bytes.Equal(changed, actual) {
		t.Fatal("stale plan overwrote the revised source")
	}
}

func TestTrimClaudeReportsIndexFailureAfterHistoryCommits(t *testing.T) {
	row, _ := newClaudeTrimFixture(t)
	indexPath := filepath.Join(filepath.Dir(row.File), "sessions-index.json")
	if err := os.WriteFile(indexPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanTrim(row, HandoffOptions{ThroughUser: 1})
	if err != nil {
		t.Fatal(err)
	}
	updated, warning := ApplyTrim(plan)
	if updated.ID != row.ID || warning == nil || !strings.Contains(warning.Error(), "history was trimmed") {
		t.Fatalf("wanted successful history and index warning, got %+v, %v", updated, warning)
	}
	turns, err := Transcript(updated)
	if err != nil || len(turns) != 1 || turns[0].Role != "user" {
		t.Fatalf("wrong retained transcript: %+v, %v", turns, err)
	}
}
