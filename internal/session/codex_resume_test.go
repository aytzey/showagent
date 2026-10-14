package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexResumeBypassesDaemonForLargeHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	row := Row{Provider: ProviderCodex, ID: "session-id", File: path}
	for _, size := range []int64{0, (64 << 20) - 1, 64 << 20} {
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
		for _, dangerous := range []bool{false, true} {
			options := ResumeOptions{Dangerous: dangerous}
			want := "codex resume"
			if size >= 64<<20 {
				want += " --no-daemon"
			}
			if dangerous {
				want += " --dangerously-bypass-approvals-and-sandbox"
			}
			want += " session-id"
			if got := strings.Join(row.ResumeCommand(options), " "); got != want {
				t.Fatalf("size=%d dangerous=%t: resume = %q, want %q", size, dangerous, got, want)
			}
			if got := strings.Join(row.CompoundCommand(options, "capture learning"), " "); got != want+" capture learning" {
				t.Fatalf("compound = %q, want %q", got, want+" capture learning")
			}
		}
	}
}
