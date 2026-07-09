package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Gemini CLI support.
//
// Gemini CLI (github.com/google-gemini/gemini-cli) records chats under
// <home>/.gemini/tmp/<project>/chats/session-<timestamp>-<sid8>.json[l],
// where <project> is either a slug registered in ~/.gemini/projects.json
// (current releases, with a .project_root marker file holding the absolute
// workspace path inside each project dir) or sha256(workspace path) hex
// (legacy releases, which current ones migrate on startup). Verified against
// google-gemini/gemini-cli at commit 172ff92c (main, 2026-07-08):
//
//	packages/core/src/utils/paths.ts             getProjectHash, GEMINI_CLI_HOME
//	packages/core/src/config/storage.ts          getProjectTempDir, migration
//	packages/core/src/config/projectRegistry.ts  projects.json, .project_root
//	packages/core/src/services/chatRecordingService.ts
//	                                             file naming, JSONL records,
//	                                             legacy whole-file fallback
//	packages/core/src/services/chatRecordingTypes.ts
//	                                             ConversationRecord schema
//	packages/cli/src/utils/sessionUtils.ts       --resume uuid|index|latest
//
// A session file is either a legacy .json document holding one
// ConversationRecord, or a .jsonl log replayed line by line: a metadata line
// (sessionId + projectHash), message records (keyed by id; a repeated id
// replaces the earlier record in place), {"$set": {...}} metadata updates
// whose optional messages array is a checkpoint replacing all messages, and
// {"$rewindTo": "<id>"} records truncating the conversation at that message.
//
// The CLI resumes sessions with `gemini --resume <sessionId>` run inside the
// workspace directory (the store is project-scoped), and GEMINI_CLI_HOME
// relocates the CLI's home directory, which keeps tests hermetic.
const ProviderGemini Provider = "gemini"

// geminiTimeFormat matches JavaScript's Date.toISOString(), which is what
// gemini-cli writes into startTime/lastUpdated/timestamp fields.
const geminiTimeFormat = "2006-01-02T15:04:05.000Z"

// geminiProvider adapts the Gemini CLI chat store to the provider registry.
type geminiProvider struct{}

func (geminiProvider) Name() Provider      { return ProviderGemini }
func (geminiProvider) DisplayName() string { return "Gemini" }
func (geminiProvider) CommandName() string { return "gemini" }
func (geminiProvider) Home() string        { return defaultGeminiHome() }

func (p geminiProvider) ScanTarget() ScanTarget {
	return ScanTarget{
		Provider: ProviderGemini,
		Path:     filepath.Join(p.Home(), "tmp"),
		EnvVar:   "GEMINI_CLI_HOME",
	}
}

func (p geminiProvider) Discover() []Row {
	return discoverGemini(p.Home())
}

func (geminiProvider) ResumeArgs(row Row, options ResumeOptions) []string {
	command := []string{"gemini"}
	if options.Dangerous {
		command = append(command, "--yolo")
	}
	return append(command, "--resume", row.ID)
}

func (p geminiProvider) CompoundArgs(row Row, options ResumeOptions, prompt string) []string {
	// --prompt-interactive submits the prompt and stays interactive; it does
	// not conflict with --resume (only --prompt/positional prompts do).
	return append(p.ResumeArgs(row, options), "--prompt-interactive", prompt)
}

func (geminiProvider) Delete(row Row) error {
	if row.File == "" {
		return errors.New("gemini session file is unknown")
	}
	return os.Remove(row.File)
}

func (geminiProvider) Transcript(row Row) ([]Turn, error) {
	return geminiTranscript(row.File)
}

func (geminiProvider) WriteConverted(source Row, turns []Turn) (Row, error) {
	return writeGeminiConverted(source, turns)
}

// defaultGeminiHome mirrors gemini-cli's own home resolution: GEMINI_CLI_HOME
// replaces the home directory the .gemini dir lives in (paths.ts homedir()).
func defaultGeminiHome() string {
	if value := os.Getenv("GEMINI_CLI_HOME"); value != "" {
		return filepath.Join(expandHome(value), ".gemini")
	}
	return filepath.Join(homeDir(), ".gemini")
}

// geminiMessage is one conversation message after replaying the record log.
type geminiMessage struct {
	ID           string
	Type         string
	Text         string
	HasToolCalls bool
}

// geminiConversation is the subset of gemini-cli's ConversationRecord that
// showagent needs.
type geminiConversation struct {
	SessionID   string
	Kind        string
	StartTime   string
	LastUpdated string
	Messages    []geminiMessage
}

