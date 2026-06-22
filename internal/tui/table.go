package tui

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/aytzey/showagent/internal/session"
)

const (
	gutterWidth        = 2
	tableProviderWidth = 8
	tableDateWidth     = 16
	tableGapWidth      = 3
)

// PrintTable renders the plain, unstyled table used when output is piped.
func PrintTable(w io.Writer, rows []session.Row) {
	width := 120
	_, _ = fmt.Fprintln(w, composeLine(width, "  ", "AGENT", "UPDATED", "WORKSPACE", "USER MESSAGE"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(w, composeLine(
			width,
			"  ",
			string(row.Provider),
			localTime(row.LastAt),
			row.CWD,
			previewFor(row, firstMessage),
		))
	}
}

func columnHeader(th *theme, width int, mode previewMode) string {
	line := composeLine(width, "  ", "AGENT", "UPDATED", "WORKSPACE", "USER MESSAGE · "+modeShort(mode))
	return th.header.Render(line)
}

func renderTableRow(th *theme, width int, row session.Row, mode previewMode, selected bool) string {
	_, pw, dw, cw, vw := tableWidths(width)
	date := relativeTime(row.LastAt)
	preview := emptyFallback(previewFor(row, mode))

	if selected {
		inner := fmt.Sprintf(
			"❯ %-*s %-*s %-*s %s",
			pw, providerPlainLabel(string(row.Provider), pw),
			dw, truncateCells(date, dw),
			cw, truncateMiddle(row.CWD, cw),
			truncateCells(preview, vw),
		)
		return th.selected.Width(width).Render(truncateCells(inner, width))
	}

	cells := []string{
		providerBadge(th, string(row.Provider), pw),
		th.date.Width(dw).Render(truncateCells(date, dw)),
		renderWorkspaceCell(th, row.CWD, cw),
		th.message.Width(vw).Render(truncateCells(preview, vw)),
	}
	return padCells("  "+strings.Join(cells, " "), width)
}

func providerBadge(th *theme, provider string, width int) string {
	label := providerPlainLabel(provider, width)
	switch session.Provider(provider) {
	case session.ProviderCodex:
		return th.codexBadge.Width(width).Render(label)
	case session.ProviderClaude:
		return th.claudeBadge.Width(width).Render(label)
	default:
		return th.chip.Width(width).Render(label)
	}
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

func composeLine(width int, gutter, provider, date, cwd, preview string) string {
	_, pw, dw, cw, vw := tableWidths(width)
	line := gutter + fmt.Sprintf(
		"%-*s %-*s %-*s %s",
		pw, truncateCells(provider, pw),
		dw, truncateCells(date, dw),
		cw, truncateMiddle(cwd, cw),
		truncateCells(preview, vw),
	)
	return padCells(truncateCells(line, width), width)
}

func tableWidths(width int) (gutter, provider, date, cwd, preview int) {
	gutter = gutterWidth
	inner := width - gutter
	if inner < tableProviderWidth+tableDateWidth+tableGapWidth+10 {
		provider = min(tableProviderWidth, max(3, inner/5))
		date = min(tableDateWidth, max(5, inner/4))
		cwd = max(5, inner-provider-date-tableGapWidth-5)
		preview = max(1, inner-provider-date-cwd-tableGapWidth)
		return
	}
	provider = tableProviderWidth
	date = tableDateWidth
	cwd = clamp(inner/3, 22, 46)
	preview = max(1, inner-provider-date-cwd-tableGapWidth)
	return
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
		return string([]rune(value)[:min(len([]rune(value)), width)])
	}

	var builder strings.Builder
	for _, r := range value {
		next := builder.String() + string(r)
		if lipgloss.Width(next)+3 > width {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String() + "..."
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
	right := min(width/2, lipgloss.Width(clean))
	suffix := rightCells(clean, right)
	prefixWidth := width - lipgloss.Width(suffix) - 3
	return truncateCells(clean, prefixWidth) + "..." + suffix
}

func rightCells(value string, width int) string {
	runes := []rune(value)
	for i := len(runes); i >= 0; i-- {
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
