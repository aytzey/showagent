package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aytzey/showagent/internal/session"
)

type continuationChooser struct {
	row      session.Row
	target   session.Provider
	kind     sessionMutation
	messages []session.UserMessage
	index    int // Zero is the complete conversation; the rest are newest first.
	offset   int
	loading  bool
	err      error
}

type continuationLoadedMsg struct {
	chooser  *continuationChooser
	messages []session.UserMessage
	err      error
}

func (m model) startContinuation(kind sessionMutation, target session.Provider) (tea.Model, tea.Cmd) {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, m.list.NewStatusMessage("no session selected")
	}
	m.deleteArmed = ""
	m.pendingConvert = nil
	c := &continuationChooser{row: selected.row, target: target, kind: kind, loading: true}
	m.continuation = c
	return m, func() tea.Msg {
		messages, err := session.RecentUserMessages(selected.row)
		return continuationLoadedMsg{chooser: c, messages: messages, err: err}
	}
}

func (m model) updateContinuation(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := m.continuation
	switch msg.String() {
	case "esc", "q":
		m.continuation = nil
		return m, nil
	}
	if c.loading || c.err != nil {
		return m, nil
	}
	listRows, detailRows := m.continuationHeights()
	previous := c.index
	switch msg.String() {
	case "up", "k":
		c.index = max(0, c.index-1)
	case "down", "j":
		c.index = min(len(c.messages), c.index+1)
	case "pgup":
		c.index = max(0, c.index-listRows)
	case "pgdown":
		c.index = min(len(c.messages), c.index+listRows)
	case "home":
		c.index = 0
	case "end":
		c.index = len(c.messages)
	case "ctrl+d":
		c.offset = min(max(0, len(m.continuationDetail())-detailRows), c.offset+detailRows)
	case "ctrl+u":
		c.offset = max(0, c.offset-detailRows)
	case "enter", "ctrl+m":
		options := session.HandoffOptions{}
		if c.kind == mutationConvert {
			options = m.handoff
		}
		if c.index > 0 {
			options.ThroughUser = c.messages[c.index-1].Number
		}
		m.continuation = nil
		if c.kind == mutationBranch {
			return m.startMutation(func() tea.Msg {
				row, err := session.Branch(c.row, options)
				return sessionMutationMsg{kind: mutationBranch, row: row, err: err}
			}, "branch", "creating session branch…")
		}
		return m.startMutation(m.previewConversion(c.row, c.target, options), "preview", "building conversion preview…")
	}
	if c.index != previous {
		c.offset = 0
	}
	return m, nil
}

func (m model) continuationHeights() (listRows, detailRows int) {
	height := m.height
	if height <= 0 {
		height = 24
	}
	reserved := 9
	if m.continuation.kind == mutationConvert && m.handoff.MaxTurns > 0 {
		reserved++
	}
	listRows = max(1, min(12, min(len(m.continuation.messages)+1, (height-reserved)/2)))
	detailRows = max(1, height-reserved-listRows)
	return listRows, detailRows
}

func (m model) continuationDetail() []string {
	c := m.continuation
	text := "Keep the conversation through its latest reply."
	if c.index > 0 {
		text = c.messages[c.index-1].Text
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	return strings.Split(ansi.Hardwrap(text, max(1, m.width-4), true), "\n")
}

func (m model) continuationView() string {
	c := m.continuation
	th := m.render.theme
	operation := "Branch"
	if c.kind == mutationConvert {
		operation = "Hand off to " + session.DisplayName(c.target)
	}
	lines := []string{
		th.title.Render("Continue from · " + operation),
		session.SafeDisplayText(string(c.row.Provider) + " " + c.row.ID + " · " + c.row.CWD),
		"The selected user message is included; all later turns are left out.",
		"The original session and project files stay as they are.",
		"",
	}
	footer := "↑/↓ choose · enter create branch · esc cancel"
	if c.kind == mutationConvert {
		footer = "↑/↓ choose · enter preview hand-off · esc cancel"
	}
	switch {
	case c.loading:
		lines = append(lines, "Loading the last 30 user messages…")
	case c.err != nil:
		lines = append(lines, th.deleteBanner.Render("Cannot read messages: "+session.SafeDisplayText(c.err.Error())))
	default:
		listRows, detailRows := m.continuationHeights()
		start := min(max(0, c.index-listRows/2), max(0, len(c.messages)+1-listRows))
		for index := start; index < min(start+listRows, len(c.messages)+1); index++ {
			label := "Continue from the end"
			if index > 0 {
				message := c.messages[index-1]
				label = fmt.Sprintf("#%d  %s", message.Number, session.SafeDisplayText(message.Text))
			}
			prefix, style := "  ", th.message
			if index == c.index {
				prefix, style = "❯ ", th.selected
			}
			lines = append(lines, style.Render(truncateCells(prefix+label, max(1, m.width))))
		}
		lines = append(lines, th.muted.Render(fmt.Sprintf("Last %d user messages · newest first · choice %d/%d", len(c.messages), c.index+1, len(c.messages)+1)))
		detail := m.continuationDetail()
		offset := min(c.offset, max(0, len(detail)-detailRows))
		title := "Message preview"
		if c.index > 0 {
			title = fmt.Sprintf("User #%d · lines %d–%d/%d", c.messages[c.index-1].Number, offset+1, min(len(detail), offset+detailRows), len(detail))
		}
		body := strings.Join(detail[offset:min(len(detail), offset+detailRows)], "\n")
		lines = append(lines, titledBox(th.detailEdge, th.detailEdge, title, body, max(4, m.width)))
		if c.kind == mutationConvert && m.handoff.MaxTurns > 0 {
			lines = append(lines, fmt.Sprintf("Scope: last %d turns ending at this point.", m.handoff.MaxTurns))
		}
		footer += " · ctrl+d/u scroll message"
	}
	if m.width > 0 && m.width < 70 {
		footer = "esc cancel · enter choose · ↑/↓ · ctrl+d/u scroll"
	}
	content := strings.Join(lines, "\n")
	if m.height > 0 {
		content = clampLines(content, m.height-1)
	}
	return clampLineWidths(content+"\n"+th.hint.Render(footer), m.width)
}
