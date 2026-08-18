package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
	ProviderJCode  Provider = "jcode"
)

// scanBufferMax bounds a single JSONL line during scanning. Modern LLM turns
// can be large, so this is generous; it is shared by every provider scanner so
// behavior is consistent across Codex and Claude session files.
const scanBufferMax = 16 * 1024 * 1024

type Row struct {
	Provider  Provider
	ID        string
	LastAt    time.Time
	CWD       string
	LaunchCWD string
	File      string
	FirstUser string
	LastUser  string
}

type ResumeOptions struct {
	Dangerous bool
}

// ResumeCommand is the argv that resumes r in its own CLI, or nil when the
// provider is unknown.
func (r Row) ResumeCommand(options ResumeOptions) []string {
	impl, ok := providerFor(r.Provider)
	if !ok {
		return nil
	}
	return impl.ResumeArgs(r, options)
}

// providerCommand is the CLI executable that resumes sessions for provider,
// or "" when the provider is unknown.
func providerCommand(provider Provider) string {
	if impl, ok := providerFor(provider); ok {
		return impl.CommandName()
	}
	return ""
}

func ProviderCommandAvailable(provider Provider) bool {
	command := providerCommand(provider)
	if command == "" {
		return false
	}
	_, err := exec.LookPath(command)
	return err == nil
}

// ScanTarget describes one directory Discover reads for a provider, together
// with the environment variable that overrides its location.
type ScanTarget struct {
	Provider Provider
	Path     string
	EnvVar   string
	// Note explains why a target is currently skipped, or is empty.
	Note string
}

// ScanTargets reports the exact directories Discover scans right now, so
// callers can tell the user where sessions are expected to live.
func ScanTargets() []ScanTarget {
	var targets []ScanTarget
	for _, impl := range registry {
		targets = append(targets, impl.ScanTargets()...)
	}
	return targets
}

// ValidateResume reports why resuming row would fail, so callers can surface
// the problem before tearing down their UI and exec-ing the provider CLI.
func ValidateResume(row Row) error {
	return validateLaunch(row.Provider, row.resumeCWD())
}

// ValidateCompound is ValidateResume for a compound pass: the chosen agent's
// CLI must be on PATH and the session workspace must be a real directory.
func ValidateCompound(row Row, agent Provider) error {
	return validateLaunch(agent, row.resumeCWD())
}

func validateLaunch(provider Provider, cwd string) error {
	command := providerCommand(provider)
	if command == "" {
		return fmt.Errorf("unsupported provider %q", provider)
	}
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("%s not found in PATH", command)
	}
	if _, err := launchDir(cwd); err != nil {
		return err
	}
	return nil
}

func (r Row) FilterValue() string {
	return strings.Join([]string{
		string(r.Provider),
		r.ID,
		r.CWD,
		r.LaunchCWD,
		r.File,
		r.FirstUser,
		r.LastUser,
	}, "\n")
}

func Discover() []Row {
	// Providers own independent stores. Scan them concurrently so a large
	// Codex archive does not serialize Claude/Gemini discovery or the OpenCode
	// CLI query. Results are reassembled in registry order before the final
	// deterministic sort.
	byProvider := make([][]Row, len(registry))
	var wg sync.WaitGroup
	for index, impl := range registry {
		wg.Add(1)
		go func() {
			defer wg.Done()
			byProvider[index] = impl.Discover()
		}()
	}
	wg.Wait()

	var rows []Row
	for _, providerRows := range byProvider {
		rows = append(rows, providerRows...)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].LastAt.Equal(rows[j].LastAt) {
			return rows[i].LastAt.After(rows[j].LastAt)
		}
		if rows[i].Provider != rows[j].Provider {
			return rows[i].Provider < rows[j].Provider
		}
		if rows[i].ID != rows[j].ID {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].File < rows[j].File
	})
	return rows
}

func Resume(row Row, options ResumeOptions) error {
	return launch(row.resumeCWD(), row.ResumeCommand(options))
}

