package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/aytzey/showagent/internal/session"
)

type trimPreview struct {
	plan    session.TrimPlan
	before  bool
	loading bool
	err     error
}

type trimPreviewMsg struct {
	request *trimPreview
	plan    session.TrimPlan
	err     error
}

type trimAppliedMsg struct {
	source session.Row
	row    session.Row
	err    error
}

func (m model) previewTrim(options session.HandoffOptions) (tea.Model, tea.Cmd) {
	c := m.continuation
	message, before := c.cut()
	if message == nil {
		return m, nil
	}
	boundary := *message
	request := &trimPreview{before: before, loading: true}
	c.trim = request
	return m, func() tea.Msg {
		plan, err := session.PlanTrim(c.row, options)
		if err == nil && !plan.Boundary.SameBoundary(boundary) {
			err = errors.New("the selected message changed. Reopen trim to see current messages")
		}
		return trimPreviewMsg{request: request, plan: plan, err: err}
	}
}

func (m model) applyTrimPreview(msg trimPreviewMsg) (tea.Model, tea.Cmd) {
	if c := m.continuation; c != nil && c.trim != nil && c.trim == msg.request {
		c.trim.loading = false
		c.trim.plan = msg.plan
		c.trim.err = msg.err
	}
	return m, nil
}

func (m model) updateTrimPreview(key string) (tea.Model, tea.Cmd) {
	c := m.continuation
	preview := c.trim
	switch key {
	case "esc":
		c.trim = nil
	case "q":
		m.continuation = nil
	case "D":
		if !preview.loading && preview.err == nil && m.trimConfirmationFits() {
			plan := preview.plan
			m.continuation = nil
			return m.startMutation(func() tea.Msg {
				row, err := session.ApplyTrim(plan)
				return trimAppliedMsg{source: plan.Row, row: row, err: err}
			}, "trim", "trimming session…")
		}
	}
	return m, nil
}

func (m model) finishTrim(msg trimAppliedMsg) (tea.Model, tea.Cmd) {
	m.busy = ""
	if msg.err != nil && msg.row.ID == "" {
		return m, m.list.NewStatusMessage("trim failed: " + session.SafeDisplayText(msg.err.Error()))
	}
	if msg.source.ID != "" && rowKey(msg.source) != rowKey(msg.row) {
		m.allRows = removeRow(m.allRows, msg.source)
	}
	m.allRows = upsertAndSortRows(m.allRows, msg.row)
	delete(m.collapsedGroups, msg.row.CWD)
	m.refreshSessionList()
	if m.list.IsFiltered() && rowItemIndex(m.list.VisibleItems(), msg.row, -1) < 0 {
		m.list.ResetFilter()
		m.refreshSessionList()
	}
	selectRowItem(&m.list, msg.row)
	if msg.err != nil {
		return m, m.list.NewStatusMessage("trimmed session with warning · " + session.SafeDisplayText(msg.err.Error()))
	}
	return m, m.list.NewStatusMessage("trimmed session " + session.SafeDisplayText(msg.row.ID))
}

func (m model) trimBoundarySummary(number int, before bool) string {
	if before {
		if m.continuation.row.Provider == session.ProviderCodex {
			return firstFitting(m.width,
				fmt.Sprintf("Removes user #%d and all later messages.", number),
				fmt.Sprintf("Removes from user #%d.", number))
		}
		return firstFitting(m.width,
			fmt.Sprintf("Deletes user #%d and everything after it.", number),
			fmt.Sprintf("Deletes from user #%d.", number),
			fmt.Sprintf("Deletes from #%d.", number))
	}
	return firstFitting(m.width,
		fmt.Sprintf("Keeps user #%d. Deletes its reply and everything after it.", number),
		fmt.Sprintf("Deletes after user #%d.", number),
		fmt.Sprintf("Deletes after #%d.", number))
}

func (m model) trimConfirmationFits() bool {
	minHeight := 8
	if m.continuation.row.Provider == session.ProviderCodex {
		minHeight++ // Leave a row for the previous-rollout notice.
	}
	return (m.width <= 0 || m.width >= 24) && (m.height <= 0 || m.height >= minHeight)
}

func (m model) trimPreviewView() string {
	c := m.continuation
	preview := c.trim
	th := m.render.theme
	codex := c.row.Provider == session.ProviderCodex
	titleWidth := m.width - th.deleteBanner.GetHorizontalFrameSize()
	title := firstFitting(titleWidth, "Permanently trim session", "Confirm permanent trim")
	if codex {
		title = firstFitting(titleWidth, "Trim Codex conversation", "Trim Codex session")
	}
	lines := []string{th.deleteBanner.Render(title)}
	footer := "esc back"
	switch {
	case preview.loading:
		lines = append(lines, "Building trim preview…")
	case preview.err != nil:
		lines = append(lines, "Cannot trim session", session.SafeDisplayText(preview.err.Error()))
	case !m.trimConfirmationFits():
		lines = append(lines, "Resize to review the trim.")
	default:
		plan := preview.plan
		provider := session.DisplayName(plan.Row.Provider)
		id := session.SafeDisplayText(plan.Row.ID)
		if m.width > 0 {
			id = truncateMiddle(id, max(1, m.width-len(provider)-3))
		}
		lines = append(lines, firstFitting(m.width, "Close it in the agent first.", "Close the agent session."))
		if codex {
			lines = append(lines, firstFitting(m.width, "Codex keeps the previous rollout on disk.", "Old rollout kept on disk"))
		}
		lines = append(lines,
			m.trimBoundarySummary(plan.Boundary.Number, preview.before),
			provider+" · "+id,
			firstFitting(m.width,
				fmt.Sprintf("Turns · keep %d, delete %d", plan.KeptTurns, plan.RemovedTurns),
				fmt.Sprintf("Delete %d of %d turns", plan.RemovedTurns, plan.KeptTurns+plan.RemovedTurns)),
			firstFitting(m.width,
				fmt.Sprintf("User messages · keep %d, cut %d", plan.KeptUserMessages, plan.RemovedUserMessages),
				fmt.Sprintf("Cut %d of %d user messages", plan.RemovedUserMessages, plan.KeptUserMessages+plan.RemovedUserMessages)),
			session.SafeDisplayText(plan.Boundary.Text),
		)
		footer = "D delete · esc back"
		if codex {
			footer = "D trim · esc back"
		}
	}
	content := strings.Join(lines, "\n")
	if m.height == 1 {
		return clampLineWidths(th.hint.Render(footer), m.width)
	}
	if m.height > 1 {
		content = clampLines(content, m.height-1)
	}
	return clampLineWidths(content+"\n"+th.hint.Render(footer), m.width)
}
