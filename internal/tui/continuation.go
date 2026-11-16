package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aytzey/showagent/internal/session"
)

// wholeConversation is the choice that ends a copy where the session ends.
const wholeConversation = -1

// continuationChooser picks where a branch or hand-off stops: at the session's
// end, on one of its user messages, or on the reply in front of that message.
type continuationChooser struct {
	row      session.Row
	target   session.Provider
	kind     sessionMutation
	messages []session.UserMessage // Every user message, newest first.
	folded   []string              // The messages' text in lower case, for the search.
	total    int                   // Turns a copy of the whole conversation keeps.
	choices  []int                 // Listed rows: wholeConversation or an index into messages.
	index    int                   // Cursor position in choices.
	offset   int                   // First line of the message preview on screen.
	leaveOut bool                  // Stop in front of the chosen message instead of on it.
	search   textinput.Model
	typing   bool // The search field has the keyboard.
	loading  bool
	err      error
}

type continuationLoadedMsg struct {
	chooser  *continuationChooser
	messages []session.UserMessage
	total    int
	err      error
}

func (m model) startContinuation(kind sessionMutation, target session.Provider) (tea.Model, tea.Cmd) {
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, m.list.NewStatusMessage("no session selected")
	}
	m.deleteArmed = ""
	m.pendingConvert = nil
	search := textinput.New()
	search.Prompt = "search: "
	search.Placeholder = "words from a message, or #12"
	search.CharLimit = 200
	c := &continuationChooser{row: selected.row, target: target, kind: kind, search: search, loading: true}
	m.continuation = c
	return m, func() tea.Msg {
		messages, total, err := session.UserMessages(selected.row, 0, 0)
		return continuationLoadedMsg{chooser: c, messages: messages, total: total, err: err}
	}
}

func (c *continuationChooser) loaded(msg continuationLoadedMsg) {
	c.loading = false
	c.messages, c.total, c.err = msg.messages, msg.total, msg.err
	c.folded = make([]string, len(c.messages))
	for index, message := range c.messages {
		c.folded[index] = strings.ToLower(message.Text)
	}
	c.applySearch()
}

// applySearch lists the messages that contain every search word, like the
// session search does, and keeps the cursor on its message while that message
// still matches. A word such as #12 names a message by its number.
func (c *continuationChooser) applySearch() {
	selected := wholeConversation
	if c.index < len(c.choices) {
		selected = c.choices[c.index]
	}
	words := strings.Fields(strings.ToLower(c.search.Value()))
	c.choices = c.choices[:0]
	if len(words) == 0 {
		c.choices = append(c.choices, wholeConversation)
	}
	for index, message := range c.messages {
		matched := true
		for _, word := range words {
			if word != "#"+strconv.Itoa(message.Number) && !strings.Contains(c.folded[index], word) {
				matched = false
				break
			}
		}
		if matched {
			c.choices = append(c.choices, index)
		}
	}
	previous := c.index
	c.index = max(0, slices.Index(c.choices, selected))
	if c.index != previous || len(c.choices) == 0 || c.choices[c.index] != selected {
		c.offset = 0
	}
}

// updateSearch hands a message to the search field and lists the matches again
// when it changed what is typed there.
func (c *continuationChooser) updateSearch(msg tea.Msg, width int) tea.Cmd {
	typed := c.search.Value()
	var cmd tea.Cmd
	c.search, cmd = c.search.Update(msg)
	if c.search.Value() != typed {
		c.applySearch()
	}
	c.fitSearch(width)
	return cmd
}

// fitSearch sizes the search field to what it shows, up to width cells, so the
// match count sits next to the typed words.
func (c *continuationChooser) fitSearch(width int) {
	shown := c.search.Value()
	if shown == "" {
		shown = c.search.Placeholder
	}
	c.search.SetWidth(max(1, min(width, ansi.StringWidth(shown))))
	c.search.SetCursor(c.search.Position()) // Scrolls a long search to its cursor.
}

// cut is the user message the copy ends at, or nil for the whole
// conversation, and whether the copy stops in front of that message. A
// message with nothing before it can only be included.
func (c *continuationChooser) cut() (message *session.UserMessage, leaveOut bool) {
	if c.index >= len(c.choices) || c.choices[c.index] == wholeConversation {
		return nil, false
	}
	message = &c.messages[c.choices[c.index]]
	return message, c.leaveOut && message.Turns > 1
}