func (r Row) resumeCWD() string {
	if r.Provider == ProviderClaude && strings.TrimSpace(r.LaunchCWD) != "" {
		return r.LaunchCWD
	}
	return r.CWD
}

func Handoff(row Row, target Provider, resumeOptions ResumeOptions, handoffOptions HandoffOptions) error {
	converted, err := Convert(row, target, handoffOptions)
	if err != nil {
		return err
	}
	return Resume(converted, resumeOptions)
}

func Branch(row Row) (Row, error) {
	return Convert(row, row.Provider, HandoffOptions{})
}

func Delete(row Row) error {
	impl, ok := providerFor(row.Provider)
	if !ok {
		return fmt.Errorf("unsupported provider %q", row.Provider)
	}
	return impl.Delete(row)
}

func launch(cwd string, command []string) error {
	if len(command) == 0 {
		return errors.New("no launch command for this provider")
	}
	dir, err := launchDir(cwd)
	if err != nil {
		return err
	}
	if dir != "" {
		if err := os.Chdir(dir); err != nil {
			return err
		}
	}

	path, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("%s not found in PATH", command[0])
	}
	return syscallExec(path, command, os.Environ())
}

func launchDir(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || strings.HasPrefix(cwd, "(") {
		return "", nil
	}

	info, err := os.Stat(cwd)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("workspace not found: %s", cwd)
		}
		return "", fmt.Errorf("workspace unavailable: %s: %w", cwd, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory: %s", cwd)
	}
	return cwd, nil
}

var (
	sessionIDPattern = regexp.MustCompile(`(?i)([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
	timestampPattern = regexp.MustCompile(`"timestamp"\s*:\s*"((?:\\.|[^"\\])*)"`)

	// Secret redaction is intentionally preview/MCP-facing only. Native
	// session conversion preserves the transcript exactly (apart from unsafe
	// terminal controls), including formatting and user-authored values.
	passwordAssignmentPattern = regexp.MustCompile(`(?i)(["']?(?:password|passwd|pwd|parola|sifre|şifre)\w*["']?\s*(?::|=|\bis\b|\bwas\b|\bidi\b)\s*)(?:"[^"]*"|'[^']*'|[^\s,;}]+)`)
	passwordBarePattern       = regexp.MustCompile(`(?i)\b((?:password|passwd|pwd|parola|sifre|şifre)\s+)(?:"[^"]*"|'[^']*'|[^\s,;}]+)`)
	secretAssignmentPattern   = regexp.MustCompile(`(?i)\b((?:api[ _-]?key|access[ _-]?token|auth[ _-]?token|bearer[ _-]?token|client[ _-]?secret|secret(?:[ _-]?key)?)\s*(?::|=|\bis\b|\bwas\b|\bidi\b)\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	knownSecretPattern        = regexp.MustCompile(`\b(?:sk-(?:proj-|ant-)?[A-Za-z0-9_-]{12,}|github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AIza[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{10,})\b`)
	bearerSecretPattern       = regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]{12,}`)
	jwtSecretPattern          = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	privateKeyPattern         = regexp.MustCompile(`(?s)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`)
)

func defaultCodexHome() string {
	if value := os.Getenv("CODEX_HOME"); value != "" {
		return expandHome(value)
	}
	return filepath.Join(homeDir(), ".codex")
}

func defaultClaudeHome() string {
	if value := os.Getenv("CLAUDE_HOME"); value != "" {
		return expandHome(value)
	}
	return filepath.Join(homeDir(), ".claude")
}

func defaultJCodeHome() string {
	if value := os.Getenv("JCODE_HOME"); value != "" {
		return expandHome(value)
	}
	return filepath.Join(homeDir(), ".jcode")
}

func JCodeAvailable() bool {
	return ProviderCommandAvailable(ProviderJCode)
}

func homeDir() string {
	if value, err := os.UserHomeDir(); err == nil {
		return value
	}
	return "."
}

func expandHome(path string) string {
	if path == "~" {
		return homeDir()
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), strings.TrimPrefix(path, "~/"))
	}
	return path
}

