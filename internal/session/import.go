package session

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ImportMode identifies whether imported text becomes a new native session or
// is added to an existing session's model context.
type ImportMode string

const (
	ImportCreate        ImportMode = "create"
	ImportAppendContext ImportMode = "append-context"

	importPlanVersion = 1
)

var (
	ErrImportAppendUnavailable = errors.New("append-context import is unavailable for this provider")
	ErrImportTargetChanged     = errors.New("import target changed; review the import again")
	ErrImportVerification      = errors.New("native import verification failed")
	ErrImportOutcomeUncertain  = errors.New("an earlier import may have completed; inspect the target before retrying")
	errImportNotStarted        = errors.New("native import did not start")
)

// ImportMessage is the session package's deliberately small boundary type.
// Source parsers map their richer message records into this type without
// coupling native session writers to importer metadata or acquisition logic.
type ImportMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// ImportCapability reports the two independently supported target operations.
// NativeHistoryVisible describes append-context display behavior; Codex keeps
// injected items in model-visible history but does not expose them as bubbles.
type ImportCapability struct {
	CanCreate            bool   `json:"can_create"`
	CanAppendContext     bool   `json:"can_append_context"`
	NativeHistoryVisible bool   `json:"native_history_visible"`
	RequiresCLI          bool   `json:"requires_cli"`
	Reason               string `json:"reason,omitempty"`
}

// ImportRevision is the preview-time identity of a native session file. The
// aggregate Revision is convenient for receipts, while the individual fields
// make review output and diagnostics explicit.
type ImportRevision struct {
	Size     int64     `json:"size"`
	SHA256   string    `json:"sha256"`
	ModTime  time.Time `json:"mtime"`
	Revision string    `json:"revision"`
}

// ImportRequest is accepted by PlanImport. Create uses Provider+CWD; append
// uses Target and never substitutes the caller's current working directory.
// ObservedRevision may carry a revision shown by an earlier preview. When set,
// planning refuses to silently replace that reviewed target snapshot.
type ImportRequest struct {
	Mode             ImportMode      `json:"mode"`
	Provider         Provider        `json:"provider,omitempty"`
	CWD              string          `json:"cwd,omitempty"`
	Target           Row             `json:"target,omitempty"`
	Messages         []ImportMessage `json:"messages"`
	ObservedRevision *ImportRevision `json:"observed_revision,omitempty"`
}

// ImportPlan is a validated preview and immutable content snapshot. Callers
// can inspect ReviewedMessages; ApplyImport uses a private copy so mutating a
// parser/request slice after preview cannot change what is written.
type ImportPlan struct {
	PlanVersion        int              `json:"plan_version"`
	OperationID        string           `json:"operation_id"`
	Mode               ImportMode       `json:"mode"`
	Provider           Provider         `json:"provider"`
	CWD                string           `json:"cwd"`
	Target             Row              `json:"target,omitempty"`
	ObservedRevision   ImportRevision   `json:"observed_revision,omitempty"`
	SourceSnapshotHash string           `json:"source_snapshot_hash"`
	MessageCount       int              `json:"message_count"`
	Capability         ImportCapability `json:"capability"`
	ReviewedMessages   []ImportMessage  `json:"reviewed_messages"`

	messages []ImportMessage
}

// ImportReceipt describes the native target produced or updated by an import.
type ImportReceipt struct {
	OperationID          string         `json:"operation_id"`
	Mode                 ImportMode     `json:"mode"`
	Provider             Provider       `json:"provider"`
	Row                  Row            `json:"row"`
	MessageCount         int            `json:"message_count"`
	SourceSnapshotHash   string         `json:"source_snapshot_hash"`
	BeforeRevision       ImportRevision `json:"before_revision,omitempty"`
	AfterRevision        ImportRevision `json:"after_revision,omitempty"`
	NativeHistoryVisible bool           `json:"native_history_visible"`
	NoOp                 bool           `json:"no_op,omitempty"`
}

// ImportCapabilities returns conservative provider support. Every registered
// provider already has a native new-session writer. Only Codex currently has a
// proven no-model-turn append adapter.
func ImportCapabilities(provider Provider) ImportCapability {
	if _, ok := providerFor(provider); !ok {
		return ImportCapability{Reason: fmt.Sprintf("unsupported provider %q", provider)}
	}

	capability := ImportCapability{CanCreate: true}
	if provider == ProviderCodex {
		capability.CanAppendContext = true
		capability.RequiresCLI = true
		return capability
	}

	// OpenCode's create writer also goes through its CLI. The remaining local
	// writers can create sessions without launching their provider executable.
	capability.RequiresCLI = provider == ProviderOpenCode
	capability.Reason = fmt.Sprintf("%s has no verified native append-context API that avoids starting a model turn", DisplayName(provider))
	return capability
}

