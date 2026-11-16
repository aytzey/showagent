package tui

import (
	"encoding/json"
	"fmt"
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

// TestContinuationHeaderNamesTheSourceSession: the chooser says which session
// it copies from: its agent, its id and its workspace with home shortened.
func TestContinuationHeaderNamesTheSourceSession(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	t.Setenv("NO_COLOR", "")
	row := continuationFixture(t)
	row.CWD = filepath.Join(home, "code", "api-server")
	withFakeCommands(t, "codex", "claude")
	m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), 'x')
	m = applyContinuationCommand(t, m, cmd)

	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(lines) < 2 || !strings.Contains(lines[0], "Hand off to Claude") {
		t.Fatalf("chooser title is missing:\n%s", strings.Join(lines, "\n"))
	}
	want := "CODEX  source · ~" + string(filepath.Separator) + filepath.Join("code", "api-server")
	if got := strings.TrimSpace(lines[1]); got != want {
		t.Fatalf("chooser source line = %q, want %q", got, want)
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

// typeContinuationText presses the keys of text one at a time.
func typeContinuationText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		updated, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
		m = asModel(t, updated)
	}
	return m
}

// TestContinuationTabLeavesTheChosenMessageOut: tab moves the end of the copy
// to the reply in front of the chosen message, so it can be asked again.
func TestContinuationTabLeavesTheChosenMessageOut(t *testing.T) {
	for _, action := range []rune{'n', 'x'} {
		t.Run(string(action), func(t *testing.T) {
			row := continuationFixture(t)
			withFakeCommands(t, "codex", "claude")
			m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), action)
			m = applyContinuationCommand(t, m, cmd)

			// The end of the conversation has no message to leave out.
			m, _ = pressContinuationKey(t, m, tea.KeyTab)
			if m.continuation.leaveOut || strings.Contains(ansi.Strip(m.View().Content), "tab ") {
				t.Fatal("tab is offered where there is no message to leave out")
			}

			m, _ = pressContinuationKey(t, m, tea.KeyDown)
			m, _ = pressContinuationKey(t, m, tea.KeyDown)
			if view := ansi.Strip(m.View().Content); !strings.Contains(view, "User #2 · included · keeps 3 of 6 turns") ||
				!strings.Contains(view, "tab leave message out") {
				t.Fatalf("included message is not described:\n%s", view)
			}
			m, _ = pressContinuationKey(t, m, tea.KeyTab)
			view := ansi.Strip(m.View().Content)
			for _, want := range []string{
				"ends on the reply before user #2",
				"#2  left out · Eski konuya dön",
				"User #2 · left out · keeps 2 of 6 turns",
				"tab include message",
			} {
				if !strings.Contains(view, want) {
					t.Fatalf("left-out message view is missing %q:\n%s", want, view)
				}
			}

			m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
			m = applyContinuationCommand(t, m, cmd)
			if action == 'x' {
				if m.pendingConvert == nil || m.pendingConvert.options.BeforeUser != 2 || m.pendingConvert.options.ThroughUser != 0 {
					t.Fatalf("hand-off preview lost the chosen end: %#v", m.pendingConvert)
				}
				if got := m.pendingConvert.preview.Scope; got != "before user #2" {
					t.Fatalf("preview scope = %q, want before user #2", got)
				}
				m, cmd = pressContinuationKey(t, m, 'x')
				m = applyContinuationCommand(t, m, cmd)
			}
			if len(m.allRows) != 2 || m.continuation != nil {
				t.Fatalf("completed action has %d rows, chooser=%#v", len(m.allRows), m.continuation)
			}
			turns, err := session.Transcript(m.list.SelectedItem().(item).row)
			if err != nil || len(turns) != 2 || turns[1].Role != "assistant" || turns[1].Text != "Eski yanıt" {
				t.Fatalf("copy should end on the reply before user #2: %#v, err=%v", turns, err)
			}
		})
	}
}

