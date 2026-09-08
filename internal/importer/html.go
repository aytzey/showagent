package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxEmbeddedJSONDocuments = 256

type conversationCandidate struct {
	title          string
	sourceRevision string
	messages       []Message
	warnings       map[string]int
	completeness   Completeness
}

func parseShareHTML(data []byte, source SourceKind) (Conversation, error) {
	if len(data) > MaxHTMLBytes {
		return Conversation{}, fmt.Errorf("%w: HTML exceeds %d bytes", ErrLimitExceeded, MaxHTMLBytes)
	}
	if !utf8.Valid(data) {
		return Conversation{}, ErrInvalidUTF8
	}
	documents := embeddedJSONDocuments(data)
	if len(documents) == 0 {
		return Conversation{}, ErrConversationUnidentifiable
	}
	return parseShareDocuments(documents, source)
}

func parseShareJSON(data []byte, source SourceKind) (Conversation, error) {
	if len(data) > MaxHTMLBytes {
		return Conversation{}, fmt.Errorf("%w: snapshot JSON exceeds %d bytes", ErrLimitExceeded, MaxHTMLBytes)
	}
	if !utf8.Valid(data) {
		return Conversation{}, ErrInvalidUTF8
	}
	document, ok := decodeJSONValue(data)
	if !ok {
		return Conversation{}, ErrConversationUnidentifiable
	}
	return parseShareDocuments([]any{document}, source)
}

func parseShareDocuments(documents []any, source SourceKind) (Conversation, error) {
	var candidates []conversationCandidate
	for _, document := range documents {
		var err error
		switch source {
		case SourceChatGPTShare:
			err = walkChatGPTJSON(document, 0, &candidates)
		case SourceClaudeShare:
			err = collectClaudeCandidates(document, &candidates)
		default:
			return Conversation{}, ErrConversationUnidentifiable
		}
		if err != nil {
			return Conversation{}, fmt.Errorf("%w: %v", ErrConversationUnidentifiable, err)
		}
	}
	candidates = uniqueCandidates(candidates)
	if len(candidates) != 1 {
		return Conversation{}, ErrConversationUnidentifiable
	}
	candidate := candidates[0]
	if err := validateMessages(candidate.messages); err != nil {
		return Conversation{}, err
	}

	return Conversation{
		SchemaVersion:  SchemaVersion,
		SourceKind:     source,
		AcquiredAt:     time.Now().UTC(),
		Title:          candidate.title,
		SourceRevision: candidate.sourceRevision,
		Messages:       candidate.messages,
		Warnings:       warningList(candidate.warnings),
		Completeness:   candidate.completeness,
	}, nil
}

func embeddedJSONDocuments(data []byte) []any {
	lower := bytes.ToLower(data)
	var documents []any
	for offset := 0; offset < len(data) && len(documents) < maxEmbeddedJSONDocuments; {
		relative := bytes.Index(lower[offset:], []byte("<script"))
		if relative < 0 {
			break
		}
		start := offset + relative
		afterName := start + len("<script")
		if afterName < len(data) && !isHTMLSpace(data[afterName]) && data[afterName] != '>' {
			offset = afterName
			continue
		}
		openEnd := findTagEnd(data, afterName)
		if openEnd < 0 {
			break
		}
		closeRelative := bytes.Index(lower[openEnd+1:], []byte("</script"))
		if closeRelative < 0 {
			break
		}
		closeStart := openEnd + 1 + closeRelative
		tag := data[start : openEnd+1]
		body := bytes.TrimSpace(data[openEnd+1 : closeStart])
		if isEmbeddedJSONTag(tag) {
			if document, ok := decodeJSONValue(body); ok {
				documents = append(documents, document)
			}
		} else if document, ok := decodeKnownJSONAssignment(body); ok {
			documents = append(documents, document)
		} else if document, ok := decodeReactRouterEnqueue(body); ok {
			documents = append(documents, document)
		}
		offset = closeStart + len("</script")
	}
	return documents
}

