package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aytzey/showagent/internal/session"
)

func trimFixture(t *testing.T) (session.Row, []session.Turn, []byte) {
	t.Helper()
	root := t.TempDir()
	claudeDir := filepath.Join(root, "claude")
	t.Setenv("CLAUDE_HOME", claudeDir)
	project := filepath.Join(claudeDir, "projects", "sample")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "621bf4e3-e179-45ce-9cd1-1b5e2ec554e8"
	const latest = "Latest request password=synthetic-old"
	row := session.Row{Provider: session.ProviderClaude, ID: id, CWD: root, File: filepath.Join(project, id+".jsonl"),
		FirstUser: "First request", LastUser: session.RedactTranscriptText(latest)}
	turns := []session.Turn{
		{Role: "user", Text: row.FirstUser}, {Role: "assistant", Text: "First answer"},
		{Role: "user", Text: "Selected request"}, {Role: "assistant", Text: "Selected answer"},
		{Role: "user", Text: latest}, {Role: "assistant", Text: "Latest answer"},
	}
	var source bytes.Buffer
	encoder := json.NewEncoder(&source)
	var parent any
	for index, turn := range turns {
		uuid := fmt.Sprintf("724bf4e3-e179-45ce-9cd1-%012d", index+1)
		row.LastAt = time.Date(2026, 10, 2, 18, index, 0, 0, time.UTC)
		if err := encoder.Encode(map[string]any{
			"type": turn.Role, "cwd": root, "sessionId": id, "uuid": uuid, "parentUuid": parent,
			"timestamp": row.LastAt.Format(time.RFC3339), "message": map[string]any{
				"role": turn.Role, "content": []map[string]string{{"type": "text", "text": turn.Text}},
			},
		}); err != nil {
			t.Fatal(err)
		}
		parent = uuid
	}
	if err := os.WriteFile(row.File, source.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return row, turns, source.Bytes()
}

func assertTrimSource(t *testing.T, row session.Row, want []byte) {
	t.Helper()
	got, err := os.ReadFile(row.File)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("trim touched the source unexpectedly: %v", err)
	}
}

func TestTrimPickerBeforeOnlyRejectsTheFirstUserAndExplainsCodexRetention(t *testing.T) {
	row := session.Row{Provider: session.ProviderCodex, ID: "example"}
	m := sizedModel([]session.Row{row})
	c := &continuationChooser{row: row, kind: mutationTrim, trimBeforeOnly: true,
		messages: []session.UserMessage{{Number: 1, Turns: 1, Text: "First request"}}}
	c.loaded(continuationLoadedMsg{messages: c.messages, total: 1})
	m.continuation = c
	if len(c.choices) != 0 || !strings.Contains(m.continuationSummary(), "no later user message") {
		t.Fatal("first user was offered as a before-only cutoff")
	}
	m, cmd := pressContinuationKey(t, m, tea.KeyEnter)
	if cmd != nil || m.continuation.trim != nil {
		t.Fatal("empty choice started a trim plan")
	}
	second := session.UserMessage{Number: 2, Turns: 3, Text: "Second request"}
	c.loaded(continuationLoadedMsg{messages: append([]session.UserMessage{second}, c.messages...), total: 4})
	m, _ = pressContinuationKey(t, m, tea.KeyTab)
	options := c.options(m.continuationScope())
	if len(c.choices) != 1 || options.BeforeUser != 2 || options.ThroughUser != 0 {
		t.Fatalf("unexpected before-only boundary: %#v", options)
	}
	c.trim = &trimPreview{before: true, plan: session.TrimPlan{
		Row: row, Boundary: second, KeptTurns: 2, RemovedTurns: 2, KeptUserMessages: 1, RemovedUserMessages: 1,
	}}
	m.width, m.height = 24, 9
	// Match the padded warning banner even in a monochrome test environment.
	m.render.theme.deleteBanner = m.render.theme.deleteBanner.Padding(0, 1)
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Trim Codex session", "Close the agent session.", "Old rollout kept on disk", "Removes from user #2.", "D trim · esc back"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Codex confirmation missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Permanently") || strings.Contains(view, "D delete") {
		t.Fatalf("Codex confirmation overstates on-disk deletion:\n%s", view)
	}
}