// TestContinuationFirstMessageCannotBeLeftOut: nothing comes before the first
// user message, so a copy that ends there always includes it.
func TestContinuationFirstMessageCannotBeLeftOut(t *testing.T) {
	row := continuationFixture(t)
	withFakeCommands(t, "codex")
	m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), 'n')
	m = applyContinuationCommand(t, m, cmd)

	m, _ = pressContinuationKey(t, m, tea.KeyEnd)
	m, _ = pressContinuationKey(t, m, tea.KeyTab)
	if view := ansi.Strip(m.View().Content); m.continuation.leaveOut || !strings.Contains(view, "User #1 · included · keeps 1 of 6 turns") {
		t.Fatalf("the first message was offered for leaving out:\n%s", view)
	}

	// Leaving out stays switched on while the cursor moves; the first message
	// says why it is kept all the same.
	m, _ = pressContinuationKey(t, m, tea.KeyUp)
	m, _ = pressContinuationKey(t, m, tea.KeyTab)
	m, _ = pressContinuationKey(t, m, tea.KeyDown)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Nothing comes before user #1") || !strings.Contains(view, "User #1 · included") {
		t.Fatalf("the first message does not explain why it is kept:\n%s", view)
	}
	m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
	m = applyContinuationCommand(t, m, cmd)
	turns, err := session.Transcript(m.list.SelectedItem().(item).row)
	if err != nil || len(turns) != 1 || turns[0].Text != "Önceki karar" {
		t.Fatalf("copy should hold the first message only: %#v, err=%v", turns, err)
	}
}

// TestContinuationSearchNarrowsTheMessages: / searches the session's user
// messages by their words, the way the picker searches sessions.
func TestContinuationSearchNarrowsTheMessages(t *testing.T) {
	row := continuationFixture(t)
	withFakeCommands(t, "codex")
	m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), 'n')
	m = applyContinuationCommand(t, m, cmd)

	m, focus := pressContinuationKey(t, m, '/')
	if !m.continuation.typing || focus == nil {
		t.Fatal("/ did not open the search field")
	}
	m = typeContinuationText(t, m, "ESKI konu")
	view := ansi.Strip(m.View().Content)
	if len(m.continuation.choices) != 1 || !strings.Contains(view, "1 of 3 match") || !strings.Contains(view, "User #2 · included") {
		t.Fatalf("search did not narrow the messages to the one that matches:\n%s", view)
	}
	if strings.Contains(view, "Continue from the end") || strings.Contains(view, "Başka iş") {
		t.Fatalf("search still lists what does not match:\n%s", view)
	}

	// Letters that are keys elsewhere are typed, and a search without a match
	// offers nothing to create.
	m = typeContinuationText(t, m, "q")
	if m.continuation == nil || m.continuation.search.Value() != "ESKI konuq" {
		t.Fatal("q was not typed into the search")
	}
	view = ansi.Strip(m.View().Content)
	if len(m.continuation.choices) != 0 || !strings.Contains(view, "No user message matches the search.") || !strings.Contains(view, "0 of 3 match") {
		t.Fatalf("search without a match is not explained:\n%s", view)
	}
	m, _ = pressContinuationKey(t, m, tea.KeyEnter)
	m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
	if cmd != nil || m.continuation == nil || len(m.allRows) != 1 {
		t.Fatal("a search without a match created a session")
	}

	// A pasted search replaces nothing on disk and narrows like typed words.
	m, _ = pressContinuationKey(t, m, '/')
	m, _ = pressContinuationKey(t, m, tea.KeyBackspace)
	pasted, _ := m.Update(tea.PasteMsg{Content: " dön"})
	m = asModel(t, pasted)
	if got := m.continuation.search.Value(); got != "ESKI konu dön" || len(m.continuation.choices) != 1 {
		t.Fatalf("search after paste = %q with %d choices, want the pasted words and 1 match", got, len(m.continuation.choices))
	}

	// enter keeps the search and returns to the list; esc clears it first and
	// cancels only when there is nothing to clear.
	m, _ = pressContinuationKey(t, m, tea.KeyEnter)
	view = ansi.Strip(m.View().Content)
	if m.continuation.typing || !strings.Contains(view, "search: ESKI konu dön · 1 of 3 user messages") || !strings.Contains(view, "esc clear search") {
		t.Fatalf("kept search is not shown:\n%s", view)
	}
	m, _ = pressContinuationKey(t, m, tea.KeyEscape)
	if m.continuation == nil || m.continuation.search.Value() != "" || len(m.continuation.choices) != 4 {
		t.Fatal("esc did not clear the search first")
	}
	if m.continuation.choices[m.continuation.index] != 1 {
		t.Fatal("clearing the search moved the cursor off the chosen message")
	}

	// A number finds its message.
	m, _ = pressContinuationKey(t, m, '/')
	m = typeContinuationText(t, m, "#3")
	if len(m.continuation.choices) != 1 || m.continuation.messages[m.continuation.choices[0]].Number != 3 {
		t.Fatalf("#3 did not find user message 3: %v", m.continuation.choices)
	}
	m, _ = pressContinuationKey(t, m, tea.KeyEscape)
	if m.continuation == nil || m.continuation.typing || m.continuation.search.Value() != "" {
		t.Fatal("esc while typing should clear the search and keep the chooser open")
	}
	m, _ = pressContinuationKey(t, m, tea.KeyEscape)
	if m.continuation != nil || len(m.allRows) != 1 {
		t.Fatal("esc with nothing to clear should cancel")
	}
}