func findTagEnd(data []byte, start int) int {
	var quote byte
	for index := start; index < len(data); index++ {
		switch {
		case quote != 0 && data[index] == quote:
			quote = 0
		case quote == 0 && (data[index] == '\'' || data[index] == '"'):
			quote = data[index]
		case quote == 0 && data[index] == '>':
			return index
		}
	}
	return -1
}

func isHTMLSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n' || value == '\f'
}

func isEmbeddedJSONTag(tag []byte) bool {
	scriptType := scriptAttribute(tag, "type")
	id := scriptAttribute(tag, "id")
	return strings.EqualFold(scriptType, "application/json") || strings.EqualFold(id, "__NEXT_DATA__")
}

func scriptAttribute(tag []byte, wanted string) string {
	index := len("<script")
	for index < len(tag) {
		for index < len(tag) && isHTMLSpace(tag[index]) {
			index++
		}
		if index >= len(tag) || tag[index] == '>' || tag[index] == '/' {
			return ""
		}
		nameStart := index
		for index < len(tag) && isAttributeNameByte(tag[index]) {
			index++
		}
		if nameStart == index {
			index++
			continue
		}
		name := string(tag[nameStart:index])
		for index < len(tag) && isHTMLSpace(tag[index]) {
			index++
		}
		value := ""
		if index < len(tag) && tag[index] == '=' {
			index++
			for index < len(tag) && isHTMLSpace(tag[index]) {
				index++
			}
			if index < len(tag) && (tag[index] == '\'' || tag[index] == '"') {
				quote := tag[index]
				index++
				valueStart := index
				for index < len(tag) && tag[index] != quote {
					index++
				}
				value = string(tag[valueStart:index])
				if index < len(tag) {
					index++
				}
			} else {
				valueStart := index
				for index < len(tag) && !isHTMLSpace(tag[index]) && tag[index] != '>' {
					index++
				}
				value = string(tag[valueStart:index])
			}
		}
		if strings.EqualFold(name, wanted) {
			return value
		}
	}
	return ""
}

func isAttributeNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_' || value == '-' || value == ':'
}

func decodeJSONValue(data []byte) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return value, true
}

func decodeKnownJSONAssignment(data []byte) (any, bool) {
	markers := []string{"__NEXT_DATA__", "__INITIAL_STATE__", "__CLAUDE_STATE__", "__remixContext"}
	text := string(data)
	for _, marker := range markers {
		markerIndex := strings.Index(text, marker)
		if markerIndex < 0 {
			continue
		}
		equals := strings.IndexByte(text[markerIndex+len(marker):], '=')
		if equals < 0 {
			continue
		}
		start := markerIndex + len(marker) + equals + 1
		decoder := json.NewDecoder(strings.NewReader(text[start:]))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) == nil {
			return value, true
		}
	}
	return nil, false
}

// decodeReactRouterEnqueue handles the inert, index-deduplicated snapshot used
// by current ChatGPT share pages. It decodes JSON strings and references only;
// no JavaScript is evaluated.
func decodeReactRouterEnqueue(data []byte) (any, bool) {
	const marker = "window.__reactRouterContext.streamController.enqueue("
	text := string(data)
	index := strings.Index(text, marker)
	if index < 0 {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(text[index+len(marker):]))
	var payload string
	if err := decoder.Decode(&payload); err != nil {
		return nil, false
	}
	flattened, ok := decodeJSONValue([]byte(payload))
	if !ok {
		return nil, false
	}
	table, ok := flattened.([]any)
	if !ok || len(table) == 0 || len(table) > 200_000 {
		return nil, false
	}
	document, err := newFlattenedJSONDecoder(table).decodeIndex(0, 0)
	return document, err == nil
}

type flattenedJSONDecoder struct {
	table   []any
	state   []uint8
	decoded []any
}

func newFlattenedJSONDecoder(table []any) *flattenedJSONDecoder {
	return &flattenedJSONDecoder{
		table:   table,
		state:   make([]uint8, len(table)),
		decoded: make([]any, len(table)),
	}
}

