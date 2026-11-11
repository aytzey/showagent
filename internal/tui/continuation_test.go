package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aytzey/showagent/internal/session"
)

func continuationFixture(t *testing.T) session.Row {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("CLAUDE_HOME", filepath.Join(root, "claude"))
	path := filepath.Join(root, "source.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, turn := range []session.Turn{
		{Role: "user", Text: "Önceki karar"},
		{Role: "assistant", Text: "Eski yanıt"},
		{Role: "user", Text: "Eski konuya dön\n" + strings.Repeat("uzun mesaj 中文\n", 60) + "password=synthetic-only\u001b[31m"},
		{Role: "assistant", Text: "Dahil edilmemesi gereken cevap"},
		{Role: "user", Text: "Başka iş"},
		{Role: "assistant", Text: "Başka cevap"},
	} {
		if err := encoder.Encode(map[string]any{"type": "response_item", "payload": map[string]any{
			"type": "message", "role": turn.Role, "content": []map[string]string{{"type": "input_text", "text": turn.Text}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return session.Row{Provider: session.ProviderCodex, ID: "source", File: path, CWD: root, FirstUser: "Önceki karar"}
}

func pressContinuationKey(t *testing.T, m model, code rune) (model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: code}))
	return asModel(t, updated), cmd
}

func applyContinuationCommand(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing action command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		msg = batch[0]() // The other command expires the status line on a timer.
	}
	updated, _ := m.Update(msg)
	return asModel(t, updated)
}

func TestContinuationChooserCreatesTheSelectedPrefix(t *testing.T) {
	for _, action := range []rune{'n', 'x'} {
		t.Run(string(action), func(t *testing.T) {
			row := continuationFixture(t)
			withFakeCommands(t, "codex", "claude")
			before, err := os.ReadFile(row.File)
			if err != nil {
				t.Fatal(err)
			}
			m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), action)
			m = applyContinuationCommand(t, m, cmd)
			if len(m.continuation.messages) != 3 || len(m.allRows) != 1 {
				t.Fatal("opening message choices wrote a session or lost choices")
			}
			m, _ = pressContinuationKey(t, m, tea.KeyDown)
			m, _ = pressContinuationKey(t, m, tea.KeyDown)
			view := ansi.Strip(m.View().Content)
			if !strings.Contains(view, "#2") || !strings.Contains(view, "Eski konuya dön") || strings.Contains(view, "synthetic-only") {
				t.Fatalf("selected message preview is missing or unsafe:\n%s", view)
			}
			m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
			m = applyContinuationCommand(t, m, cmd)
			if action == 'x' {
				if m.pendingConvert == nil || m.pendingConvert.options.ThroughUser != 2 || len(m.allRows) != 1 {
					t.Fatal("hand-off skipped preview or lost the selected user message")
				}
				m, cmd = pressContinuationKey(t, m, 'x')
				m = applyContinuationCommand(t, m, cmd)
			}
			if len(m.allRows) != 2 || m.continuation != nil {
				t.Fatalf("completed action has %d rows, chooser=%#v", len(m.allRows), m.continuation)
			}
			copy := m.list.SelectedItem().(item).row
			turns, err := session.Transcript(copy)
			if err != nil || len(turns) != 3 || turns[2].Role != "user" || !strings.HasPrefix(turns[2].Text, "Eski konuya dön") {
				t.Fatalf("copy contains the wrong context: %#v, err=%v", turns, err)
			}
			after, err := os.ReadFile(row.File)
			if err != nil || string(before) != string(after) {
				t.Fatal("original session changed")
			}
		})
	}
}

func TestContinuationCancelDiscardsLateLoadsAndReadFailures(t *testing.T) {
	row := continuationFixture(t)
	m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), 'n')
	loading := m.continuation
	m, _ = pressContinuationKey(t, m, tea.KeyEscape)
	m = applyContinuationCommand(t, m, cmd)
	if m.continuation != nil || len(m.allRows) != 1 {
		t.Fatal("late messages reopened a cancelled chooser")
	}
	m, cmd = pressContinuationKey(t, m, 'n')
	oldResult, _ := m.Update(continuationLoadedMsg{chooser: loading})
	m = asModel(t, oldResult)
	if !m.continuation.loading {
		t.Fatal("a previous request changed the current chooser")
	}
	if err := os.Remove(row.File); err != nil {
		t.Fatal(err)
	}
	m = applyContinuationCommand(t, m, cmd)
	if m.continuation.err == nil || !strings.Contains(m.View().Content, "Cannot read messages") {
		t.Fatal("read error was not shown")
	}
	m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
	if cmd != nil || m.continuation == nil || len(m.allRows) != 1 {
		t.Fatal("failed read allowed writing")
	}
}

func TestContinuationLongMessageScrollAndTerminalSizes(t *testing.T) {
	row := continuationFixture(t)
	m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), 'n')
	m = applyContinuationCommand(t, m, cmd)
	m, _ = pressContinuationKey(t, m, tea.KeyDown)
	m, _ = pressContinuationKey(t, m, tea.KeyDown)
	updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	m = asModel(t, updated)
	if m.continuation.offset == 0 {
		t.Fatal("long message cannot scroll")
	}
	for _, size := range [][2]int{{110, 36}, {80, 24}, {40, 12}, {20, 8}} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := asModel(t, updated).View().Content
		if lipgloss.Height(view) > size[1] {
			t.Fatalf("view exceeds %dx%d: %d rows", size[0], size[1], lipgloss.Height(view))
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("line exceeds %d columns: %q", size[0], line)
			}
		}
	}
	m, _ = pressContinuationKey(t, m, tea.KeyUp)
	if m.continuation.offset != 0 {
		t.Fatal("changing messages retained the old scroll position")
	}
}
