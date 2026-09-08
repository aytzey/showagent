package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestImportCapabilitiesExposeCreateAndProviderSpecificAppend(t *testing.T) {
	for _, provider := range ProviderOrder() {
		capability := ImportCapabilities(provider)
		if !capability.CanCreate {
			t.Fatalf("%s create capability = false", provider)
		}
		if provider == ProviderCodex {
			if !capability.CanAppendContext || capability.NativeHistoryVisible || !capability.RequiresCLI {
				t.Fatalf("Codex capability = %#v", capability)
			}
			continue
		}
		if capability.CanAppendContext || strings.TrimSpace(capability.Reason) == "" {
			t.Fatalf("%s append capability = %#v", provider, capability)
		}
	}

	unknown := ImportCapabilities(Provider("unknown"))
	if unknown.CanCreate || unknown.CanAppendContext || strings.TrimSpace(unknown.Reason) == "" {
		t.Fatalf("unknown capability = %#v", unknown)
	}
}

func TestApplyImportLocalCreateProvidersPreserveContentAndRetry(t *testing.T) {
	sources := []struct {
		name     string
		messages []ImportMessage
	}{
		{
			name: "user and assistant",
			messages: []ImportMessage{
				{Role: "user", Text: "Keep this decision — İstanbul.\n```go\n\twork()\n```"},
				{Role: "assistant", Text: "Yanıt 東京\n  Keep indentation and api_key=local-fixture-value."},
			},
		},
		{
			name:     "assistant only",
			messages: []ImportMessage{{Role: "assistant", Text: "Only an answer — 東京\n```text\n  preserved\n```"}},
		},
	}
	for _, provider := range []Provider{ProviderCodex, ProviderClaude, ProviderGemini, ProviderJCode, ProviderPi} {
		t.Run(string(provider), func(t *testing.T) {
			for _, source := range sources {
				t.Run(source.name, func(t *testing.T) {
					root := t.TempDir()
					setEmptyHomes(t, root)
					t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "state"))
					t.Setenv("PATH", filepath.Join(root, "empty-bin"))
					workspace := filepath.Join(root, "project with spaces")
					if err := os.MkdirAll(workspace, 0o700); err != nil {
						t.Fatal(err)
					}
					plan, err := PlanImport(ImportRequest{Mode: ImportCreate, Provider: provider, CWD: workspace, Messages: source.messages})
					if err != nil {
						t.Fatal(err)
					}
					first, err := ApplyImport(context.Background(), plan)
					if err != nil {
						t.Fatal(err)
					}
					if first.Row.ID == "" || first.Row.Provider != provider || !sameImportPath(first.Row.CWD, plan.CWD) {
						t.Fatalf("native target identity=%#v", first.Row)
					}
					before, err := os.ReadFile(first.Row.File)
					if err != nil {
						t.Fatal(err)
					}
					turns, err := Transcript(first.Row)
					if err != nil || !reflect.DeepEqual(turns, importTurns(source.messages)) {
						t.Fatalf("native transcript=%#v, err=%v, want=%#v", turns, err, source.messages)
					}
					repeatPlan, err := PlanImport(ImportRequest{Mode: ImportCreate, Provider: provider, CWD: workspace, Messages: source.messages})
					if err != nil {
						t.Fatal(err)
					}
					second, err := ApplyImport(context.Background(), repeatPlan)
					if err != nil || !second.NoOp || second.Row.ID != first.Row.ID || !sameImportPath(second.Row.CWD, plan.CWD) {
						t.Fatalf("native retry=%#v, err=%v", second, err)
					}
					after, err := os.ReadFile(second.Row.File)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("verified retry changed native bytes, err=%v", err)
					}
				})
			}
		})
	}
}