// options adds the chosen end to a hand-off scope.
func (c *continuationChooser) options(scope session.HandoffOptions) session.HandoffOptions {
	switch message, leaveOut := c.cut(); {
	case message == nil:
	case leaveOut:
		scope.BeforeUser = message.Number
	default:
		scope.ThroughUser = message.Number
	}
	return scope
}

// keptTurns is how many turns the copy holds for the current choice.
func (c *continuationChooser) keptTurns(maxTurns int) int {
	kept := c.total
	if message, leaveOut := c.cut(); message != nil {
		kept = message.Turns
		if leaveOut {
			kept--
		}
	}
	if maxTurns > 0 {
		kept = min(kept, maxTurns)
	}
	return kept
}

// scope is the hand-off scope the choice is added to. A branch keeps every
// turn up to the chosen end; a hand-off also honors the picker's turn limit.
func (m model) continuationScope() session.HandoffOptions {
	if m.continuation.kind == mutationConvert {
		return m.handoff
	}
	return session.HandoffOptions{}
}

func (m model) updateContinuation(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := m.continuation
	keyStr := msg.String()
	if c.loading || c.err != nil {
		if keyStr == "esc" || keyStr == "q" {
			m.continuation = nil
		}
		return m, nil
	}
	if c.typing {
		switch keyStr {
		case "esc":
			c.typing = false
			c.search.Blur()
			c.search.SetValue("")
			c.applySearch()
			return m, nil
		case "enter", "ctrl+m":
			c.typing = false
			c.search.Blur()
			return m, nil
		case "up", "down", "pgup", "pgdown", "tab":
			// The list stays in reach while the search field has the keyboard.
		default:
			return m, c.updateSearch(msg, m.continuationSearchWidth())
		}
	} else {
		switch keyStr {
		case "esc":
			if c.search.Value() == "" {
				m.continuation = nil
				return m, nil
			}
			c.search.SetValue("")
			c.applySearch()
			return m, nil
		case "q":
			m.continuation = nil
			return m, nil
		case "/":
			c.typing = true
			c.search.CursorEnd()
			c.fitSearch(m.continuationSearchWidth())
			return m, c.search.Focus()
		case "k":
			keyStr = "up"
		case "j":
			keyStr = "down"
		}
	}

	_, listRows, detailRows := m.continuationLayout()
	last := max(0, len(c.choices)-1)
	previous := c.index
	switch keyStr {
	case "up":
		c.index = max(0, c.index-1)
	case "down":
		c.index = min(last, c.index+1)
	case "pgup":
		c.index = max(0, c.index-listRows)
	case "pgdown":
		c.index = min(last, c.index+listRows)
	case "home":
		c.index = 0
	case "end":
		c.index = last
	case "ctrl+d":
		c.offset = min(max(0, len(m.continuationDetail())-detailRows), c.offset+detailRows)
	case "ctrl+u":
		c.offset = max(0, c.offset-detailRows)
	case "tab":
		if message, _ := c.cut(); message != nil && message.Turns > 1 {
			c.leaveOut = !c.leaveOut
		}
	case "enter", "ctrl+m":
		if len(c.choices) == 0 {
			return m, nil
		}
		options := c.options(m.continuationScope())
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

// continuationSearchWidth leaves room on the search line for its prompt and
// the match count.
func (m model) continuationSearchWidth() int {
	return max(4, min(48, m.width-34))
}

// continuationLayout splits the screen: how many header lines sit above the
// list, and how many rows the list and the message preview get. The list keeps
// its height while a search narrows it, so the preview does not jump.
func (m model) continuationLayout() (headerRows, listRows, detailRows int) {
	height := m.height
	if height <= 0 {
		height = 24
	}
	headerRows = 5
	switch {
	case height < 12:
		headerRows = 2 // The title and what the choice keeps.
	case height < 16:
		headerRows = 3 // Plus the source session.
	}
	reserved := headerRows + 4 // The list's summary, the box edges and the footer.
	if m.continuation.kind == mutationConvert && m.handoff.MaxTurns > 0 {
		reserved++
	}
	listRows = max(1, min(len(m.continuation.messages)+1, (height-reserved)/2))
	detailRows = max(1, height-reserved-listRows)
	return headerRows, listRows, detailRows
}

func (m model) continuationDetail() []string {
	c := m.continuation
	text := "Keep the conversation through its latest reply."
	if message, _ := c.cut(); message != nil {
		text = message.Text
	} else if len(c.choices) == 0 {
		text = "No user message contains these words. Change the search, or press esc to clear it."
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	return strings.Split(ansi.Hardwrap(text, max(1, m.width-4), true), "\n")
}

// firstFitting returns the first of forms that is at most width cells wide,
// or the last and shortest one when none is.
func firstFitting(width int, forms ...string) string {
	for _, form := range forms {
		if lipgloss.Width(form) <= width {
			return form
		}
	}
	return forms[len(forms)-1]
}

// continuationSummary says in one line where the new session ends.
func (m model) continuationSummary() string {
	c := m.continuation
	message, leaveOut := c.cut()
	switch {
	case len(c.choices) == 0:
		return "No user message matches the search."
	case message == nil:
		return "The new session keeps the whole conversation."
	case leaveOut:
		return firstFitting(m.width,
			fmt.Sprintf("The new session ends on the reply before user #%d, so you can ask it differently.", message.Number),
			fmt.Sprintf("Ends on the reply before user #%d. Ask it differently.", message.Number),
			fmt.Sprintf("Ends before user #%d.", message.Number))
	case c.leaveOut:
		return firstFitting(m.width,
			fmt.Sprintf("Nothing comes before user #%d, so the new session ends on it.", message.Number),
			fmt.Sprintf("Ends on user #%d: nothing comes before it.", message.Number),
			fmt.Sprintf("Ends on user #%d.", message.Number))
	}
	return firstFitting(m.width,
		fmt.Sprintf("The new session ends on user #%d. Later turns are left out.", message.Number),
		fmt.Sprintf("Ends on user #%d. Later turns are left out.", message.Number),
		fmt.Sprintf("Ends on user #%d.", message.Number))
}

// continuationBoxTitle names the chosen end and what it keeps, in the longest
// form that fits the box edge.
func (m model) continuationBoxTitle() string {
	c := m.continuation
	kept := c.keptTurns(m.continuationScope().MaxTurns)
	message, leaveOut := c.cut()
	var forms []string
	switch {
	case len(c.choices) == 0:
		forms = []string{"No match"}
	case message == nil && kept == c.total:
		forms = []string{fmt.Sprintf("Whole conversation · keeps all %d turns", c.total), "Whole conversation"}
	case message == nil:
		forms = []string{fmt.Sprintf("Whole conversation · keeps %d of %d turns", kept, c.total), "Whole conversation"}
	default:
		state := "included"
		if leaveOut {
			state = "left out"
		}
		forms = []string{
			fmt.Sprintf("User #%d · %s · keeps %d of %d turns", message.Number, state, kept, c.total),
			fmt.Sprintf("User #%d · %s · %d/%d turns", message.Number, state, kept, c.total),
			fmt.Sprintf("#%d · %s", message.Number, state),
		}
	}
	return firstFitting(m.width-5, forms...) // The box edge takes five cells.
}

// continuationListSummary is the line above the list: the search field while
// it has the keyboard, otherwise how many messages are listed.
func (m model) continuationListSummary() string {
	c := m.continuation
	th := m.render.theme
	matches := len(c.choices)
	if c.typing {
		return c.search.View() + th.muted.Render(fmt.Sprintf("  %d of %d match", matches, len(c.messages)))
	}
	if value := strings.TrimSpace(c.search.Value()); value != "" {
		line := fmt.Sprintf("search: %s · %d of %d user messages", session.SafeDisplayText(value), matches, len(c.messages))
		if matches > 0 {
			line += fmt.Sprintf(" · choice %d/%d", c.index+1, matches)
		}
		return th.muted.Render(line)
	}
	noun := "user messages"
	if len(c.messages) == 1 {
		noun = "user message"
	}
	return th.muted.Render(fmt.Sprintf("%d %s · newest first · choice %d/%d", len(c.messages), noun, c.index+1, matches))
}

// continuationFooter lists the keys that act in the current state, most
// important first, and stops where the terminal width does.
func (m model) continuationFooter() string {
	c := m.continuation
	var hints []string
	switch {
	case c.loading || c.err != nil:
		hints = []string{"esc cancel"}
	case c.typing:
		hints = []string{"enter keep search", "esc clear", "↑/↓ choose"}
	default:
		create := "enter create branch"
		if c.kind == mutationConvert {
			create = "enter preview"
		}
		hints = []string{create, "↑/↓ choose"}
		if message, leaveOut := c.cut(); message != nil && message.Turns > 1 {
			if leaveOut {
				hints = append(hints, "tab include message")
			} else {
				hints = append(hints, "tab leave message out")
			}
		}
		cancel := "esc cancel"
		if c.search.Value() != "" {
			cancel = "esc clear search"
		}
		hints = append(hints, "/ search", cancel, "ctrl+d/u scroll")
	}
	footer := hints[0]
	for _, hint := range hints[1:] {
		if m.width > 0 && lipgloss.Width(footer+" · "+hint) > m.width {
			break
		}
		footer += " · " + hint
	}
	return footer
}

func (m model) continuationView() string {
	c := m.continuation
	th := m.render.theme
	title := "Branch · choose where the copy picks up"
	if c.kind == mutationConvert {
		title = "Hand off to " + session.DisplayName(c.target) + " · choose where it picks up"
	}
	source := m.providerWord(c.row.Provider) + " " + session.SafeDisplayText(c.row.ID) +
		th.muted.Render(" · "+collapseHome(session.SafeDisplayText(c.row.CWD)))
	const unchanged = "The original session and project files stay as they are."

	var lines []string
	switch {
	case c.loading:
		lines = []string{th.title.Render(title), source, unchanged, "", "Reading the session's user messages…"}
	case c.err != nil:
		lines = []string{th.title.Render(title), source, unchanged, "",
			th.deleteBanner.Render("Cannot read messages: " + session.SafeDisplayText(c.err.Error()))}
	default:
		headerRows, listRows, detailRows := m.continuationLayout()
		lines = []string{th.title.Render(title)}
		if headerRows >= 3 {
			lines = append(lines, source)
		}
		lines = append(lines, m.continuationSummary())
		if headerRows >= 5 {
			lines = append(lines, unchanged, "")
		}

		lines = append(lines, m.continuationListSummary())

		// Rows above the cursor are newer than the chosen end: the copy leaves
		// them out, so they are dimmed.
		_, leaveOut := c.cut()
		start := min(max(0, c.index-listRows/2), max(0, len(c.choices)-listRows))
		for position := start; position < start+listRows; position++ {
			if position >= len(c.choices) {
				lines = append(lines, "")
				continue
			}
			label := "Continue from the end"
			if choice := c.choices[position]; choice != wholeConversation {
				message := c.messages[choice]
				text := session.SafeDisplayText(message.Text)
				if position == c.index && leaveOut {
					text = "left out · " + text
				}
				label = fmt.Sprintf("#%d  %s", message.Number, text)
			}
			prefix, style := "  ", th.message
			switch {
			case position == c.index:
				prefix, style = "❯ ", th.selected
			case position < c.index:
				style = th.muted
			}
			lines = append(lines, style.Render(truncateCells(prefix+label, max(1, m.width))))
		}

		detail := m.continuationDetail()
		offset := min(c.offset, max(0, len(detail)-detailRows))
		position := ""
		if len(detail) > detailRows {
			position = th.muted.Render(fmt.Sprintf("lines %d–%d/%d", offset+1, min(len(detail), offset+detailRows), len(detail)))
		}
		body := strings.Join(detail[offset:min(len(detail), offset+detailRows)], "\n")
		lines = append(lines, titledBox(th.detailEdge, th.detailEdge, m.continuationBoxTitle(), position, body, max(4, m.width)))
		if c.kind == mutationConvert && m.handoff.MaxTurns > 0 {
			lines = append(lines, fmt.Sprintf("Scope: last %d turns ending at this point.", m.handoff.MaxTurns))
		}
	}
	content := strings.Join(lines, "\n")
	if m.height > 0 {
		content = clampLines(content, m.height-1)
	}
	return clampLineWidths(content+"\n"+th.hint.Render(m.continuationFooter()), m.width)
}