// TestContinuationSearchKeepsTheListInReach: the arrows and tab still choose
// while the search field has the keyboard, and enter then creates the copy.
func TestContinuationSearchKeepsTheListInReach(t *testing.T) {
	row := continuationFixture(t)
	withFakeCommands(t, "codex")
	m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), 'n')
	m = applyContinuationCommand(t, m, cmd)
	m, _ = pressContinuationKey(t, m, '/')
	m = typeContinuationText(t, m, "i")
	if len(m.continuation.choices) != 3 {
		t.Fatalf("choices = %d, want the three messages that contain an i", len(m.continuation.choices))
	}
	m, _ = pressContinuationKey(t, m, tea.KeyDown)
	m, _ = pressContinuationKey(t, m, tea.KeyTab)
	if !m.continuation.typing || !m.continuation.leaveOut || m.continuation.search.Value() != "i" {
		t.Fatal("down and tab should act on the list without leaving the search")
	}
	m, _ = pressContinuationKey(t, m, tea.KeyEnter)
	m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
	m = applyContinuationCommand(t, m, cmd)
	turns, err := session.Transcript(m.list.SelectedItem().(item).row)
	if err != nil || len(turns) != 2 || turns[1].Text != "Eski yanıt" {
		t.Fatalf("copy should end on the reply before user #2: %#v, err=%v", turns, err)
	}
}

