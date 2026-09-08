package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestApplyImportAppendsCodexContextWithSameIDAndNoModelTurn(t *testing.T) {
	target, original := writeCodexImportTarget(t)
	logPath := filepath.Join(t.TempDir(), "methods.log")
	installFakeCodexAppServer(t, target, logPath, fakeCodexAppServerOptions{})

	messages := []ImportMessage{
		{Role: "user", Text: "Imported question. Türkçe\r\n```text\r\n  keep spaces\r\n```"},
		{Role: "assistant", Text: "Imported answer."},
	}
	plan, err := PlanImport(ImportRequest{
		Mode:     ImportAppendContext,
		Target:   target,
		Messages: messages,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ApplyImport(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Row.ID != target.ID || receipt.Row.Provider != ProviderCodex {
		t.Fatalf("receipt target = %#v, want same Codex id %q", receipt.Row, target.ID)
	}
	if receipt.NativeHistoryVisible {
		t.Fatal("Codex injected context must not claim native chat-bubble visibility")
	}

	after, err := os.ReadFile(target.File)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), string(original)) {
		t.Fatal("original target bytes are not an exact prefix after append")
	}
	wantMethods := []string{"initialize", "initialized", "thread/read", "thread/resume", "thread/inject_items", "thread/unsubscribe"}
	methods := readMethodLog(t, logPath)
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("app-server methods = %#v, want %#v", methods, wantMethods)
	}
	for _, forbidden := range []string{"turn/start", "turn/steer", "exec"} {
		if strings.Contains(strings.Join(methods, "\n"), forbidden) {
			t.Fatalf("model/runtime method %q was called", forbidden)
		}
	}

	repeatPlan, err := PlanImport(ImportRequest{
		Mode: ImportAppendContext, Target: target, Messages: messages,
	})
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := ApplyImport(context.Background(), repeatPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !repeat.NoOp || repeat.Row.ID != target.ID {
		t.Fatalf("repeat receipt = %#v, want verified same-ID no-op", repeat)
	}
	if repeatedMethods := readMethodLog(t, logPath); !reflect.DeepEqual(repeatedMethods, wantMethods) {
		t.Fatalf("duplicate import called app-server again: %#v", repeatedMethods)
	}
}

func TestApplyImportReadsCodexThreadWithoutTurns(t *testing.T) {
	target, _ := writeCodexImportTarget(t)
	logPath := filepath.Join(t.TempDir(), "methods.log")
	installFakeCodexAppServer(t, target, logPath, fakeCodexAppServerOptions{
		RejectThreadReadWithTurns: true,
	})

	plan, err := PlanImport(ImportRequest{
		Mode:     ImportAppendContext,
		Target:   target,
		Messages: []ImportMessage{{Role: "user", Text: "small import"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyImport(context.Background(), plan); err != nil {
		t.Fatalf("ApplyImport loaded existing turns during thread/read: %v", err)
	}
	if methods := readMethodLog(t, logPath); !containsString(methods, "thread/inject_items") {
		t.Fatalf("import did not reach injection after metadata-only thread/read: %#v", methods)
	}
}

func TestApplyImportRejectsMismatchedCodexRPCThreadBeforeInjection(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		field     string
		wantError string
	}{
		{name: "read id", method: "thread/read", field: "id", wantError: "returned thread"},
		{name: "read path", method: "thread/read", field: "path", wantError: "different native session file"},
		{name: "read cwd", method: "thread/read", field: "cwd", wantError: "different workspace"},
		{name: "resume id", method: "thread/resume", field: "id", wantError: "returned thread"},
		{name: "resume path", method: "thread/resume", field: "path", wantError: "different native session file"},
		{name: "resume cwd", method: "thread/resume", field: "cwd", wantError: "different workspace"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, _ := writeCodexImportTarget(t)
			logPath := filepath.Join(t.TempDir(), "methods.log")
			mismatch := target
			switch test.field {
			case "id":
				mismatch.ID = "different-thread-id"
			case "path":
				mismatch.File = filepath.Join(t.TempDir(), "different-rollout.jsonl")
			case "cwd":
				mismatch.CWD = filepath.Join(t.TempDir(), "different-workspace")
			default:
				t.Fatalf("unknown mismatch field %q", test.field)
			}

			options := fakeCodexAppServerOptions{}
			if test.method == "thread/read" {
				options.ThreadReadTarget = &mismatch
			} else {
				options.ThreadResumeTarget = &mismatch
			}
			installFakeCodexAppServer(t, target, logPath, options)

			plan, err := PlanImport(ImportRequest{
				Mode:     ImportAppendContext,
				Target:   target,
				Messages: []ImportMessage{{Role: "assistant", Text: "must not be injected"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = ApplyImport(context.Background(), plan)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ApplyImport error = %v, want mismatch error containing %q", err, test.wantError)
			}

			methods := readMethodLog(t, logPath)
			if containsString(methods, "thread/inject_items") {
				t.Fatalf("mismatched %s %s reached injection: %#v", test.method, test.field, methods)
			}
			if !containsString(methods, test.method) {
				t.Fatalf("fake server did not exercise %s mismatch: %#v", test.method, methods)
			}
		})
	}
}

func TestApplyImportRequiresReviewAgainWhenTargetChangedAfterPlan(t *testing.T) {
	target, _ := writeCodexImportTarget(t)
	started := false
	previous := codexAppServerCommand
	codexAppServerCommand = func(ctx context.Context, cwd string) *exec.Cmd {
		started = true
		return previous(ctx, cwd)
	}
	t.Cleanup(func() { codexAppServerCommand = previous })

	plan, err := PlanImport(ImportRequest{
		Mode:     ImportAppendContext,
		Target:   target,
		Messages: []ImportMessage{{Role: "assistant", Text: "planned answer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(target.File, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("{\"type\":\"external-change\"}\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("mutate target: write=%v close=%v", writeErr, closeErr)
	}

	_, err = ApplyImport(context.Background(), plan)
	if err == nil || !errorsIs(err, ErrImportTargetChanged) || !strings.Contains(strings.ToLower(err.Error()), "review") {
		t.Fatalf("ApplyImport error = %v, want review-again target change", err)
	}
	if started {
		t.Fatal("app-server started even though the observed target revision changed")
	}
}

func TestApplyImportReturnsBoundedCodexProtocolErrorAndCleansUp(t *testing.T) {
	target, _ := writeCodexImportTarget(t)
	logPath := filepath.Join(t.TempDir(), "methods.log")
	installFakeCodexAppServer(t, target, logPath, fakeCodexAppServerOptions{
		ErrorMethod: "thread/inject_items",
	})

	plan, err := PlanImport(ImportRequest{
		Mode:     ImportAppendContext,
		Target:   target,
		Messages: []ImportMessage{{Role: "user", Text: "will fail"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyImport(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "thread/inject_items") {
		t.Fatalf("ApplyImport error = %v", err)
	}
	if len(err.Error()) > importErrorMax+512 {
		t.Fatalf("protocol error was not bounded: %d bytes", len(err.Error()))
	}
	methods := readMethodLog(t, logPath)
	if !containsString(methods, "thread/unsubscribe") {
		t.Fatalf("app-server was not unsubscribed after protocol error: %#v", methods)
	}
}

func TestCodexAppServerHelperProcess(t *testing.T) {
	if os.Getenv("SHOWAGENT_FAKE_CODEX_APP_SERVER") != "1" {
		return
	}

	targetPath := os.Getenv("SHOWAGENT_FAKE_CODEX_TARGET")
	logPath := os.Getenv("SHOWAGENT_FAKE_CODEX_LOG")
	errorMethod := os.Getenv("SHOWAGENT_FAKE_CODEX_ERROR_METHOD")
	readTarget := Row{
		ID:   os.Getenv("SHOWAGENT_FAKE_CODEX_READ_ID"),
		File: os.Getenv("SHOWAGENT_FAKE_CODEX_READ_PATH"),
		CWD:  os.Getenv("SHOWAGENT_FAKE_CODEX_READ_CWD"),
	}
	resumeTarget := Row{
		ID:   os.Getenv("SHOWAGENT_FAKE_CODEX_RESUME_ID"),
		File: os.Getenv("SHOWAGENT_FAKE_CODEX_RESUME_PATH"),
		CWD:  os.Getenv("SHOWAGENT_FAKE_CODEX_RESUME_CWD"),
	}
	rejectThreadReadWithTurns := os.Getenv("SHOWAGENT_FAKE_CODEX_REJECT_READ_WITH_TURNS") == "true"

	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(11)
		}
		appendFakeMethod(logPath, request.Method)
		if request.ID == 0 {
			continue
		}
		if request.Method == errorMethod {
			_ = encoder.Encode(map[string]any{"id": request.ID, "error": map[string]any{
				"code": -32001, "message": "fake protocol failure " + strings.Repeat("x", importErrorMax*2),
			}})
			continue
		}

		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
		case "thread/read":
			if rejectThreadReadWithTurns {
				var params struct {
					IncludeTurns *bool `json:"includeTurns"`
				}
				if err := json.Unmarshal(request.Params, &params); err != nil {
					os.Exit(18)
				}
				if params.IncludeTurns != nil && *params.IncludeTurns {
					_ = encoder.Encode(map[string]any{"id": request.ID, "error": map[string]any{
						"code": -32602, "message": "thread/read must not load existing turns",
					}})
					continue
				}
			}
			result = map[string]any{"thread": map[string]any{
				"id": readTarget.ID, "path": readTarget.File, "cwd": readTarget.CWD,
			}}
		case "thread/resume":
			result = map[string]any{"thread": map[string]any{
				"id": resumeTarget.ID, "path": resumeTarget.File, "cwd": resumeTarget.CWD,
			}}
		case "thread/inject_items":
			var params struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				os.Exit(12)
			}
			file, err := os.OpenFile(targetPath, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				os.Exit(13)
			}
			fileEncoder := json.NewEncoder(file)
			for _, item := range params.Items {
				if err := fileEncoder.Encode(map[string]any{
					"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
					"type":      "response_item",
					"payload":   item,
				}); err != nil {
					os.Exit(14)
				}
			}
			if err := file.Close(); err != nil {
				os.Exit(15)
			}
		case "thread/unsubscribe":
			result = map[string]any{"status": "unsubscribed"}
		default:
			_ = encoder.Encode(map[string]any{"id": request.ID, "error": map[string]any{
				"code": -32601, "message": "unexpected method " + request.Method,
			}})
			continue
		}
		if err := encoder.Encode(map[string]any{"id": request.ID, "result": result}); err != nil {
			os.Exit(16)
		}
	}
	if err := scanner.Err(); err != nil {
		os.Exit(17)
	}
	os.Exit(0)
}

func writeCodexImportTarget(t *testing.T) (Row, []byte) {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	if err := makeTestDir(workspace); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(root, "codex")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("SHOWAGENT_STATE_DIR", filepath.Join(root, "showagent-state"))
	id := "5287a80e-3fea-4624-ae1b-434f23abc951"
	path := filepath.Join(codexHome, "sessions", "2026", "09", "08", "rollout-2026-09-08T08-26-31-"+id+".jsonl")
	if err := makeTestDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	records := []map[string]any{
		{"timestamp": "2026-09-08T08:26:31Z", "type": "session_meta", "payload": map[string]any{
			"id": id, "timestamp": "2026-09-08T08:26:31Z", "cwd": workspace,
			"originator": "showagent", "source": "cli", "thread_source": "user",
		}},
		{"timestamp": "2026-09-08T08:26:32Z", "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "original request"}},
		}},
		{"timestamp": "2026-09-08T08:26:33Z", "type": "response_item", "payload": map[string]any{
			"type": "message", "role": "assistant", "content": []map[string]string{{"type": "output_text", "text": "original answer"}},
		}},
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return Row{Provider: ProviderCodex, ID: id, CWD: workspace, LaunchCWD: workspace, File: path}, original
}

type fakeCodexAppServerOptions struct {
	ErrorMethod               string
	RejectThreadReadWithTurns bool
	ThreadReadTarget          *Row
	ThreadResumeTarget        *Row
}

func installFakeCodexAppServer(t *testing.T, target Row, logPath string, options fakeCodexAppServerOptions) {
	t.Helper()
	readTarget := target
	if options.ThreadReadTarget != nil {
		readTarget = *options.ThreadReadTarget
	}
	resumeTarget := target
	if options.ThreadResumeTarget != nil {
		resumeTarget = *options.ThreadResumeTarget
	}
	previous := codexAppServerCommand
	codexAppServerCommand = func(ctx context.Context, cwd string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCodexAppServerHelperProcess", "--")
		command.Dir = cwd
		command.Env = append(os.Environ(),
			"SHOWAGENT_FAKE_CODEX_APP_SERVER=1",
			"SHOWAGENT_FAKE_CODEX_TARGET="+target.File,
			"SHOWAGENT_FAKE_CODEX_LOG="+logPath,
			"SHOWAGENT_FAKE_CODEX_ERROR_METHOD="+options.ErrorMethod,
			"SHOWAGENT_FAKE_CODEX_READ_ID="+readTarget.ID,
			"SHOWAGENT_FAKE_CODEX_READ_PATH="+readTarget.File,
			"SHOWAGENT_FAKE_CODEX_READ_CWD="+readTarget.CWD,
			"SHOWAGENT_FAKE_CODEX_RESUME_ID="+resumeTarget.ID,
			"SHOWAGENT_FAKE_CODEX_RESUME_PATH="+resumeTarget.File,
			"SHOWAGENT_FAKE_CODEX_RESUME_CWD="+resumeTarget.CWD,
			fmt.Sprintf("SHOWAGENT_FAKE_CODEX_REJECT_READ_WITH_TURNS=%t", options.RejectThreadReadWithTurns),
		)
		return command
	}
	t.Cleanup(func() { codexAppServerCommand = previous })
}

func appendFakeMethod(path, method string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(21)
	}
	_, err = fmt.Fprintln(file, method)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		os.Exit(22)
	}
}

func readMethodLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func makeTestDir(path string) error {
	return os.MkdirAll(path, 0o700)
}

func errorsIs(err, target error) bool {
	return err != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