func discoverGemini(geminiHome string) []Row {
	tmpRoot := filepath.Join(geminiHome, "tmp")
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		return nil
	}
	pathBySlug := geminiRegistryPaths(geminiHome)

	var rows []Row
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		projectDir := filepath.Join(tmpRoot, entry.Name())
		cwd := geminiProjectRootMarker(projectDir)
		if cwd == "" {
			cwd = pathBySlug[entry.Name()]
		}

		chatsDir := filepath.Join(projectDir, "chats")
		chats, err := os.ReadDir(chatsDir)
		if err != nil {
			continue
		}
		for _, chat := range chats {
			// Subagent transcripts live in nested per-parent directories and
			// without the session- prefix; both checks keep them out.
			if chat.IsDir() || !strings.HasPrefix(chat.Name(), "session-") {
				continue
			}
			if ext := filepath.Ext(chat.Name()); ext != ".json" && ext != ".jsonl" {
				continue
			}
			if row, ok := parseGemini(filepath.Join(chatsDir, chat.Name()), cwd); ok {
				rows = append(rows, row)
			}
		}
	}
	return rows
}

// geminiProjectRootMarker reads the .project_root ownership marker gemini-cli
// keeps in each project temp dir, returning "" when it is missing.
func geminiProjectRootMarker(projectDir string) string {
	content, err := os.ReadFile(filepath.Join(projectDir, ".project_root"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}

// geminiRegistryPaths inverts ~/.gemini/projects.json, which maps absolute
// workspace paths to project dir slugs.
func geminiRegistryPaths(geminiHome string) map[string]string {
	content, err := os.ReadFile(filepath.Join(geminiHome, "projects.json"))
	if err != nil {
		return nil
	}
	var registry struct {
		Projects map[string]string `json:"projects"`
	}
	if err := json.Unmarshal(content, &registry); err != nil {
		return nil
	}
	paths := make(map[string]string, len(registry.Projects))
	for path, slug := range registry.Projects {
		paths[slug] = path
	}
	return paths
}

func parseGemini(path string, cwd string) (Row, bool) {
	conversation, err := loadGeminiConversation(path)
	if err != nil || conversation.SessionID == "" {
		// Deliberate skip, matching the other providers: discovery shows what
		// it can parse and never fails the whole listing.
		return Row{}, false
	}
	// Subagent sessions are implementation details of a tool call; gemini-cli
	// hides them from resume flows and so does showagent.
	if conversation.Kind == "subagent" {
		return Row{}, false
	}

	firstUser := ""
	lastUser := ""
	resumable := false
	for _, message := range conversation.Messages {
		switch message.Type {
		case "user":
			text := geminiUserText(message)
			if text == "" {
				continue
			}
			resumable = true
			if firstUser == "" {
				firstUser = text
			}
			lastUser = text
		case "gemini":
			if strings.TrimSpace(message.Text) != "" || message.HasToolCalls {
				resumable = true
			}
		}
	}
	// Sessions with no conversation content (startup-only, command-only) are
	// not offered for resumption by gemini-cli either.
	if !resumable {
		return Row{}, false
	}

	timestamp, ok := parseTimestamp(conversation.LastUpdated)
	if !ok {
		timestamp, ok = parseTimestamp(conversation.StartTime)
	}
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
		Provider:  ProviderGemini,
		ID:        conversation.SessionID,
		LastAt:    timestamp,
		CWD:       cwd,
		LaunchCWD: cwd,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, true
}

// geminiIgnoredUserText mirrors gemini-cli's isIgnoredUserContent: slash and
// shell-passthrough commands plus injected context never count as prompts.
func geminiIgnoredUserText(text string) bool {
	text = strings.TrimSpace(text)
	return text == "" ||
		strings.HasPrefix(text, "/") ||
		strings.HasPrefix(text, "?") ||
		strings.HasPrefix(text, "<session_context>") ||
		strings.HasPrefix(text, "<hook_context>")
}

func geminiUserText(message geminiMessage) string {
	if message.Type != "user" || geminiIgnoredUserText(message.Text) {
		return ""
	}
	text := cleanPreviewText(message.Text)
	if !usefulUserText(text) {
		return ""
	}
	return text
}

// loadGeminiConversation replays a session file into its final conversation
// state. JSONL files are replayed record by record; a file whose lines yield
// no metadata falls back to being parsed as one legacy JSON document.
func loadGeminiConversation(path string) (geminiConversation, error) {
	file, err := os.Open(path)
	if err != nil {
		return geminiConversation{}, err
	}
	defer func() { _ = file.Close() }()

	var conversation geminiConversation
	indexByID := map[string]int{}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scanBufferMax)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			// Individual bad lines are ignored, exactly like gemini-cli's
			// reader; a legacy pretty-printed document lands here too and is
			// handled by the whole-file fallback below.
			continue
		}

		switch {
		case hasStringField(record, "$rewindTo"):
			rewindTo, _ := record["$rewindTo"].(string)
			if index, ok := indexByID[rewindTo]; ok {
				conversation.Messages = conversation.Messages[:index]
			} else {
				conversation.Messages = conversation.Messages[:0]
			}
			indexByID = reindexGeminiMessages(conversation.Messages)
		case hasStringField(record, "id"):
			message := geminiMessageFromRecord(record)
			if index, ok := indexByID[message.ID]; ok {
				conversation.Messages[index] = message
			} else {
				indexByID[message.ID] = len(conversation.Messages)
				conversation.Messages = append(conversation.Messages, message)
			}
		case hasObjectField(record, "$set"):
			set, _ := record["$set"].(map[string]any)
			applyGeminiMetadata(&conversation, set)
			if messages, ok := geminiMessagesFromAny(set["messages"]); ok {
				// Checkpoint: replace the conversation with the snapshot.
				conversation.Messages = messages
				indexByID = reindexGeminiMessages(messages)
			}
		case hasStringField(record, "sessionId") && hasStringField(record, "projectHash"):
			// Initial metadata line, or an entire legacy record on one line.
			applyGeminiMetadata(&conversation, record)
			if messages, ok := geminiMessagesFromAny(record["messages"]); ok {
				conversation.Messages = messages
				indexByID = reindexGeminiMessages(messages)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return geminiConversation{}, err
	}

	if conversation.SessionID != "" {
		return conversation, nil
	}
	return loadGeminiLegacyConversation(path)
}

// loadGeminiLegacyConversation parses a pre-JSONL session file: one JSON
// document holding the whole ConversationRecord, typically pretty-printed.
func loadGeminiLegacyConversation(path string) (geminiConversation, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return geminiConversation{}, err
	}
	var record map[string]any
	if err := json.Unmarshal(content, &record); err != nil {
		return geminiConversation{}, fmt.Errorf("parse gemini session %s: %w", path, err)
	}

	var conversation geminiConversation
	applyGeminiMetadata(&conversation, record)
	if messages, ok := geminiMessagesFromAny(record["messages"]); ok {
		conversation.Messages = messages
	}
	if conversation.SessionID == "" {
		return geminiConversation{}, fmt.Errorf("%s is not a gemini session file", path)
	}
	return conversation, nil
}

