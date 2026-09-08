package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	importApplyTimeout = 30 * time.Second
	importRPCTimeout   = 10 * time.Second
	importCloseTimeout = 2 * time.Second
	importErrorMax     = 4 * 1024
)

// codexAppServerCommand is a process seam for tests. Production always uses
// Codex's native stdio app-server and passes conversation text only as JSON on
// stdin, never as shell arguments.
var codexAppServerCommand = func(ctx context.Context, cwd string) *exec.Cmd {
	command := exec.CommandContext(ctx, "codex", "app-server", "--stdio")
	command.Dir = cwd
	return command
}

var codexImportPathLocks = codexPathLockTable{locks: make(map[string]*codexPathLock)}

type codexPathLockTable struct {
	mu    sync.Mutex
	locks map[string]*codexPathLock
}

type codexPathLock struct {
	mu   sync.Mutex
	refs int
}

// codexImportPathLock prevents two showagent imports in this process from
// racing on one rollout. It is advisory: a separate Codex/Desktop/showagent
// process can still write concurrently. Prefix and persisted-item checks catch
// destructive overlap, but the native protocol currently offers no cross-
// process lock that showagent can enforce.
func acquireCodexImportPathLock(path string) func() {
	key := filepath.Clean(path)
	codexImportPathLocks.mu.Lock()
	lock := codexImportPathLocks.locks[key]
	if lock == nil {
		lock = &codexPathLock{}
		codexImportPathLocks.locks[key] = lock
	}
	lock.refs++
	codexImportPathLocks.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		codexImportPathLocks.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(codexImportPathLocks.locks, key)
		}
		codexImportPathLocks.mu.Unlock()
	}
}

func applyCodexContextImport(parent context.Context, plan ImportPlan) (ImportReceipt, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := importContext(parent)
	defer cancel()

	releasePathLock := acquireCodexImportPathLock(plan.Target.File)
	defer releasePathLock()

	before, err := observeImportRevision(plan.Target.File)
	if err != nil {
		return ImportReceipt{}, codexImportPreWriteError(err)
	}
	if !sameImportRevision(plan.ObservedRevision, before) {
		return ImportReceipt{}, ErrImportTargetChanged
	}

	client, err := startCodexImportClient(ctx, plan.Target.CWD)
	if err != nil {
		return ImportReceipt{}, codexImportPreWriteError(err)
	}
	defer client.close()

	if err := client.call(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "showagent-import", "version": "1"},
		"capabilities": map[string]bool{"experimentalApi": true},
	}, nil); err != nil {
		return ImportReceipt{}, codexImportPreWriteError(err)
	}
	if err := client.notify("initialized"); err != nil {
		return ImportReceipt{}, codexImportPreWriteError(err)
	}

	var read codexImportThreadResult
	if err := client.call(ctx, "thread/read", map[string]any{
		"threadId": plan.Target.ID, "includeTurns": false,
	}, &read); err != nil {
		return ImportReceipt{}, codexImportPreWriteError(err)
	}
	if err := verifyCodexRPCThread(plan.Target, read.Thread); err != nil {
		return ImportReceipt{}, codexImportPreWriteError(err)
	}

	var resumed codexImportThreadResult
	if err := client.call(ctx, "thread/resume", map[string]any{
		"threadId": plan.Target.ID,
		"cwd":      plan.Target.CWD,
	}, &resumed); err != nil {
		return ImportReceipt{}, codexImportPreWriteError(err)
	}
	if err := verifyCodexRPCThread(plan.Target, resumed.Thread); err != nil {
		return ImportReceipt{}, codexImportPreWriteError(err)
	}

	items := codexImportItems(plan.messages)
	if err := client.call(ctx, "thread/inject_items", map[string]any{
		"threadId": plan.Target.ID,
		"items":    items,
	}, nil); err != nil {
		// Once resume succeeds, unsubscribe is attempted even when injection
		// returns a protocol error. There is no blind retry or raw-file fallback.
		_ = client.call(ctx, "thread/unsubscribe", map[string]string{"threadId": plan.Target.ID}, nil)
		return ImportReceipt{}, err
	}
	if err := client.call(ctx, "thread/unsubscribe", map[string]string{"threadId": plan.Target.ID}, nil); err != nil {
		return ImportReceipt{}, err
	}
	client.close()

	after, err := verifyCodexImportPersistence(plan.Target.File, before, plan.messages)
	if err != nil {
		return ImportReceipt{}, err
	}
	receipt := importReceiptForPlan(plan, plan.Target)
	receipt.BeforeRevision = before
	receipt.AfterRevision = after
	return receipt, nil
}

func codexImportPreWriteError(err error) error {
	return fmt.Errorf("%w: %w", errImportNotStarted, err)
}

func importContext(parent context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := parent.Deadline(); hasDeadline {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, importApplyTimeout)
}

type codexImportThreadResult struct {
	Thread struct {
		ID   string `json:"id"`
		Path string `json:"path"`
		CWD  string `json:"cwd"`
	} `json:"thread"`
}