func TestApplyImportOpenCodeCreatePreservesContentAndRetryThroughCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the existing fake OpenCode CLI fixture uses a POSIX shell")
	}
	for _, source := range []struct {
		name     string
		messages []ImportMessage
	}{
		{"user and assistant", []ImportMessage{{Role: "user", Text: "Preserve Unicode — 東京."}, {Role: "assistant", Text: "```go\n\twork()\n```"}}},
		{"assistant only", []ImportMessage{{Role: "assistant", Text: "An answer without a user preview."}}},
	} {
		t.Run(source.name, func(t *testing.T) {
			root := t.TempDir()
			setEmptyHomes(t, root)
			t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "state"))
			fixtureDir := opencodeFixtureHome(t)
			withFakeOpenCode(t, fixtureDir)
			plan, err := PlanImport(ImportRequest{Mode: ImportCreate, Provider: ProviderOpenCode, CWD: root, Messages: source.messages})
			if err != nil {
				t.Fatal(err)
			}
			first, err := ApplyImport(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := os.ReadFile(filepath.Join(fixtureDir, "imported.json"))
			if err != nil {
				t.Fatal(err)
			}
			// Serve exactly what the import command received as the CLI's export
			// and make that new ID discoverable for the subsequent receipt check.
			if err := os.WriteFile(filepath.Join(fixtureDir, "export.json"), payload, 0o600); err != nil {
				t.Fatal(err)
			}
			sessions, err := json.Marshal([]map[string]any{{"id": first.Row.ID, "directory": plan.CWD, "title": "Imported conversation", "created": time.Now().UnixMilli(), "updated": time.Now().UnixMilli()}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fixtureDir, "sessions.json"), sessions, 0o600); err != nil {
				t.Fatal(err)
			}
			turns, err := Transcript(first.Row)
			if err != nil || !reflect.DeepEqual(turns, importTurns(source.messages)) {
				t.Fatalf("OpenCode exported transcript=%#v, err=%v", turns, err)
			}
			second, err := ApplyImport(context.Background(), plan)
			if err != nil || !second.NoOp || second.Row.ID != first.Row.ID || !sameImportPath(second.Row.CWD, plan.CWD) {
				t.Fatalf("OpenCode retry=%#v, err=%v", second, err)
			}
			calls, err := os.ReadFile(filepath.Join(fixtureDir, "calls.log"))
			if err != nil {
				t.Fatal(err)
			}
			imports := 0
			for _, call := range strings.Split(string(calls), "\n") {
				if strings.HasPrefix(call, "import ") {
					imports++
				}
			}
			if imports != 1 {
				t.Fatalf("OpenCode import invoked %d times, want exactly one", imports)
			}
		})
	}
}