func (decoder *flattenedJSONDecoder) decodeIndex(index, depth int) (any, error) {
	if index < 0 {
		// Negative indexes are serializer sentinels such as null/undefined. None
		// can contain conversation messages, so treating them as nil is safe.
		return nil, nil
	}
	if index >= len(decoder.table) || depth > 128 {
		return nil, errors.New("flattened JSON reference is invalid")
	}
	switch decoder.state[index] {
	case 1:
		// Shared/cyclic metadata outside the transcript is irrelevant to the
		// conversation candidate and cannot be represented in the returned JSON
		// tree. Break that reference rather than rejecting the whole snapshot.
		return nil, nil
	case 2:
		return decoder.decoded[index], nil
	}
	decoder.state[index] = 1
	value, err := decoder.decodeValue(decoder.table[index], depth+1)
	if err != nil {
		return nil, err
	}
	decoder.decoded[index] = value
	decoder.state[index] = 2
	return value, nil
}

func (decoder *flattenedJSONDecoder) decodeValue(raw any, depth int) (any, error) {
	switch typed := raw.(type) {
	case map[string]any:
		value := make(map[string]any, len(typed))
		for encodedKey, encodedValue := range typed {
			if !strings.HasPrefix(encodedKey, "_") {
				value[encodedKey] = encodedValue
				continue
			}
			keyIndex, err := strconv.Atoi(strings.TrimPrefix(encodedKey, "_"))
			if err != nil {
				return nil, errors.New("flattened JSON object key reference is invalid")
			}
			decodedKey, err := decoder.decodeIndex(keyIndex, depth+1)
			if err != nil {
				return nil, err
			}
			key, ok := decodedKey.(string)
			if !ok {
				continue
			}
			decodedValue, err := decoder.decodeReference(encodedValue, depth+1)
			if err != nil {
				return nil, err
			}
			value[key] = decodedValue
		}
		return value, nil
	case []any:
		value := make([]any, len(typed))
		for index, item := range typed {
			decoded, err := decoder.decodeReference(item, depth+1)
			if err != nil {
				return nil, err
			}
			value[index] = decoded
		}
		return value, nil
	default:
		return raw, nil
	}
}

func (decoder *flattenedJSONDecoder) decodeReference(raw any, depth int) (any, error) {
	reference, ok := raw.(json.Number)
	if !ok {
		return raw, nil
	}
	index, err := strconv.Atoi(reference.String())
	if err != nil {
		return nil, errors.New("flattened JSON value reference is not an integer")
	}
	return decoder.decodeIndex(index, depth+1)
}

// walkChatGPTJSON searches only for ChatGPT's selected mapping envelope. Share
// pages can nest that envelope under Next.js or React Router loader data, but a
// generic messages array elsewhere in the page is not a conversation signal.
func walkChatGPTJSON(value any, depth int, candidates *[]conversationCandidate) error {
	if depth > 64 || len(*candidates) > maxEmbeddedJSONDocuments {
		return errors.New("embedded JSON nesting or candidate limit exceeded")
	}
	switch typed := value.(type) {
	case map[string]any:
		if candidate, found, err := mappingCandidate(typed); err != nil {
			return err
		} else if found {
			*candidates = append(*candidates, candidate)
		}
		for key, child := range typed {
			if key == "mapping" {
				continue
			}
			if err := walkChatGPTJSON(child, depth+1, candidates); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkChatGPTJSON(child, depth+1, candidates); err != nil {
				return err
			}
		}
	}
	return nil
}

