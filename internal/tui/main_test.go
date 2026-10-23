package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aytzey/showagent/internal/session"
)

// TestMain keeps the one-time star hint marker away from the real state dir.
func TestMain(m *testing.M) {
	state, err := os.MkdirTemp("", "showagent-tui-state-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("SHOWAGENT_STATE_DIR", state)
	code := m.Run()
	_ = os.RemoveAll(state)
	os.Exit(code)
}

func TestFirstConversionShowsStarHintOnce(t *testing.T) {
	t.Setenv("SHOWAGENT_STATE_DIR", t.TempDir())
	row := session.Row{Provider: session.ProviderCodex, ID: "new", LastAt: time.Now(), File: "/tmp/new.jsonl", FirstUser: "new message"}
	m := sizedModel([]session.Row{row})

	first, _ := m.Update(sessionMutationMsg{kind: mutationConvert, row: row})
	firstModel := asModel(t, first)
	if view := firstModel.View().Content; !strings.Contains(view, session.StarHint) {
		t.Fatalf("first conversion should show the star hint:\n%s", view)
	}

	second, _ := firstModel.Update(sessionMutationMsg{kind: mutationConvert, row: row})
	if view := asModel(t, second).View().Content; strings.Contains(view, session.StarHint) || !strings.Contains(view, "converted to codex") {
		t.Fatalf("second conversion should show the normal status only:\n%s", view)
	}
}
