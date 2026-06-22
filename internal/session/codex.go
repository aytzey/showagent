package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type codexLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID  string `json:"id"`
	CWD string `json:"cwd"`
}

type codexTurnContext struct {
	CWD string `json:"cwd"`
}

type codexMessagePayload struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func discoverCodex(codexHome string) []Row {
	var rows []Row
	walkJSONL(filepath.Join(codexHome, "sessions"), func(path string) {
		if row, ok := parseCodex(path); ok {
			rows = append(rows, row)
		}
	})
	return rows
}

func parseCodex(path string) (Row, bool) {
	id, cwd, firstUser := scanCodexStart(path)
	if id == "" {
		return Row{}, false
	}

	lastAt, ok := bestTimestamp(path)
	if !ok {
		return Row{}, false
	}

	if cwd == "" {
		cwd = "(unknown cwd)"
	}

	return Row{
		Provider:  ProviderCodex,
		ID:        id,
		LastAt:    lastAt,
		CWD:       cwd,
		LaunchCWD: cwd,
		File:      path,
		FirstUser: firstUser,
		LastUser:  scanCodexLastUser(path),
	}, true
}

func scanCodexStart(path string) (string, string, string) {
	id := sessionIDFromPath(path)
	cwd := ""
	firstUser := ""

	file, err := os.Open(path)
	if err != nil {
		return "", "", ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scanBufferMax)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record codexLine
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		switch record.Type {
		case "session_meta":
			var meta codexSessionMeta
			if json.Unmarshal(record.Payload, &meta) == nil {
				if meta.ID != "" {
					id = meta.ID
				}
				if meta.CWD != "" {
					cwd = meta.CWD
				}
			}
		case "turn_context":
			var context codexTurnContext
			if json.Unmarshal(record.Payload, &context) == nil && context.CWD != "" {
				cwd = context.CWD
			}
		case "response_item":
			if firstUser == "" {
				if role, text := codexMessage(record.Payload); role == "user" && usefulUserText(text) {
					firstUser = text
				}
			}
		}
	}

	return id, cwd, firstUser
}

func scanCodexLastUser(path string) string {
	lastUser := ""
	_ = reverseLines(path, func(line string) bool {
		var record codexLine
		if err := json.Unmarshal([]byte(line), &record); err != nil || record.Type != "response_item" {
			return true
		}
		role, text := codexMessage(record.Payload)
		if role == "user" && usefulUserText(text) {
			lastUser = text
			return false
		}
		return true
	})
	return lastUser
}

func codexMessage(raw json.RawMessage) (string, string) {
	var payload codexMessagePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", ""
	}
	if payload.Type != "message" {
		return "", ""
	}
	return payload.Role, cleanText(textFromContent(payload.Content))
}