func applyGeminiMetadata(conversation *geminiConversation, fields map[string]any) {
	if value, ok := fields["sessionId"].(string); ok && value != "" {
		conversation.SessionID = value
	}
	if value, ok := fields["kind"].(string); ok && value != "" {
		conversation.Kind = value
	}
	if value, ok := fields["startTime"].(string); ok && value != "" {
		conversation.StartTime = value
	}
	if value, ok := fields["lastUpdated"].(string); ok && value != "" {
		conversation.LastUpdated = value
	}
}

func geminiMessageFromRecord(record map[string]any) geminiMessage {
	message := geminiMessage{}
	message.ID, _ = record["id"].(string)
	message.Type, _ = record["type"].(string)
	message.Text = geminiContentText(record["content"])
	if calls, ok := record["toolCalls"].([]any); ok && len(calls) > 0 {
		message.HasToolCalls = true
	}
	return message
}

func geminiMessagesFromAny(value any) ([]geminiMessage, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	messages := make([]geminiMessage, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok || !hasStringField(record, "id") {
			continue
		}
		messages = append(messages, geminiMessageFromRecord(record))
	}
	return messages, true
}

func reindexGeminiMessages(messages []geminiMessage) map[string]int {
	index := make(map[string]int, len(messages))
	for i, message := range messages {
		index[message.ID] = i
	}
	return index
}