// collectClaudeCandidates accepts only the two public Claude shapes we know:
// a snapshot object with chat_messages, or a top-level conversation envelope
// containing that snapshot. It intentionally does not recursively discover
// chat_messages in unrelated application state.
func collectClaudeCandidates(value any, candidates *[]conversationCandidate) error {
	root, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	container := root
	if conversation, exists := root["conversation"]; exists {
		var valid bool
		container, valid = conversation.(map[string]any)
		if !valid {
			return errors.New("claude conversation envelope is malformed")
		}
	}
	rawMessages, ok := container["chat_messages"].([]any)
	if !ok {
		return nil
	}
	candidate, found, err := claudeMessageCandidate(container, rawMessages)
	if err != nil {
		return err
	}
	if found {
		*candidates = append(*candidates, candidate)
	}
	return nil
}

func mappingCandidate(container map[string]any) (conversationCandidate, bool, error) {
	mapping, ok := container["mapping"].(map[string]any)
	if !ok || len(mapping) == 0 {
		return conversationCandidate{}, false, nil
	}
	selected := firstString(container, "current_node", "currentNode")
	if selected == "" {
		return conversationCandidate{}, false, nil
	}

	var reversed []map[string]any
	seen := make(map[string]bool)
	for selected != "" {
		if seen[selected] || len(seen) > MaxMessages {
			return conversationCandidate{}, false, errors.New("mapping path contains a cycle or exceeds the message limit")
		}
		seen[selected] = true
		node, ok := mapping[selected].(map[string]any)
		if !ok {
			return conversationCandidate{}, false, errors.New("selected mapping path is incomplete")
		}
		reversed = append(reversed, node)
		selected = firstString(node, "parent")
	}

	candidate := conversationCandidate{
		title:          firstString(container, "title", "name", "snapshot_name"),
		sourceRevision: firstString(container, "source_revision", "revision", "uuid"),
		warnings:       make(map[string]int),
		completeness:   CompletenessVisiblePath,
	}
	for index := len(reversed) - 1; index >= 0; index-- {
		rawMessage, exists := reversed[index]["message"]
		if !exists || rawMessage == nil {
			continue
		}
		messageObject, ok := rawMessage.(map[string]any)
		if !ok {
			return conversationCandidate{}, false, errors.New("mapping message is malformed")
		}
		message, supported, explicit, losses, err := messageFromObject(messageObject)
		if err != nil {
			return conversationCandidate{}, false, err
		}
		if !explicit || !supported {
			candidate.warnings["omitted_unsupported_roles"]++
			continue
		}
		if losses > 0 {
			candidate.warnings["omitted_non_text_content"] += losses
		}
		candidate.messages = append(candidate.messages, message)
	}
	if len(candidate.messages) == 0 {
		return conversationCandidate{}, false, errors.New("selected mapping path has no user or assistant text")
	}
	return candidate, true, nil
}

func claudeMessageCandidate(container map[string]any, rawMessages []any) (conversationCandidate, bool, error) {
	candidate := conversationCandidate{
		title:          firstString(container, "title", "name", "snapshot_name"),
		sourceRevision: firstString(container, "source_revision", "revision", "uuid"),
		warnings:       make(map[string]int),
		completeness:   CompletenessVisiblePath,
	}
	recognized := false
	for _, rawMessage := range rawMessages {
		messageObject, ok := rawMessage.(map[string]any)
		if !ok {
			candidate.warnings["omitted_non_text_content"]++
			continue
		}
		message, supported, explicit, losses, err := messageFromObject(messageObject)
		if err != nil {
			return conversationCandidate{}, false, err
		}
		if !explicit {
			candidate.warnings["omitted_non_text_content"]++
			continue
		}
		if !supported {
			candidate.warnings["omitted_unsupported_roles"]++
			continue
		}
		recognized = true
		if losses > 0 {
			candidate.warnings["omitted_non_text_content"] += losses
		}
		candidate.messages = append(candidate.messages, message)
	}
	if !recognized {
		return conversationCandidate{}, false, nil
	}
	return candidate, true, nil
}

