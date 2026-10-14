package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCodexResumeBypassesDaemonForLargeHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	row := Row{Provider: ProviderCodex, ID: "session-id", File: path}
	for _, supported := range []bool{true, false} {
		stubCodexNoDaemon(t, supported)
		for _, size := range []int64{0, (64 << 20) - 1, 64 << 20} {
			if err := os.Truncate(path, size); err != nil {
				t.Fatal(err)
			}
			for _, dangerous := range []bool{false, true} {
				options := ResumeOptions{Dangerous: dangerous}
				want := "codex resume"
				if size >= 64<<20 && supported {
					want += " --no-daemon"
				}
				if dangerous {
					want += " --dangerously-bypass-approvals-and-sandbox"
				}
				want += " session-id"
				if got := strings.Join(row.ResumeCommand(options), " "); got != want {
					t.Fatalf("supported=%t size=%d dangerous=%t: resume = %q, want %q", supported, size, dangerous, got, want)
				}
				if got := strings.Join(row.CompoundCommand(options, "capture learning"), " "); got != want+" capture learning" {
					t.Fatalf("compound = %q, want %q", got, want+" capture learning")
				}
			}
		}
	}
}

// TestProbeCodexNoDaemon: only a codex whose resume help lists the flag gets
// it; an older CLI (no flag before 0.156) or a missing one must not.
func TestProbeCodexNoDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codex is a shell script")
	}
	for _, tc := range []struct {
		name string
		help string
		want bool
	}{
		{"0.162", "      --no-daemon\n          Run in-process", true},
		{"0.153", "      --last\n          Continue the most recent session", false},
		{"missing", "", false},
	} {
		bin := t.TempDir()
		t.Setenv("PATH", bin)
		if tc.help != "" {
			// PATH holds only this script, so it sticks to shell builtins.
			script := "#!/bin/sh\n[ \"$1 $2\" = \"resume --help\" ] || exit 2\nprintf '%s\\n' '" + tc.help + "'\n"
			if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if got := probeCodexNoDaemon(); got != tc.want {
			t.Fatalf("%s: probe = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func stubCodexNoDaemon(t *testing.T, supported bool) {
	t.Helper()
	previous := codexNoDaemon
	codexNoDaemon = func() bool { return supported }
	t.Cleanup(func() { codexNoDaemon = previous })
}
