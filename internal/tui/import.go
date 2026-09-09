package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"

	"github.com/aytzey/showagent/internal/importer"
	"github.com/aytzey/showagent/internal/session"
)

// Function variables keep TUI tests hermetic: link parsing and native writes
// are exercised through messages without reaching the network or real stores.
var (
	parseImportText = importer.ParseText
	fetchImportLink = func(ctx context.Context, value string) (importer.Conversation, error) {
		return importer.ImportURL(ctx, value, importer.FetchOptions{})
	}
	planSessionImport   = session.PlanImport
	applySessionImport  = session.ApplyImport
	importWorkingDir    = os.Getwd
	readImportClipboard = clipboard.ReadAll
)

type importStep int

const (
	importChooseSource importStep = iota
	importEnterSource
	importPreview
	importDestination
	importCreateDestination
	importExistingDestination
	importReview
	importSuccess
)

type importSource int

const (
	importShareLink importSource = iota
	importPastedText
)

type importDestinationKind int

const (
	importCreateNew importDestinationKind = iota
	importAppendExisting
)

type importParsedMsg struct {
	conversation importer.Conversation
	err          error
}

type importPlannedMsg struct {
	plan session.ImportPlan
	err  error
}

type importAppliedMsg struct {
	receipt session.ImportReceipt
	err     error
}

type importClipboardMsg struct {
	text string
	err  error
}

type importWizard struct {
	step          importStep
	source        importSource
	destination   importDestinationKind
	linkInput     textinput.Model
	pasteInput    textarea.Model
	pasteOriginal *string
	pasteHint     string
	cwdInput      textinput.Model
	reviewFocused bool
	applyFocused  bool
	asNote        bool
	conversation  importer.Conversation
	providerIndex int
	existingIndex int
	plan          session.ImportPlan
	receipt       session.ImportReceipt
	busy          string
	notice        string
}

func newImportWizard() *importWizard {
	link := textinput.New()
	link.Prompt = "> "
	link.Placeholder = "https://chatgpt.com/share/... or https://claude.ai/share/..."
	link.CharLimit = 512
	link.SetWidth(64)

	paste := textarea.New()
	paste.Placeholder = "User:\nPaste the question here.\n\nAssistant:\nPaste the answer here."
	paste.ShowLineNumbers = false
	paste.MaxHeight = 0
	paste.Prompt = "│ "
	paste.SetWidth(64)
	paste.SetHeight(9)

	cwd := textinput.New()
	cwd.Prompt = "> "
	cwd.Placeholder = "absolute project folder"
	cwd.SetWidth(64)
	if value, err := importWorkingDir(); err == nil {
		cwd.SetValue(value)
	}

	return &importWizard{
		step:       importChooseSource,
		linkInput:  link,
		pasteInput: paste,
		cwdInput:   cwd,
	}
}

func (m model) openImport() (tea.Model, tea.Cmd) {
	m.importFlow = newImportWizard()
	m.help.ShowAll = false
	return m, nil
}

