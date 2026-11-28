package session

import (
	"bytes"
	"encoding/json"
	"errors"
)

func trimPi(row Row, source []byte, options HandoffOptions) (trimResult, error) {
	var turns recordedTrimTurns
	var headerSeen bool
	var offset int
	var previousID string
	seen := make(map[string]bool)
	for native := range bytes.SplitAfterSeq(source, []byte("\n")) {
		if len(native) > scanBufferMax {
			return trimResult{}, errors.New("the Pi session contains a record too large to review")
		}
		start := offset
		offset += len(native)
		line := bytes.TrimSpace(native)
		if len(line) == 0 {
			continue
		}
		if !headerSeen {
			var header piSessionHeader
			if err := json.Unmarshal(line, &header); err != nil || header.Type != "session" ||
				header.ID != row.ID || header.Version != 3 {
				return trimResult{}, errors.New("trimming requires a matching Pi version 3 session header")
			}
			headerSeen = true
			continue
		}

		var entry piSessionEntry
		if err := json.Unmarshal(line, &entry); err != nil || entry.Type == "" ||
			entry.Type == "session" || entry.ID == "" || seen[entry.ID] {
			return trimResult{}, errors.New("the Pi session contains an invalid entry or repeated ID")
		}
		// Keeping a byte prefix is safe only when each entry belongs to one
		// uninterrupted lineage. Refuse before touching alternate branches.
		if entry.ParentID != previousID {
			return trimResult{}, errors.New("the Pi session contains branches or a broken parent chain")
		}
		seen[entry.ID] = true
		previousID = entry.ID
		if entry.Type == "message" {
			text := cleanTranscriptText(textFromContent(entry.Message.Content))
			turns.add(entry.Message.Role, text, start, offset)
		}
	}
	if !headerSeen {
		return trimResult{}, errors.New("the Pi session is missing its header")
	}
	cut, result, err := turns.cut(options)
	if err != nil {
		return trimResult{}, err
	}
	result.replacement = source[:cut]
	return result, nil
}
