package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// claudeProvider adapts Claude Code's session store (per-project *.jsonl
// files under ~/.claude/projects) to the provider registry.
type claudeProvider struct{}

func (claudeProvider) Name() Provider      { return ProviderClaude }
func (claudeProvider) DisplayName() string { return "Claude" }
func (claudeProvider) CommandName() string { return "claude" }
func (claudeProvider) Home() string        { return defaultClaudeHome() }

func (p claudeProvider) ScanTargets() []ScanTarget {
	return []ScanTarget{{
		Provider: ProviderClaude,
		Path:     filepath.Join(p.Home(), "projects"),
		EnvVar:   "CLAUDE_HOME",
	}}
}

func (p claudeProvider) Discover() []Row {
	return discoverClaude(p.Home())
}

func (claudeProvider) ResumeArgs(row Row, options ResumeOptions) []string {
	command := []string{"claude"}
	if options.Dangerous {
		command = append(command, "--dangerously-skip-permissions")
	}
	return append(command, "--resume", row.ID)
}

func (p claudeProvider) CompoundArgs(row Row, options ResumeOptions, prompt string) []string {
	return append(p.ResumeArgs(row, options), prompt)
}

func (claudeProvider) Delete(row Row) error {
	if row.File == "" {
		return errors.New("claude session file is unknown")
	}
	// The sessions index is Claude's own derived data in a private format:
	// it is rebuilt by Claude on its next scan, and its schema may change
	// under us. Clean it up on a best-effort basis, but never let a corrupt
	// or concurrently rewritten index make a session impossible to delete.
	_ = removeClaudeIndexEntry(row)
	return os.Remove(row.File)
}

func removeClaudeIndexEntry(row Row) error {
	indexPath := filepath.Join(filepath.Dir(row.File), "sessions-index.json")
	original, err := os.ReadFile(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(original, &document); err != nil {
		return fmt.Errorf("parse %s: %w", indexPath, err)
	}
	var entries []json.RawMessage
	if raw, ok := document["entries"]; !ok {
		return nil
	} else if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("parse entries in %s: %w", indexPath, err)
	}

	kept := entries[:0]
	removed := false
	for _, raw := range entries {
		var entry struct {
			SessionID string `json:"sessionId"`
			FullPath  string `json:"fullPath"`
		}
		if json.Unmarshal(raw, &entry) == nil &&
			(entry.SessionID == row.ID || samePath(entry.FullPath, row.File)) {
			removed = true
			continue
		}
		kept = append(kept, raw)
	}
	if !removed {
		return nil
	}
	encodedEntries, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	document["entries"] = encodedEntries

	// Avoid overwriting an index that changed while the session file was being
	// removed. A concurrent Claude process can rebuild the index on its next
	// scan; preserving its newer data is safer than forcing this cleanup.
	current, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return errors.New("sessions-index.json changed concurrently")
	}
	return writeFileAtomic(indexPath, func(file *os.File) error {
		encoder := json.NewEncoder(file)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		return encoder.Encode(document)
	})
}

func samePath(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (claudeProvider) Transcript(row Row) ([]Turn, error) {
	return claudeTranscript(row.File)
}

func (claudeProvider) WriteConverted(source Row, turns []Turn) (Row, error) {
	return writeClaudeConverted(source, turns)
}

type claudeRecord struct {
	Type      string         `json:"type"`
	SessionID string         `json:"sessionId"`
	Timestamp string         `json:"timestamp"`
	CWD       string         `json:"cwd"`
	Message   *claudeMessage `json:"message"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func discoverClaude(claudeHome string) []Row {
	paths := jsonlPaths(filepath.Join(claudeHome, "projects"))
	filtered := paths[:0]
	for _, path := range paths {
		if strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			continue
		}
		filtered = append(filtered, path)
	}
	return parseRowsBounded(filtered, parseClaude)
}

func parseClaude(path string) (Row, bool) {
	projectDir := filepath.Base(filepath.Dir(path))
	id, launchCWD, firstExistingCWD, firstUser, ok := scanClaudeHead(path, projectDir)
	if !ok || id == "" {
		return Row{}, false
	}
	tail := scanClaudeTail(path)
	if tail.id != "" {
		id = tail.id
	}
	cwd := tail.cwd
	if launchCWD == "" {
		launchCWD = existingDir(cwd)
	}
	if launchCWD == "" {
		launchCWD = firstExistingCWD
	}

	timestamp, ok := parseTimestamp(tail.lastAt)
	if !ok {
		timestamp, ok = fallbackMTime(path)
	}
	if !ok {
		return Row{}, false
	}
	if cwd == "" {
		cwd = "(unknown cwd)"
	}

	return Row{
		Provider:  ProviderClaude,
		ID:        id,
		LastAt:    timestamp,
		CWD:       cwd,
		LaunchCWD: launchCWD,
		File:      path,
		FirstUser: firstUser,
		LastUser:  tail.lastUser,
	}, true
}

func scanClaudeHead(path string, projectDir string) (id, launchCWD, firstExistingCWD, firstUser string, ok bool) {
	id = sessionIDFromPath(path)
	file, err := os.Open(path)
	if err != nil {
		return "", "", "", "", false
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scanBufferMax)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record claudeRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record.SessionID != "" {
			id = record.SessionID
		}
		if record.CWD != "" {
			if firstExistingCWD == "" {
				firstExistingCWD = existingDir(record.CWD)
			}
			if claudeProjectDir(record.CWD) == projectDir {
				launchCWD = record.CWD
			}
		}

		if firstUser == "" {
			firstUser = claudeUserText(record)
		}
		if id != "" && launchCWD != "" && firstUser != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", "", "", false
	}
	return id, launchCWD, firstExistingCWD, firstUser, true
}

type claudeTail struct {
	id       string
	cwd      string
	lastAt   string
	lastUser string
}

func scanClaudeTail(path string) claudeTail {
	var tail claudeTail
	_ = reverseLines(path, func(line string) bool {
		var record claudeRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return true
		}
		if tail.id == "" {
			tail.id = record.SessionID
		}
		if tail.cwd == "" {
			tail.cwd = strings.TrimSpace(record.CWD)
		}
		if tail.lastAt == "" {
			tail.lastAt = record.Timestamp
		}
		if tail.lastUser == "" {
			tail.lastUser = claudeUserText(record)
		}
		return tail.id == "" || tail.cwd == "" || tail.lastAt == "" || tail.lastUser == ""
	})
	return tail
}

func claudeUserText(record claudeRecord) string {
	if record.Type != "user" || record.Message == nil || record.Message.Role != "user" {
		return ""
	}
	text := cleanPreviewText(textFromContent(record.Message.Content))
	if !usefulUserText(text) {
		return ""
	}
	return text
}