func (m model) updateImport(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	w := m.importFlow
	if w == nil {
		return m, nil
	}
	if w.busy != "" {
		return m, nil
	}
	if msg.String() == "esc" {
		m.importFlow = nil
		return m, nil
	}

	switch w.step {
	case importChooseSource:
		switch msg.String() {
		case "up", "down", "j", "k", "tab":
			if w.source == importShareLink {
				w.source = importPastedText
			} else {
				w.source = importShareLink
			}
		case "enter":
			w.step = importEnterSource
			w.notice = ""
			if w.source == importShareLink {
				return m, w.linkInput.Focus()
			}
			return m, w.pasteInput.Focus()
		}
	case importEnterSource:
		if w.reviewFocused {
			switch msg.String() {
			case "enter":
				w.reviewFocused = false
				return m.startImportParse()
			case "tab", "shift+tab":
				w.reviewFocused = false
				return m, w.focusSourceInput()
			}
			return m, nil
		}
		if msg.String() == "f2" && w.source == importShareLink {
			w.source = importPastedText
			w.notice = "Paste the conversation with explicit User:/Assistant: labels."
			w.linkInput.Blur()
			return m, w.pasteInput.Focus()
		}
		if msg.String() == "ctrl+r" && w.source == importPastedText {
			w.asNote = !w.asNote
			w.notice = ""
			return m, nil
		}
		if msg.String() == "tab" {
			w.reviewFocused = true
			w.linkInput.Blur()
			w.pasteInput.Blur()
			return m, nil
		}
		if isImportReviewKey(msg) {
			return m.startImportParse()
		}
		if w.source == importPastedText && (msg.String() == "ctrl+v" || msg.String() == "shift+insert") {
			return m, func() tea.Msg {
				text, err := readImportClipboard()
				return importClipboardMsg{text: text, err: err}
			}
		}
		return m.updateImportInput(msg)
	case importPreview:
		switch msg.String() {
		case "enter":
			w.step = importDestination
			w.notice = ""
		case "b":
			w.step = importEnterSource
			w.reviewFocused = false
			w.notice = ""
			return m, w.focusSourceInput()
		}
	case importDestination:
		switch msg.String() {
		case "up", "down", "j", "k", "tab":
			if w.destination == importCreateNew {
				w.destination = importAppendExisting
			} else {
				w.destination = importCreateNew
			}
		case "enter":
			w.notice = ""
			if w.destination == importCreateNew {
				w.step = importCreateDestination
				return m, w.cwdInput.Focus()
			}
			w.step = importExistingDestination
			w.existingIndex = 0
		}
	case importCreateDestination:
		switch msg.String() {
		case "tab":
			w.providerIndex = nextImportProviderIndex(w.providerIndex)
			w.notice = ""
			return m, nil
		case "enter", "ctrl+enter":
			return m.startImportPlan(session.ImportRequest{
				Mode:     session.ImportCreate,
				Provider: w.selectedProvider(),
				CWD:      w.cwdInput.Value(),
				Messages: importMessages(w.conversation),
			})
		case "alt+left":
			w.cwdInput.Blur()
			w.step = importDestination
			return m, nil
		}
		var cmd tea.Cmd
		w.cwdInput, cmd = w.cwdInput.Update(msg)
		return m, cmd
	case importExistingDestination:
		switch msg.String() {
		case "up", "k":
			w.moveExisting(-1, len(m.allRows))
			w.notice = ""
		case "down", "j":
			w.moveExisting(1, len(m.allRows))
			w.notice = ""
		case "b":
			w.step = importDestination
			w.notice = ""
		case "enter":
			if len(m.allRows) == 0 {
				w.notice = "No existing session is available. Choose Create a new session."
				return m, nil
			}
			row := m.allRows[w.existingIndex]
			capability := importAppendCapability(row)
			if !capability.CanAppendContext {
				w.notice = capability.Reason
				return m, nil
			}
			return m.startImportPlan(session.ImportRequest{
				Mode:     session.ImportAppendContext,
				Target:   row,
				Messages: importMessages(w.conversation),
			})
		}
	case importReview:
		switch msg.String() {
		case "tab", "shift+tab":
			w.applyFocused = !w.applyFocused
		case "enter":
			if w.applyFocused {
				return m.startImportApply()
			}
		case "b":
			w.plan = session.ImportPlan{}
			w.step = importDestination
			w.notice = ""
		case "ctrl+enter":
			return m.startImportApply()
		}
	case importSuccess:
		if msg.String() == "enter" {
			m.importFlow = nil
			return m, nil
		}
	}
	return m, nil
}

func (m model) startImportApply() (tea.Model, tea.Cmd) {
	w := m.importFlow
	w.busy = "Applying import"
	w.notice = ""
	plan := w.plan
	return m, func() tea.Msg {
		receipt, err := applySessionImport(context.Background(), plan)
		return importAppliedMsg{receipt: receipt, err: err}
	}
}