func verifyCodexRPCThread(target Row, thread struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	CWD  string `json:"cwd"`
}) error {
	if thread.ID != target.ID {
		return fmt.Errorf("codex app-server returned thread %q, want exact target %q", thread.ID, target.ID)
	}
	if !sameImportPath(thread.Path, target.File) {
		return fmt.Errorf("codex app-server returned a different native session file: %s", boundedImportText(thread.Path))
	}
	if !sameImportPath(thread.CWD, target.CWD) {
		return fmt.Errorf("codex app-server returned a different workspace: %s", boundedImportText(thread.CWD))
	}
	return nil
}

func codexImportItems(messages []ImportMessage) []map[string]any {
	items := make([]map[string]any, len(messages))
	for index, message := range messages {
		contentType := "output_text"
		if message.Role == "user" {
			contentType = "input_text"
		}
		items[index] = map[string]any{
			"type": "message",
			"role": message.Role,
			"content": []map[string]string{{
				"type": contentType,
				"text": message.Text,
			}},
		}
	}
	return items
}

func verifyCodexImportPersistence(path string, before ImportRevision, messages []ImportMessage) (ImportRevision, error) {
	file, err := os.Open(path)
	if err != nil {
		return ImportRevision{}, fmt.Errorf("%w: reopen Codex session: %v", ErrImportVerification, err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return ImportRevision{}, fmt.Errorf("%w: stat Codex session: %v", ErrImportVerification, err)
	}
	if info.Size() < before.Size {
		return ImportRevision{}, fmt.Errorf("%w: native session became shorter", ErrImportVerification)
	}

	prefixHash := sha256.New()
	fullHash := sha256.New()
	written, err := io.CopyN(io.MultiWriter(prefixHash, fullHash), file, before.Size)
	if err != nil || written != before.Size {
		return ImportRevision{}, fmt.Errorf("%w: read original native prefix", ErrImportVerification)
	}
	if hex.EncodeToString(prefixHash.Sum(nil)) != before.SHA256 {
		return ImportRevision{}, fmt.Errorf("%w: original native bytes are not an exact prefix", ErrImportVerification)
	}

	remaining := make(map[importMessageKey]int, len(messages))
	for _, message := range messages {
		remaining[importMessageKey(message)]++
	}
	scanner := bufio.NewScanner(io.TeeReader(file, fullHash))
	scanner.Buffer(make([]byte, 64*1024), scanBufferMax)
	for scanner.Scan() {
		var record codexLine
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Type != "response_item" {
			continue
		}
		var payload codexMessagePayload
		if json.Unmarshal(record.Payload, &payload) != nil || payload.Type != "message" {
			continue
		}
		key := importMessageKey{Role: payload.Role, Text: textFromContent(payload.Content)}
		if remaining[key] > 0 {
			remaining[key]--
		}
	}
	if err := scanner.Err(); err != nil {
		return ImportRevision{}, fmt.Errorf("%w: scan appended native records: %v", ErrImportVerification, err)
	}
	for message, count := range remaining {
		if count > 0 {
			return ImportRevision{}, fmt.Errorf("%w: %d %s message(s) missing after original prefix (text sha256 %s)",
				ErrImportVerification, count, message.Role, shortImportTextHash(message.Text))
		}
	}
	readSize, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return ImportRevision{}, fmt.Errorf("%w: locate verified Codex session end: %v", ErrImportVerification, err)
	}
	afterInfo, err := file.Stat()
	if err != nil {
		return ImportRevision{}, fmt.Errorf("%w: restat Codex session: %v", ErrImportVerification, err)
	}
	if readSize != afterInfo.Size() {
		return ImportRevision{}, fmt.Errorf("%w: native session changed while it was being verified", ErrImportVerification)
	}
	digest := hex.EncodeToString(fullHash.Sum(nil))
	mtime := afterInfo.ModTime().UTC()
	return ImportRevision{
		Size:     readSize,
		SHA256:   digest,
		ModTime:  mtime,
		Revision: fmt.Sprintf("%d:%d:%s", readSize, mtime.UnixNano(), digest),
	}, nil
}

type importMessageKey struct {
	Role string
	Text string
}

func shortImportTextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