// cleanPreviewText makes untrusted session text safe and compact for terminal
// rendering. It removes every ANSI/OSC control sequence, strips remaining
// control and bidi-override characters, folds whitespace, and redacts common
// credentials. It must not be used for transcript conversion because folding
// whitespace destroys code blocks and indentation.
func cleanPreviewText(value string) string {
	return strings.TrimSpace(RedactSecrets(SafeDisplayText(value)))
}

// SafeDisplayText turns untrusted session metadata into one terminal-safe
// line without redacting its contents. Keep raw IDs and paths in Row for
// provider operations; call this only at human-facing rendering boundaries.
func SafeDisplayText(value string) string {
	value = stripTerminalControls(value, false)
	return strings.Join(strings.Fields(value), " ")
}

// cleanTranscriptText preserves newlines, tabs, and indentation while
// removing terminal escape/control sequences that could execute when a
// transcript is later displayed. Secret values are preserved here: a local
// branch/convert operation is expected to carry the original conversation.
func cleanTranscriptText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(stripTerminalControls(value, true))
}

// RedactSecrets redacts common credentials without changing surrounding
// layout. MCP uses it before returning transcript text to a model; previews
// additionally fold whitespace via cleanPreviewText.
func RedactSecrets(value string) string {
	value = privateKeyPattern.ReplaceAllString(value, "[redacted-private-key]")
	value = passwordAssignmentPattern.ReplaceAllString(value, "${1}[redacted]")
	value = passwordBarePattern.ReplaceAllString(value, "${1}[redacted]")
	value = secretAssignmentPattern.ReplaceAllString(value, "${1}[redacted]")
	value = bearerSecretPattern.ReplaceAllString(value, "${1}[redacted]")
	value = knownSecretPattern.ReplaceAllString(value, "[redacted-secret]")
	return jwtSecretPattern.ReplaceAllString(value, "[redacted-jwt]")
}

// RedactTranscriptText prepares local handoff text for a non-interactive file:
// preserve code layout, remove terminal/bidirectional controls, then redact
// common credential shapes.
func RedactTranscriptText(value string) string {
	return RedactSecrets(stripTerminalControls(value, true))
}

func stripTerminalControls(value string, preserveLayout bool) string {
	value = ansi.Strip(value)
	return strings.Map(func(r rune) rune {
		if preserveLayout && (r == '\n' || r == '\t') {
			return r
		}
		if unicode.IsControl(r) || isBidiControl(r) {
			if !preserveLayout && unicode.IsSpace(r) {
				return ' '
			}
			return -1
		}
		return r
	}, value)
}

