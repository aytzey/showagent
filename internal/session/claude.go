package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

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
	var rows []Row
	walkJSONL(filepath.Join(claudeHome, "projects"), func(path string) {
		if strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			return
		}
		if row, ok := parseClaude(path); ok {
			rows = append(rows, row)
		}
	})
	return rows
}

func parseClaude(path string) (Row, bool) {
	id := sessionIDFromPath(path)
	cwd := ""
	launchCWD := ""
	firstUser := ""
	lastUser := ""
	var lastAt string
	projectDir := filepath.Base(filepath.Dir(path))

	file, err := os.Open(path)
	if err != nil {
		return Row{}, false
	}
	defer file.Close()

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
			cwd = record.CWD
			if claudeProjectDir(record.CWD) == projectDir {
				launchCWD = record.CWD
			}
		}
		if record.Timestamp != "" {
			lastAt = record.Timestamp
		}

		text := claudeUserText(record)
		if text != "" {
			if firstUser == "" {
				firstUser = text
			}
			lastUser = text
		}
	}

	if id == "" {
		return Row{}, false
	}

	timestamp, ok := parseTimestamp(lastAt)
	if !ok {
		timestamp, ok = fallbackMTime(path)
	}
	if !ok {
		return Row{}, false
	}
	if cwd == "" {
		cwd = "(unknown cwd)"
	}
	if launchCWD == "" {
		launchCWD = claudeProjectPath(projectDir)
	}

	return Row{
		Provider:  ProviderClaude,
		ID:        id,
		LastAt:    timestamp,
		CWD:       cwd,
		LaunchCWD: launchCWD,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, true
}

func claudeProjectPath(projectDir string) string {
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" || projectDir == "." || projectDir == "-unknown-cwd" {
		return ""
	}
	if strings.HasPrefix(projectDir, "-") {
		return string(filepath.Separator) + strings.ReplaceAll(strings.TrimPrefix(projectDir, "-"), "-", string(filepath.Separator))
	}
	return strings.ReplaceAll(projectDir, "-", string(filepath.Separator))
}

func claudeUserText(record claudeRecord) string {
	if record.Type != "user" || record.Message == nil || record.Message.Role != "user" {
		return ""
	}
	text := cleanText(textFromContent(record.Message.Content))
	if !usefulUserText(text) {
		return ""
	}
	return text
}