func TestTrimPickerRequiresSeparateConfirmationAndRefreshesSameSession(t *testing.T) {
	for _, test := range []struct {
		name       string
		before     bool
		boundary   string
		kept       int
		userCounts string
		lastUser   string
	}{
		{name: "delete only final answer", boundary: "Deletes after user #3.", kept: 5, userCounts: "keep 3, cut 0", lastUser: "Latest request password=synthetic-old"},
		{name: "delete message and later turns", before: true, boundary: "Deletes from user #2.", kept: 2, userCounts: "keep 1, cut 2", lastUser: "First request"},
	} {
		t.Run(test.name, func(t *testing.T) {
			row, turns, source := trimFixture(t)
			m, _ := pressContinuationKey(t, sizedModel([]session.Row{row}), '/')
			filtered, _ := m.Update(tea.PasteMsg{Content: "Latest request"})
			m, _ = pressContinuationKey(t, asModel(t, filtered), tea.KeyEnter)
			if !m.list.IsFiltered() {
				t.Fatal("fixture search did not match the session")
			}
			m, cmd := pressContinuationKey(t, m, 'D')
			m = applyContinuationCommand(t, m, cmd)
			if m.continuation == nil || m.continuation.kind != mutationTrim || len(m.continuation.choices) != 3 {
				t.Fatal("trim did not open the user-message chooser")
			}
			if test.before {
				m, _ = pressContinuationKey(t, m, tea.KeyDown)
				m, _ = pressContinuationKey(t, m, tea.KeyTab)
			}
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "Trim session") || !strings.Contains(view, "enter preview") {
				t.Fatalf("missing trim chooser guidance:\n%s", view)
			}
			m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
			preview := cmd
			m, cmd = pressContinuationKey(t, m, 'D')
			if cmd != nil {
				t.Fatal("confirmation executed before the plan loaded")
			}
			m = applyContinuationCommand(t, m, preview)
			m.width, m.height = 20, 5
			m, cmd = pressContinuationKey(t, m, 'D')
			if cmd != nil || !strings.Contains(ansi.Strip(m.View().Content), "Resize") {
				t.Fatal("trim was confirmable when its preview did not fit")
			}
			m.width, m.height = 32, 8
			view = ansi.Strip(m.View().Content)
			for _, want := range []string{"Permanently trim session", "Close it in the agent first.", test.boundary, "User messages · " + test.userCounts, "D delete · esc back"} {
				if !strings.Contains(view, want) {
					t.Fatalf("trim confirmation missing %q:\n%s", want, view)
				}
			}
			if strings.Contains(view, "synthetic-old") || lipgloss.Width(view) > m.width || lipgloss.Height(view) > m.height {
				t.Fatalf("trim confirmation escaped its viewport or displayed a secret:\n%s", view)
			}
			assertTrimSource(t, row, source)
			for _, key := range []rune{'d', tea.KeyEnter} {
				m, cmd = pressContinuationKey(t, m, key)
				if cmd != nil || m.continuation.trim == nil {
					t.Fatalf("%q accepted or dismissed the trim confirmation", key)
				}
			}
			m, _ = pressContinuationKey(t, m, tea.KeyEscape)
			if m.continuation == nil || m.continuation.trim != nil {
				t.Fatal("escape did not back out to the chosen message")
			}
			assertTrimSource(t, row, source)
			m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
			m = applyContinuationCommand(t, m, cmd)
			m, cmd = pressContinuationKey(t, m, 'D')
			msg := cmd().(tea.BatchMsg)[0]().(trimAppliedMsg)
			if msg.err != nil {
				t.Fatal(msg.err)
			}
			updated, _ := m.Update(msg)
			m = asModel(t, updated)
			selected, ok := m.list.SelectedItem().(item)
			if !ok || rowKey(selected.row) != rowKey(row) || len(m.allRows) != 1 || m.busy != "" {
				t.Fatalf("trim did not retain the existing row: %#v", m.allRows)
			}
			if filtered := m.list.IsFiltered(); filtered == test.before {
				t.Fatalf("applied filter state = %v after trimming, want %v", filtered, !test.before)
			}
			got, err := session.Transcript(selected.row)
			if err != nil || !reflect.DeepEqual(got, turns[:test.kept]) || selected.row.LastUser != session.RedactTranscriptText(test.lastUser) {
				t.Fatalf("wrong conversation after trim: %#v, last user %q, error %v", got, selected.row.LastUser, err)
			}
		})
	}
}

