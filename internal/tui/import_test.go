package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/aytzey/showagent/internal/importer"
	"github.com/aytzey/showagent/internal/session"
)

func TestEmptyViewOffersConversationImport(t *testing.T) {
	view := sizedModel(nil).emptyView()
	if !strings.Contains(view, "i") || !strings.Contains(view, "Import conversation") {
		t.Fatalf("empty view does not offer import:\n%s", view)
	}
}

func TestImportKeyOpensModalWithZeroRowsAndIsolatesListKeys(t *testing.T) {
	m := sizedModel(nil)
	opened, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'i'}))
	modal := asModel(t, opened)
	if view := modal.View().Content; !strings.Contains(view, "Import conversation") || !strings.Contains(view, "Share link") {
		t.Fatalf("i did not open the import modal:\n%s", view)
	}

	// Slash belongs to the modal while it is open; it must not activate the
	// session list's search field behind the overlay.
	afterSlash, _ := modal.Update(tea.KeyPressMsg(tea.Key{Code: '/'}))
	isolated := asModel(t, afterSlash)
	if isolated.list.SettingFilter() {
		t.Fatal("list search activated behind import modal")
	}
	if view := isolated.View().Content; !strings.Contains(view, "Import conversation") {
		t.Fatalf("modal closed after unrelated list key:\n%s", view)
	}
}

func TestHelpIncludesConversationImport(t *testing.T) {
	view := sizedModel(nil).helpView()
	if !strings.Contains(view, "import") {
		t.Fatalf("help does not include import action:\n%s", view)
	}
}

func TestPastedTextKeepsNewlinesAndCtrlEnterReviews(t *testing.T) {
	previous := parseImportText
	t.Cleanup(func() { parseImportText = previous })

	var parsed string
	parseImportText = func(value string, options importer.TextOptions) (importer.Conversation, error) {
		parsed = value
		return importer.Conversation{
			SourceKind: importer.SourcePastedText,
			Messages: []importer.Message{
				{Role: importer.RoleUser, Text: "question"},
				{Role: importer.RoleAssistant, Text: "answer"},
			},
		}, nil
	}

	m := openPasteImport(t, sizedModel(nil))
	want := "User:\nfirst line\nsecond line\n\nAssistant:\nanswer"
	m.importFlow.pasteInput.SetValue(want)

	// Ordinary Enter is owned by the textarea and inserts a newline.
	entered, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = asModel(t, entered)
	if !strings.Contains(m.importFlow.pasteInput.Value(), "\n") {
		t.Fatalf("textarea lost multiline input: %q", m.importFlow.pasteInput.Value())
	}
	if m.importFlow.step != importEnterSource {
		t.Fatalf("plain Enter advanced from textarea: step=%v", m.importFlow.step)
	}

	reviewing, cmd := m.Update(ctrlEnter())
	m = asModel(t, reviewing)
	if cmd == nil || m.importFlow.busy == "" {
		t.Fatal("Ctrl+Enter did not start asynchronous parsing")
	}
	parsedModel, _ := m.Update(cmd())
	m = asModel(t, parsedModel)
	if m.importFlow.step != importPreview || parsed != m.importFlow.pasteInput.Value() {
		t.Fatalf("parse result/input mismatch: step=%v parsed=%q input=%q", m.importFlow.step, parsed, m.importFlow.pasteInput.Value())
	}
}

func TestPastedTextTabFocusesVisibleReviewAction(t *testing.T) {
	previous := parseImportText
	t.Cleanup(func() { parseImportText = previous })
	parseImportText = func(value string, options importer.TextOptions) (importer.Conversation, error) {
		return importer.Conversation{
			SourceKind: importer.SourcePastedText,
			Messages:   []importer.Message{{Role: importer.RoleUser, Text: value}},
		}, nil
	}

	m := openPasteImport(t, sizedModel(nil))
	m.importFlow.pasteInput.SetValue("User:\nportable review")

	focused, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = asModel(t, focused)
	if !m.importFlow.reviewFocused || !strings.Contains(m.View().Content, "Review conversation") {
		t.Fatalf("Tab did not focus the visible Review action:\n%s", m.View().Content)
	}

	reviewing, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = asModel(t, reviewing)
	if cmd == nil || m.importFlow.busy == "" {
		t.Fatal("Enter on the focused Review action did not start parsing")
	}
	parsed, _ := m.Update(cmd())
	m = asModel(t, parsed)
	if m.importFlow.step != importPreview {
		t.Fatalf("review action ended at step %v, want preview", m.importFlow.step)
	}
}

func TestAmbiguousPasteRequiresLabelsOrVisibleOneNoteToggle(t *testing.T) {
	previous := parseImportText
	t.Cleanup(func() { parseImportText = previous })

	parseImportText = func(value string, options importer.TextOptions) (importer.Conversation, error) {
		if options.AsOneContextNote {
			return importer.Conversation{SourceKind: importer.SourcePastedText, Messages: []importer.Message{{Role: importer.RoleUser, Text: value}}}, nil
		}
		conversation := importer.Conversation{SourceKind: importer.SourcePastedText, Messages: []importer.Message{{Role: importer.RoleUnassigned, Text: value}}}
		return conversation, &importer.AmbiguityError{UnassignedBlocks: 1, Conversation: conversation}
	}

	m := openPasteImport(t, sizedModel(nil))
	m.importFlow.pasteInput.SetValue("unlabelled historical context")
	started, cmd := m.Update(ctrlEnter())
	m = asModel(t, started)
	failed, _ := m.Update(cmd())
	m = asModel(t, failed)
	view := m.importView()
	for _, want := range []string{"User:/Assistant:", "one context note", "ctrl+r"} {
		if !strings.Contains(view, want) {
			t.Fatalf("ambiguity help missing %q:\n%s", want, view)
		}
	}

	toggled, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = asModel(t, toggled)
	if !m.importFlow.asNote {
		t.Fatal("one-context-note toggle did not turn on")
	}
	started, cmd = m.Update(ctrlEnter())
	m = asModel(t, started)
	previewed, _ := m.Update(cmd())
	m = asModel(t, previewed)
	if m.importFlow.step != importPreview || len(m.importFlow.conversation.Messages) != 1 {
		t.Fatalf("as-note parse did not reach one-message preview: %#v", m.importFlow.conversation)
	}
}