func TestApplyImportCreatesNativeSessionInSelectedAbsoluteFolder(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "İstanbul project")
	if err := makeTestDir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "showagent-state"))

	messages := []ImportMessage{
		{Role: "user", Text: "Keep spacing:\r\n\r\n```go\r\n\twork()\r\n```"},
		{Role: "assistant", Text: "Tamam — spacing preserved."},
	}
	request := ImportRequest{
		Mode:     ImportCreate,
		Provider: ProviderCodex,
		CWD:      workspace,
		Messages: messages,
	}
	plan, err := PlanImport(request)
	if err != nil {
		t.Fatal(err)
	}

	// The plan owns an immutable content snapshot rather than retaining the
	// caller's parser/importer slice.
	messages[0].Text = "mutated after preview"
	receipt, err := ApplyImport(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Row.Provider != ProviderCodex || receipt.Row.ID == "" || receipt.Row.CWD != resolvedWorkspace {
		t.Fatalf("created row = %#v", receipt.Row)
	}

	got, err := Transcript(receipt.Row)
	if err != nil {
		t.Fatal(err)
	}
	want := []Turn{
		{Role: "user", Text: "Keep spacing:\n\n```go\n\twork()\n```"},
		{Role: "assistant", Text: "Tamam — spacing preserved."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("created transcript = %#v, want %#v", got, want)
	}
}

func TestApplyImportCreateIsIdempotentAfterCommittedReceipt(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	if err := makeTestDir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "showagent-state"))
	request := ImportRequest{
		Mode: ImportCreate, Provider: ProviderCodex, CWD: workspace,
		Messages: []ImportMessage{{Role: "user", Text: "same imported decision"}},
	}

	firstPlan, err := PlanImport(request)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ApplyImport(context.Background(), firstPlan)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := PlanImport(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ApplyImport(context.Background(), secondPlan)
	if err != nil {
		t.Fatal(err)
	}
	if second.NoOp != true || second.Row.ID != first.Row.ID {
		t.Fatalf("second receipt = %#v, want verified no-op for %q", second, first.Row.ID)
	}
	rows := codexProvider{}.Discover()
	if len(rows) != 1 {
		t.Fatalf("idempotent create wrote %d native sessions", len(rows))
	}
	receipts, err := filepath.Glob(filepath.Join(root, "showagent-state", "imports", "*.committed.json"))
	if err != nil || len(receipts) != 1 {
		t.Fatalf("committed receipts = %#v, err=%v", receipts, err)
	}
	stored, err := os.ReadFile(receipts[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "same imported decision") || strings.Contains(string(stored), workspace) {
		t.Fatalf("receipt persisted transcript or project path: %s", stored)
	}
}

func TestApplyImportAtomicCreateFailureRemainsRetryable(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "codex")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	blockedPath := filepath.Join(store, "sessions")
	if err := os.WriteFile(blockedPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", store)
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "state"))
	plan, err := PlanImport(ImportRequest{
		Mode: ImportCreate, Provider: ProviderCodex, CWD: root,
		Messages: []ImportMessage{{Role: "user", Text: "retry after repairing the native store"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyImport(context.Background(), plan); !errors.Is(err, errImportNotStarted) {
		t.Fatalf("blocked native create = %v, want safely retryable failure", err)
	}
	receipts, err := filepath.Glob(filepath.Join(root, "state", "imports", "*.json"))
	if err != nil || len(receipts) != 0 {
		t.Fatalf("failed atomic creation left receipts=%v, err=%v", receipts, err)
	}
	if err := os.Remove(blockedPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyImport(context.Background(), plan); err != nil {
		t.Fatalf("retry after repairing the native store: %v", err)
	}
}

func TestApplyImportCreateRetryVerifiesUnfilteredNativePrefix(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "state"))
	plan, err := PlanImport(ImportRequest{
		Mode: ImportCreate, Provider: ProviderCodex, CWD: root,
		Messages: []ImportMessage{
			{Role: "user", Text: "# AGENTS.md instructions\nPreserve this imported file example."},
			{Role: "assistant", Text: "The example is part of our conversation."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ApplyImport(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := ApplyImport(context.Background(), plan); err != nil || !second.NoOp || second.Row.ID != first.Row.ID {
		t.Fatalf("unchanged instruction-shaped import retry=%#v, err=%v", second, err)
	}
	file, err := os.OpenFile(first.Row.File, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"A later turn.\"}]}}\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append later turn: write=%v, close=%v", writeErr, closeErr)
	}
	if repeated, err := ApplyImport(context.Background(), plan); err != nil || !repeated.NoOp {
		t.Fatalf("continued instruction-shaped import retry=%#v, err=%v", repeated, err)
	}
	data, err := os.ReadFile(first.Row.File)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "Preserve this imported file example.", "This original content was replaced.", 1))
	if err := os.WriteFile(first.Row.File, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyImport(context.Background(), plan); !errors.Is(err, ErrImportVerification) {
		t.Fatalf("modified original import = %v, want verification failure", err)
	}
}

func TestApplyImportCreateRetryRejectsMovedWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "state"))
	plan, err := PlanImport(ImportRequest{
		Mode: ImportCreate, Provider: ProviderCodex, CWD: root,
		Messages: []ImportMessage{{Role: "user", Text: "same text, selected workspace"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ApplyImport(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(first.Row.File, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Codex's latest turn_context owns its current workspace, even while the
	// creation prefix remains intact.
	otherWorkspace := t.TempDir()
	encoder := json.NewEncoder(file)
	writeErr := encoder.Encode(map[string]any{"type": "turn_context", "payload": map[string]string{"cwd": otherWorkspace}})
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write changed workspace: write=%v, close=%v", writeErr, closeErr)
	}
	if _, err := ApplyImport(context.Background(), plan); !errors.Is(err, ErrImportVerification) || !strings.Contains(err.Error(), "different workspace") {
		t.Fatalf("moved target retry = %v, want workspace verification failure", err)
	}
}

func TestApplyImportJCodeCreateRetryWithoutCLIOrUserPreview(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JCODE_HOME", filepath.Join(root, "jcode"))
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("PATH", filepath.Join(root, "empty-bin"))
	plan, err := PlanImport(ImportRequest{
		Mode: ImportCreate, Provider: ProviderJCode, CWD: root,
		Messages: []ImportMessage{{Role: "assistant", Text: "An imported answer can be the entire conversation."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ApplyImport(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len((jcodeProvider{}).Discover()) != 0 {
		t.Fatal("fixture should be excluded by ordinary JCode discovery")
	}
	second, err := ApplyImport(context.Background(), plan)
	if err != nil || !second.NoOp || second.Row.ID != first.Row.ID || !sameImportPath(second.Row.CWD, plan.CWD) {
		t.Fatalf("assistant-only JCode retry without CLI=%#v, err=%v", second, err)
	}
	files, err := filepath.Glob(filepath.Join(root, "jcode", "sessions", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("JCode retry created native files=%v, err=%v", files, err)
	}
}

func TestJCodeImportReceiptLookupRejectsChangedOrUnsafeTarget(t *testing.T) {
	for _, scenario := range []string{"missing", "native id", "workspace", "traversal id", "stream id", "outside symlink"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("JCODE_HOME", filepath.Join(root, "jcode"))
			t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "state"))
			t.Setenv("PATH", filepath.Join(root, "empty-bin"))
			plan, err := PlanImport(ImportRequest{
				Mode: ImportCreate, Provider: ProviderJCode, CWD: root,
				Messages: []ImportMessage{{Role: "assistant", Text: "A known native session with an exact identity."}},
			})
			if err != nil {
				t.Fatal(err)
			}
			first, err := ApplyImport(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			id := first.Row.ID
			switch scenario {
			case "missing":
				if err := os.Remove(first.Row.File); err != nil {
					t.Fatal(err)
				}
			case "native id", "workspace":
				native, ok := readJCodeSession(first.Row.File)
				if !ok {
					t.Fatal("could not read fixture")
				}
				if scenario == "native id" {
					native.ID = "another_native_session"
				} else {
					native.WorkingDir = t.TempDir()
				}
				data, err := json.Marshal(native)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(first.Row.File, data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "traversal id":
				id = "../" + id
			case "stream id":
				id += ":stream"
			case "outside symlink":
				outside := filepath.Join(t.TempDir(), filepath.Base(first.Row.File))
				if err := os.Rename(first.Row.File, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, first.Row.File); err != nil {
					t.Skipf("file symlinks unavailable: %v", err)
				}
			}
			if _, err := importJCodeReceiptTarget(plan.CWD, id); !errors.Is(err, ErrImportVerification) {
				t.Fatalf("unsafe or changed target lookup = %v, want verification failure", err)
			}
		})
	}
}

func TestPlanImportSupportsSymlinkedNativeStore(t *testing.T) {
	target, _ := writeCodexImportTarget(t)
	store := filepath.Join(os.Getenv("CODEX_HOME"), "sessions")
	realStore := filepath.Join(os.Getenv("CODEX_HOME"), "actual-sessions")
	if err := os.Rename(store, realStore); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realStore, store); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	plan, err := PlanImport(ImportRequest{
		Mode: ImportAppendContext, Target: target,
		Messages: []ImportMessage{{Role: "user", Text: "use the configured native store"}},
	})
	if err != nil {
		t.Fatalf("symlinked configured store: %v", err)
	}
	if !pathWithin(realStore, plan.Target.File) {
		t.Fatalf("target was not resolved inside the configured store: %s", plan.Target.File)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	data, err := os.ReadFile(plan.Target.File)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	target.File = outside
	if _, err := PlanImport(ImportRequest{Mode: ImportAppendContext, Target: target, Messages: plan.ReviewedMessages}); err == nil {
		t.Fatal("outside-store session was accepted")
	}
}

func TestPlanImportRejectsEscapedRecordsBeyondNativeReaderLimit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "state"))
	for _, provider := range []Provider{ProviderCodex, ProviderClaude, ProviderPi, ProviderGemini} {
		for _, character := range []string{"\"", "\\"} {
			_, err := PlanImport(ImportRequest{
				Mode: ImportCreate, Provider: provider, CWD: root,
				Messages: []ImportMessage{{Role: "user", Text: strings.Repeat(character, 9*1024*1024)}},
			})
			if err == nil || !strings.Contains(err.Error(), "native record limit after JSON escaping") {
				t.Fatalf("%s escaped %q message: %v", provider, character, err)
			}
		}
		if _, err := PlanImport(ImportRequest{
			Mode: ImportCreate, Provider: provider, CWD: root,
			Messages: []ImportMessage{{Role: "user", Text: "small \\\"escaped\\\" example"}},
		}); err != nil {
			t.Fatalf("%s small escaped message: %v", provider, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "state")); !os.IsNotExist(err) {
		t.Fatalf("planning wrote receipt state: %v", err)
	}
	_, err := PlanImport(ImportRequest{
		Mode: ImportCreate, Provider: ProviderGemini, CWD: root,
		Messages: []ImportMessage{
			{Role: "user", Text: strings.Repeat("\"", 5*1024*1024)},
			{Role: "assistant", Text: strings.Repeat("\\", 4*1024*1024)},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "import conversation exceeds") {
		t.Fatalf("Gemini whole conversation record limit: %v", err)
	}
}

func TestApplyImportStopsBeforeNativeWriteForPreparedReceipt(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		root := t.TempDir()
		workspace := filepath.Join(root, "project")
		if err := makeTestDir(workspace); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
		t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "showagent-state"))

		plan, err := PlanImport(ImportRequest{
			Mode: ImportCreate, Provider: ProviderCodex, CWD: workspace,
			Messages: []ImportMessage{{Role: "user", Text: "must not be written twice"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if prior, _, err := prepareImportReceipt(plan); err != nil || prior != nil {
			t.Fatalf("seed prepared receipt: prior=%#v, err=%v", prior, err)
		}

		writes := 0
		previousRegistry := registry
		registry = append([]ProviderImpl(nil), registry...)
		for index, impl := range registry {
			if impl.Name() == ProviderCodex {
				registry[index] = countingImportProvider{ProviderImpl: impl, writes: &writes}
				break
			}
		}
		defer func() { registry = previousRegistry }()

		_, err = ApplyImport(context.Background(), plan)
		if !errors.Is(err, ErrImportOutcomeUncertain) {
			t.Fatalf("ApplyImport error = %v, want ErrImportOutcomeUncertain", err)
		}
		if writes != 0 {
			t.Fatalf("native provider writer called %d times", writes)
		}
	})

	t.Run("codex append", func(t *testing.T) {
		target, original := writeCodexImportTarget(t)
		plan, err := PlanImport(ImportRequest{
			Mode: ImportAppendContext, Target: target,
			Messages: []ImportMessage{{Role: "assistant", Text: "must not be injected twice"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if prior, _, err := prepareImportReceipt(plan); err != nil || prior != nil {
			t.Fatalf("seed prepared receipt: prior=%#v, err=%v", prior, err)
		}

		starts := 0
		previousCommand := codexAppServerCommand
		codexAppServerCommand = func(ctx context.Context, cwd string) *exec.Cmd {
			starts++
			return previousCommand(ctx, cwd)
		}
		defer func() { codexAppServerCommand = previousCommand }()

		_, err = ApplyImport(context.Background(), plan)
		if !errors.Is(err, ErrImportOutcomeUncertain) {
			t.Fatalf("ApplyImport error = %v, want ErrImportOutcomeUncertain", err)
		}
		if starts != 0 {
			t.Fatalf("Codex app-server started %d times", starts)
		}
		after, err := os.ReadFile(target.File)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, original) {
			t.Fatal("Codex target changed despite prepared-receipt guard")
		}
	})
}

func TestApplyImportMissingRequiredCLIRemainsRetryable(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	if err := makeTestDir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "showagent-state"))
	t.Setenv("PATH", filepath.Join(root, "empty-bin"))

	plan, err := PlanImport(ImportRequest{
		Mode: ImportCreate, Provider: ProviderOpenCode, CWD: workspace,
		Messages: []ImportMessage{{Role: "user", Text: "retry after installing the CLI"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	writes := 0
	previousRegistry := registry
	registry = append([]ProviderImpl(nil), registry...)
	for index, impl := range registry {
		if impl.Name() == ProviderOpenCode {
			registry[index] = countingImportProvider{ProviderImpl: impl, writes: &writes}
			break
		}
	}
	defer func() { registry = previousRegistry }()

	for attempt := 1; attempt <= 2; attempt++ {
		_, err = ApplyImport(context.Background(), plan)
		if err == nil || !strings.Contains(err.Error(), "opencode not found in PATH") {
			t.Fatalf("attempt %d error = %v, want missing CLI", attempt, err)
		}
		if errors.Is(err, ErrImportOutcomeUncertain) {
			t.Fatalf("attempt %d became outcome-uncertain before a native write", attempt)
		}
	}
	if writes != 0 {
		t.Fatalf("native provider writer called %d times", writes)
	}
	receipts, err := filepath.Glob(filepath.Join(root, "showagent-state", "imports", "*.json"))
	if err != nil || len(receipts) != 0 {
		t.Fatalf("pre-write failure left receipts = %#v, err=%v", receipts, err)
	}
}

type countingImportProvider struct {
	ProviderImpl
	writes *int
}

func (provider countingImportProvider) WriteConverted(source Row, turns []Turn) (Row, error) {
	*provider.writes++
	return provider.ProviderImpl.WriteConverted(source, turns)
}

func TestPlanImportValidatesMessagesAndCreateFolder(t *testing.T) {
	workspace := t.TempDir()
	tests := []struct {
		name    string
		request ImportRequest
		want    string
	}{
		{
			name: "relative folder",
			request: ImportRequest{Mode: ImportCreate, Provider: ProviderCodex, CWD: ".",
				Messages: []ImportMessage{{Role: "user", Text: "hello"}}},
			want: "absolute",
		},
		{
			name: "missing folder",
			request: ImportRequest{Mode: ImportCreate, Provider: ProviderCodex, CWD: filepath.Join(workspace, "missing"),
				Messages: []ImportMessage{{Role: "user", Text: "hello"}}},
			want: "workspace",
		},
		{
			name: "empty text",
			request: ImportRequest{Mode: ImportCreate, Provider: ProviderCodex, CWD: workspace,
				Messages: []ImportMessage{{Role: "user", Text: " \r\n\t"}}},
			want: "empty",
		},
		{
			name: "unassigned role",
			request: ImportRequest{Mode: ImportCreate, Provider: ProviderCodex, CWD: workspace,
				Messages: []ImportMessage{{Role: "unassigned", Text: "hello"}}},
			want: "role",
		},
		{
			name:    "no messages",
			request: ImportRequest{Mode: ImportCreate, Provider: ProviderCodex, CWD: workspace},
			want:    "no user or assistant",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PlanImport(tt.request)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("PlanImport error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestPlanImportRejectsAppendForNonCodexProvider(t *testing.T) {
	_, err := PlanImport(ImportRequest{
		Mode: ImportAppendContext,
		Target: Row{
			Provider: ProviderClaude,
			ID:       "exact-id",
			CWD:      t.TempDir(),
			File:     filepath.Join(t.TempDir(), "session.jsonl"),
		},
		Messages: []ImportMessage{{Role: "user", Text: "hello"}},
	})
	if err == nil || !errors.Is(err, ErrImportAppendUnavailable) {
		t.Fatalf("PlanImport error = %v", err)
	}
}
