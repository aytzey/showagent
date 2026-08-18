package session

import (
	"fmt"
	"strings"
)

// ProviderImpl bundles everything showagent needs to support one agent CLI.
// Adding an agent means implementing this interface in one file and appending
// it to the registry below; no other dispatch site exists.
type ProviderImpl interface {
	// Name is the stable lowercase identifier ("codex").
	Name() Provider
	// DisplayName is the human-facing name ("Codex").
	DisplayName() string
	// CommandName is the CLI executable that resumes this provider's sessions.
	CommandName() string
	// Home is the provider's base data directory, honoring its env override.
	Home() string
	// ScanTargets reports where Discover looks for sessions right now, so
	// callers can tell the user where sessions are expected to live.
	ScanTargets() []ScanTarget
	// Discover parses the provider's local session store into rows.
	Discover() []Row
	// ResumeArgs is the argv that resumes row in the provider's own CLI.
	ResumeArgs(row Row, options ResumeOptions) []string
	// CompoundArgs is ResumeArgs with an initial prompt injected, or nil when
	// the provider cannot start a resumed session with a prompt.
	CompoundArgs(row Row, options ResumeOptions, prompt string) []string
	// Delete permanently removes row from the provider's session store.
	Delete(row Row) error
	// Transcript extracts row's user/assistant turns for conversion.
	Transcript(row Row) ([]Turn, error)
	// WriteConverted writes turns as a new native session of this provider
	// and returns the row describing it.
	WriteConverted(source Row, turns []Turn) (Row, error)
}

// registry lists the supported providers. The order is part of the UI
// contract: digit filter keys, the compound chooser, and provider labels all
// number providers by this order.
var registry = []ProviderImpl{
	codexProvider{},
	claudeProvider{},
	jcodeProvider{},
	opencodeProvider{},
	geminiProvider{},
	piProvider{},
}

func providerFor(name Provider) (ProviderImpl, bool) {
	for _, impl := range registry {
		if impl.Name() == name {
			return impl, true
		}
	}
	return nil, false
}

// ProviderOrder lists the provider names in stable registry order.
func ProviderOrder() []Provider {
	order := make([]Provider, 0, len(registry))
	for _, impl := range registry {
		order = append(order, impl.Name())
	}
	return order
}

// ParseProvider resolves a CLI/user-supplied provider name against the
// registry. The returned value is the stable lowercase provider id.
func ParseProvider(value string) (Provider, error) {
	for _, impl := range registry {
		if string(impl.Name()) == value {
			return impl.Name(), nil
		}
	}
	return "", fmt.Errorf("unsupported provider %q (supported: %s)", value, strings.Join(ProviderNames(), ", "))
}

// ProviderNames returns the stable lowercase provider ids in registry order.
func ProviderNames() []string {
	names := make([]string, 0, len(registry))
	for _, impl := range registry {
		names = append(names, string(impl.Name()))
	}
	return names
}

// DisplayName is the human-facing name for provider ("Codex"). Unknown
// providers fall back to their raw name.
func DisplayName(provider Provider) string {
	if impl, ok := providerFor(provider); ok {
		return impl.DisplayName()
	}
	return string(provider)
}

// CompoundAgents lists the providers able to run a compound pass (resume a
// session with an injected prompt), in registry order.
func CompoundAgents() []Provider {
	var agents []Provider
	for _, impl := range registry {
		if impl.CompoundArgs(Row{Provider: impl.Name(), ID: "probe"}, ResumeOptions{}, "probe") != nil {
			agents = append(agents, impl.Name())
		}
	}
	return agents
}
