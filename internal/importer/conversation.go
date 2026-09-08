// Package importer parses user-supplied conversation transcripts and supported
// public share pages. It deliberately has no dependency on native session stores.
package importer

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion = 1
	MaxTextBytes  = 10 * 1024 * 1024
	MaxHTMLBytes  = 20 * 1024 * 1024
	MaxMessages   = 10_000
)

var (
	ErrNoConversation             = errors.New("no conversation text found")
	ErrInvalidUTF8                = errors.New("conversation is not valid UTF-8")
	ErrLimitExceeded              = errors.New("import limit exceeded")
	ErrAmbiguousRoles             = errors.New("some text has no speaker")
	ErrInvalidJSON                = errors.New("invalid conversation JSON")
	ErrUnsupportedSchema          = errors.New("unsupported conversation schema")
	ErrUnsupportedURL             = errors.New("unsupported conversation share URL")
	ErrUnsafeAddress              = errors.New("share URL resolved to a non-public address")
	ErrFetch                      = errors.New("share page fetch failed")
	ErrHTTPStatus                 = errors.New("share page returned an unexpected HTTP status")
	ErrUnexpectedContent          = errors.New("share page returned unsupported content")
	ErrRedirectLimit              = errors.New("share page redirect limit exceeded")
	ErrConversationUnidentifiable = errors.New("the page opened, but its conversation could not be identified")
)

type SourceKind string

const (
	SourceChatGPTShare   SourceKind = "chatgpt_share"
	SourceClaudeShare    SourceKind = "claude_share"
	SourcePastedText     SourceKind = "pasted_text"
	SourceTranscriptFile SourceKind = "transcript_file"
)

type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleUnassigned Role = "unassigned"
)

type Completeness string

const (
	CompletenessUnknown     Completeness = "unknown"
	CompletenessVisiblePath Completeness = "visible_path"
)

type Message struct {
	SourceID        string     `json:"source_id,omitempty"`
	Role            Role       `json:"role"`
	Text            string     `json:"text"`
	SourceTimestamp *time.Time `json:"source_timestamp,omitempty"`
}

type Warning struct {
	Code  string `json:"code"`
	Count int    `json:"count,omitempty"`
}

type Conversation struct {
	SchemaVersion  int          `json:"schema_version"`
	SourceKind     SourceKind   `json:"source_kind"`
	AcquiredAt     time.Time    `json:"acquired_at"`
	Title          string       `json:"title,omitempty"`
	SourceRevision string       `json:"source_revision,omitempty"`
	Messages       []Message    `json:"messages"`
	Warnings       []Warning    `json:"warnings,omitempty"`
	Completeness   Completeness `json:"completeness"`
}

// AmbiguityError preserves the parsed blocks for an interactive role review.
// Callers may instead rerun ParseText with AsOneContextNote.
type AmbiguityError struct {
	UnassignedBlocks int
	Conversation     Conversation
}

func (e *AmbiguityError) Error() string {
	return fmt.Sprintf("%v (%d unassigned block(s)); assign roles or import as one context note", ErrAmbiguousRoles, e.UnassignedBlocks)
}

func (e *AmbiguityError) Unwrap() error { return ErrAmbiguousRoles }

func normalizeLineEndings(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func validSourceKind(kind SourceKind) bool {
	switch kind {
	case SourceChatGPTShare, SourceClaudeShare, SourcePastedText, SourceTranscriptFile:
		return true
	default:
		return false
	}
}

func validRole(role Role) bool {
	switch role {
	case RoleUser, RoleAssistant, RoleUnassigned:
		return true
	default:
		return false
	}
}

func validateMessages(messages []Message) error {
	if len(messages) == 0 {
		return ErrNoConversation
	}
	if len(messages) > MaxMessages {
		return fmt.Errorf("%w: messages exceed %d", ErrLimitExceeded, MaxMessages)
	}

	totalBytes := 0
	for index := range messages {
		message := &messages[index]
		if !validRole(message.Role) {
			return fmt.Errorf("message %d has unsupported role %q", index, message.Role)
		}
		if !utf8.ValidString(message.Text) {
			return fmt.Errorf("message %d: %w", index, ErrInvalidUTF8)
		}
		if strings.TrimSpace(message.Text) == "" {
			return fmt.Errorf("message %d: %w", index, ErrNoConversation)
		}
		totalBytes += len(message.Text)
		if totalBytes > MaxTextBytes {
			return fmt.Errorf("%w: message text exceeds %d bytes", ErrLimitExceeded, MaxTextBytes)
		}
	}
	return nil
}

func countUnassigned(messages []Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == RoleUnassigned {
			count++
		}
	}
	return count
}

func validateConversation(conversation Conversation) error {
	if conversation.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrUnsupportedSchema, conversation.SchemaVersion, SchemaVersion)
	}
	if !validSourceKind(conversation.SourceKind) {
		return fmt.Errorf("unsupported source kind %q", conversation.SourceKind)
	}
	if conversation.AcquiredAt.IsZero() {
		return errors.New("acquired_at is required")
	}
	if conversation.Completeness != CompletenessUnknown && conversation.Completeness != CompletenessVisiblePath {
		return fmt.Errorf("unsupported completeness %q", conversation.Completeness)
	}
	return validateMessages(conversation.Messages)
}

// Validate checks the versioned model without modifying message text.
func (conversation Conversation) Validate() error {
	return validateConversation(conversation)
}