func (w *importWizard) focusSourceInput() tea.Cmd {
	if w.source == importShareLink {
		return w.linkInput.Focus()
	}
	return w.pasteInput.Focus()
}

func (m model) updateImportInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	w := m.importFlow
	if w == nil || w.busy != "" || w.reviewFocused {
		return m, nil
	}
	var cmd tea.Cmd
	switch w.step {
	case importEnterSource:
		if w.source == importShareLink {
			w.linkInput, cmd = w.linkInput.Update(msg)
		} else {
			switch pasted := msg.(type) {
			case tea.PasteMsg:
				w.setPastedConversation(pasted.Content)
				return m, nil
			case importClipboardMsg:
				if pasted.err != nil {
					w.notice = "Clipboard could not be read. Use your terminal's paste action instead."
				} else {
					w.setPastedConversation(pasted.text)
				}
				return m, nil
			case tea.KeyPressMsg:
				if w.pasteOriginal != nil {
					switch pasted.String() {
					case "up", "down", "left", "right", "home", "end", "pgup", "pgdown":
					default:
						w.notice = "Original formatting is preserved. Edit the source and paste the complete conversation again."
						return m, nil
					}
				}
			}
			w.pasteInput, cmd = w.pasteInput.Update(msg)
		}
	case importCreateDestination:
		w.cwdInput, cmd = w.cwdInput.Update(msg)
	}
	return m, cmd
}

