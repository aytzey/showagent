package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	importReceiptVersion   = 1
	importReceiptPrepared  = "prepared"
	importReceiptCommitted = "committed"
	importRepresentation   = "role_text_v1"
)

type storedImportReceipt struct {
	Version              int            `json:"version"`
	Key                  string         `json:"key"`
	Status               string         `json:"status"`
	OperationID          string         `json:"operation_id"`
	Mode                 ImportMode     `json:"mode"`
	Provider             Provider       `json:"provider"`
	TargetID             string         `json:"target_id,omitempty"`
	MessageCount         int            `json:"message_count"`
	SourceSnapshotHash   string         `json:"source_snapshot_hash"`
	Representation       string         `json:"representation"`
	BeforeRevision       ImportRevision `json:"before_revision,omitempty"`
	AfterRevision        ImportRevision `json:"after_revision,omitempty"`
	NativeHistoryVisible bool           `json:"native_history_visible"`
	CreatedAt            time.Time      `json:"created_at"`
	CommittedAt          *time.Time     `json:"committed_at,omitempty"`
}

func prepareImportReceipt(plan ImportPlan) (*storedImportReceipt, string, error) {
	directory, err := importReceiptDirectory()
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", fmt.Errorf("create import receipt directory: %w", err)
	}
	key := importReceiptKey(plan)
	path := filepath.Join(directory, key+".prepared.json")
	committedPath := committedImportReceiptPath(path)
	if stored, err := readStoredImportReceipt(committedPath); err == nil {
		if stored.Status != importReceiptCommitted {
			return nil, path, fmt.Errorf("committed import receipt has unknown status %q", stored.Status)
		}
		return &stored, path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, path, err
	}
	if stored, err := readStoredImportReceipt(path); err == nil {
		if stored.Status == importReceiptPrepared {
			return nil, path, ErrImportOutcomeUncertain
		}
		return nil, path, fmt.Errorf("prepared import receipt has unknown status %q", stored.Status)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, path, err
	}

	prepared := storedImportReceipt{
		Version:              importReceiptVersion,
		Key:                  key,
		Status:               importReceiptPrepared,
		OperationID:          plan.OperationID,
		Mode:                 plan.Mode,
		Provider:             plan.Provider,
		MessageCount:         plan.MessageCount,
		SourceSnapshotHash:   plan.SourceSnapshotHash,
		Representation:       importRepresentation,
		BeforeRevision:       plan.ObservedRevision,
		NativeHistoryVisible: plan.Capability.NativeHistoryVisible,
		CreatedAt:            time.Now().UTC(),
	}
	if plan.Mode == ImportAppendContext {
		prepared.TargetID = plan.Target.ID
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return prepareImportReceipt(plan)
	}
	if err != nil {
		return nil, path, fmt.Errorf("prepare import receipt: %w", err)
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(prepared); err != nil {
		return nil, path, fmt.Errorf("write prepared import receipt: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, path, fmt.Errorf("protect prepared import receipt: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, path, fmt.Errorf("sync prepared import receipt: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, path, fmt.Errorf("close prepared import receipt: %w", err)
	}
	remove = false
	return nil, path, nil
}

func commitImportReceipt(path string, receipt ImportReceipt) error {
	stored, err := readStoredImportReceipt(path)
	if err != nil {
		return err
	}
	if stored.Status != importReceiptPrepared || stored.OperationID != receipt.OperationID {
		return errors.New("prepared import receipt no longer matches the completed operation")
	}
	now := time.Now().UTC()
	stored.Status = importReceiptCommitted
	stored.TargetID = receipt.Row.ID
	stored.BeforeRevision = receipt.BeforeRevision
	stored.AfterRevision = receipt.AfterRevision
	stored.NativeHistoryVisible = receipt.NativeHistoryVisible
	stored.CommittedAt = &now
	committedPath := committedImportReceiptPath(path)
	if err := writeFileAtomic(committedPath, func(file *os.File) error {
		encoder := json.NewEncoder(file)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(stored)
	}); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove prepared import receipt: %w", err)
	}
	return nil
}

func committedImportReceiptPath(preparedPath string) string {
	return strings.TrimSuffix(preparedPath, ".prepared.json") + ".committed.json"
}

func verifiedCommittedImport(plan ImportPlan, stored storedImportReceipt) (ImportReceipt, error) {
	if stored.Version != importReceiptVersion || stored.Key != importReceiptKey(plan) ||
		stored.Mode != plan.Mode || stored.Provider != plan.Provider ||
		stored.SourceSnapshotHash != plan.SourceSnapshotHash || stored.MessageCount != plan.MessageCount ||
		stored.Representation != importRepresentation {
		return ImportReceipt{}, fmt.Errorf("%w: committed receipt does not match the reviewed import", ErrImportVerification)
	}

	row := plan.Target
	if plan.Mode == ImportCreate {
		impl, ok := providerFor(plan.Provider)
		if !ok {
			return ImportReceipt{}, fmt.Errorf("%w: provider is unavailable", ErrImportVerification)
		}
		var err error
		row, err = importCreateReceiptTarget(impl, plan, stored.TargetID)
		if err != nil {
			return ImportReceipt{}, err
		}
		if !sameImportPath(row.CWD, plan.CWD) {
			return ImportReceipt{}, fmt.Errorf("%w: committed target session is in a different workspace", ErrImportVerification)
		}
		prefixIntact, err := committedImportPrefixIntact(row.File, stored.AfterRevision)
		if err != nil {
			return ImportReceipt{}, fmt.Errorf("%w: read committed target: %v", ErrImportVerification, err)
		}
		if !prefixIntact {
			// Some providers rewrite whole-session JSON files on continuation;
			// OpenCode has no native file. Retain transcript verification there.
			turns, err := impl.Transcript(row)
			if err != nil {
				return ImportReceipt{}, fmt.Errorf("%w: read committed target: %v", ErrImportVerification, err)
			}
			if !importMessagesAtStart(turns, plan.messages) {
				return ImportReceipt{}, fmt.Errorf("%w: committed target no longer contains the imported messages", ErrImportVerification)
			}
		}
	} else {
		if row.ID != stored.TargetID {
			return ImportReceipt{}, fmt.Errorf("%w: committed target id changed", ErrImportVerification)
		}
		if _, err := verifyCodexImportPersistence(row.File, stored.BeforeRevision, plan.messages); err != nil {
			return ImportReceipt{}, err
		}
	}

	return ImportReceipt{
		OperationID:          stored.OperationID,
		Mode:                 stored.Mode,
		Provider:             stored.Provider,
		Row:                  row,
		MessageCount:         stored.MessageCount,
		SourceSnapshotHash:   stored.SourceSnapshotHash,
		BeforeRevision:       stored.BeforeRevision,
		AfterRevision:        stored.AfterRevision,
		NativeHistoryVisible: stored.NativeHistoryVisible,
		NoOp:                 true,
	}, nil
}

func importCreateReceiptTarget(impl ProviderImpl, plan ImportPlan, id string) (Row, error) {
	if plan.Provider == ProviderJCode {
		// JCode's picker intentionally requires its CLI and a useful user
		// preview. Neither is needed to verify our own local imported session.
		return importJCodeReceiptTarget(plan.CWD, id)
	}
	for _, candidate := range impl.Discover() {
		if candidate.ID == id {
			return candidate, nil
		}
	}
	return Row{}, fmt.Errorf("%w: committed target session %q is missing", ErrImportVerification, id)
}

func importJCodeReceiptTarget(cwd, id string) (Row, error) {
	// IDs are receipt data, not paths. Exclude separators, traversal and
	// Windows alternate stream syntax before constructing the native path.
	if id == "" {
		return Row{}, fmt.Errorf("%w: committed JCode target id is empty", ErrImportVerification)
	}
	for _, character := range id {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return Row{}, fmt.Errorf("%w: committed JCode target id is invalid", ErrImportVerification)
		}
	}
	store, err := filepath.EvalSymlinks(filepath.Join(defaultJCodeHome(), "sessions"))
	if err != nil {
		return Row{}, fmt.Errorf("%w: committed JCode store is unavailable: %v", ErrImportVerification, err)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(store, id+".json"))
	if err != nil {
		return Row{}, fmt.Errorf("%w: committed JCode target is unavailable: %v", ErrImportVerification, err)
	}
	if !pathWithin(store, path) {
		return Row{}, fmt.Errorf("%w: committed JCode target is outside its native store", ErrImportVerification)
	}
	native, ok := readJCodeSession(path)
	if !ok || native.ID != id {
		return Row{}, fmt.Errorf("%w: committed JCode target identity changed", ErrImportVerification)
	}
	if native.WorkingDir == "" || !sameImportPath(native.WorkingDir, cwd) {
		return Row{}, fmt.Errorf("%w: committed target session is in a different workspace", ErrImportVerification)
	}
	firstUser, lastUser := jcodeUserPreviews(native.Messages)
	lastAt, ok := parseTimestamp(native.UpdatedAt)
	if !ok {
		lastAt, _ = parseTimestamp(native.CreatedAt)
	}
	return Row{
		Provider: ProviderJCode, ID: native.ID, CWD: native.WorkingDir,
		LaunchCWD: native.WorkingDir, File: path, LastAt: lastAt,
		FirstUser: firstUser, LastUser: lastUser,
	}, nil
}

