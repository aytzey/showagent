package tui

import (
	"os"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"
)

// theme bundles every style the picker renders with. It is rebuilt whenever the
// terminal reports its background color so the UI stays readable on both light
// and dark terminals. NO_COLOR yields a monochrome theme.
type theme struct {
	title        lipgloss.Style
	muted        lipgloss.Style
	chip         lipgloss.Style
	yoloChip     lipgloss.Style
	header       lipgloss.Style
	groupHeader  lipgloss.Style
	cursor       lipgloss.Style
	selected     lipgloss.Style
	codexBadge   lipgloss.Style
	claudeBadge  lipgloss.Style
	date         lipgloss.Style
	workspaceDim lipgloss.Style
	workspace    lipgloss.Style
	message      lipgloss.Style
	deleteBanner lipgloss.Style
	detail       lipgloss.Style
	label        lipgloss.Style
	hint         lipgloss.Style
	spinner      lipgloss.Style
	help         help.Styles
}

func newTheme(isDark bool) *theme {
	if os.Getenv("NO_COLOR") != "" {
		return monoTheme()
	}

	ld := lipgloss.LightDark(isDark)
	c := func(light, dark string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(ld(lipgloss.Color(light), lipgloss.Color(dark)))
	}

	accent := ld(lipgloss.Color("#0969DA"), lipgloss.Color("#7DCAFF"))
	selBg := ld(lipgloss.Color("#0969DA"), lipgloss.Color("#1F6FEB"))
	white := lipgloss.Color("#FFFFFF")
	chipBg := ld(lipgloss.Color("#EAEEF2"), lipgloss.Color("#21262D"))
	chipFg := ld(lipgloss.Color("#1F2328"), lipgloss.Color("#C9D1D9"))

	return &theme{
		title:    lipgloss.NewStyle().Bold(true).Foreground(accent),
		muted:    c("#57606A", "#8B949E"),
		chip:     lipgloss.NewStyle().Foreground(chipFg).Background(chipBg).Padding(0, 1),
		yoloChip: lipgloss.NewStyle().Bold(true).Foreground(white).Background(ld(lipgloss.Color("#BC4C00"), lipgloss.Color("#BB8009"))).Padding(0, 1),
		header: lipgloss.NewStyle().Bold(true).
			Foreground(ld(lipgloss.Color("#1F2328"), lipgloss.Color("#C9D1D9"))).
			Background(ld(lipgloss.Color("#EAEEF2"), lipgloss.Color("#30363D"))),
		groupHeader: lipgloss.NewStyle().Bold(true).
			Foreground(ld(lipgloss.Color("#8250DF"), lipgloss.Color("#D2A8FF"))),
		cursor: lipgloss.NewStyle().Bold(true).Foreground(accent),
		selected: lipgloss.NewStyle().Bold(true).
			Foreground(white).Background(selBg),
		codexBadge: lipgloss.NewStyle().Bold(true).
			Foreground(white).Background(ld(lipgloss.Color("#0969DA"), lipgloss.Color("#1F6FEB"))),
		claudeBadge: lipgloss.NewStyle().Bold(true).
			Foreground(ld(lipgloss.Color("#FFFFFF"), lipgloss.Color("#0D1117"))).
			Background(ld(lipgloss.Color("#8250DF"), lipgloss.Color("#D2A8FF"))),
		date:         c("#6E7781", "#8B949E"),
		workspaceDim: c("#6E7781", "#8B949E"),
		workspace:    lipgloss.NewStyle().Bold(true).Foreground(ld(lipgloss.Color("#1F2328"), lipgloss.Color("#E6EDF3"))),
		message:      c("#1F2328", "#C9D1D9"),
		deleteBanner: lipgloss.NewStyle().Bold(true).
			Foreground(white).Background(ld(lipgloss.Color("#CF222E"), lipgloss.Color("#8E1519"))).Padding(0, 1),
		detail: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ld(lipgloss.Color("#1A7F37"), lipgloss.Color("#3FB950"))).
			Padding(0, 1),
		label:   lipgloss.NewStyle().Bold(true).Foreground(ld(lipgloss.Color("#BC4C00"), lipgloss.Color("#FFA657"))),
		hint:    c("#57606A", "#8B949E"),
		spinner: lipgloss.NewStyle().Foreground(accent),
		help:    help.DefaultStyles(isDark),
	}
}

func monoTheme() *theme {
	plain := lipgloss.NewStyle()
	bold := lipgloss.NewStyle().Bold(true)
	reverse := lipgloss.NewStyle().Reverse(true).Bold(true)
	return &theme{
		title:        bold,
		muted:        plain,
		chip:         lipgloss.NewStyle().Reverse(true).Padding(0, 1),
		yoloChip:     reverse,
		header:       bold,
		groupHeader:  bold,
		cursor:       bold,
		selected:     reverse,
		codexBadge:   bold,
		claudeBadge:  bold,
		date:         plain,
		workspaceDim: plain,
		workspace:    bold,
		message:      plain,
		deleteBanner: reverse,
		detail:       lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1),
		label:        bold,
		hint:         plain,
		spinner:      plain,
		help:         help.Styles{},
	}
}
