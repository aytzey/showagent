package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/aytzey/showagent/internal/session"
)

const (
	gutterWidth        = 2
	tableProviderWidth = 8
	tableDateWidth     = 16
	// tableAgeWidth fits every relativeTime value ("just now", "59m ago").
	tableAgeWidth = 8
)

// PrintTable renders the plain, unstyled table used when output is piped or
// 'showagent list' runs. width is the terminal width; values <= 0 fall back
// to 120. Session ids are always printed in full so the output stays usable
// with 'showagent resume <id>'.
func PrintTable(w io.Writer, width int, rows []session.Row) {
	if width <= 0 {
		width = 120
	}
	idWidth := len("ID")
	for _, row := range rows {
		if n := lipgloss.Width(session.SafeDisplayText(row.ID)); n > idWidth {
			idWidth = n
		}
	}
	_, _ = fmt.Fprintln(w, plainLine(width, idWidth, "ID", "AGENT", "UPDATED", "WORKSPACE", "USER MESSAGE"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(w, plainLine(
			width,
			idWidth,
			session.SafeDisplayText(row.ID),
			string(row.Provider),
			localTime(row.LastAt),
			session.SafeDisplayText(row.CWD),
			session.SafeDisplayText(previewFor(row, firstMessage)),
		))
	}
}

// plainLine lays out one plain-table row. The id column is never truncated;
// the workspace and message columns absorb whatever width remains.
func plainLine(width, idWidth int, id, provider, date, cwd, preview string) string {
	rest := width - gutterWidth - idWidth - tableProviderWidth - tableDateWidth - 4
	cw := clamp(rest/2, 10, 46)
	vw := max(1, rest-cw-1)
	line := "  " + fmt.Sprintf(
		"%-*s %-*s %-*s %-*s %s",
		idWidth, id,
		tableProviderWidth, truncateCells(provider, tableProviderWidth),
		tableDateWidth, truncateCells(date, tableDateWidth),
		cw, truncateMiddle(cwd, cw),
		truncateCells(preview, vw),
	)
	return strings.TrimRight(line, " ")
}

func columnHeader(th *theme, width int, mode previewMode, withWorkspace bool) string {
	line := composeLine(width, withWorkspace, "  ", "AGENT", "UPDATED", "WORKSPACE", strings.ToUpper(modeLong(mode)))
	return th.header.Render(line)
}

func renderGroupHeader(th *theme, width int, h headerItem, selected bool) string {
	if width <= 0 {
		width = 80
	}
	count := fmt.Sprintf(" (%d)", h.count)
	icon := "▾"
	if h.collapsed {
		icon = "▸"
	}
	label := icon + " " + collapseHome(session.SafeDisplayText(h.path))
	text := truncateMiddle(label, max(1, width-lipgloss.Width(count))) + count
	if selected {
		return th.selected.Width(width).Render(truncateCells(text, width))
	}
	return th.groupHeader.Width(width).Render(truncateCells(text, width))
}

func baseName(path string) string {
	return filepath.Base(filepath.Clean(path))
}

func collapseHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+string(filepath.Separator)) {
			return "~" + path[len(home):]
		}
	}
	return path
}

// renderTableRow draws one session. withWorkspace is false in the grouped view,
// where the group header already names the workspace and the message gets the
// space instead.
func renderTableRow(th *theme, width int, row session.Row, mode previewMode, selected, withWorkspace bool) string {
	pw, dw, cw, vw := tableWidths(width, withWorkspace)
	date := relativeTime(row.LastAt)
	workspace := collapseHome(session.SafeDisplayText(row.CWD))
	preview := emptyFallback(session.SafeDisplayText(previewFor(row, mode)))

	if selected {
		line := composeLine(width, withWorkspace, "❯ ", providerPlainLabel(string(row.Provider), pw), date, workspace, preview)
		return th.selected.Width(width).Render(line)
	}

	cells := []string{
		providerBadge(th, string(row.Provider), pw),
		th.date.Width(dw).Render(truncateCells(date, dw)),
	}
	if cw > 0 {
		cells = append(cells, renderWorkspaceCell(th, workspace, cw))
	}
	cells = append(cells, th.message.Width(vw).Render(truncateCells(preview, vw)))
	return padCells("  "+strings.Join(cells, " "), width)
}

