package session

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrTrimChanged means that a reviewed trim cannot be applied to the current
// version of the session. The caller must build a new preview.
var ErrTrimChanged = errors.New("session changed since the trim preview; review the current history and try again")

// TrimPlan describes a same-ID edit of the native conversation. Its private
// snapshot binds ApplyTrim to the version that was previewed.
type TrimPlan struct {
	Row                 Row
	Options             HandoffOptions
	Boundary            UserMessage
	KeptUserMessages    int
	RemovedUserMessages int
	KeptTurns           int
	RemovedTurns        int

	target   Row
	options  HandoffOptions
	revision trimRevision
	codex    codexTrimSnapshot
}

type trimRevision struct {
	stamp discoveryStamp
	hash  [sha256.Size]byte
}

type trimResult struct {
	replacement             []byte
	keptUsers, removedUsers int
	keptTurns, removedTurns int
	boundaryText            string
	nativeMessages          int
}

type trimAdapter struct {
	root       string
	acceptPath func(string) bool
	cut        func(Row, []byte, HandoffOptions) (trimResult, error)
	parse      func(string) (Row, bool)
}

func sessionTrimAdapter(row Row) (trimAdapter, error) {
	if row.HandoffOnly {
		return trimAdapter{}, fmt.Errorf("%s session is handoff-only and cannot be trimmed", DisplayName(row.Provider))
	}
	switch row.Provider {
	case ProviderCodex:
		return trimAdapter{
			root: filepath.Join(defaultCodexHome(), "sessions"),
			acceptPath: func(relative string) bool {
				return filepath.Ext(relative) == ".jsonl" && strings.HasPrefix(filepath.Base(relative), "rollout-")
			},
		}, nil
	case ProviderClaude:
		return trimAdapter{
			root: filepath.Join(defaultClaudeHome(), "projects"),
			acceptPath: func(relative string) bool {
				return filepath.Ext(relative) == ".jsonl" && strings.Count(relative, string(filepath.Separator)) == 1 &&
					sessionIDFromPath(relative) == row.ID
			},
			cut: trimClaude, parse: parseClaude,
		}, nil
	case ProviderGemini:
		return trimAdapter{
			root: filepath.Join(defaultGeminiHome(), "tmp"),
			acceptPath: func(relative string) bool {
				parts := strings.Split(relative, string(filepath.Separator))
				ext := filepath.Ext(relative)
				return len(parts) == 3 && parts[1] == "chats" && strings.HasPrefix(parts[2], "session-") && (ext == ".jsonl" || ext == ".json")
			},
			cut:   trimGemini,
			parse: func(path string) (Row, bool) { return parseGemini(path, row.CWD) },
		}, nil
	case ProviderPi:
		return trimAdapter{
			root: defaultPiSessionRoot(), acceptPath: func(relative string) bool { return filepath.Ext(relative) == ".jsonl" },
			cut: trimPi, parse: parsePi,
		}, nil
	default:
		return trimAdapter{}, fmt.Errorf("%s does not support trimming native history", DisplayName(row.Provider))
	}
}

// ValidateTrim checks whether the row refers to a supported local store.
// PlanTrim additionally validates the file and selected message.
func ValidateTrim(row Row) error {
	adapter, err := sessionTrimAdapter(row)
	if err != nil {
		return err
	}
	_, err = trimRelativePath(row, adapter)
	return err
}

// CanTrimThroughUser reports whether the provider can preserve an individual
// user message while removing the reply in its native turn.
func CanTrimThroughUser(row Row) bool {
	return row.Provider != ProviderCodex && ValidateTrim(row) == nil
}