// geminiContentText flattens a PartListUnion (string, part object, or a list
// of either) into text, skipping thought parts like gemini-cli's own resume
// conversion does.
func geminiContentText(content any) string {
	switch typed := content.(type) {
	case nil:
		return ""
	case []any:
		var parts []string
		for _, item := range typed {
			if text := geminiPartText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return geminiPartText(typed)
	}
}

func geminiPartText(part any) string {
	switch typed := part.(type) {
	case string:
		return typed
	case map[string]any:
		if thought, ok := typed["thought"].(bool); ok && thought {
			return ""
		}
		if text, ok := typed["text"].(string); ok {
			return text
		}
	}
	return ""
}

func geminiTranscript(path string) ([]Turn, error) {
	conversation, err := loadGeminiConversation(path)
	if err != nil {
		return nil, err
	}

	var turns []Turn
	for _, message := range conversation.Messages {
		var role string
		switch message.Type {
		case "user":
			if geminiIgnoredUserText(message.Text) {
				continue
			}
			role = "user"
		case "gemini":
			role = "assistant"
		default:
			// info/error/warning records are UI noise, not conversation.
			continue
		}
		text := cleanTranscriptText(message.Text)
		if !keepTranscriptTurn(role, text) {
			continue
		}
		turns = append(turns, Turn{Role: role, Text: text})
	}
	return turns, nil
}

// writeGeminiConverted synthesizes a legacy-format session document, which
// both current gemini-cli (whose reader takes a single-line legacy record or
// falls back to whole-file parsing) and older hash-dir releases load, and
// which `gemini --resume` migrates to the JSONL log on first use.
func writeGeminiConverted(source Row, turns []Turn) (Row, error) {
	cwd := strings.TrimSpace(source.CWD)
	if cwd == "" || strings.HasPrefix(cwd, "(") {
		return Row{}, errors.New("gemini conversion needs a known workspace directory")
	}

	sessionID, err := newUUID()
	if err != nil {
		return Row{}, err
	}
	now := time.Now().UTC()

	geminiHome := defaultGeminiHome()
	projectDir := filepath.Join(geminiHome, "tmp", geminiProjectDirName(geminiHome, cwd))
	chatsDir := filepath.Join(projectDir, "chats")
	if err := os.MkdirAll(chatsDir, 0o700); err != nil {
		return Row{}, err
	}
	// Claim the project dir the way gemini-cli does, so both gemini's slug
	// migration and our own discovery can map it back to the workspace.
	// Without the marker a freshly created hash dir resolves to no cwd and the
	// converted session becomes undiscoverable (and undeletable) in the picker.
	markerPath := filepath.Join(projectDir, ".project_root")
	if _, err := os.Stat(markerPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(markerPath, []byte(cwd+"\n"), 0o600); err != nil {
			return Row{}, err
		}
	}
	path := filepath.Join(chatsDir, fmt.Sprintf(
		// The CLI's own naming: session-<UTC minute stamp>-<sessionId[:8]>.
		"session-%s-%s.json", now.Format("2006-01-02T15-04"), sessionID[:8],
	))

	messages := make([]map[string]any, 0, len(turns))
	for index, turn := range turns {
		messageID, err := newUUID()
		if err != nil {
			return Row{}, err
		}
		messageType := "gemini"
		if turn.Role == "user" {
			messageType = "user"
		}
		messages = append(messages, map[string]any{
			"id":        messageID,
			"timestamp": now.Add(time.Duration(index+1) * time.Millisecond).Format(geminiTimeFormat),
			"type":      messageType,
			"content":   turn.Text,
		})
	}

	lastAt := now.Add(time.Duration(len(turns)) * time.Millisecond)
	if err := writeFileAtomic(path, func(file *os.File) error {
		encoder := json.NewEncoder(file)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(map[string]any{
			"sessionId":   sessionID,
			"projectHash": geminiProjectHash(cwd),
			"startTime":   now.Format(geminiTimeFormat),
			"lastUpdated": lastAt.Format(geminiTimeFormat),
			"messages":    messages,
		})
	}); err != nil {
		return Row{}, err
	}

	firstUser, lastUser := userPreviewFromTurns(turns)
	return Row{
		Provider:  ProviderGemini,
		ID:        sessionID,
		LastAt:    lastAt,
		CWD:       source.CWD,
		LaunchCWD: source.CWD,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, nil
}

// geminiProjectDirName picks the project directory for cwd: an existing dir
// claimed via a .project_root marker, then the projects.json registry entry,
// then the legacy sha256 name — which old releases read natively and current
// ones migrate into a slug dir on their next start.
func geminiProjectDirName(geminiHome string, cwd string) string {
	tmpRoot := filepath.Join(geminiHome, "tmp")
	if entries, err := os.ReadDir(tmpRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && geminiProjectRootMarker(filepath.Join(tmpRoot, entry.Name())) == cwd {
				return entry.Name()
			}
		}
	}

	content, err := os.ReadFile(filepath.Join(geminiHome, "projects.json"))
	if err == nil {
		var registry struct {
			Projects map[string]string `json:"projects"`
		}
		if err := json.Unmarshal(content, &registry); err == nil {
			if slug, ok := registry.Projects[cwd]; ok && slug != "" {
				return slug
			}
		}
	}

	return geminiProjectHash(cwd)
}

// geminiProjectHash is gemini-cli's getProjectHash: sha256 hex of the
// workspace path.
func geminiProjectHash(cwd string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(cwd)))
}

func hasStringField(record map[string]any, key string) bool {
	value, ok := record[key].(string)
	return ok && value != ""
}

func hasObjectField(record map[string]any, key string) bool {
	_, ok := record[key].(map[string]any)
	return ok
}
