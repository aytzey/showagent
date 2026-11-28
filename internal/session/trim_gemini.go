package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
)

type trimPoint struct {
	before int
	after  int
	turn   int
	text   string
}

// recordedTrimTurns maps the message chooser's numbers back to native
// record positions. Positions may be array indices or byte offsets.
type recordedTrimTurns struct {
	users []trimPoint
	turns int
}

func (t *recordedTrimTurns) add(role, text string, before, after int) {
	if !keepTranscriptTurn(role, text) {
		return
	}
	if role == "user" {
		t.users = append(t.users, trimPoint{before: before, after: after, turn: t.turns, text: text})
	}
	t.turns++
}

func (t recordedTrimTurns) cut(options HandoffOptions) (int, trimResult, error) {
	number := options.ThroughUser
	if options.BeforeUser > 0 {
		number = options.BeforeUser
	}
	if number <= 0 || number > len(t.users) {
		return 0, trimResult{}, fmt.Errorf("user message #%d is unavailable", number)
	}
	selected := t.users[number-1]
	cut, users, turns := selected.after, number, selected.turn+1
	if options.BeforeUser > 0 {
		cut, users, turns = selected.before, number-1, selected.turn
	}
	if users == 0 {
		return 0, trimResult{}, errors.New("keep at least one user message in the session")
	}
	if turns == t.turns {
		return 0, trimResult{}, errors.New("there are no conversation messages after the selected cutoff")
	}
	return cut, trimResult{
		keptUsers:    users,
		removedUsers: len(t.users) - users,
		keptTurns:    turns,
		removedTurns: t.turns - turns,
		boundaryText: selected.text,
	}, nil
}

type geminiTrimMessage struct {
	native json.RawMessage
	text   geminiMessage
}

type geminiTrimLog struct {
	metadata map[string]json.RawMessage
	messages []geminiTrimMessage
	byID     map[string]int
}

func trimGemini(row Row, source []byte, options HandoffOptions) (trimResult, error) {
	legacy := filepath.Ext(row.File) == ".json"
	log, err := readGeminiTrimLog(source, legacy)
	if err != nil {
		return trimResult{}, err
	}
	if geminiTrimString(log.metadata["sessionId"]) != row.ID || geminiTrimString(log.metadata["kind"]) == "subagent" {
		return trimResult{}, errors.New("the Gemini CLI session header does not match the selected conversation")
	}

	var turns recordedTrimTurns
	for index, stored := range log.messages {
		switch message := stored.text; message.Type {
		case "user":
			if !geminiIgnoredUserText(message.Text) {
				turns.add("user", cleanTranscriptText(message.Text), index, index+1)
			}
		case "gemini":
			turns.add("assistant", cleanTranscriptText(message.Text), index, index+1)
		}
	}
	cut, result, err := turns.cut(options)
	if err != nil {
		return trimResult{}, err
	}

	// These generated fields can summarize text past the cutoff.
	delete(log.metadata, "summary")
	delete(log.metadata, "memoryScratchpad")
	retained := make([]json.RawMessage, cut)
	for i, message := range log.messages[:cut] {
		retained[i] = message.native
	}
	var replacement bytes.Buffer
	encoder := json.NewEncoder(&replacement)
	if legacy {
		encoder.SetIndent("", "  ")
		log.metadata["messages"], err = json.Marshal(retained)
		if err != nil {
			return trimResult{}, err
		}
	}
	if err := encoder.Encode(log.metadata); err != nil {
		return trimResult{}, err
	}
	if !legacy {
		for _, message := range retained {
			if err := encoder.Encode(message); err != nil {
				return trimResult{}, err
			}
		}
	}
	result.replacement = replacement.Bytes()
	return result, nil
}

