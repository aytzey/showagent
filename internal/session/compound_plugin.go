package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	compoundPluginMarketplace       = "compound-engineering-plugin"
	compoundPluginMarketplaceSource = "EveryInc/compound-engineering-plugin"
	compoundPluginSelector          = "compound-engineering@compound-engineering-plugin"
	piCompoundPluginSource          = "git:github.com/EveryInc/compound-engineering-plugin"
	piSubagentsSource               = "npm:pi-subagents"
	piAskUserSource                 = "npm:pi-ask-user"
)

var compoundPluginCommandTimeout = 30 * time.Second
var compoundPluginInstallTimeout = 2 * time.Minute

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
// plugin for the local Codex, Claude, and Pi CLIs when those CLIs are available
// and the plugin is not already installed.
func EnsureCompoundEngineeringPlugin() ([]CompoundPluginSetupResult, error) {
	results := make([]CompoundPluginSetupResult, 0, 3)
	setups := []func() (CompoundPluginSetupResult, error){
		ensureCodexCompoundPlugin,
		ensureClaudeCompoundPlugin,
		ensurePiCompoundPlugin,
	}
	var setupErrors []error
	for _, setup := range setups {
		result, err := setup()
		results = append(results, result)
		if err != nil {
			setupErrors = append(setupErrors, err)
		}
	}
	return results, errors.Join(setupErrors...)
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

func ensurePiCompoundPlugin() (CompoundPluginSetupResult, error) {
	result := CompoundPluginSetupResult{Provider: ProviderPi, Command: "pi"}
	if !commandAvailable("pi") {
		return result, nil
	}
	result.Available = true

	list, err := runOutput("pi", "list", "--no-approve")
	if err != nil {
		return result, err
	}
	sources := []string{piCompoundPluginSource, piSubagentsSource, piAskUserSource}
	var missing []string
	for _, source := range sources {
		if !strings.Contains(list, source) {
			missing = append(missing, source)
		}
	}
	if len(missing) == 0 {
		result.AlreadyInstalled = true
		return result, nil
	}
	for _, source := range missing {
		if _, err := runOutputWithTimeout(compoundPluginInstallTimeout, "pi", "install", source); err != nil {
			return result, err
		}
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
	return runOutputWithTimeout(compoundPluginCommandTimeout, name, args...)
}

func runOutputWithTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%s %s timed out after %s", name, strings.Join(args, " "), timeout)
		}
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