// Keep the original whenever the editor would expand tabs, remove characters,
// or truncate a large paste. A new paste explicitly replaces the source.
func (w *importWizard) setPastedConversation(text string) {
	if !utf8.ValidString(text) {
		w.notice = "Pasted text is not valid UTF-8. The previous source is unchanged."
		return
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if len(text) > importer.MaxTextBytes {
		w.notice = "Pasted text exceeds the 10 MiB limit. The previous source is unchanged."
		return
	}
	const previewBytes = 8192
	display := text
	if len(display) > previewBytes {
		end := previewBytes
		for !utf8.RuneStart(display[end]) {
			end--
		}
		display = display[:end]
	}
	w.pasteInput.SetValue(display)
	w.pasteOriginal = nil
	w.pasteHint = ""
	w.notice = ""
	if w.pasteInput.Value() != text {
		w.pasteOriginal = &text
		w.pasteHint = "Original formatting preserved. To edit, paste the revised conversation."
		if len(display) < len(text) {
			w.pasteHint = fmt.Sprintf("Showing the first 8 KiB; all %d bytes are kept. Paste again to replace.", len(text))
		}
	}
}

func (m model) startImportParse() (tea.Model, tea.Cmd) {
	w := m.importFlow
	if w == nil {
		return m, nil
	}
	w.busy = "Reviewing conversation"
	w.notice = ""
	source := w.source
	link := strings.TrimSpace(w.linkInput.Value())
	paste := w.pasteInput.Value()
	if w.pasteOriginal != nil {
		paste = *w.pasteOriginal
	}
	asNote := w.asNote
	return m, func() tea.Msg {
		var conversation importer.Conversation
		var err error
		if source == importShareLink {
			conversation, err = fetchImportLink(context.Background(), link)
		} else {
			conversation, err = parseImportText(paste, importer.TextOptions{AsOneContextNote: asNote})
		}
		return importParsedMsg{conversation: conversation, err: err}
	}
}

func (m model) applyImportParsed(msg importParsedMsg) (tea.Model, tea.Cmd) {
	w := m.importFlow
	if w == nil {
		return m, nil
	}
	w.busy = ""
	if msg.err != nil {
		w.notice = importParseError(w.source, msg.err)
		return m, nil
	}
	w.conversation = msg.conversation
	w.notice = ""
	w.linkInput.Blur()
	w.pasteInput.Blur()
	w.step = importPreview
	return m, nil
}

func (m model) startImportPlan(request session.ImportRequest) (tea.Model, tea.Cmd) {
	w := m.importFlow
	if w == nil {
		return m, nil
	}
	w.busy = "Checking destination"
	w.notice = ""
	return m, func() tea.Msg {
		plan, err := planSessionImport(request)
		return importPlannedMsg{plan: plan, err: err}
	}
}

func (m model) applyImportPlanned(msg importPlannedMsg) (tea.Model, tea.Cmd) {
	w := m.importFlow
	if w == nil {
		return m, nil
	}
	w.busy = ""
	if msg.err != nil {
		w.notice = "Destination cannot be used: " + session.SafeDisplayText(msg.err.Error())
		return m, nil
	}
	w.plan = msg.plan
	w.applyFocused = false
	w.notice = ""
	w.cwdInput.Blur()
	w.step = importReview
	return m, nil
}

func (m model) applyImportApplied(msg importAppliedMsg) (tea.Model, tea.Cmd) {
	w := m.importFlow
	if w == nil {
		return m, nil
	}
	w.busy = ""
	if msg.err != nil {
		w.plan = session.ImportPlan{}
		w.step = importDestination
		w.notice = "Import failed: " + session.SafeDisplayText(msg.err.Error()) + ". Review the destination and try again."
		return m, nil
	}
	w.receipt = msg.receipt
	w.step = importSuccess
	w.notice = ""

	row := msg.receipt.Row
	if row.ID != "" {
		m.providers[row.Provider] = true
		delete(m.collapsedGroups, row.CWD)
		m.allRows = upsertAndSortRows(m.allRows, row)
		m.list.ResetFilter()
		cmd := m.list.SetItems(m.currentItems())
		selectRowItem(&m.list, row)
		return m, cmd
	}
	return m, nil
}

func isImportReviewKey(msg tea.KeyPressMsg) bool {
	return msg.String() == "ctrl+enter"
}

func nextImportProviderIndex(current int) int {
	providers := creatableImportProviders()
	if len(providers) == 0 {
		return 0
	}
	return (current + 1) % len(providers)
}

func creatableImportProviders() []session.Provider {
	providers := make([]session.Provider, 0, len(session.ProviderOrder()))
	for _, provider := range session.ProviderOrder() {
		if session.ImportCapabilities(provider).CanCreate {
			providers = append(providers, provider)
		}
	}
	return providers
}

func (w *importWizard) selectedProvider() session.Provider {
	providers := creatableImportProviders()
	if len(providers) == 0 {
		return ""
	}
	if w.providerIndex < 0 || w.providerIndex >= len(providers) {
		w.providerIndex = 0
	}
	return providers[w.providerIndex]
}

func (w *importWizard) moveExisting(delta, count int) {
	if count <= 0 {
		w.existingIndex = 0
		return
	}
	w.existingIndex = (w.existingIndex + delta + count) % count
}

func importMessages(conversation importer.Conversation) []session.ImportMessage {
	messages := make([]session.ImportMessage, 0, len(conversation.Messages))
	for _, message := range conversation.Messages {
		messages = append(messages, session.ImportMessage{Role: string(message.Role), Text: message.Text})
	}
	return messages
}

func importParseError(source importSource, err error) string {
	var ambiguity *importer.AmbiguityError
	if errors.As(err, &ambiguity) {
		return "Some text has no speaker. Add explicit User:/Assistant: labels, or press ctrl+r to import as one context note."
	}
	if source == importShareLink {
		if errors.Is(err, importer.ErrUnsupportedURL) {
			return "This is not a supported conversation share link. Press F2 to paste the conversation text instead."
		}
		if errors.Is(err, importer.ErrConversationUnidentifiable) {
			return "The page opened, but its conversation could not be identified. Press F2 to paste the text instead."
		}
		return "This link could not be read. It may need access or be unavailable. Press F2 to paste the text you can access."
	}
	return "Conversation could not be reviewed: " + session.SafeDisplayText(err.Error())
}

func (m model) importView() string {
	w := m.importFlow
	if w == nil {
		return ""
	}
	var lines []string
	switch w.step {
	case importChooseSource:
		lines = m.importSourceView()
	case importEnterSource:
		lines = m.importInputView()
	case importPreview:
		lines = m.importPreviewView()
	case importDestination:
		lines = m.importDestinationView()
	case importCreateDestination:
		lines = m.importCreateView()
	case importExistingDestination:
		lines = m.importExistingView()
	case importReview:
		lines = m.importReviewView()
	case importSuccess:
		lines = m.importSuccessView()
	}
	if w.busy != "" {
		lines = append(lines, "", m.render.theme.muted.Render(m.spinner.View()+" "+w.busy+"…"))
	}
	if w.notice != "" {
		lines = append(lines, "", m.render.theme.deleteBanner.Render(w.notice))
	}
	return m.centerImportBox(lines)
}

func (m model) importSourceView() []string {
	w := m.importFlow
	return []string{
		m.render.theme.title.Render("Import conversation"),
		"",
		m.importOption(w.source == importShareLink, "Share link", "Read an accessible ChatGPT or Claude share link."),
		"",
		m.importOption(w.source == importPastedText, "Paste text", "Process the conversation locally without publishing it."),
		"",
		m.render.theme.hint.Render("↑/↓ choose · enter continue · esc cancel"),
	}
}

func (m model) importInputView() []string {
	w := m.importFlow
	lines := []string{m.render.theme.title.Render("Import conversation · Source"), ""}
	review := m.importOption(w.reviewFocused, "Review conversation", "Parse and preview the text before choosing a destination.")
	if w.source == importShareLink {
		lines = append(lines,
			m.render.theme.workspace.Render("ChatGPT or Claude share link"),
			w.linkInput.View(),
			m.render.theme.muted.Render("Prefer pasted text if you do not want to share the conversation publicly."),
			"",
			review,
			"",
			m.render.theme.hint.Render("tab focus Review · ctrl+enter review · F2 paste fallback · esc cancel"),
		)
		return lines
	}
	note := "off"
	if w.asNote {
		note = "on · imported as one user context message"
	}
	editing := "Use explicit User:/Assistant: labels. Plain Enter adds a newline."
	if w.pasteOriginal != nil {
		editing = "Use explicit User:/Assistant: labels in the source, or import as one context note."
	}
	return append(lines,
		m.render.theme.workspace.Render("Pasted conversation"),
		w.pasteInput.View(),
		m.render.theme.muted.Render(editing),
		m.render.theme.muted.Render("Pasting replaces the conversation. "+w.pasteHint),
		m.render.theme.muted.Render("Import as one context note: "+note),
		"",
		review,
		"",
		m.render.theme.hint.Render("tab focus Review · ctrl+enter review · ctrl+r toggle one context note · esc cancel"),
	)
}

func (m model) importPreviewView() []string {
	w := m.importFlow
	first, last := importBoundaries(w.conversation)
	lines := []string{
		m.render.theme.title.Render("Import conversation · Preview"),
		"",
		m.importField("source", importSourceLabel(w.conversation.SourceKind)),
		m.importField("messages", fmt.Sprintf("%d · text only", len(w.conversation.Messages))),
	}
	if w.conversation.Title != "" {
		lines = append(lines, m.importField("title", session.SafeDisplayText(session.RedactTranscriptText(w.conversation.Title))))
	}
	lines = append(lines, m.importSourceWarnings()...)
	lines = append(lines,
		m.importField("first", first),
		m.importField("last", last),
		"",
		m.render.theme.muted.Render("Attachments, tool state, permissions, and files are not imported."),
		m.render.theme.hint.Render("enter choose destination · b edit source · esc cancel"),
	)
	return lines
}

func (m model) importDestinationView() []string {
	w := m.importFlow
	return []string{
		m.render.theme.title.Render("Import conversation · Destination"),
		"",
		m.importOption(w.destination == importCreateNew, "Create a new session", "Choose an agent and an absolute project folder."),
		"",
		m.importOption(w.destination == importAppendExisting, "Add context to an existing session", "Choose one exact session row; support is provider-specific."),
		"",
		m.render.theme.hint.Render("↑/↓ choose · enter continue · esc cancel"),
	}
}

func (m model) importSourceWarnings() []string {
	conversation := m.importFlow.conversation
	lines := []string{m.importField("scope", string(conversation.Completeness))}
	for _, warning := range conversation.Warnings {
		lines = append(lines, m.importField("omitted", warning.Summary()))
	}
	return lines
}

func (m model) importCreateView() []string {
	w := m.importFlow
	provider := w.selectedProvider()
	lines := []string{
		m.render.theme.title.Render("Import conversation · New session"),
		"",
		m.importField("agent", session.DisplayName(provider)),
		m.render.theme.muted.Render("tab cycles the target agent"),
		"",
		m.render.theme.workspace.Render("Project folder"),
		w.cwdInput.View(),
		m.render.theme.muted.Render("The agent works here; conversation data stays in its native session storage."),
	}
	if provider == session.ProviderOpenCode && !session.ProviderCommandAvailable(provider) {
		lines = append(lines, m.render.theme.muted.Render("Creating this provider's session requires its CLI in PATH."))
	}
	return append(lines, "", m.render.theme.hint.Render("enter review destination · tab agent · alt+← back · esc cancel"))
}

func (m model) importExistingView() []string {
	w := m.importFlow
	lines := []string{m.render.theme.title.Render("Import conversation · Existing session"), ""}
	if len(m.allRows) == 0 {
		return append(lines,
			m.render.theme.muted.Render("No existing sessions are available."),
			"",
			m.render.theme.hint.Render("b choose Create a new session · esc cancel"),
		)
	}
	start := max(0, w.existingIndex-2)
	end := min(len(m.allRows), start+5)
	for index := start; index < end; index++ {
		row := m.allRows[index]
		label := fmt.Sprintf("%s · %s · %s", session.DisplayName(row.Provider), session.SafeDisplayText(row.ID), collapseHome(session.SafeDisplayText(row.CWD)))
		lines = append(lines, m.importOption(index == w.existingIndex, label, importRowPreview(row)))
	}
	selected := m.allRows[w.existingIndex]
	capability := importAppendCapability(selected)
	lines = append(lines, "", m.importField("exact id", session.SafeDisplayText(selected.ID)))
	if capability.CanAppendContext {
		lines = append(lines, m.render.theme.muted.Render("Messages are added to this session's context; they may not appear as chat bubbles."))
	} else {
		lines = append(lines, m.render.theme.deleteBanner.Render(capability.Reason))
	}
	return append(lines, "", m.render.theme.hint.Render("↑/↓ exact row · enter review · b back · esc cancel"))
}

func (m model) importReviewView() []string {
	w := m.importFlow
	plan := w.plan
	operation := "Create a new " + session.DisplayName(plan.Provider) + " session"
	id := "new ID"
	if plan.Mode == session.ImportAppendContext {
		operation = fmt.Sprintf("Add %d messages to this session's context", plan.MessageCount)
		id = session.SafeDisplayText(plan.Target.ID) + " (preserved)"
	}
	lines := []string{
		m.render.theme.title.Render("Import conversation · Final review"),
		"",
		m.importField("source", importSourceLabel(w.conversation.SourceKind)),
		m.importField("operation", operation),
		m.importField("agent", session.DisplayName(plan.Provider)),
		m.importField("project", collapseHome(session.SafeDisplayText(plan.CWD))),
		m.importField("session", id),
		m.importField("content", fmt.Sprintf("%d messages · text only", plan.MessageCount)),
		"",
		m.render.theme.muted.Render("This saves history without launching an interactive agent or starting a model response."),
	}
	if plan.Mode == session.ImportAppendContext {
		lines = append(lines, m.render.theme.muted.Render("Close other clients using this session before importing."))
	}
	if plan.Mode == session.ImportAppendContext && !plan.Capability.NativeHistoryVisible {
		lines = append(lines, m.render.theme.muted.Render("Imported context may not appear as chat bubbles in the target agent."))
	}
	lines = append(lines, m.importSourceWarnings()...)
	return append(lines, "",
		m.importOption(w.applyFocused, "Apply import", "Save the reviewed conversation to this destination."),
		"", m.render.theme.hint.Render("tab focus Apply · enter activate · ctrl+enter apply · b back · esc cancel"))
}

func (m model) importSuccessView() []string {
	receipt := m.importFlow.receipt
	verb := "Created"
	idNote := "new ID"
	if receipt.NoOp {
		verb = "Already imported into"
		idNote = "existing receipt"
	}
	if receipt.Mode == session.ImportAppendContext {
		verb = "Added context to"
		idNote = "preserved ID"
		if receipt.NoOp {
			verb = "Context already present in"
			idNote = "preserved ID"
		}
	}
	lines := []string{
		m.render.theme.title.Render("Import complete"),
		"",
		m.render.theme.workspace.Render(fmt.Sprintf("%s %s session", verb, session.DisplayName(receipt.Provider))),
		m.importField("session", session.SafeDisplayText(receipt.Row.ID)+" ("+idNote+")"),
		m.importField("project", collapseHome(session.SafeDisplayText(receipt.Row.CWD))),
		m.importField("content", fmt.Sprintf("%d messages", receipt.MessageCount)),
	}
	if receipt.Mode == session.ImportAppendContext && !receipt.NativeHistoryVisible {
		lines = append(lines, "", m.render.theme.muted.Render("Imported context may not appear as chat bubbles in the target agent."))
	}
	return append(lines,
		"",
		m.render.theme.muted.Render("Saved locally. Resume from the session list when ready."),
		m.render.theme.hint.Render("enter close · esc close"),
	)
}

func (m model) importOption(selected bool, label, description string) string {
	cursor := "  "
	style := m.render.theme.muted
	if selected {
		cursor = "❯ "
		style = m.render.theme.workspace
	}
	return m.render.theme.cursor.Render(cursor) + style.Render(label) + "\n  " + m.render.theme.muted.Render(description)
}

func (m model) importField(label, value string) string {
	return m.render.theme.label.Render(padLabel(label)) + value
}

func importSourceLabel(kind importer.SourceKind) string {
	switch kind {
	case importer.SourceChatGPTShare:
		return "ChatGPT share link"
	case importer.SourceClaudeShare:
		return "Claude share link"
	case importer.SourcePastedText:
		return "pasted text"
	default:
		return "conversation text"
	}
}

func importBoundaries(conversation importer.Conversation) (string, string) {
	if len(conversation.Messages) == 0 {
		return "(none)", "(none)"
	}
	boundary := func(message importer.Message) string {
		text := session.SafeDisplayText(session.RedactTranscriptText(message.Text))
		return fmt.Sprintf("%s: %s", message.Role, truncateCells(text, 52))
	}
	return boundary(conversation.Messages[0]), boundary(conversation.Messages[len(conversation.Messages)-1])
}

func importRowPreview(row session.Row) string {
	preview := strings.Join(strings.Fields(session.SafeDisplayText(bestLast(row))), " ")
	return truncateCells(preview, 60)
}

func importAppendCapability(row session.Row) session.ImportCapability {
	capability := session.ImportCapabilities(row.Provider)
	if row.HandoffOnly {
		capability.CanAppendContext = false
		capability.Reason = "This row is a handoff-only copy, not a native session that Codex can update."
	}
	return capability
}

func (m model) centerImportBox(lines []string) string {
	boxWidth := 72
	if m.width > 0 {
		boxWidth = max(1, min(m.width, boxWidth))
	}
	frameW, _ := m.render.theme.detail.GetFrameSize()
	innerWidth := max(1, boxWidth-frameW)
	for index := range lines {
		lines[index] = clampLineWidths(lines[index], innerWidth)
	}
	box := m.render.theme.detail.Width(boxWidth).Render(strings.Join(lines, "\n"))
	if m.width <= 0 || m.height <= 0 {
		return box
	}
	return clampLineWidths(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box), m.width)
}
