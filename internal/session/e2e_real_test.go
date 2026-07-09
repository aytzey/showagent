//go:build realcli

package session

// Real-CLI end-to-end helpers, driven by scripts/e2e-real.sh. These run
// against the REAL agent homes (~/.claude, ~/.codex, ~/.gemini, opencode's
// data dir) and only ever touch sessions whose cwd is one of the workspaces
// passed in SHOWAGENT_E2E_WS_LIST. Never enabled in CI: the build tag plus
// the env guard keep `go test ./...` unaffected.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type e2eArtifact struct {
	Kind     string `json:"kind"` // original | branched | converted
	Provider string `json:"provider"`
	ID       string `json:"id"`
	File     string `json:"file"`
	CWD      string `json:"cwd"`
}

func e2eWorkspaces(t *testing.T) []string {
	t.Helper()
	raw := os.Getenv("SHOWAGENT_E2E_WS_LIST")
	if raw == "" {
		t.Skip("SHOWAGENT_E2E_WS_LIST not set; real-CLI e2e disabled")
	}
	return strings.Split(raw, ":")
}

func e2eManifestPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("SHOWAGENT_E2E_MANIFEST")
	if p == "" {
		t.Fatal("SHOWAGENT_E2E_MANIFEST not set")
	}
	return p
}

func inWorkspaces(cwd string, workspaces []string) bool {
	for _, ws := range workspaces {
		if cwd == ws {
			return true
		}
	}
	return false
}

func discoverWorkspaceRows(t *testing.T, workspaces []string) []Row {
	t.Helper()
	var rows []Row
	for _, row := range Discover() {
		if inWorkspaces(row.CWD, workspaces) {
			rows = append(rows, row)
		}
	}
	return rows
}

// TestRealCLIMutate branches every session found in the test workspaces and
// converts each one to every other provider whose CLI is installed, then
// writes a manifest of everything it created (plus the originals) so the
// shell runner can verify the artifacts with the real CLIs and clean up.
func TestRealCLIMutate(t *testing.T) {
	workspaces := e2eWorkspaces(t)
	manifest := e2eManifestPath(t)

	rows := discoverWorkspaceRows(t, workspaces)
	if len(rows) == 0 {
		t.Fatalf("no sessions discovered in workspaces %v — create them first", workspaces)
	}

	var artifacts []e2eArtifact
	for _, row := range rows {
		artifacts = append(artifacts, e2eArtifact{
			Kind: "original", Provider: string(row.Provider), ID: row.ID, File: row.File, CWD: row.CWD,
		})
	}

	targets := []Provider{ProviderCodex, ProviderClaude, ProviderGemini, ProviderOpenCode}
	for _, row := range rows {
		branched, err := Branch(row)
		if err != nil {
			t.Fatalf("branch %s/%s: %v", row.Provider, row.ID, err)
		}
		t.Logf("branched %s/%s -> %s (%s)", row.Provider, row.ID, branched.ID, branched.File)
		artifacts = append(artifacts, e2eArtifact{
			Kind: "branched", Provider: string(branched.Provider), ID: branched.ID, File: branched.File, CWD: branched.CWD,
		})

		for _, target := range targets {
			if target == row.Provider {
				continue
			}
			if !ProviderCommandAvailable(target) {
				t.Logf("skip convert %s -> %s: CLI not installed", row.Provider, target)
				continue
			}
			converted, err := Convert(row, target, HandoffOptions{})
			if err != nil {
				t.Fatalf("convert %s/%s -> %s: %v", row.Provider, row.ID, target, err)
			}
			t.Logf("converted %s/%s -> %s/%s (%s)", row.Provider, row.ID, target, converted.ID, converted.File)
			artifacts = append(artifacts, e2eArtifact{
				Kind: "converted", Provider: string(converted.Provider), ID: converted.ID, File: converted.File, CWD: converted.CWD,
			})
		}
	}

	data, err := json.MarshalIndent(artifacts, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRealCLICleanup deletes every artifact recorded in the manifest through
// showagent's own Delete path, exercising per-provider deletion for real.
func TestRealCLICleanup(t *testing.T) {
	workspaces := e2eWorkspaces(t)
	manifest := e2eManifestPath(t)

	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var artifacts []e2eArtifact
	if err := json.Unmarshal(data, &artifacts); err != nil {
		t.Fatal(err)
	}

	rows := map[string]Row{}
	for _, row := range Discover() {
		if inWorkspaces(row.CWD, workspaces) {
			rows[row.ID] = row
		}
	}

	for _, a := range artifacts {
		row, ok := rows[a.ID]
		if !ok {
			t.Errorf("cleanup: artifact %s/%s (%s) not discoverable", a.Provider, a.ID, a.Kind)
			continue
		}
		if err := Delete(row); err != nil {
			t.Errorf("delete %s/%s (%s): %v", a.Provider, a.ID, a.Kind, err)
			continue
		}
		t.Logf("deleted %s/%s (%s)", a.Provider, a.ID, a.Kind)
	}

	for _, row := range discoverWorkspaceRows(t, workspaces) {
		t.Errorf("cleanup incomplete: %s/%s still discoverable at %s", row.Provider, row.ID, row.File)
	}
}
