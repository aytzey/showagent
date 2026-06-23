package session

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

const (
	compoundPluginMarketplace       = "compound-engineering-plugin"
	compoundPluginMarketplaceSource = "EveryInc/compound-engineering-plugin"
	compoundPluginSelector          = "compound-engineering@compound-engineering-plugin"
)

type CompoundPluginSetupResult struct {
	Provider         Provider
	Command          string
	Available        bool
	AlreadyInstalled bool
	MarketplaceAdded bool
	Installed        bool
}

func (r CompoundPluginSetupResult) Status() string {
	if !r.Available {
		return "not found"
	}
	if r.AlreadyInstalled {
		return "already installed"
	}
	if r.Installed {
		return "installed"
	}
	return "not installed"
}

// EnsureCompoundEngineeringPlugin installs EveryInc's Compound Engineering
// plugin for the local Codex and Claude CLIs when those CLIs are available and
// the plugin is not already installed.
func EnsureCompoundEngineeringPlugin() ([]CompoundPluginSetupResult, error) {
	results := make([]CompoundPluginSetupResult, 0, 2)

	codex, err := ensureCodexCompoundPlugin()
	if err != nil {
		return append(results, codex), err
	}
	results = append(results, codex)

	claude, err := ensureClaudeCompoundPlugin()
	if err != nil {
		return append(results, claude), err
	}
	results = append(results, claude)

	return results, nil
}

func ensureCodexCompoundPlugin() (CompoundPluginSetupResult, error) {
	result := CompoundPluginSetupResult{Provider: ProviderCodex, Command: "codex"}
	if !commandAvailable("codex") {
		return result, nil
	}
	result.Available = true

	list, err := runOutput("codex", "plugin", "list")
	if err != nil {
		return result, err
	}
	if pluginListContainsCompoundEngineering(list) {
		result.AlreadyInstalled = true
		return result, nil
	}

	marketplaces, err := runOutput("codex", "plugin", "marketplace", "list")
	if err != nil {
		return result, err
	}
	if !strings.Contains(marketplaces, compoundPluginMarketplace) {
		if _, err := runOutput("codex", "plugin", "marketplace", "add", compoundPluginMarketplaceSource, "--ref", "main"); err != nil {
			return result, err
		}
		result.MarketplaceAdded = true
	}

	if _, err := runOutput("codex", "plugin", "add", compoundPluginSelector); err != nil {
		return result, err
	}
	result.Installed = true
	return result, nil
}

func ensureClaudeCompoundPlugin() (CompoundPluginSetupResult, error) {
	result := CompoundPluginSetupResult{Provider: ProviderClaude, Command: "claude"}
	if !commandAvailable("claude") {
		return result, nil
	}
	result.Available = true

	list, err := runOutput("claude", "plugin", "list")
	if err != nil {
		return result, err
	}
	if pluginListContainsCompoundEngineering(list) {
		result.AlreadyInstalled = true
		return result, nil
	}

	marketplaces, err := runOutput("claude", "plugin", "marketplace", "list")
	if err != nil {
		return result, err
	}
	if !strings.Contains(marketplaces, compoundPluginMarketplace) {
		if _, err := runOutput("claude", "plugin", "marketplace", "add", compoundPluginMarketplaceSource); err != nil {
			return result, err
		}
		result.MarketplaceAdded = true
	}

	if _, err := runOutput("claude", "plugin", "install", compoundPluginSelector, "--scope", "user"); err != nil {
		return result, err
	}
	result.Installed = true
	return result, nil
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func pluginListContainsCompoundEngineering(output string) bool {
	return strings.Contains(output, compoundPluginSelector)
}

func runOutput(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			return "", fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, detail)
	}
	return stdout.String(), nil
}
