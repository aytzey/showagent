package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func trimClaude(row Row, source []byte, options HandoffOptions) (trimResult, error) {
	boundary := max(options.BeforeUser, options.ThroughUser)
	var result trimResult
	var users, turns, messages, offset, cut int
	var lastMessage string
	var customTitle []byte
	parents := map[string]string{}
	pendingTools := map[string]bool{}
	for line := range bytes.Lines(source) {
		start := offset
		offset += len(line)
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record struct {
			claudeRecord
			UUID      string          `json:"uuid"`
			Parent    json.RawMessage `json:"parentUuid"`
			Sidechain bool            `json:"isSidechain"`
			Subtype   string          `json:"subtype"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type == "" {
			return trimResult{}, errors.New("the Claude session contains a malformed JSONL record")
		}
		if record.SessionID != "" && record.SessionID != row.ID {
			return trimResult{}, errors.New("the Claude session contains records for another session")
		}
		if record.Sidechain || record.Subtype == "compact_boundary" {
			return trimResult{}, errors.New("the Claude session has a sidechain or compaction boundary and cannot be trimmed safely")
		}
		if cut > 0 && record.Type == "custom-title" && record.SessionID == row.ID && record.Message == nil && record.UUID == "" {
			customTitle = source[start:offset]
		}
		var parent string
		if record.UUID != "" && len(record.Parent) > 0 {
			if _, exists := parents[record.UUID]; exists || json.Unmarshal(record.Parent, &parent) != nil {
				return trimResult{}, errors.New("the Claude session contains invalid message links")
			}
			if _, exists := parents[parent]; parent != "" && !exists {
				return trimResult{}, errors.New("the Claude session has a missing parent record")
			}
			parents[record.UUID] = parent
		}
		if record.Message == nil {
			continue
		}
		role := record.Message.Role
		if role != "user" && role != "assistant" {
			continue
		}
		if record.Type != role || record.SessionID != row.ID || record.UUID == "" || len(record.Parent) == 0 {
			return trimResult{}, errors.New("the Claude session contains a message without matching native metadata")
		}
		for parent != "" && parent != lastMessage {
			// Non-conversation records may occur between two linked messages.
			// A link through an older conversation message is a branch.
			if messageParent, exists := parents[parent]; exists {
				parent = messageParent
			} else {
				break
			}
		}
		if parent != lastMessage {
			return trimResult{}, errors.New("the Claude session has a forked or incomplete message chain")
		}
		lastMessage = record.UUID
		messages++
		text := cleanTranscriptText(textFromContent(record.Message.Content))
		kept := keepTranscriptTurn(role, text)
		if kept {
			turns++
			if role == "user" {
				users++
			}
		}
		isBoundary := kept && role == "user" && users == boundary
		if isBoundary && options.BeforeUser > 0 {
			cut = start
			result.keptUsers, result.keptTurns = users-1, turns-1
			result.nativeMessages = messages - 1
			result.boundaryText = text
			if len(pendingTools) != 0 {
				return trimResult{}, errors.New("this point splits a Claude tool call from its result")
			}
		}
		if cut == 0 {
			trackClaudeTrimTools(record.Message.Content, pendingTools)
		}
		if isBoundary && options.ThroughUser > 0 {
			cut = offset
			result.keptUsers, result.keptTurns = users, turns
			result.nativeMessages = messages
			result.boundaryText = text
			if len(pendingTools) != 0 {
				return trimResult{}, errors.New("this point splits a Claude tool call from its result")
			}
		}
	}
	if users < boundary {
		return trimResult{}, fmt.Errorf("user message #%d is not in this session", boundary)
	}
	if result.keptUsers == 0 {
		return trimResult{}, errors.New("trimming here would remove every user message; delete the whole session separately if intended")
	}
	if result.keptTurns == turns {
		return trimResult{}, errors.New("there are no conversation turns to remove at this point")
	}
	result.replacement = source[:cut]
	if len(customTitle) != 0 {
		// A user-assigned session title names the whole session. It remains
		// available after a trim even when it was written near the old tail.
		result.replacement = append(source[:cut:cut], customTitle...)
	}
	result.removedUsers, result.removedTurns = users-result.keptUsers, turns-result.keptTurns
	return result, nil
}

// refreshTrimmedClaudeIndex preserves Claude's existing identity and fields
// unrelated to the transcript while discarding stale generated previews.
func refreshTrimmedClaudeIndex(row Row, messages int) error {
	indexPath := filepath.Join(filepath.Dir(row.File), "sessions-index.json")
	file, err := openTrimWriter(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	original, revision, err := readTrimSnapshot(indexPath, file)
	if err != nil {
		return err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(original, &document); err != nil {
		return fmt.Errorf("read Claude session index: %w", err)
	}
	var entries []json.RawMessage
	if raw, ok := document["entries"]; !ok {
		return nil
	} else if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("read Claude session index entries: %w", err)
	}
	info, err := os.Stat(row.File)
	if err != nil {
		return err
	}
	changed := false
	for i, raw := range entries {
		var entry map[string]json.RawMessage
		var id string
		if json.Unmarshal(raw, &entry) != nil || json.Unmarshal(entry["sessionId"], &id) != nil || id != row.ID {
			continue
		}
		for key, value := range map[string]any{
			"fileMtime":    info.ModTime().UnixMilli(),
			"modified":     row.LastAt.UTC().Format(time.RFC3339Nano),
			"messageCount": messages,
		} {
			entry[key], _ = json.Marshal(value)
		}
		for _, key := range []string{"summary", "aiTitle", "lastPrompt"} {
			delete(entry, key)
		}
		entries[i], _ = json.Marshal(entry)
		changed = true
	}
	if !changed {
		return nil
	}
	document["entries"], _ = json.Marshal(entries)
	return writeFileAtomicWith(indexPath, func(file *os.File) error {
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		return encoder.Encode(document)
	}, func(from, to string) error {
		if stamp, err := trimStamp(to, file); err != nil || stamp != revision.stamp {
			return errors.New("the Claude session index changed concurrently")
		}
		return replaceTrimFile(from, to)
	})
}

func trackClaudeTrimTools(content any, pending map[string]bool) {
	blocks, _ := content.([]any)
	for _, block := range blocks {
		fields, _ := block.(map[string]any)
		switch fields["type"] {
		case "tool_use":
			if id, _ := fields["id"].(string); id != "" {
				pending[id] = true
			}
		case "tool_result":
			if id, _ := fields["tool_use_id"].(string); id != "" {
				delete(pending, id)
			}
		}
	}
}