// PlanImport validates roles, text, target ownership, and folder selection,
// then captures the exact content and (for append) native file revision that
// ApplyImport must see again.
func PlanImport(request ImportRequest) (ImportPlan, error) {
	messages, err := validateImportMessages(request.Messages)
	if err != nil {
		return ImportPlan{}, err
	}

	operationID, err := newUUID()
	if err != nil {
		return ImportPlan{}, fmt.Errorf("allocate import operation: %w", err)
	}
	plan := ImportPlan{
		PlanVersion:        importPlanVersion,
		OperationID:        operationID,
		Mode:               request.Mode,
		SourceSnapshotHash: importMessagesHash(messages),
		MessageCount:       len(messages),
		ReviewedMessages:   cloneImportMessages(messages),
		messages:           cloneImportMessages(messages),
	}

	switch request.Mode {
	case ImportCreate:
		capability := ImportCapabilities(request.Provider)
		if !capability.CanCreate {
			return ImportPlan{}, fmt.Errorf("cannot create import target: %s", capability.Reason)
		}
		cwd, err := resolveImportWorkspace(request.CWD)
		if err != nil {
			return ImportPlan{}, err
		}
		plan.Provider = request.Provider
		plan.CWD = cwd
		plan.Capability = capability
	case ImportAppendContext:
		capability := ImportCapabilities(request.Target.Provider)
		if !capability.CanAppendContext {
			return ImportPlan{}, fmt.Errorf("%w: %s", ErrImportAppendUnavailable, capability.Reason)
		}
		target, revision, err := validateAppendTarget(request.Target)
		if err != nil {
			return ImportPlan{}, err
		}
		if request.ObservedRevision != nil && !sameImportRevision(*request.ObservedRevision, revision) {
			return ImportPlan{}, ErrImportTargetChanged
		}
		plan.Provider = target.Provider
		plan.CWD = target.CWD
		plan.Target = target
		plan.ObservedRevision = revision
		plan.Capability = capability
	default:
		return ImportPlan{}, fmt.Errorf("unsupported import mode %q", request.Mode)
	}

	return plan, nil
}

// ApplyImport applies only a plan produced by PlanImport. New sessions use the
// existing provider writer behind a source-free API; append is delegated to a
// provider-specific native adapter.
func ApplyImport(ctx context.Context, plan ImportPlan) (ImportReceipt, error) {
	if plan.PlanVersion != importPlanVersion || plan.OperationID == "" || len(plan.messages) == 0 {
		return ImportReceipt{}, errors.New("invalid import plan; preview the import again")
	}
	// Check executable availability before persisting intent. OpenCode create
	// and Codex append have not touched native state at this point, so a missing
	// CLI is safely retryable after installation rather than outcome-uncertain.
	if importNeedsProviderCommand(plan) && !ProviderCommandAvailable(plan.Provider) {
		return ImportReceipt{}, fmt.Errorf("%s not found in PATH; install it and retry", providerCommand(plan.Provider))
	}
	prior, receiptPath, err := prepareImportReceipt(plan)
	if err != nil {
		return ImportReceipt{}, err
	}
	if prior != nil {
		return verifiedCommittedImport(plan, *prior)
	}

	receipt, err := applyImportNative(ctx, plan)
	if err != nil {
		if errors.Is(err, ErrImportTargetChanged) || errors.Is(err, ErrImportAppendUnavailable) || errors.Is(err, errImportNotStarted) {
			_ = os.Remove(receiptPath)
		}
		return ImportReceipt{}, err
	}
	if err := commitImportReceipt(receiptPath, receipt); err != nil {
		return ImportReceipt{}, fmt.Errorf("native import completed but its receipt could not be committed; %w: %v", ErrImportOutcomeUncertain, err)
	}
	return receipt, nil
}

func importNeedsProviderCommand(plan ImportPlan) bool {
	return plan.Mode == ImportCreate && plan.Provider == ProviderOpenCode
}

func applyImportNative(ctx context.Context, plan ImportPlan) (ImportReceipt, error) {
	switch plan.Mode {
	case ImportCreate:
		impl, ok := providerFor(plan.Provider)
		if !ok {
			return ImportReceipt{}, fmt.Errorf("unsupported target provider %q", plan.Provider)
		}
		turns := importTurns(plan.messages)
		row, err := impl.WriteConverted(Row{CWD: plan.CWD, LaunchCWD: plan.CWD}, turns)
		if err != nil {
			return ImportReceipt{}, fmt.Errorf("create %s session from import: %w", DisplayName(plan.Provider), err)
		}
		receipt := importReceiptForPlan(plan, row)
		if row.File != "" {
			if revision, err := observeImportRevision(row.File); err == nil {
				receipt.AfterRevision = revision
			}
		}
		return receipt, nil
	case ImportAppendContext:
		if plan.Provider != ProviderCodex {
			return ImportReceipt{}, ErrImportAppendUnavailable
		}
		return applyCodexContextImport(ctx, plan)
	default:
		return ImportReceipt{}, fmt.Errorf("unsupported import mode %q", plan.Mode)
	}
}