// An intact native prefix proves the original import still exists, including
// arbitrary user text that the ordinary transcript reader filters as provider
// boilerplate. It also accepts a safely continued, append-only native session.
func committedImportPrefixIntact(path string, revision ImportRevision) (bool, error) {
	if path == "" || revision.Size <= 0 || revision.SHA256 == "" {
		return false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() < revision.Size {
		return false, nil
	}
	hash := sha256.New()
	if _, err := io.CopyN(hash, file, revision.Size); err != nil {
		return false, err
	}
	return hex.EncodeToString(hash.Sum(nil)) == revision.SHA256, nil
}

func importMessagesAtStart(turns []Turn, messages []ImportMessage) bool {
	if len(turns) < len(messages) {
		return false
	}
	for index, message := range messages {
		if turns[index].Role != message.Role || turns[index].Text != cleanTranscriptText(message.Text) {
			return false
		}
	}
	return true
}

func readStoredImportReceipt(path string) (storedImportReceipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return storedImportReceipt{}, err
	}
	var receipt storedImportReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return storedImportReceipt{}, fmt.Errorf("read import receipt: %w", err)
	}
	return receipt, nil
}

func importReceiptKey(plan ImportPlan) string {
	scope := plan.CWD
	if plan.Mode == ImportAppendContext {
		scope = plan.Target.ID
	}
	data, _ := json.Marshal(struct {
		Version        int        `json:"version"`
		Mode           ImportMode `json:"mode"`
		Provider       Provider   `json:"provider"`
		Scope          string     `json:"scope"`
		Snapshot       string     `json:"snapshot"`
		Representation string     `json:"representation"`
	}{importReceiptVersion, plan.Mode, plan.Provider, scope, plan.SourceSnapshotHash, importRepresentation})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func importReceiptDirectory() (string, error) {
	if override := strings.TrimSpace(os.Getenv("SHOWAGENT_STATE_DIR")); override != "" {
		absolute, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve SHOWAGENT_STATE_DIR: %w", err)
		}
		return filepath.Join(absolute, "imports"), nil
	}
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "showagent", "imports"), nil
		}
	}
	if state := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); state != "" {
		return filepath.Join(state, "showagent", "imports"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate import receipt directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "showagent", "imports"), nil
}
