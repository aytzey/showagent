package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

type Row struct {
	Provider  Provider
	ID        string
	LastAt    time.Time
	CWD       string
	File      string
	FirstUser string
	LastUser  string
}

type ResumeOptions struct {
	Dangerous bool
}

func (r Row) ResumeCommand(options ResumeOptions) []string {
	switch r.Provider {
	case ProviderClaude:
		command := []string{"claude"}
		if options.Dangerous {
			command = append(command, "--dangerously-skip-permissions")
		}
		return append(command, "--resume", r.ID)
	default:
		command := []string{"codex", "resume"}
		if options.Dangerous {
			command = append(command, "--dangerously-bypass-approvals-and-sandbox")
		}
		return append(command, r.ID)
	}
}

func OtherProvider(provider Provider) Provider {
	if provider == ProviderCodex {
		return ProviderClaude
	}
	return ProviderCodex
}

func (r Row) FilterValue() string {
	return strings.Join([]string{
		string(r.Provider),
		r.ID,
		r.CWD,
		r.File,
		r.FirstUser,
		r.LastUser,
	}, "\n")
}

func Discover() []Row {
	rows := append(discoverCodex(defaultCodexHome()), discoverClaude(defaultClaudeHome())...)
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].LastAt.After(rows[j].LastAt)
	})
	return rows
}

func Resume(row Row, options ResumeOptions) error {
	return launch(row.CWD, row.ResumeCommand(options))
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
	switch row.Provider {
	case ProviderCodex:
		command := exec.Command("codex", "delete", "--force", row.ID)
		if info, err := os.Stat(row.CWD); err == nil && info.IsDir() {
			command.Dir = row.CWD
		}
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("codex delete failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	case ProviderClaude:
		if row.File == "" {
			return errors.New("claude session file is unknown")
		}
		return os.Remove(row.File)
	default:
		return fmt.Errorf("unsupported provider %q", row.Provider)
	}
}

func launch(cwd string, command []string) error {
	if cwd != "" {
		if info, err := os.Stat(cwd); err == nil && info.IsDir() {
			if err := os.Chdir(cwd); err != nil {
				return err
			}
		}
	}

	path, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("%s not found in PATH", command[0])
	}
	return syscallExec(path, command, os.Environ())
}

var (
	sessionIDPattern   = regexp.MustCompile(`(?i)([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
	timestampPattern   = regexp.MustCompile(`"timestamp"\s*:\s*"((?:\\.|[^"\\])*)"`)
	secretValuePattern = regexp.MustCompile(`(?i)\b((?:password|passwd|pass|pwd|parola|sifre|şifre)\w*\s*(?:[:=]|is|was|idi)?\s*)(\S+)`)
	openAIKeyPattern   = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`)
	ansiPattern        = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
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

func cleanText(value string) string {
	value = ansiPattern.ReplaceAllString(value, "")
	value = strings.Join(strings.Fields(value), " ")
	value = secretValuePattern.ReplaceAllString(value, "${1}[redacted]")
	value = openAIKeyPattern.ReplaceAllString(value, "[redacted-openai-key]")
	return strings.TrimSpace(value)
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

func reverseLines(path string, fn func(string) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	const blockSize int64 = 64 * 1024
	position, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	buffer := []byte{}
	for position > 0 {
		readSize := blockSize
		if position < readSize {
			readSize = position
		}
		position -= readSize
		if _, err := file.Seek(position, io.SeekStart); err != nil {
			return err
		}
		chunk := make([]byte, readSize)
		if _, err := io.ReadFull(file, chunk); err != nil {
			return err
		}

		buffer = append(chunk, buffer...)
		lines := strings.Split(string(buffer), "\n")
		buffer = []byte(lines[0])
		for i := len(lines) - 1; i >= 1; i-- {
			line := strings.TrimSpace(lines[i])
			if line != "" && !fn(line) {
				return nil
			}
		}
	}

	if line := strings.TrimSpace(string(buffer)); line != "" {
		fn(line)
	}
	return nil
}

func lastTimestamp(path string) (time.Time, bool) {
	var found time.Time
	_ = reverseLines(path, func(line string) bool {
		if timestamp, ok := timestampFromLine(line); ok {
			found = timestamp
			return false
		}
		return true
	})
	return found, !found.IsZero()
}

func fallbackMTime(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func walkJSONL(root string, fn func(string)) {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		fn(path)
		return nil
	})
}

func bestTimestamp(path string) (time.Time, bool) {
	if timestamp, ok := lastTimestamp(path); ok {
		return timestamp, true
	}
	return fallbackMTime(path)
}

func errNoExec() error {
	return errors.New("exec is unsupported on this platform")
}