func TestTrimPickerRejectsChangesBeforeAndAfterPreview(t *testing.T) {
	for _, beforePreview := range []bool{true, false} {
		t.Run(fmt.Sprintf("before-preview=%t", beforePreview), func(t *testing.T) {
			row, _, original := trimFixture(t)
			m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), 'D')
			m = applyContinuationCommand(t, m, cmd)
			changed := bytes.Replace(original, []byte("synthetic-old"), []byte("synthetic-new"), 1)
			writeChange := func() {
				if err := os.WriteFile(row.File, changed, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if beforePreview {
				writeChange()
			}
			m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
			m = applyContinuationCommand(t, m, cmd)
			if beforePreview {
				if m.continuation.trim.err == nil || !strings.Contains(m.View().Content, "selected message changed") {
					t.Fatal("the chooser accepted a changed boundary hidden by redaction")
				}
				m, cmd = pressContinuationKey(t, m, 'D')
				if cmd != nil {
					t.Fatal("could confirm a failed preview")
				}
			} else {
				if m.continuation.trim.err != nil {
					t.Fatal(m.continuation.trim.err)
				}
				writeChange()
				m, cmd = pressContinuationKey(t, m, 'D')
				m = applyContinuationCommand(t, m, cmd)
				if m.busy != "" || !strings.Contains(m.View().Content, "trim failed") || m.allRows[0] != row {
					t.Fatal("stale trim did not report failure and preserve the old row")
				}
			}
			assertTrimSource(t, row, changed)
		})
	}
}

func TestTrimPickerRefreshesCommittedRowEvenWithIndexWarning(t *testing.T) {
	row := session.Row{Provider: session.ProviderClaude, ID: "example", CWD: "/fixture",
		FirstUser: "First request", LastUser: "Removed request"}
	m := sizedModel([]session.Row{row})
	m.busy = "trim"
	updated := row
	updated.LastUser = row.FirstUser
	result, _ := m.Update(trimAppliedMsg{row: updated, err: errors.New("could not refresh Claude session index")})
	m = asModel(t, result)
	if len(m.allRows) != 1 || m.allRows[0] != updated || m.list.SelectedItem().(item).row != updated || m.busy != "" {
		t.Fatalf("trim warning did not refresh the existing row: %#v", m.allRows)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "trimmed session with warning") || !strings.Contains(view, "could not refresh Claude session index") {
		t.Fatalf("committed trim did not explain its warning:\n%s", view)
	}
}

func TestTrimPickerReplacesPriorCodexRollout(t *testing.T) {
	row := session.Row{Provider: session.ProviderCodex, ID: "example", CWD: "/fixture", File: "/fixture/rollout.jsonl"}
	other := session.Row{Provider: session.ProviderCodex, ID: "other", CWD: "/fixture", File: "/fixture/other.jsonl"}
	m := sizedModel([]session.Row{row, other})
	updated := row
	updated.File = "/fixture/rollout_revision.jsonl"
	result, _ := m.Update(trimAppliedMsg{source: row, row: updated})
	m = asModel(t, result)
	if len(m.allRows) != 2 || indexOfRow(m.allRows, row) >= 0 || indexOfRow(m.allRows, other) < 0 ||
		m.list.SelectedItem().(item).row != updated {
		t.Fatalf("trim kept a duplicate old rollout or lost the new selection: %#v", m.allRows)
	}
}
