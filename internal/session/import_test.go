package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	if receipt.Row.Provider != ProviderCodex || receipt.Row.ID == "" || receipt.Row.CWD != workspace {
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