func trimRelativePath(row Row, adapter trimAdapter) (string, error) {
	root, err := filepath.Abs(adapter.root)
	if err != nil {
		return "", err
	}
	if !pathWithin(root, row.File) {
		// Codex may return the physical path beneath a linked provider root.
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil || strings.TrimSpace(row.ID) == "" || !filepath.IsAbs(row.File) || !pathWithin(root, row.File) {
		return "", errors.New("trim requires an exact native session file inside its provider store")
	}
	relative, err := filepath.Rel(root, row.File)
	if err != nil || !adapter.acceptPath(relative) {
		return "", errors.New("session path does not identify a supported native conversation")
	}
	return relative, nil
}

func trimPath(row Row, adapter trimAdapter) (string, error) {
	relative, err := trimRelativePath(row, adapter)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(adapter.root)
	if err != nil {
		return "", fmt.Errorf("open native session store: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, relative)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("open native session file: %w", err)
	}
	if relation, err := filepath.Rel(path, resolved); err != nil || relation != "." {
		return "", errors.New("trim requires a session file without symlinked descendants in its store")
	}
	return path, nil
}

func trimMessageNumber(options HandoffOptions) (int, error) {
	if options.MaxTurns != 0 || options.BeforeUser < 0 || options.ThroughUser < 0 || (options.BeforeUser > 0) == (options.ThroughUser > 0) {
		return 0, errors.New("choose exactly one positive user message with --before-user or --through-user")
	}
	return max(options.BeforeUser, options.ThroughUser), nil
}

// PlanTrim reads and reviews a cut without editing the source session.
func PlanTrim(row Row, options HandoffOptions) (TrimPlan, error) {
	number, err := trimMessageNumber(options)
	if err != nil {
		return TrimPlan{}, err
	}
	adapter, err := sessionTrimAdapter(row)
	if err != nil {
		return TrimPlan{}, err
	}
	path, err := trimPath(row, adapter)
	if err != nil {
		return TrimPlan{}, err
	}
	if row.Provider == ProviderCodex {
		return planCodexTrim(row, path, options)
	}
	file, err := os.Open(path)
	if err != nil {
		return TrimPlan{}, err
	}
	defer func() { _ = file.Close() }()
	source, revision, err := readTrimSnapshot(path, file)
	if err != nil {
		return TrimPlan{}, err
	}
	result, err := adapter.cut(row, source, options)
	if err != nil {
		return TrimPlan{}, err
	}
	boundaryTurn := result.keptTurns
	if options.BeforeUser > 0 {
		boundaryTurn++
	}
	return TrimPlan{
		Row: row, Options: options, Boundary: newUserMessage(number, boundaryTurn, result.boundaryText),
		KeptUserMessages: result.keptUsers, RemovedUserMessages: result.removedUsers,
		KeptTurns: result.keptTurns, RemovedTurns: result.removedTurns,
		target: row, options: options, revision: revision,
	}, nil
}

// ApplyTrim replaces the source with the reviewed prefix. Close the native
// agent session before applying so it cannot continue writing its old state.
// A nonzero row and error indicate that the session was trimmed, but a
// secondary metadata update failed.
func ApplyTrim(plan TrimPlan) (Row, error) {
	row := plan.target
	adapter, err := sessionTrimAdapter(row)
	if err != nil {
		return Row{}, err
	}
	path, err := trimPath(row, adapter)
	if err != nil {
		return Row{}, errors.Join(ErrTrimChanged, err)
	}
	if row.Provider == ProviderCodex {
		return applyCodexTrim(plan, path)
	}
	file, err := openTrimWriter(path)
	if err != nil {
		return Row{}, fmt.Errorf("cannot lock session for trim; close it in %s: %w", DisplayName(row.Provider), err)
	}
	defer func() { _ = file.Close() }()
	source, revision, err := readTrimSnapshot(path, file)
	if err != nil {
		return Row{}, err
	}
	if revision != plan.revision {
		return Row{}, ErrTrimChanged
	}
	result, err := adapter.cut(row, source, plan.options)
	if err != nil {
		return Row{}, err
	}
	var updated Row
	err = writeFileAtomicWith(path, func(replacement *os.File) error {
		if _, err := replacement.Write(result.replacement); err != nil {
			return err
		}
		var ok bool
		updated, ok = adapter.parse(replacement.Name())
		if !ok || updated.ID != row.ID || updated.Provider != row.Provider {
			return errors.New("trimmed history could not be read with the original session identity")
		}
		return nil
	}, func(from, to string) error {
		current, err := trimStamp(path, file)
		if err != nil || current != revision.stamp {
			return ErrTrimChanged
		}
		return replaceTrimFile(from, to)
	})
	if err != nil {
		return Row{}, err
	}
	updated.File = row.File
	impl, _ := providerFor(row.Provider)
	removeDiscoveryPreview(row, impl)
	if row.Provider == ProviderClaude {
		if err := refreshTrimmedClaudeIndex(updated, result.nativeMessages); err != nil {
			return updated, fmt.Errorf("history was trimmed, but Claude's session index could not be refreshed: %w", err)
		}
	}
	return updated, nil
}

const maxTrimFileBytes = 256 << 20

func readTrimSnapshot(path string, file *os.File) ([]byte, trimRevision, error) {
	before, err := trimStamp(path, file)
	if err != nil {
		return nil, trimRevision{}, err
	}
	if before.Size > maxTrimFileBytes {
		return nil, trimRevision{}, errors.New("session exceeds the 256 MiB trim limit")
	}
	source, err := io.ReadAll(io.LimitReader(file, maxTrimFileBytes+1))
	if err != nil {
		return nil, trimRevision{}, fmt.Errorf("read session for trim: %w", err)
	}
	after, err := trimStamp(path, file)
	if err != nil || before != after || int64(len(source)) != before.Size {
		return nil, trimRevision{}, ErrTrimChanged
	}
	return source, trimRevision{stamp: before, hash: sha256.Sum256(source)}, nil
}

func trimStamp(path string, file *os.File) (discoveryStamp, error) {
	open, err := file.Stat()
	if err != nil {
		return discoveryStamp{}, err
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(open, current) {
		return discoveryStamp{}, ErrTrimChanged
	}
	stamp, ok := stampFile(path)
	if !ok || stamp.Identity == "" || stamp.Size != open.Size() || stamp.Modified != open.ModTime().UnixNano() {
		return discoveryStamp{}, ErrTrimChanged
	}
	return stamp, nil
}
