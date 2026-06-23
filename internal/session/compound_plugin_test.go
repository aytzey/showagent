package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCompoundEngineeringPluginInstallsMissingPlugins(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls.log")
	writeFakePluginCLI(t, bin, "codex", calls)
	writeFakePluginCLI(t, bin, "claude", calls)
	t.Setenv("PATH", bin)
	t.Setenv("CALLS_FILE", calls)
	t.Setenv("CODEX_PLUGIN_LIST", "github@openai-curated installed, enabled")
	t.Setenv("CODEX_MARKETPLACE_LIST", "MARKETPLACE ROOT\nopenai-curated /tmp/openai")
	t.Setenv("CLAUDE_PLUGIN_LIST", "Installed plugins:\ncontext7@claude-plugins-official\nStatus: ✔ enabled")
	t.Setenv("CLAUDE_MARKETPLACE_LIST", "Configured marketplaces:\nclaude-plugins-official")

	results, err := EnsureCompoundEngineeringPlugin()
	if err != nil {
		t.Fatal(err)
	}
	assertSetupResult(t, results, ProviderCodex, true, true, true, false)
	assertSetupResult(t, results, ProviderClaude, true, true, true, false)

	log := readCalls(t, calls)
	for _, want := range []string{
		"codex plugin marketplace add EveryInc/compound-engineering-plugin --ref main",
		"codex plugin add compound-engineering@compound-engineering-plugin",
		"claude plugin marketplace add EveryInc/compound-engineering-plugin",
		"claude plugin install compound-engineering@compound-engineering-plugin --scope user",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("calls log missing %q:\n%s", want, log)
		}
	}
}

func TestEnsureCompoundEngineeringPluginSkipsAlreadyInstalledPlugins(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls.log")
	writeFakePluginCLI(t, bin, "codex", calls)
	writeFakePluginCLI(t, bin, "claude", calls)
	t.Setenv("PATH", bin)
	t.Setenv("CALLS_FILE", calls)
	t.Setenv("CODEX_PLUGIN_LIST", "compound-engineering@compound-engineering-plugin installed, enabled 3.13.1")
	t.Setenv("CLAUDE_PLUGIN_LIST", "compound-engineering@compound-engineering-plugin\nStatus: ✔ enabled")

	results, err := EnsureCompoundEngineeringPlugin()
	if err != nil {
		t.Fatal(err)
	}
	assertSetupResult(t, results, ProviderCodex, true, false, false, true)
	assertSetupResult(t, results, ProviderClaude, true, false, false, true)

	log := readCalls(t, calls)
	for _, unwanted := range []string{"marketplace add", "plugin add compound-engineering", "plugin install compound-engineering"} {
		if strings.Contains(log, unwanted) {
			t.Fatalf("calls log unexpectedly contains %q:\n%s", unwanted, log)
		}
	}
}

func TestEnsureCompoundEngineeringPluginSkipsUnavailableCLIs(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	results, err := EnsureCompoundEngineeringPlugin()
	if err != nil {
		t.Fatal(err)
	}
	assertSetupResult(t, results, ProviderCodex, false, false, false, false)
	assertSetupResult(t, results, ProviderClaude, false, false, false, false)
}

func assertSetupResult(t *testing.T, results []CompoundPluginSetupResult, provider Provider, available bool, installed bool, marketplace bool, already bool) {
	t.Helper()
	for _, result := range results {
		if result.Provider != provider {
			continue
		}
		if result.Available != available || result.Installed != installed || result.MarketplaceAdded != marketplace || result.AlreadyInstalled != already {
			t.Fatalf("%s result = %#v, want available=%v installed=%v marketplace=%v already=%v", provider, result, available, installed, marketplace, already)
		}
		return
	}
	t.Fatalf("%s result missing in %#v", provider, results)
}

func writeFakePluginCLI(t *testing.T, dir string, name string, calls string) {
	t.Helper()
	upper := strings.ToUpper(name)
	path := filepath.Join(dir, name)
	script := `#!/bin/sh
base=${0##*/}
echo "$base $*" >> "$CALLS_FILE"
case "$1 $2" in
  "plugin list")
    eval "printf '%s\n' \"${` + upper + `_PLUGIN_LIST}\""
    exit 0
    ;;
  "plugin marketplace")
    if [ "$3" = "list" ]; then
      eval "printf '%s\n' \"${` + upper + `_MARKETPLACE_LIST}\""
      exit 0
    fi
    if [ "$3" = "add" ]; then
      exit 0
    fi
    ;;
  "plugin add")
    exit 0
    ;;
  "plugin install")
    exit 0
    ;;
esac
echo "unexpected command: $*" >&2
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = calls
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
