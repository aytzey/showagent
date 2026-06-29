package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type jcodeSession struct {
	ID              string         `json:"id"`
	ParentID        any            `json:"parent_id"`
	Title           any            `json:"title"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
	Messages        []jcodeMessage `json:"messages"`
	ProviderKey     string         `json:"provider_key"`
	Model           string         `json:"model"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	IsCanary        bool           `json:"is_canary"`
	WorkingDir      string         `json:"working_dir"`
	ShortName       string         `json:"short_name"`
	Status          string         `json:"status"`
	LastPID         int            `json:"last_pid"`
	LastActiveAt    string         `json:"last_active_at"`
	IsDebug         bool           `json:"is_debug"`
	Saved           bool           `json:"saved"`
}

type jcodeMessage struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	DisplayRole string `json:"display_role,omitempty"`
	Content     any    `json:"content"`
	Timestamp   string `json:"timestamp"`
}

func discoverJCode(jcodeHome string) []Row {
	if !JCodeAvailable() {
		return nil
	}

	var rows []Row
	root := filepath.Join(jcodeHome, "sessions")
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil
	}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		if row, ok := parseJCode(path); ok {
			rows = append(rows, row)
		}
		return nil
	})
	return rows
}

func parseJCode(path string) (Row, bool) {
	session, ok := readJCodeSession(path)
	if !ok {
		return Row{}, false
	}

	id := session.ID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if id == "" {
		return Row{}, false
	}

	lastAt, ok := parseTimestamp(session.UpdatedAt)
	if !ok {
		lastAt, ok = parseTimestamp(session.CreatedAt)
	}
	if !ok {
		lastAt, ok = fallbackMTime(path)
	}
	if !ok {
		return Row{}, false
	}

	cwd := strings.TrimSpace(session.WorkingDir)
	if cwd == "" {
		cwd = "(unknown cwd)"
	}

	firstUser, lastUser := jcodeUserPreviews(session.Messages)
	if firstUser == "" && lastUser == "" {
		return Row{}, false
	}
	return Row{
		Provider:  ProviderJCode,
		ID:        id,
		LastAt:    lastAt,
		CWD:       cwd,
		LaunchCWD: cwd,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, true
}

func readJCodeSession(path string) (jcodeSession, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return jcodeSession{}, false
	}
	var session jcodeSession
	if err := json.Unmarshal(content, &session); err != nil {
		return jcodeSession{}, false
	}
	return session, true
}

func jcodeTranscript(path string) ([]Turn, error) {
	session, ok := readJCodeSession(path)
	if !ok {
		return nil, fmt.Errorf("read jcode session %s", path)
	}

	turns := make([]Turn, 0, len(session.Messages))
	for _, message := range session.Messages {
		role := message.Role
		text := cleanText(textFromContent(message.Content))
		if message.DisplayRole == "system" {
			continue
		}
		if !keepTranscriptTurn(role, text) {
			continue
		}
		turns = append(turns, Turn{Role: role, Text: text})
	}
	return turns, nil
}

func writeJCodeConverted(source Row, turns []Turn) (Row, error) {
	sessionID, err := newJCodeSessionID()
	if err != nil {
		return Row{}, err
	}

	now := time.Now().UTC()
	path := filepath.Join(defaultJCodeHome(), "sessions", sessionID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Row{}, err
	}

	providerKey, model, effort := jcodeDefaults(source)
	messages := make([]jcodeMessage, 0, len(turns)+1)
	messages = append(messages, jcodeMessage{
		ID:          newJCodeMessageID(now, 0),
		Role:        "user",
		DisplayRole: "system",
		Content:     []map[string]string{{"type": "text", "text": jcodeSystemReminder(source)}},
		Timestamp:   now.Format(time.RFC3339Nano),
	})

	for index, turn := range turns {
		timestamp := now.Add(time.Duration(index+1) * time.Millisecond)
		messages = append(messages, jcodeMessage{
			ID:        newJCodeMessageID(timestamp, index+1),
			Role:      turn.Role,
			Content:   []map[string]string{{"type": "text", "text": turn.Text}},
			Timestamp: timestamp.Format(time.RFC3339Nano),
		})
	}

	updatedAt := now.Add(time.Duration(len(turns)) * time.Millisecond).Format(time.RFC3339Nano)
	session := jcodeSession{
		ID:              sessionID,
		ParentID:        nil,
		Title:           nil,
		CreatedAt:       now.Format(time.RFC3339Nano),
		UpdatedAt:       updatedAt,
		Messages:        messages,
		ProviderKey:     providerKey,
		Model:           model,
		ReasoningEffort: effort,
		IsCanary:        false,
		WorkingDir:      source.CWD,
		ShortName:       "showagent",
		Status:          "Closed",
		LastPID:         0,
		LastActiveAt:    now.Format(time.RFC3339Nano),
		IsDebug:         false,
		Saved:           false,
	}

	content, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return Row{}, err
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return Row{}, err
	}

	firstUser, lastUser := userPreviewFromTurns(turns)
	return Row{
		Provider:  ProviderJCode,
		ID:        sessionID,
		LastAt:    now.Add(time.Duration(len(turns)) * time.Millisecond),
		CWD:       source.CWD,
		LaunchCWD: source.CWD,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, nil
}

func jcodeDefaults(source Row) (string, string, string) {
	if source.Provider == ProviderJCode {
		if session, ok := readJCodeSession(source.File); ok {
			return nonEmpty(session.ProviderKey, "openai"), nonEmpty(session.Model, "unknown"), session.ReasoningEffort
		}
	}
	return nonEmpty(jcodeDefaultProvider(), "openai"), "unknown", ""
}

func jcodeDefaultProvider() string {
	content, err := os.ReadFile(filepath.Join(defaultJCodeHome(), "config.toml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "default_provider") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		return strings.Trim(strings.TrimSpace(parts[1]), `"`)
	}
	return ""
}

func jcodeSystemReminder(source Row) string {
	return strings.Join([]string{
		"<system-reminder>",
		"# Session Context",
		"Transferred by showagent from " + string(source.Provider) + ".",
		"Workspace: " + source.CWD,
		"</system-reminder>",
	}, "\n")
}

func jcodeUserPreviews(messages []jcodeMessage) (string, string) {
	firstUser := ""
	lastUser := ""
	for _, message := range messages {
		if message.Role != "user" || message.DisplayRole == "system" {
			continue
		}
		text := cleanText(textFromContent(message.Content))
		if !usefulUserText(text) {
			continue
		}
		if firstUser == "" {
			firstUser = text
		}
		lastUser = text
	}
	return firstUser, lastUser
}

func newJCodeSessionID() (string, error) {
	suffix, err := randomHex(8)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("session_showagent_%d_%s", time.Now().UnixMilli(), suffix), nil
}

func newJCodeMessageID(timestamp time.Time, index int) string {
	return fmt.Sprintf("message_%d_%d", timestamp.UnixMilli(), index)
}

func randomHex(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