func importReceiptForPlan(plan ImportPlan, row Row) ImportReceipt {
	return ImportReceipt{
		OperationID:          plan.OperationID,
		Mode:                 plan.Mode,
		Provider:             plan.Provider,
		Row:                  row,
		MessageCount:         plan.MessageCount,
		SourceSnapshotHash:   plan.SourceSnapshotHash,
		BeforeRevision:       plan.ObservedRevision,
		NativeHistoryVisible: plan.Capability.NativeHistoryVisible,
	}
}

func validateImportMessages(input []ImportMessage) ([]ImportMessage, error) {
	if len(input) == 0 {
		return nil, errors.New("import has no user or assistant messages")
	}
	messages := make([]ImportMessage, len(input))
	for index, message := range input {
		if message.Role != "user" && message.Role != "assistant" {
			return nil, fmt.Errorf("import message %d has unsupported role %q; review roles as user or assistant", index+1, message.Role)
		}
		if !utf8.ValidString(message.Text) {
			return nil, fmt.Errorf("import message %d is not valid UTF-8", index+1)
		}
		if strings.TrimSpace(message.Text) == "" {
			return nil, fmt.Errorf("import message %d has empty text", index+1)
		}
		message.Text = normalizeImportNewlines(message.Text)
		messages[index] = message
	}
	return messages, nil
}

func normalizeImportNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func cloneImportMessages(messages []ImportMessage) []ImportMessage {
	return slices.Clone(messages)
}

func importTurns(messages []ImportMessage) []Turn {
	turns := make([]Turn, len(messages))
	for index, message := range messages {
		turns[index] = Turn(message)
	}
	return turns
}

func importMessagesHash(messages []ImportMessage) string {
	data, _ := json.Marshal(struct {
		Version  int             `json:"version"`
		Messages []ImportMessage `json:"messages"`
	}{Version: 1, Messages: messages})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func resolveImportWorkspace(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("import workspace is required")
	}
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("import workspace must be an absolute path: %s", cwd)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(cwd))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("import workspace not found: %s", cwd)
		}
		return "", fmt.Errorf("import workspace unavailable: %s: %w", cwd, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("import workspace unavailable: %s: %w", cwd, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("import workspace is not a directory: %s", cwd)
	}
	return filepath.Clean(resolved), nil
}

func validateAppendTarget(row Row) (Row, ImportRevision, error) {
	if row.Provider != ProviderCodex || row.HandoffOnly {
		return Row{}, ImportRevision{}, fmt.Errorf("%w: target must be an exact native Codex session", ErrImportAppendUnavailable)
	}
	if strings.TrimSpace(row.ID) == "" {
		return Row{}, ImportRevision{}, errors.New("append target requires an exact session id")
	}
	cwd, err := resolveImportWorkspace(row.CWD)
	if err != nil {
		return Row{}, ImportRevision{}, err
	}
	if !filepath.IsAbs(row.File) || strings.TrimSpace(row.File) == "" {
		return Row{}, ImportRevision{}, errors.New("append target requires an absolute native session file")
	}
	file, err := filepath.EvalSymlinks(filepath.Clean(row.File))
	if err != nil {
		return Row{}, ImportRevision{}, fmt.Errorf("append target session file unavailable: %w", err)
	}
	if !pathWithin(filepath.Join(defaultCodexHome(), "sessions"), file) {
		return Row{}, ImportRevision{}, errors.New("append target is not in the native Codex sessions store")
	}
	if err := validateCodexSessionID(file, row.ID); err != nil {
		return Row{}, ImportRevision{}, err
	}
	revision, err := observeImportRevision(file)
	if err != nil {
		return Row{}, ImportRevision{}, err
	}
	row.CWD = cwd
	row.LaunchCWD = cwd
	row.File = file
	return row, revision, nil
}

func validateCodexSessionID(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open append target: %w", err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scanBufferMax)
	for scanner.Scan() {
		var record codexLine
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Type != "session_meta" {
			continue
		}
		var meta codexSessionMeta
		if err := json.Unmarshal(record.Payload, &meta); err != nil || meta.ID == "" {
			return errors.New("append target has invalid Codex session metadata")
		}
		if meta.ID != want {
			return fmt.Errorf("append target id %q does not match native session id %q", want, meta.ID)
		}
		return nil
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan append target: %w", err)
	}
	return errors.New("append target has no Codex session metadata")
}

func observeImportRevision(path string) (ImportRevision, error) {
	file, err := os.Open(path)
	if err != nil {
		return ImportRevision{}, fmt.Errorf("open import target revision: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return ImportRevision{}, fmt.Errorf("stat import target revision: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ImportRevision{}, errors.New("import target is not a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ImportRevision{}, fmt.Errorf("hash import target revision: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	mtime := info.ModTime().UTC()
	return ImportRevision{
		Size:     info.Size(),
		SHA256:   digest,
		ModTime:  mtime,
		Revision: fmt.Sprintf("%d:%d:%s", info.Size(), mtime.UnixNano(), digest),
	}, nil
}

func sameImportRevision(left, right ImportRevision) bool {
	return left.Size == right.Size && left.SHA256 == right.SHA256 && left.ModTime.Equal(right.ModTime) && left.Revision == right.Revision
}

func pathWithin(root, candidate string) bool {
	root, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