func TestExistingDestinationShowsCapabilityReasonAndBlocksUnsupportedAppend(t *testing.T) {
	previous := planSessionImport
	t.Cleanup(func() { planSessionImport = previous })
	plans := 0
	planSessionImport = func(request session.ImportRequest) (session.ImportPlan, error) {
		plans++
		return session.ImportPlan{}, errors.New("must not be called")
	}

	row := session.Row{Provider: session.ProviderClaude, ID: "exact-claude-id", CWD: t.TempDir()}
	m := sizedModel([]session.Row{row})
	m.importFlow = previewWizard()

	destination, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = asModel(t, destination)
	chooseExisting, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = asModel(t, chooseExisting)
	existing, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = asModel(t, existing)
	blocked, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = asModel(t, blocked)
	if cmd != nil || plans != 0 {
		t.Fatalf("unsupported append reached PlanImport: cmd=%v plans=%d", cmd, plans)
	}
	view := m.importView()
	if !strings.Contains(view, "exact-claude-id") || !strings.Contains(view, "no verified native append-context API") {
		t.Fatalf("existing target/capability reason missing:\n%s", view)
	}
}

func TestImportApplySuccessAndErrorStates(t *testing.T) {
	previous := applySessionImport
	t.Cleanup(func() { applySessionImport = previous })

	t.Run("success keeps receipt id and never resumes", func(t *testing.T) {
		applySessionImport = func(_ context.Context, _ session.ImportPlan) (session.ImportReceipt, error) {
			return session.ImportReceipt{
				Mode:         session.ImportAppendContext,
				Provider:     session.ProviderCodex,
				Row:          session.Row{Provider: session.ProviderCodex, ID: "preserved-id", CWD: "C:\\project"},
				MessageCount: 2,
			}, nil
		}
		m := sizedModel(nil)
		m.importFlow = reviewWizard(session.ImportAppendContext)
		started, cmd := m.Update(ctrlEnter())
		m = asModel(t, started)
		if cmd == nil || m.selected != nil {
			t.Fatal("apply did not start asynchronously or selected a resume action")
		}
		completed, _ := m.Update(cmd())
		m = asModel(t, completed)
		if m.importFlow.step != importSuccess || m.selected != nil {
			t.Fatalf("success state=%v selection=%#v", m.importFlow.step, m.selected)
		}
		view := m.importView()
		if !strings.Contains(view, "preserved-id") || !strings.Contains(view, "may not appear as chat bubbles") {
			t.Fatalf("append receipt is unclear:\n%s", view)
		}
	})

	t.Run("apply error stays visible", func(t *testing.T) {
		applySessionImport = func(_ context.Context, _ session.ImportPlan) (session.ImportReceipt, error) {
			return session.ImportReceipt{}, errors.New("target changed; review again")
		}
		m := sizedModel(nil)
		m.importFlow = reviewWizard(session.ImportCreate)
		started, cmd := m.Update(ctrlEnter())
		m = asModel(t, started)
		failed, _ := m.Update(cmd())
		m = asModel(t, failed)
		if m.importFlow.step != importDestination || !strings.Contains(m.importView(), "Import failed") {
			t.Fatalf("apply error state is not visible: step=%v\n%s", m.importFlow.step, m.importView())
		}
	})
}

func TestBusyImportCannotBeDismissedBeforeItsResult(t *testing.T) {
	m := sizedModel(nil)
	m.importFlow = reviewWizard(session.ImportCreate)
	started, cmd := m.Update(ctrlEnter())
	m = asModel(t, started)
	if cmd == nil || m.importFlow.busy == "" {
		t.Fatal("apply did not enter a busy state")
	}

	afterEscape, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = asModel(t, afterEscape)
	if m.importFlow == nil || m.importFlow.busy == "" {
		t.Fatal("Escape hid an import while its native write was still running")
	}
}

func ctrlEnter() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModCtrl})
}

func openPasteImport(t *testing.T, m model) model {
	t.Helper()
	opened, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'i'}))
	m = asModel(t, opened)
	chosen, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = asModel(t, chosen)
	input, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	return asModel(t, input)
}

func previewWizard() *importWizard {
	return &importWizard{
		step: importPreview,
		conversation: importer.Conversation{
			SourceKind: importer.SourcePastedText,
			Messages:   []importer.Message{{Role: importer.RoleUser, Text: "question"}},
		},
	}
}

func reviewWizard(mode session.ImportMode) *importWizard {
	wizard := previewWizard()
	wizard.step = importReview
	wizard.plan = session.ImportPlan{
		PlanVersion:  1,
		OperationID:  "test-plan",
		Mode:         mode,
		Provider:     session.ProviderCodex,
		CWD:          "C:\\project",
		Target:       session.Row{Provider: session.ProviderCodex, ID: "preserved-id", CWD: "C:\\project"},
		MessageCount: 2,
	}
	return wizard
}
