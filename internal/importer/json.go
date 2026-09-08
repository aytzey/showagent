package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func ParseJSON(data []byte) (Conversation, error) {
	if len(data) > MaxHTMLBytes {
		return Conversation{}, fmt.Errorf("%w: JSON exceeds %d bytes", ErrLimitExceeded, MaxHTMLBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var conversation Conversation
	if err := decoder.Decode(&conversation); err != nil {
		return Conversation{}, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Conversation{}, err
	}
	for index := range conversation.Messages {
		conversation.Messages[index].Text = normalizeLineEndings(conversation.Messages[index].Text)
	}
	if err := validateConversation(conversation); err != nil {
		if errors.Is(err, ErrUnsupportedSchema) || errors.Is(err, ErrLimitExceeded) || errors.Is(err, ErrInvalidUTF8) || errors.Is(err, ErrNoConversation) {
			return conversation, err
		}
		return conversation, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if unassigned := countUnassigned(conversation.Messages); unassigned > 0 {
		return conversation, &AmbiguityError{UnassignedBlocks: unassigned, Conversation: conversation}
	}
	return conversation, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return fmt.Errorf("%w: multiple JSON values", ErrInvalidJSON)
}