func isBidiControl(r rune) bool {
	return (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')
}

func usefulUserText(value string) bool {
	if value == "" {
		return false
	}

	skippedPrefixes := []string{
		"# AGENTS.md instructions",
		"<environment_context>",
		"<permissions instructions>",
		"<collaboration_mode>",
		"<apps_instructions>",
		"<skills_instructions>",
		"<system-reminder>",
		"<local-command-caveat>",
		"<local-command-",
		"<command-name>",
		"<turn_aborted>",
	}
	for _, prefix := range skippedPrefixes {
		if strings.HasPrefix(value, prefix) {
			return false
		}
	}

	if strings.HasPrefix(value, "# ") {
		head := value
		if len(head) > 1000 {
			head = head[:1000]
		}
		for _, marker := range []string{"<INSTRUCTIONS>", "<filesystem>", "========= MEMORY_SUMMARY BEGINS ========="} {
			if strings.Contains(head, marker) {
				return false
			}
		}
	}

	return true
}

func textFromContent(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			if text, ok := item.(string); ok {
				parts = append(parts, text)
				continue
			}
			object, ok := item.(map[string]any)
			if !ok || object["type"] == "tool_result" {
				continue
			}
			for _, key := range []string{"text", "input_text", "output_text"} {
				if text, ok := object[key].(string); ok {
					parts = append(parts, text)
					break
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func parseTimestamp(value any) (time.Time, bool) {
	text, ok := value.(string)
	if !ok || text == "" {
		return time.Time{}, false
	}

	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err == nil {
		return parsed, true
	}

	parsed, err = time.Parse("2006-01-02T15:04:05.000Z07:00", text)
	return parsed, err == nil
}

func timestampFromLine(line string) (time.Time, bool) {
	match := timestampPattern.FindStringSubmatch(line)
	if len(match) != 2 {
		return time.Time{}, false
	}

	var value string
	if err := json.Unmarshal([]byte(`"`+match[1]+`"`), &value); err != nil {
		return time.Time{}, false
	}
	return parseTimestamp(value)
}

func sessionIDFromPath(path string) string {
	match := sessionIDPattern.FindStringSubmatch(filepath.Base(path))
	if len(match) == 2 {
		return match[1]
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// reverseLines calls fn with each non-empty line of path from last to first,
// stopping early once fn returns false. Blocks are read back to front; the
// fragments of a line that spans blocks are buffered up to scanBufferMax
// bytes, and a longer line is degraded to its trailing scanBufferMax bytes so
// a newline-poor multi-gigabyte file is never held in memory at once.
func reverseLines(path string, fn func(string) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	position := info.Size()

	// pending holds the fragments of the line currently being assembled, in
	// reverse file order. truncated records that the line already exceeded
	// scanBufferMax and its earlier bytes are being dropped.
	var pending [][]byte
	pendingSize := 0
	truncated := false

	// emit joins first (the earliest fragment of the current line) with the
	// pending fragments, resets the line state, and passes the line to fn.
	// It reports whether scanning should continue.
	emit := func(first []byte) bool {
		if truncated {
			first = nil
		}
		line := make([]byte, 0, len(first)+pendingSize)
		line = append(line, first...)
		for i := len(pending) - 1; i >= 0; i-- {
			line = append(line, pending[i]...)
		}
		pending = pending[:0]
		pendingSize = 0
		truncated = false
		text := strings.TrimSpace(string(line))
		if text == "" {
			return true
		}
		return fn(text)
	}

	const blockSize = 64 * 1024
	chunk := make([]byte, blockSize)
	for position > 0 {
		readSize := int64(blockSize)
		if position < readSize {
			readSize = position
		}
		position -= readSize
		block := chunk[:readSize]
		if _, err := file.ReadAt(block, position); err != nil {
			return err
		}

		for {
			index := bytes.LastIndexByte(block, '\n')
			if index < 0 {
				break
			}
			if !emit(block[index+1:]) {
				return nil
			}
			block = block[:index]
		}

		if len(block) == 0 {
			continue
		}
		if truncated || pendingSize+len(block) > scanBufferMax {
			// The current line no longer fits: keep only its tail and drop
			// the earlier bytes instead of buffering the whole file.
			truncated = true
			continue
		}
		pending = append(pending, append([]byte(nil), block...))
		pendingSize += len(block)
	}

	emit(nil)
	return nil
}

func fallbackMTime(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func jsonlPaths(root string) []string {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil
	}
	var paths []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	sort.Strings(paths)
	return paths
}

// parseRowsBounded parses independent session files with a small worker pool.
// Four readers keep SSDs busy without multiplying 16 MiB scanner buffers or
// turning discovery into an unbounded goroutine fan-out on large archives.
func parseRowsBounded(paths []string, parse func(string) (Row, bool)) []Row {
	if len(paths) == 0 {
		return nil
	}
	workers := min(runtime.GOMAXPROCS(0), 4, len(paths))
	jobs := make(chan string)
	rows := make(chan Row, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if row, ok := parse(path); ok {
					rows <- row
				}
			}
		}()
	}
	go func() {
		for _, path := range paths {
			jobs <- path
		}
		close(jobs)
		wg.Wait()
		close(rows)
	}()

	parsed := make([]Row, 0, len(paths))
	for row := range rows {
		parsed = append(parsed, row)
	}
	return parsed
}
