package tui

import (
	"os"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"

	"github.com/aytzey/showagent/internal/session"
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
	badges       map[session.Provider]lipgloss.Style
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

// badgeFor is the badge style for provider; providers without a dedicated
// accent fall back to the neutral chip style so new registry entries render
// sensibly before they get colors.
func (t *theme) badgeFor(provider session.Provider) lipgloss.Style {
	if style, ok := t.badges[provider]; ok {
		return style
	}
	return t.chip
}

// badgeColors is one provider's badge palette: background and foreground for
// light and dark terminals.
type badgeColors struct {
	lightBg, darkBg string
	lightFg, darkFg string
}

// providerAccents assigns each known provider its brand-ish accent. Providers
// missing here (future registry additions) fall back to theme.chip.
var providerAccents = map[session.Provider]badgeColors{
	session.ProviderCodex:    {lightBg: "#0969DA", darkBg: "#1F6FEB", lightFg: "#FFFFFF", darkFg: "#FFFFFF"},
	session.ProviderClaude:   {lightBg: "#8250DF", darkBg: "#D2A8FF", lightFg: "#FFFFFF", darkFg: "#0D1117"},
	session.ProviderJCode:    {lightBg: "#1A7F37", darkBg: "#238636", lightFg: "#FFFFFF", darkFg: "#FFFFFF"},
	session.ProviderOpenCode: {lightBg: "#1B7C8C", darkBg: "#39C5CF", lightFg: "#FFFFFF", darkFg: "#0D1117"},
	session.ProviderGemini:   {lightBg: "#B04A17", darkBg: "#F0883E", lightFg: "#FFFFFF", darkFg: "#0D1117"},
}

// providerBadges builds one badge style per registered provider, so every
// badge consumer stays in sync with the provider registry.
func providerBadges(isDark bool) map[session.Provider]lipgloss.Style {
	ld := lipgloss.LightDark(isDark)
	badges := make(map[session.Provider]lipgloss.Style, len(providerAccents))
	for _, provider := range session.ProviderOrder() {
		colors, ok := providerAccents[provider]
		if !ok {
			continue
		}
		badges[provider] = lipgloss.NewStyle().Bold(true).
			Foreground(ld(lipgloss.Color(colors.lightFg), lipgloss.Color(colors.darkFg))).
			Background(ld(lipgloss.Color(colors.lightBg), lipgloss.Color(colors.darkBg)))
	}
	return badges
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
		badges:       providerBadges(isDark),
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
	badges := map[session.Provider]lipgloss.Style{}
	for _, provider := range session.ProviderOrder() {
		badges[provider] = bold
	}
	return &theme{
		title:        bold,
		muted:        plain,
		chip:         lipgloss.NewStyle().Reverse(true).Padding(0, 1),
		yoloChip:     reverse,
		header:       bold,
		groupHeader:  bold,
		cursor:       bold,
		selected:     reverse,
		badges:       badges,
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
