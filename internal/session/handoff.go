package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

func HandoffPrompt(row Row, target Provider, options HandoffOptions) (string, error) {
	turns, err := Transcript(row)
	if err != nil {
		return "", err
	}
	turns = options.apply(turns)

	var builder strings.Builder
	fmt.Fprintf(&builder, "You are taking over an existing local AI coding session from %s.\n", row.Provider)
	fmt.Fprintf(&builder, "Continue the work in the same workspace using the context below.\n\n")
	fmt.Fprintf(&builder, "Target agent: %s\n", target)
	fmt.Fprintf(&builder, "Source session: %s\n", row.ID)
	fmt.Fprintf(&builder, "Workspace: %s\n", row.CWD)
	fmt.Fprintf(&builder, "Transfer scope: %s\n", options.Label())
	fmt.Fprintf(&builder, "Source transcript file: %s\n\n", row.File)
	builder.WriteString("Important:\n")
	builder.WriteString("- This is a transferred context, not a native resume of the old provider's state.\n")
	builder.WriteString("- Preserve the user's intent and continue from the latest useful user request.\n")
	builder.WriteString("- Do not repeat the entire transcript back to the user.\n\n")
	if options.MaxTurns <= 0 {
		builder.WriteString("Transcript, oldest to newest:\n")
	} else {
		builder.WriteString("Transcript excerpt, oldest to newest:\n")
	}
	for _, turn := range turns {
		fmt.Fprintf(&builder, "\n[%s]\n%s\n", strings.ToUpper(turn.Role), turn.Text)
	}
	builder.WriteString("\nContinue from this context now.\n")
	return builder.String(), nil
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
