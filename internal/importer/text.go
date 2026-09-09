package importer

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type TextOptions struct {
	AsOneContextNote bool
	SourceKind       SourceKind
	AcquiredAt       time.Time
	Title            string
}

type textBlock struct {
	role Role
	text string
}

func ParseText(input string, options TextOptions) (Conversation, error) {
	if !utf8.ValidString(input) {
		return Conversation{}, ErrInvalidUTF8
	}
	input = normalizeLineEndings(input)
	if len(input) > MaxTextBytes {
		return Conversation{}, fmt.Errorf("%w: text exceeds %d bytes", ErrLimitExceeded, MaxTextBytes)
	}
	if strings.TrimSpace(input) == "" {
		return Conversation{}, ErrNoConversation
	}

	conversation := newTextConversation(options)
	if !validSourceKind(conversation.SourceKind) {
		return Conversation{}, fmt.Errorf("unsupported source kind %q", conversation.SourceKind)
	}
	if options.AsOneContextNote {
		conversation.Messages = []Message{{Role: RoleUser, Text: contextNoteText(conversation.SourceKind, input)}}
		if err := validateConversation(conversation); err != nil {
			return conversation, err
		}
		return conversation, nil
	}

	blocks := splitExplicitRoleBlocks(input)
	conversation.Messages = make([]Message, 0, len(blocks))
	for _, block := range blocks {
		conversation.Messages = append(conversation.Messages, Message{Role: block.role, Text: block.text})
	}
	if err := validateConversation(conversation); err != nil {
		return conversation, err
	}
	if unassigned := countUnassigned(conversation.Messages); unassigned > 0 {
		return conversation, &AmbiguityError{UnassignedBlocks: unassigned, Conversation: conversation}
	}
	return conversation, nil
}

func contextNoteText(kind SourceKind, input string) string {
	label := strings.ReplaceAll(string(kind), "_", " ")
	return "[Imported conversation context from " + label + "]\n\n" + input
}

func newTextConversation(options TextOptions) Conversation {
	kind := options.SourceKind
	if kind == "" {
		kind = SourcePastedText
	}
	acquiredAt := options.AcquiredAt
	if acquiredAt.IsZero() {
		acquiredAt = time.Now()
	}
	return Conversation{
		SchemaVersion: SchemaVersion,
		SourceKind:    kind,
		AcquiredAt:    acquiredAt.UTC(),
		Title:         options.Title,
		Completeness:  CompletenessUnknown,
	}
}

func splitExplicitRoleBlocks(input string) []textBlock {
	var (
		blocks      []textBlock
		body        strings.Builder
		currentRole = RoleUnassigned
		fence       rune
		fenceLength int
	)

	flush := func(beforeRoleHeader bool) {
		text := body.String()
		body.Reset()
		if beforeRoleHeader && strings.HasSuffix(text, "\n") {
			text = strings.TrimSuffix(text, "\n")
		}
		if strings.TrimSpace(text) != "" {
			blocks = append(blocks, textBlock{role: currentRole, text: text})
		}
	}

	for offset := 0; offset < len(input); {
		end := strings.IndexByte(input[offset:], '\n')
		hasNewline := end >= 0
		if hasNewline {
			end += offset
		} else {
			end = len(input)
		}
		line := input[offset:end]
		next := end
		if hasNewline {
			next++
		}

		if marker, length, closing := parseFenceLine(line, fence, fenceLength); marker != 0 {
			body.WriteString(input[offset:next])
			if closing {
				fence, fenceLength = 0, 0
			} else if fence == 0 {
				fence, fenceLength = marker, length
			}
			offset = next
			continue
		}

		if fence == 0 && !isQuotedLine(line) {
			if role, inline, ok := explicitRoleHeader(line); ok {
				flush(true)
				currentRole = role
				if inline != "" {
					body.WriteString(inline)
					if hasNewline {
						body.WriteByte('\n')
					}
				}
				offset = next
				continue
			}
		}

		body.WriteString(input[offset:next])
		offset = next
	}
	flush(false)
	return blocks
}

func explicitRoleHeader(line string) (Role, string, bool) {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return "", "", false
	}
	label := line[:colon]
	var role Role
	switch {
	case strings.EqualFold(label, "user"), strings.EqualFold(label, "human"):
		role = RoleUser
	case strings.EqualFold(label, "assistant"), strings.EqualFold(label, "chatgpt"), strings.EqualFold(label, "claude"):
		role = RoleAssistant
	default:
		return "", "", false
	}
	inline := strings.TrimPrefix(line[colon+1:], " ")
	return role, inline, true
}

func isQuotedLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, ">")
}

func parseFenceLine(line string, open rune, openLength int) (rune, int, bool) {
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	if spaces > 3 || spaces == len(line) {
		return 0, 0, false
	}
	marker := rune(line[spaces])
	if marker != '`' && marker != '~' {
		return 0, 0, false
	}
	length := 0
	for spaces+length < len(line) && rune(line[spaces+length]) == marker {
		length++
	}
	if length < 3 {
		return 0, 0, false
	}
	if open == 0 {
		return marker, length, false
	}
	if marker != open || length < openLength || strings.TrimSpace(line[spaces+length:]) != "" {
		return 0, 0, false
	}
	return marker, length, true
}
