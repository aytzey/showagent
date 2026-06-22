package tui

import (
	"errors"
	"fmt"
	"os"

	huh "charm.land/huh/v2"

	"github.com/aytzey/showcodex/internal/session"
)

type settings struct {
	providers []session.Provider
	mode      previewMode
}

func pickSettings(rows []session.Row) (settings, bool, error) {
	providers := defaultProviders(rows)
	cfg := settings{
		providers: providers,
		mode:      firstMessage,
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[session.Provider]().
				Title("Providers").
				Description("Space toggles, enter continues.").
				Options(providerOptions(providers)...).
				Value(&cfg.providers).
				Validate(func(value []session.Provider) error {
					if len(value) == 0 {
						return fmt.Errorf("select at least one provider")
					}
					return nil
				}),
			huh.NewSelect[previewMode]().
				Title("User message preview").
				Options(
					huh.NewOption("first user message", firstMessage).Selected(true),
					huh.NewOption("last user message", lastMessage),
					huh.NewOption("first + last user messages", bothMessages),
				).
				Value(&cfg.mode),
		).Title("showcodex"),
	).
		WithTheme(settingsTheme()).
		WithWidth(82)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return cfg, false, nil
		}
		return cfg, false, err
	}

	return cfg, true, nil
}

func settingsTheme() huh.Theme {
	if os.Getenv("NO_COLOR") != "" {
		return huh.ThemeFunc(huh.ThemeBase)
	}
	return huh.ThemeFunc(huh.ThemeCharm)
}

func defaultProviders(rows []session.Row) []session.Provider {
	seen := map[session.Provider]bool{}
	for _, row := range rows {
		seen[row.Provider] = true
	}

	var providers []session.Provider
	for _, provider := range []session.Provider{session.ProviderCodex, session.ProviderClaude} {
		if seen[provider] {
			providers = append(providers, provider)
		}
	}
	return providers
}

func providerOptions(providers []session.Provider) []huh.Option[session.Provider] {
	options := make([]huh.Option[session.Provider], 0, len(providers))
	for _, provider := range providers {
		options = append(options, huh.NewOption(string(provider), provider).Selected(true))
	}
	return options
}

func filterRows(rows []session.Row, providers []session.Provider) []session.Row {
	enabled := map[session.Provider]bool{}
	for _, provider := range providers {
		enabled[provider] = true
	}

	filtered := make([]session.Row, 0, len(rows))
	for _, row := range rows {
		if enabled[row.Provider] {
			filtered = append(filtered, row)
		}
	}
	return filtered
}