// TestContinuationListsEveryUserMessage: a long session can be branched from
// its first message, not only from its most recent ones.
func TestContinuationListsEveryUserMessage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	path := filepath.Join(root, "long.jsonl")
	var lines strings.Builder
	encoder := json.NewEncoder(&lines)
	for exchange := 1; exchange <= 45; exchange++ {
		for _, turn := range []session.Turn{
			{Role: "user", Text: fmt.Sprintf("ask %d", exchange)},
			{Role: "assistant", Text: fmt.Sprintf("answer %d", exchange)},
		} {
			if err := encoder.Encode(map[string]any{"type": "response_item", "payload": map[string]any{
				"type": "message", "role": turn.Role, "content": []map[string]string{{"type": "input_text", "text": turn.Text}},
			}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	row := session.Row{Provider: session.ProviderCodex, ID: "long", File: path, CWD: root, FirstUser: "ask 1"}
	withFakeCommands(t, "codex")
	m, cmd := pressContinuationKey(t, sizedModel([]session.Row{row}), 'n')
	m = applyContinuationCommand(t, m, cmd)
	if len(m.continuation.messages) != 45 || m.continuation.total != 90 {
		t.Fatalf("chooser holds %d messages of %d turns, want 45 of 90", len(m.continuation.messages), m.continuation.total)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "45 user messages · newest first · choice 1/46") || !strings.Contains(view, "keeps all 90 turns") {
		t.Fatalf("chooser does not count the whole session:\n%s", view)
	}

	m, _ = pressContinuationKey(t, m, tea.KeyPgDown)
	if m.continuation.index <= 1 {
		t.Fatal("page down did not move a page")
	}
	m, _ = pressContinuationKey(t, m, tea.KeyEnd)
	m, _ = pressContinuationKey(t, m, tea.KeyUp)
	m, _ = pressContinuationKey(t, m, tea.KeyUp)
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "❯ #3  ask 3") || !strings.Contains(view, "User #3 · included · keeps 5 of 90 turns") {
		t.Fatalf("an early message is out of reach:\n%s", view)
	}
	m, _ = pressContinuationKey(t, m, tea.KeyHome)
	if m.continuation.index != 0 {
		t.Fatal("home did not return to the end of the conversation")
	}
	m, _ = pressContinuationKey(t, m, '/')
	m = typeContinuationText(t, m, "#3")
	m, _ = pressContinuationKey(t, m, tea.KeyEnter)
	m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
	m = applyContinuationCommand(t, m, cmd)
	turns, err := session.Transcript(m.list.SelectedItem().(item).row)
	if err != nil || len(turns) != 5 || turns[4].Text != "ask 3" {
		t.Fatalf("copy should end on user #3: %d turns, err=%v", len(turns), err)
	}
}

// TestContinuationShowsTheHandoffScope: a hand-off limited to its last turns
// says how many turns each choice really keeps.
func TestContinuationShowsTheHandoffScope(t *testing.T) {
	row := continuationFixture(t)
	withFakeCommands(t, "codex", "claude")
	m := sizedModel([]session.Row{row})
	m.handoff = session.HandoffOptions{MaxTurns: 2}
	m, cmd := pressContinuationKey(t, m, 'x')
	m = applyContinuationCommand(t, m, cmd)
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Whole conversation · keeps 2 of 6 turns") || !strings.Contains(view, "Scope: last 2 turns ending at this point.") {
		t.Fatalf("scoped hand-off does not say what it keeps:\n%s", view)
	}
	m, _ = pressContinuationKey(t, m, tea.KeyDown)
	m, _ = pressContinuationKey(t, m, tea.KeyDown)
	m, _ = pressContinuationKey(t, m, tea.KeyTab)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "User #2 · left out · keeps 2 of 6 turns") {
		t.Fatalf("scoped hand-off does not count the chosen end:\n%s", view)
	}
	m, cmd = pressContinuationKey(t, m, tea.KeyEnter)
	m = applyContinuationCommand(t, m, cmd)
	if m.pendingConvert == nil || m.pendingConvert.options != (session.HandoffOptions{MaxTurns: 2, BeforeUser: 2}) {
		t.Fatalf("hand-off options = %#v, want the scope and the chosen end", m.pendingConvert)
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
	// Every state fits the terminal: a chosen message, a message left out, and
	// the search field with a long search in it.
	leftOut, _ := pressContinuationKey(t, m, tea.KeyTab)
	searching, _ := pressContinuationKey(t, m, '/')
	searching = typeContinuationText(t, searching, "uzun mesaj "+strings.Repeat("中文 ", 30))
	for name, state := range map[string]model{"chosen": m, "left out": leftOut, "searching": searching} {
		for _, size := range [][2]int{{110, 36}, {80, 24}, {60, 14}, {40, 12}, {20, 8}, {12, 4}} {
			updated, _ := state.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			resized := asModel(t, updated)
			if name == "searching" {
				// The field is sized on the next key press.
				resized, _ = pressContinuationKey(t, resized, tea.KeyLeft)
			}
			view := resized.View().Content
			if lipgloss.Height(view) > size[1] {
				t.Fatalf("%s view exceeds %dx%d: %d rows", name, size[0], size[1], lipgloss.Height(view))
			}
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("%s line exceeds %d columns: %q", name, size[0], line)
				}
			}
		}
	}
	m, _ = pressContinuationKey(t, m, tea.KeyUp)
	if m.continuation.offset != 0 {
		t.Fatal("changing messages retained the old scroll position")
	}
}
