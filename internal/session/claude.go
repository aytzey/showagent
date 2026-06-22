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
	firstUser := ""
	lastUser := ""
	var lastAt string

	file, err := os.Open(path)
	if err != nil {
		return Row{}, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
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

	return Row{
		Provider:  ProviderClaude,
		ID:        id,
		LastAt:    timestamp,
		CWD:       cwd,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, true
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