func sameImportPath(left, right string) bool {
	left = normalizeImportPath(left)
	right = normalizeImportPath(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func normalizeImportPath(path string) string {
	path = strings.TrimSpace(path)
	if runtime.GOOS == "windows" {
		path = strings.TrimPrefix(path, `\\?\`)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	return filepath.Clean(path)
}

type codexImportRPCClient struct {
	command   *exec.Cmd
	stdin     io.WriteCloser
	responses chan codexImportRPCRead
	done      chan struct{}
	stderr    *boundedImportBuffer

	mu        sync.Mutex
	waitErr   error
	nextID    int64
	closeOnce sync.Once
}

type codexImportRPCRead struct {
	response codexImportRPCResponse
	err      error
}

type codexImportRPCResponse struct {
	ID     json.RawMessage      `json:"id"`
	Result json.RawMessage      `json:"result"`
	Error  *codexImportRPCError `json:"error"`
}

type codexImportRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func startCodexImportClient(ctx context.Context, cwd string) (*codexImportRPCClient, error) {
	command := codexAppServerCommand(ctx, cwd)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start Codex app-server stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start Codex app-server stdout: %w", err)
	}
	stderr := &boundedImportBuffer{limit: importErrorMax}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start `codex app-server --stdio`: %w", err)
	}

	client := &codexImportRPCClient{
		command: command, stdin: stdin, responses: make(chan codexImportRPCRead, 64),
		done: make(chan struct{}), stderr: stderr,
	}
	go client.read(stdout)
	go func() {
		err := command.Wait()
		client.mu.Lock()
		client.waitErr = err
		client.mu.Unlock()
		close(client.done)
	}()
	return client, nil
}

func (client *codexImportRPCClient) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), scanBufferMax)
	for scanner.Scan() {
		var response codexImportRPCResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			client.responses <- codexImportRPCRead{err: fmt.Errorf("decode Codex app-server response: %w", err)}
			return
		}
		client.responses <- codexImportRPCRead{response: response}
	}
	if err := scanner.Err(); err != nil {
		client.responses <- codexImportRPCRead{err: fmt.Errorf("read Codex app-server response: %w", err)}
	}
}

func (client *codexImportRPCClient) call(ctx context.Context, method string, params, result any) error {
	client.nextID++
	id := client.nextID
	request := map[string]any{"id": id, "method": method, "params": params}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Codex app-server %s request: %w", method, err)
	}
	data = append(data, '\n')
	if _, err := client.stdin.Write(data); err != nil {
		return client.processError(fmt.Sprintf("write Codex app-server %s request", method), err)
	}

	callCtx, cancel := context.WithTimeout(ctx, importRPCTimeout)
	defer cancel()
	for {
		select {
		case <-callCtx.Done():
			return fmt.Errorf("codex app-server %s timed out: %w", method, callCtx.Err())
		case <-client.done:
			return client.processError(fmt.Sprintf("Codex app-server exited during %s", method), nil)
		case read := <-client.responses:
			if read.err != nil {
				return client.processError(fmt.Sprintf("Codex app-server failed during %s", method), read.err)
			}
			if !rpcResponseIDEquals(read.response.ID, id) {
				// Notifications and server-initiated messages are unrelated to the
				// one sequential import request currently in flight.
				continue
			}
			if read.response.Error != nil {
				message := boundedImportText(read.response.Error.Message)
				if len(read.response.Error.Data) > 0 && string(read.response.Error.Data) != "null" {
					message += ": " + boundedImportText(string(read.response.Error.Data))
				}
				return fmt.Errorf("codex app-server %s error %d: %s", method, read.response.Error.Code, message)
			}
			if result != nil && len(read.response.Result) > 0 && string(read.response.Result) != "null" {
				if err := json.Unmarshal(read.response.Result, result); err != nil {
					return fmt.Errorf("decode Codex app-server %s result: %w", method, err)
				}
			}
			return nil
		}
	}
}

func (client *codexImportRPCClient) notify(method string) error {
	data, err := json.Marshal(map[string]any{"method": method})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := client.stdin.Write(data); err != nil {
		return client.processError(fmt.Sprintf("write Codex app-server %s notification", method), err)
	}
	return nil
}

func (client *codexImportRPCClient) closeClient() {
	client.closeOnce.Do(func() {
		_ = client.stdin.Close()
		select {
		case <-client.done:
			return
		case <-time.After(importCloseTimeout):
			if client.command.Process != nil {
				_ = client.command.Process.Kill()
			}
			select {
			case <-client.done:
			case <-time.After(importCloseTimeout):
			}
		}
	})
}

// close is kept as a method name used at call sites while the sync.Once field
// remains private implementation state.
func (client *codexImportRPCClient) close() {
	client.closeClient()
}

func (client *codexImportRPCClient) processError(prefix string, cause error) error {
	client.mu.Lock()
	waitErr := client.waitErr
	client.mu.Unlock()
	parts := []string{prefix}
	if cause != nil {
		parts = append(parts, cause.Error())
	}
	if waitErr != nil {
		parts = append(parts, waitErr.Error())
	}
	if stderr := strings.TrimSpace(client.stderr.String()); stderr != "" {
		parts = append(parts, stderr)
	}
	return errors.New(boundedImportText(strings.Join(parts, ": ")))
}

func rpcResponseIDEquals(raw json.RawMessage, want int64) bool {
	return string(bytes.TrimSpace(raw)) == strconv.FormatInt(want, 10)
}

type boundedImportBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (buffer *boundedImportBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := buffer.limit - len(buffer.data)
	if remaining > 0 {
		if len(data) < remaining {
			remaining = len(data)
		}
		buffer.data = append(buffer.data, data[:remaining]...)
	}
	return len(data), nil
}

func (buffer *boundedImportBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data)
}

func boundedImportText(value string) string {
	if len(value) <= importErrorMax {
		return value
	}
	return value[:importErrorMax] + "…"
}