func providerBadge(th *theme, provider string, width int) string {
	label := providerPlainLabel(provider, width)
	return th.badgeFor(session.Provider(provider)).Width(width).Render(label)
}

func providerPlainLabel(provider string, width int) string {
	return centerCell(" "+strings.ToUpper(provider)+" ", width)
}

func renderWorkspaceCell(th *theme, cwd string, width int) string {
	value := truncateMiddle(cwd, width)
	base := filepath.Base(filepath.Clean(cwd))
	index := strings.LastIndex(value, base)
	if index <= 0 {
		return th.workspaceDim.Width(width).Render(value)
	}
	rendered := th.workspaceDim.Render(value[:index]) + th.workspace.Render(value[index:])
	return padCells(rendered, width)
}

func composeLine(width int, withWorkspace bool, gutter, provider, date, cwd, preview string) string {
	pw, dw, cw, vw := tableWidths(width, withWorkspace)
	cells := []string{padCells(truncateCells(provider, pw), pw), padCells(truncateCells(date, dw), dw)}
	if cw > 0 {
		cells = append(cells, padCells(truncateMiddle(cwd, cw), cw))
	}
	cells = append(cells, truncateCells(preview, vw))
	return padCells(truncateCells(gutter+strings.Join(cells, " "), width), width)
}

// tableWidths splits a picker row into columns; cwd is 0 without the
// workspace column. Cells are separated by single spaces.
func tableWidths(width int, withWorkspace bool) (provider, date, cwd, preview int) {
	inner := width - gutterWidth
	gaps := 2
	if withWorkspace {
		gaps = 3
	}
	provider, date = tableProviderWidth, tableAgeWidth
	if inner < provider+date+gaps+10 {
		provider = min(provider, max(3, inner/5))
		date = min(date, max(5, inner/4))
	}
	rest := max(2, inner-provider-date-gaps)
	if withWorkspace {
		cwd = min(clamp(inner/3, 10, 46), rest/2)
	}
	return provider, date, cwd, max(1, rest-cwd)
}

func previewFor(row session.Row, mode previewMode) string {
	first := emptyFallback(row.FirstUser)
	last := bestLast(row)
	switch mode {
	case lastMessage:
		return emptyFallback(last)
	case bothMessages:
		if row.FirstUser != "" && row.LastUser != "" && row.FirstUser != row.LastUser {
			return row.FirstUser + " | " + row.LastUser
		}
	}
	return first
}

func bestLast(row session.Row) string {
	if row.LastUser != "" {
		return row.LastUser
	}
	return row.FirstUser
}

func modeShort(mode previewMode) string {
	switch mode {
	case lastMessage:
		return "latest"
	case bothMessages:
		return "first+latest"
	default:
		return "first"
	}
}

func relativeTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	d := time.Since(value)
	switch {
	case d < 0:
		return value.Local().Format("Jan 02")
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return value.Local().Format("Jan 02")
	}
}

func localTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.Local().Format("2006-01-02 15:04")
}

func emptyFallback(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}

func centerCell(value string, width int) string {
	value = truncateCells(value, width)
	valueWidth := lipgloss.Width(value)
	if valueWidth >= width {
		return value
	}
	left := (width - valueWidth) / 2
	right := width - valueWidth - left
	return strings.Repeat(" ", left) + value + strings.Repeat(" ", right)
}

func padCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	cellWidth := lipgloss.Width(value)
	if cellWidth >= width {
		return value
	}
	return value + strings.Repeat(" ", width-cellWidth)
}

func truncateCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return ansi.Truncate(value, width, "")
	}
	return ansi.Truncate(value, width, "...")
}

func truncateMiddle(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 3 {
		return truncateCells(value, width)
	}
	clean := filepath.Clean(value)
	// Bias the budget toward the suffix: the basename side is what tells
	// sibling workspaces apart.
	right := min(2*width/3, lipgloss.Width(clean))
	suffix := rightCells(clean, right)
	prefixWidth := width - lipgloss.Width(suffix) - 3
	return truncateCells(clean, prefixWidth) + "..." + suffix
}

func rightCells(value string, width int) string {
	runes := []rune(value)
	for i := 0; i <= len(runes); i++ {
		candidate := string(runes[i:])
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return ""
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