func readGeminiTrimLog(source []byte, legacy bool) (geminiTrimLog, error) {
	log := geminiTrimLog{
		metadata: make(map[string]json.RawMessage),
		byID:     make(map[string]int),
	}
	if legacy {
		if err := json.Unmarshal(source, &log.metadata); err != nil || !validGeminiTrimHeader(log.metadata) {
			return geminiTrimLog{}, errors.New("unrecognized Gemini CLI JSON session format")
		}
		if err := log.replaceMessages(log.metadata["messages"]); err != nil {
			return geminiTrimLog{}, err
		}
		return log, nil
	}

	headerSeen := false
	for native := range bytes.SplitAfterSeq(source, []byte("\n")) {
		if len(native) > scanBufferMax {
			return geminiTrimLog{}, errors.New("the Gemini CLI session contains a record too large to review")
		}
		line := bytes.TrimSpace(native)
		if len(line) == 0 {
			continue
		}
		var record map[string]json.RawMessage
		if err := json.Unmarshal(line, &record); err != nil || record == nil {
			return geminiTrimLog{}, errors.New("the Gemini CLI session contains a malformed JSONL record")
		}
		if !headerSeen {
			if !validGeminiTrimHeader(record) {
				return geminiTrimLog{}, errors.New("unrecognized Gemini CLI JSONL session header")
			}
			headerSeen = true
			log.metadata = record
			if snapshot := record["messages"]; snapshot != nil {
				if err := log.replaceMessages(snapshot); err != nil {
					return geminiTrimLog{}, err
				}
				delete(log.metadata, "messages")
			}
			continue
		}
		if err := log.replayRecord(line, record); err != nil {
			return geminiTrimLog{}, err
		}
	}
	if !headerSeen {
		return geminiTrimLog{}, errors.New("the Gemini CLI session is missing its JSONL header")
	}
	return log, nil
}

func validGeminiTrimHeader(record map[string]json.RawMessage) bool {
	return geminiTrimString(record["sessionId"]) != "" && geminiTrimString(record["projectHash"]) != ""
}

func geminiTrimString(raw json.RawMessage) string {
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

func readGeminiTrimMessage(raw json.RawMessage) (geminiTrimMessage, error) {
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return geminiTrimMessage{}, errors.New("the Gemini CLI session contains a malformed message")
	}
	message := geminiMessageFromRecord(record)
	if message.ID == "" || message.Type == "" {
		return geminiTrimMessage{}, errors.New("the Gemini CLI session contains a message without an ID or type")
	}
	return geminiTrimMessage{native: raw, text: message}, nil
}

func (log *geminiTrimLog) replaceMessages(snapshot json.RawMessage) error {
	var messages []json.RawMessage
	if err := json.Unmarshal(snapshot, &messages); err != nil || messages == nil {
		return errors.New("the Gemini CLI session has an invalid message snapshot")
	}
	log.messages = log.messages[:0]
	clear(log.byID)
	for _, native := range messages {
		message, err := readGeminiTrimMessage(native)
		if err != nil {
			return err
		}
		if _, duplicate := log.byID[message.text.ID]; duplicate {
			return errors.New("the Gemini CLI session snapshot contains duplicate message IDs")
		}
		log.byID[message.text.ID] = len(log.messages)
		log.messages = append(log.messages, message)
	}
	return nil
}

func (log *geminiTrimLog) replayRecord(line []byte, record map[string]json.RawMessage) error {
	switch {
	case record["$patch"] != nil:
		return errors.New("the Gemini CLI sessions with message patches cannot be trimmed")
	case record["$rewindTo"] != nil:
		index, ok := log.byID[geminiTrimString(record["$rewindTo"])]
		if !ok {
			return errors.New("the Gemini CLI session contains an unknown rewind target")
		}
		for _, removed := range log.messages[index:] {
			delete(log.byID, removed.text.ID)
		}
		log.messages = log.messages[:index]
	case geminiTrimString(record["id"]) != "":
		message, err := readGeminiTrimMessage(line)
		if err != nil {
			return err
		}
		if index, exists := log.byID[message.text.ID]; exists {
			log.messages[index] = message
		} else {
			log.byID[message.text.ID] = len(log.messages)
			log.messages = append(log.messages, message)
		}
	case record["$set"] != nil:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(record["$set"], &fields); err != nil || fields == nil {
			return errors.New("the Gemini CLI session contains an invalid metadata update")
		}
		if snapshot, present := fields["messages"]; present {
			if err := log.replaceMessages(snapshot); err != nil {
				return err
			}
			delete(fields, "messages")
		}
		for key, value := range fields {
			if (key == "sessionId" || key == "projectHash") && geminiTrimString(log.metadata[key]) != geminiTrimString(value) {
				return errors.New("the Gemini CLI session contains a changed session or project ID")
			}
			log.metadata[key] = value
		}
	default:
		return errors.New("the Gemini CLI session contains an unrecognized JSONL record")
	}
	return nil
}
