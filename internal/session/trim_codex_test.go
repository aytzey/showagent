package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testCodexNativeTurns = `[
 {"id":"native-first","items":[{"type":"userMessage","content":[{"type":"text","text":"keep first question"}]},{"type":"agentMessage","text":"keep first answer"}]},
 {"id":"native-second","items":[{"type":"userMessage","content":[{"type":"text","text":"remove second question"}]},{"type":"agentMessage","text":"remove second answer"}]}
]`

func newCodexTrimFixture(t *testing.T) (Row, Row, []byte) {
	t.Helper()
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	original, err := writeCodexConverted(Row{CWD: t.TempDir()}, []Turn{
		{Role: "user", Text: "keep first question"},
		{Role: "assistant", Text: "keep first answer"},
		{Role: "user", Text: "remove second question"},
		{Role: "assistant", Text: "remove second answer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(original.File)
	if err != nil {
		t.Fatal(err)
	}
	revised := original
	revised.File = strings.TrimSuffix(original.File, ".jsonl") + "_5287a80e-3fea-4624-ae1b-434f23abc951.jsonl"
	metadata := source[:bytes.IndexByte(source, '\n')+1]
	if err := os.WriteFile(revised.File, metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	return original, revised, source
}

func TestTrimCodexNativeRevertKeepsCanonicalIdentity(t *testing.T) {
	row, revised, original := newCodexTrimFixture(t)
	log := filepath.Join(t.TempDir(), "methods")
	installFakeCodexAppServer(t, row, log, fakeCodexAppServerOptions{
		NativeHistoryMode: "paginated", NativeTurns: testCodexNativeTurns,
		RevertedPath: revised.File, BeforeTurn: "native-second",
	})
	if CanTrimThroughUser(row) {
		t.Fatal("Codex exposed a partial native turn trim")
	}
	if _, err := PlanTrim(row, HandoffOptions{ThroughUser: 1}); err == nil {
		t.Fatal("allowed trim inside a native turn")
	}
	plan, err := PlanTrim(row, HandoffOptions{BeforeUser: 2})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Boundary.Number != 2 || plan.Boundary.Text != "remove second question" || plan.KeptTurns != 2 || plan.RemovedTurns != 2 ||
		containsString(readMethodLog(t, log), "thread/revert") {
		t.Fatalf("invalid or mutating Codex preview: %+v", plan)
	}
	updated, err := ApplyTrim(plan)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != row.ID || !sameImportPath(updated.File, revised.File) || updated.LastUser != "keep first question" {
		t.Fatalf("wrong updated native row: %+v", updated)
	}
	oldBytes, _ := os.ReadFile(row.File)
	if !bytes.Equal(oldBytes, original) {
		t.Fatal("showagent edited the provider's referenced rollout")
	}
	if methods := readMethodLog(t, log); !containsString(methods, "thread/revert") || containsString(methods, "turn/start") {
		t.Fatalf("unexpected apply RPCs: %v", methods)
	}

	readLog := filepath.Join(t.TempDir(), "read-methods")
	retained := strings.SplitN(testCodexNativeTurns, ",\n", 2)[0] + "]"
	installFakeCodexAppServer(t, revised, readLog, fakeCodexAppServerOptions{NativeHistoryMode: "paginated", NativeTurns: retained})
	for range 2 {
		rows := discoverCodex(defaultCodexHome())
		if len(rows) != 1 || rows[0].ID != row.ID || !sameImportPath(rows[0].File, revised.File) || rows[0].LastUser != "keep first question" {
			t.Fatalf("discovery exposed a retired rollout: %+v", rows)
		}
	}
	if methods := readMethodLog(t, readLog); strings.Count(strings.Join(methods, "\n"), "thread/turns/list") != 1 {
		t.Fatalf("unchanged canonical revision failed to reuse preview: %v", methods)
	}
	turns, err := Transcript(updated)
	if err != nil || !slices.Equal(turns, []Turn{{Role: "user", Text: "keep first question"}, {Role: "assistant", Text: "keep first answer"}}) {
		t.Fatalf("retired rollout content leaked into transcript: %+v, %v", turns, err)
	}
}

func TestTrimCodexRejectsStaleNativeProjectionBeforeRevert(t *testing.T) {
	for _, mismatch := range []string{"path", "history"} {
		t.Run(mismatch, func(t *testing.T) {
			row, revised, original := newCodexTrimFixture(t)
			installFakeCodexAppServer(t, row, filepath.Join(t.TempDir(), "read"), fakeCodexAppServerOptions{
				NativeHistoryMode: "paginated", NativeTurns: testCodexNativeTurns,
			})
			plan, err := PlanTrim(row, HandoffOptions{BeforeUser: 2})
			if err != nil {
				t.Fatal(err)
			}
			options := fakeCodexAppServerOptions{NativeHistoryMode: "paginated", NativeTurns: testCodexNativeTurns}
			if mismatch == "path" {
				options.ThreadReadTarget = &revised
			} else {
				options.NativeTurns = strings.Replace(testCodexNativeTurns, "remove second answer", "replacement answer", 1)
			}
			log := filepath.Join(t.TempDir(), "apply")
			installFakeCodexAppServer(t, row, log, options)
			if _, err := ApplyTrim(plan); !errors.Is(err, ErrTrimChanged) {
				t.Fatalf("unexpected stale plan result: %v", err)
			}
			actual, _ := os.ReadFile(row.File)
			if !bytes.Equal(actual, original) || containsString(readMethodLog(t, log), "thread/revert") {
				t.Fatal("stale native plan reached the write operation")
			}
		})
	}
}

func TestCodexCompressedRevisionDoesNotExposeRetiredRollout(t *testing.T) {
	for _, nativeReader := range []bool{true, false} {
		name := "native reader unavailable"
		if nativeReader {
			name = "native reader resolves the logical path"
		}
		t.Run(name, func(t *testing.T) {
			row, revision, original := newCodexTrimFixture(t)
			compressed := revision.File + ".zst"
			// Discovery treats compressed bytes as opaque. Codex owns decoding.
			if err := os.WriteFile(compressed, []byte{0x28, 0xb5, 0x2f, 0xfd, 0x20, 0x00, 0x01, 0x00, 0x00}, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(revision.File); err != nil {
				t.Fatal(err)
			}
			options := fakeCodexAppServerOptions{NativeHistoryMode: "paginated", NativeTurns: strings.SplitN(testCodexNativeTurns, ",\n", 2)[0] + "]"}
			if !nativeReader {
				options.ErrorMethod = "initialize"
			}
			installFakeCodexAppServer(t, revision, filepath.Join(t.TempDir(), "compressed"), options)
			rows := discoverCodex(defaultCodexHome())
			if len(rows) != 1 || rows[0].ID != row.ID || !sameImportPath(rows[0].File, compressed) {
				t.Fatalf("discovery ignored the canonical compressed revision: %+v", rows)
			}
			turns, err := Transcript(rows[0])
			if nativeReader {
				if err != nil || !slices.Equal(turns, []Turn{{Role: "user", Text: "keep first question"}, {Role: "assistant", Text: "keep first answer"}}) ||
					rows[0].LastUser != "keep first question" {
					t.Fatalf("compressed revision did not use native retained history: %+v %+v %v", rows, turns, err)
				}
			} else if rows[0].FirstUser != "" || rows[0].LastUser != "" || err == nil || len(turns) != 0 {
				t.Fatalf("unavailable native reader leaked a retired rollout: %+v %+v %v", rows, turns, err)
			}
			unchanged, readErr := os.ReadFile(row.File)
			if readErr != nil || !bytes.Equal(unchanged, original) {
				t.Fatalf("redoing discovery changed the retired rollout: %v", readErr)
			}
		})
	}
}