func messageFromObject(object map[string]any) (Message, bool, bool, int, error) {
	roleName := firstString(object, "role", "sender")
	if roleName == "" {
		if author, ok := object["author"].(map[string]any); ok {
			roleName = firstString(author, "role")
		}
	}
	if roleName == "" {
		return Message{}, false, false, 0, nil
	}
	role, supported := importedRole(roleName)
	if !supported {
		return Message{}, false, true, 0, nil
	}
	text, losses, err := textFromMessage(object)
	if err != nil {
		return Message{}, false, true, losses, err
	}
	return Message{
		SourceID:        firstString(object, "id", "uuid", "message_id"),
		Role:            role,
		Text:            normalizeLineEndings(text),
		SourceTimestamp: sourceTimestamp(object),
	}, true, true, losses, nil
}

func importedRole(raw string) (Role, bool) {
	switch strings.ToLower(raw) {
	case "user", "human":
		return RoleUser, true
	case "assistant", "chatgpt", "claude":
		return RoleAssistant, true
	default:
		return "", false
	}
}

func textFromMessage(object map[string]any) (string, int, error) {
	if text, ok := object["text"].(string); ok {
		if strings.TrimSpace(text) == "" {
			return "", 0, errors.New("message has empty text")
		}
		return text, 0, nil
	}
	text, losses, ok := textFromContent(object["content"])
	if !ok || strings.TrimSpace(text) == "" {
		return "", losses, errors.New("user or assistant message has no identifiable text")
	}
	return text, losses, nil
}

func textFromContent(content any) (string, int, bool) {
	switch typed := content.(type) {
	case string:
		return typed, 0, true
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text, 0, true
		}
		if parts, ok := typed["parts"].([]any); ok {
			return textFromContent(parts)
		}
	case []any:
		var parts []string
		losses := 0
		for _, rawPart := range typed {
			switch part := rawPart.(type) {
			case string:
				parts = append(parts, part)
			case map[string]any:
				partType := strings.ToLower(firstString(part, "type", "content_type"))
				text, hasText := part["text"].(string)
				if hasText && (partType == "" || partType == "text" || partType == "input_text" || partType == "output_text") {
					parts = append(parts, text)
				} else {
					losses++
				}
			default:
				losses++
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n"), losses, true
		}
		return "", losses, false
	}
	return "", 0, false
}

func sourceTimestamp(object map[string]any) *time.Time {
	for _, key := range []string{"source_timestamp", "created_at", "create_time", "timestamp"} {
		raw, exists := object[key]
		if !exists {
			continue
		}
		switch value := raw.(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				parsed = parsed.UTC()
				return &parsed
			}
		case json.Number:
			if seconds, err := strconv.ParseFloat(string(value), 64); err == nil {
				return unixFloatTime(seconds)
			}
		case float64:
			return unixFloatTime(value)
		}
	}
	return nil
}

func unixFloatTime(value float64) *time.Time {
	seconds := int64(value)
	nanoseconds := int64((value - float64(seconds)) * float64(time.Second))
	parsed := time.Unix(seconds, nanoseconds).UTC()
	return &parsed
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok {
			return value
		}
	}
	return ""
}

func uniqueCandidates(candidates []conversationCandidate) []conversationCandidate {
	seen := make(map[string]int)
	unique := make([]conversationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidateKey(candidate)
		if existing, ok := seen[key]; ok {
			if unique[existing].title == "" {
				unique[existing].title = candidate.title
			}
			continue
		}
		seen[key] = len(unique)
		unique = append(unique, candidate)
	}
	return unique
}

func candidateKey(candidate conversationCandidate) string {
	var key strings.Builder
	for _, message := range candidate.messages {
		key.WriteString(string(message.Role))
		key.WriteByte(0)
		key.WriteString(message.SourceID)
		key.WriteByte(0)
		key.WriteString(message.Text)
		key.WriteByte(0xff)
	}
	return key.String()
}

func warningList(counts map[string]int) []Warning {
	keys := make([]string, 0, len(counts))
	for key, count := range counts {
		if count > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	warnings := make([]Warning, 0, len(keys))
	for _, key := range keys {
		warnings = append(warnings, Warning{Code: key, Count: counts[key]})
	}
	return warnings
}
