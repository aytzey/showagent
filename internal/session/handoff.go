package session

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Turn struct {
	Role string
	Text string
}

type HandoffOptions struct {
	MaxTurns int
}

func (o HandoffOptions) Label() string {
	if o.MaxTurns <= 0 {
		return "all"
	}
	return fmt.Sprintf("last %d", o.MaxTurns)
}

func (o HandoffOptions) apply(turns []Turn) []Turn {
	if o.MaxTurns <= 0 || len(turns) <= o.MaxTurns {
		return turns
	}
	return turns[len(turns)-o.MaxTurns:]
}

func Convert(row Row, target Provider, options HandoffOptions) (Row, error) {
	turns, err := Transcript(row)
	if err != nil {
		return Row{}, err
	}
	turns = options.apply(turns)
	if len(turns) == 0 {
		return Row{}, fmt.Errorf("source session has no transferable user or assistant turns")
	}

	switch target {
	case ProviderCodex:
		return writeCodexConverted(row, turns)
	case ProviderClaude:
		return writeClaudeConverted(row, turns)
	default:
		return Row{}, fmt.Errorf("unsupported target provider %q", target)
	}
}

func Transcript(row Row) ([]Turn, error) {
	switch row.Provider {
	case ProviderCodex:
		return codexTranscript(row.File)
	case ProviderClaude:
		return claudeTranscript(row.File)
	default:
		return nil, fmt.Errorf("unsupported provider %q", row.Provider)
	}
}

func codexTranscript(path string) ([]Turn, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var turns []Turn
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record codexLine
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil || record.Type != "response_item" {
			continue
		}
		role, text := codexMessage(record.Payload)
		if !keepTranscriptTurn(role, text) {
			continue
		}
		turns = append(turns, Turn{Role: role, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return turns, nil
}

func claudeTranscript(path string) ([]Turn, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var turns []Turn
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record claudeRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil || record.Message == nil {
			continue
		}
		role := record.Message.Role
		text := cleanText(textFromContent(record.Message.Content))
		if !keepTranscriptTurn(role, text) {
			continue
		}
		turns = append(turns, Turn{Role: role, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return turns, nil
}

func keepTranscriptTurn(role, text string) bool {
	if role != "user" && role != "assistant" {
		return false
	}
	if role == "user" && !usefulUserText(text) {
		return false
	}
	return strings.TrimSpace(text) != ""
}

func writeCodexConverted(source Row, turns []Turn) (Row, error) {
	sessionID, err := newUUID()
	if err != nil {
		return Row{}, err
	}

	now := time.Now()
	path := codexSessionPath(defaultCodexHome(), sessionID, now)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Row{}, err
	}

	file, err := os.Create(path)
	if err != nil {
		return Row{}, err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	start := now.UTC()
	if err := encoder.Encode(map[string]any{
		"timestamp": start.Format(time.RFC3339Nano),
		"type":      "session_meta",
		"payload": map[string]any{
			"id":             sessionID,
			"timestamp":      start.Format(time.RFC3339Nano),
			"cwd":            source.CWD,
			"originator":     "showagent",
			"cli_version":    "showagent",
			"source":         "cli",
			"thread_source":  "user",
			"model_provider": "openai",
		},
	}); err != nil {
		return Row{}, err
	}

	for index, turn := range turns {
		timestamp := start.Add(time.Duration(index+1) * time.Millisecond).Format(time.RFC3339Nano)
		contentType := "output_text"
		if turn.Role == "user" {
			contentType = "input_text"
		}
		if err := encoder.Encode(map[string]any{
			"timestamp": timestamp,
			"type":      "response_item",
			"payload": map[string]any{
				"type": "message",
				"role": turn.Role,
				"content": []map[string]string{{
					"type": contentType,
					"text": turn.Text,
				}},
			},
		}); err != nil {
			return Row{}, err
		}
	}

	firstUser, lastUser := userPreviewFromTurns(turns)
	return Row{
		Provider:  ProviderCodex,
		ID:        sessionID,
		LastAt:    start.Add(time.Duration(len(turns)) * time.Millisecond),
		CWD:       source.CWD,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, nil
}

func writeClaudeConverted(source Row, turns []Turn) (Row, error) {
	sessionID, err := newUUID()
	if err != nil {
		return Row{}, err
	}

	now := time.Now().UTC()
	path := claudeSessionPath(defaultClaudeHome(), source.CWD, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Row{}, err
	}

	file, err := os.Create(path)
	if err != nil {
		return Row{}, err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{
		"type":           "permission-mode",
		"permissionMode": "default",
		"sessionId":      sessionID,
	}); err != nil {
		return Row{}, err
	}

	var parent any
	for index, turn := range turns {
		messageID, err := newUUID()
		if err != nil {
			return Row{}, err
		}
		record := map[string]any{
			"parentUuid":  parent,
			"isSidechain": false,
			"type":        turn.Role,
			"uuid":        messageID,
			"timestamp":   now.Add(time.Duration(index+1) * time.Millisecond).Format(time.RFC3339Nano),
			"userType":    "external",
			"entrypoint":  "cli",
			"cwd":         source.CWD,
			"sessionId":   sessionID,
			"version":     "showagent",
		}
		if turn.Role == "user" {
			record["message"] = map[string]any{
				"role":    "user",
				"content": turn.Text,
			}
			record["permissionMode"] = "default"
		} else {
			record["requestId"] = syntheticClaudeAPIID("req", messageID)
			record["message"] = map[string]any{
				"id":            syntheticClaudeAPIID("msg", messageID),
				"type":          "message",
				"role":          "assistant",
				"model":         "converted-transcript",
				"content":       []map[string]string{{"type": "text", "text": turn.Text}},
				"stop_reason":   "end_turn",
				"stop_sequence": nil,
				"usage":         map[string]any{},
			}
		}
		if err := encoder.Encode(record); err != nil {
			return Row{}, err
		}
		parent = messageID
	}

	firstUser, lastUser := userPreviewFromTurns(turns)
	return Row{
		Provider:  ProviderClaude,
		ID:        sessionID,
		LastAt:    now.Add(time.Duration(len(turns)) * time.Millisecond),
		CWD:       source.CWD,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, nil
}

func syntheticClaudeAPIID(prefix string, uuid string) string {
	return prefix + "_" + strings.ReplaceAll(uuid, "-", "")
}

func codexSessionPath(codexHome string, sessionID string, now time.Time) string {
	local := now.Local()
	return filepath.Join(
		codexHome,
		"sessions",
		local.Format("2006"),
		local.Format("01"),
		local.Format("02"),
		fmt.Sprintf("rollout-%s-%s.jsonl", local.Format("2006-01-02T15-04-05"), sessionID),
	)
}

func claudeSessionPath(claudeHome string, cwd string, sessionID string) string {
	return filepath.Join(claudeHome, "projects", claudeProjectDir(cwd), sessionID+".jsonl")
}

func claudeProjectDir(cwd string) string {
	clean := filepath.Clean(strings.TrimSpace(cwd))
	if clean == "" || clean == "." || strings.HasPrefix(clean, "(") {
		return "-unknown-cwd"
	}
	value := strings.ReplaceAll(clean, string(filepath.Separator), "-")
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return "-unknown-cwd"
	}
	return value
}

func userPreviewFromTurns(turns []Turn) (string, string) {
	firstUser := ""
	lastUser := ""
	for _, turn := range turns {
		if turn.Role != "user" {
			continue
		}
		if firstUser == "" {
			firstUser = turn.Text
		}
		lastUser = turn.Text
	}
	return firstUser, lastUser
}

func newUUID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}
