package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Google Antigravity support.
//
// Antigravity keeps agent conversations in two surfaces under ~/.gemini:
//
//	antigravity-cli/   the `agy` terminal agent; resumable with
//	                   `agy --conversation <id>` from the workspace
//	antigravity-ide/   the desktop IDE (current releases)
//	antigravity/       the desktop IDE (releases before the -ide rename)
//
// Each conversation's authoritative step log is private to Antigravity: a
// per-conversation SQLite database of protobuf steps (CLI) or an encrypted
// protobuf file (IDE). showagent reads neither. Every surface also writes a
// plain JSONL display mirror of the conversation:
//
//	brain/<id>/.system_generated/logs/transcript_full.jsonl
//	brain/<id>/.system_generated/logs/transcript.jsonl   (truncated copy)
//
// whose records carry step_index, source, type, status, created_at, and the
// step's display text in content. USER_INPUT and PLANNER_RESPONSE records are
// the conversation; everything else (tool steps, CONVERSATION_HISTORY,
// checkpoints) is runtime state that a handoff drops.
//
// The transcript does not record a workspace. agy keeps it in its
// conversation summary index, antigravity-cli/jetbox_summaries_proto.pb: an
// unencrypted exa.jetski_cortex_pb.CascadeTrajectorySummaries message whose
// summaries map holds each conversation's CortexWorkspaceMetadata (field 9,
// workspace_folder_absolute_uri). showagent decodes just those fields; when
// the index is missing it falls back to cache/last_conversations.json
// (workspace -> latest conversation) and to history.jsonl lines carrying a
// conversationId. agy's SQLite summary store is never opened. Verified
// against agy 1.4.0 on 2026-10-16.
//
// IDE conversations are listed as hand-off sources only, because agy cannot
// reopen them by id. Their workspace is known only when agy has cached a
// summary of them (cache/conversation_metadata.json).
//
// Antigravity is a hand-off source only; see errAntigravityTarget.
//
// ANTIGRAVITY_HOME relocates the directory holding all three surfaces
// (default ~/.gemini), which keeps tests hermetic.
const ProviderAntigravity Provider = "antigravity"

const (
	antigravityCLIDir       = "antigravity-cli"
	antigravityIDEDir       = "antigravity-ide"
	antigravityLegacyIDEDir = "antigravity"
)

// antigravityInjectedBlock matches the start of a metadata block Antigravity
// appends after the user's request, such as <ADDITIONAL_METADATA>.
var antigravityInjectedBlock = regexp.MustCompile(`^<[A-Z][A-Z0-9_]*>`)

// antigravityProvider adapts the Antigravity CLI and IDE stores to the
// provider registry.
type antigravityProvider struct{}

func (antigravityProvider) Name() Provider      { return ProviderAntigravity }
func (antigravityProvider) DisplayName() string { return "Antigravity" }
func (antigravityProvider) CommandName() string { return "agy" }
func (antigravityProvider) Home() string        { return defaultAntigravityHome() }

func (p antigravityProvider) ScanTargets() []ScanTarget {
	home := p.Home()
	targets := []ScanTarget{{
		Provider: ProviderAntigravity,
		Path:     filepath.Join(home, antigravityCLIDir, "brain"),
		EnvVar:   "ANTIGRAVITY_HOME",
	}}
	// IDE stores are listed only when present, so a CLI-only machine is not
	// told about directories it never had.
	for _, dir := range []string{antigravityIDEDir, antigravityLegacyIDEDir} {
		path := filepath.Join(home, dir, "brain")
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			targets = append(targets, ScanTarget{Provider: ProviderAntigravity, Path: path, EnvVar: "ANTIGRAVITY_HOME"})
		}
	}
	return targets
}

func (p antigravityProvider) Discover() []Row {
	return discoverAntigravity(p.Home())
}

func (antigravityProvider) ResumeArgs(row Row, options ResumeOptions) []string {
	command := []string{"agy"}
	if options.Dangerous {
		command = append(command, "--dangerously-skip-permissions")
	}
	return append(command, "--conversation", row.ID)
}

func (p antigravityProvider) CompoundArgs(row Row, options ResumeOptions, prompt string) []string {
	// --prompt-interactive submits the prompt and keeps the session open.
	return append(p.ResumeArgs(row, options), "--prompt-interactive", prompt)
}

func (antigravityProvider) Delete(row Row) error {
	return deleteAntigravity(row)
}

func (antigravityProvider) Transcript(row Row) ([]Turn, error) {
	return antigravityTranscript(row.File)
}

func (antigravityProvider) WriteConverted(source Row, turns []Turn) (Row, error) {
	return writeAntigravityConverted(source, turns)
}

// defaultAntigravityHome is the directory holding Antigravity's surfaces.
func defaultAntigravityHome() string {
	if value := os.Getenv("ANTIGRAVITY_HOME"); value != "" {
		return expandHome(value)
	}
	return filepath.Join(homeDir(), ".gemini")
}

// antigravityRecord is one line of a transcript display mirror.
type antigravityRecord struct {
	StepIndex int    `json:"step_index"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content"`
}

// antigravityTurnRole maps a transcript record to a conversation role, or ""
// for runtime steps a handoff drops.
func antigravityTurnRole(record antigravityRecord) string {
	switch record.Type {
	case "USER_INPUT":
		return "user"
	case "PLANNER_RESPONSE":
		return "assistant"
	default:
		return ""
	}
}

// antigravityUserText returns the prompt the user typed, without the
// <USER_REQUEST> wrapper or the metadata blocks Antigravity injects after it.
// The wrapper ends at the last closing tag followed only by injected blocks,
// so a prompt that itself quotes </USER_REQUEST> keeps all of its text.
func antigravityUserText(content string) string {
	const open, closing = "<USER_REQUEST>", "</USER_REQUEST>"
	body, ok := strings.CutPrefix(strings.TrimLeft(content, " \t\r\n"), open)
	if !ok {
		return content
	}
	end := -1
	for offset := 0; ; {
		index := strings.Index(body[offset:], closing)
		if index < 0 {
			break
		}
		index += offset
		rest := strings.TrimLeft(body[index+len(closing):], " \t\r\n")
		if rest == "" || antigravityInjectedBlock.MatchString(rest) {
			end = index
		}
		offset = index + len(closing)
	}
	if end < 0 {
		return content
	}
	text := strings.TrimPrefix(body[:end], "\r\n")
	text = strings.TrimPrefix(text, "\n")
	text = strings.TrimSuffix(text, "\n")
	return strings.TrimSuffix(text, "\r")
}

func discoverAntigravity(home string) []Row {
	workspaces := antigravityWorkspaces(filepath.Join(home, antigravityCLIDir))

	seen := map[string]bool{}
	var rows []Row
	// The CLI surface comes first: when one conversation id appears in more
	// than one surface, the resumable copy wins.
	for _, surface := range []string{antigravityCLIDir, antigravityIDEDir, antigravityLegacyIDEDir} {
		root := filepath.Join(home, surface)
		entries, err := os.ReadDir(filepath.Join(root, "brain"))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || seen[entry.Name()] {
				continue
			}
			row, ok := parseAntigravity(filepath.Join(root, "brain", entry.Name()), entry.Name())
			if !ok {
				continue
			}
			row.CWD = workspaces[row.ID]
			// Without its step database agy cannot reopen the conversation,
			// but its transcript is still a valid hand-off source.
			row.HandoffOnly = surface != antigravityCLIDir ||
				!fileExists(filepath.Join(root, "conversations", row.ID+".db"))
			if row.CWD == "" {
				row.CWD = "(unknown cwd)"
			}
			row.LaunchCWD = row.CWD
			seen[row.ID] = true
			rows = append(rows, row)
		}
	}
	return rows
}

// antigravityPreview is what discovery keeps from one streaming pass over a
// transcript; tool output and other runtime records are never retained.
type antigravityPreview struct {
	firstUser string
	lastUser  string
	lastAt    time.Time
	hasTurns  bool
}

func previewAntigravity(path string) (antigravityPreview, error) {
	var preview antigravityPreview
	err := scanAntigravityRecords(path, func(record antigravityRecord) {
		if at, ok := parseTimestamp(record.CreatedAt); ok && at.After(preview.lastAt) {
			preview.lastAt = at
		}
		switch antigravityTurnRole(record) {
		case "user":
			text := cleanPreviewText(antigravityUserText(record.Content))
			if !usefulUserText(text) {
				return
			}
			preview.hasTurns = true
			if preview.firstUser == "" {
				preview.firstUser = text
			}
			preview.lastUser = text
		case "assistant":
			if strings.TrimSpace(record.Content) != "" {
				preview.hasTurns = true
			}
		}
	})
	return preview, err
}

// parseAntigravity reads a conversation's brain directory. The complete
// transcript wins when it has conversation records; the truncated copy is
// the fallback. Each file is streamed once.
func parseAntigravity(brainDir string, id string) (Row, bool) {
	logs := filepath.Join(brainDir, ".system_generated", "logs")
	for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
		path := filepath.Join(logs, name)
		preview, err := previewAntigravity(path)
		if err != nil || !preview.hasTurns {
			continue
		}
		lastAt := preview.lastAt
		if lastAt.IsZero() {
			at, ok := fallbackMTime(path)
			if !ok {
				return Row{}, false
			}
			lastAt = at
		}
		return Row{
			Provider:  ProviderAntigravity,
			ID:        id,
			LastAt:    lastAt,
			File:      path,
			FirstUser: preview.firstUser,
			LastUser:  preview.lastUser,
		}, true
	}
	return Row{}, false
}

// scanAntigravityRecords decodes a transcript one line at a time; malformed
// lines are skipped like every other provider's readers do.
func scanAntigravityRecords(path string, visit func(antigravityRecord)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	return scanLines(file, func(line []byte) {
		var record antigravityRecord
		if json.Unmarshal(line, &record) == nil {
			visit(record)
		}
	})
}

func antigravityTranscript(path string) ([]Turn, error) {
	if path == "" {
		return nil, errors.New("antigravity transcript file is unknown")
	}
	var turns []Turn
	err := scanAntigravityRecords(path, func(record antigravityRecord) {
		role := antigravityTurnRole(record)
		if role == "" {
			return
		}
		text := record.Content
		if role == "user" {
			text = antigravityUserText(text)
		}
		text = cleanTranscriptText(text)
		if keepTranscriptTurn(role, text) {
			turns = append(turns, Turn{Role: role, Text: text})
		}
	})
	if err != nil {
		return nil, err
	}
	return turns, nil
}

// antigravityWorkspaces maps conversation ids to their workspace directory,
// preferring agy's summary index over its weaker fallbacks.
func antigravityWorkspaces(cliHome string) map[string]string {
	workspaces := map[string]string{}
	// Weakest first, so stronger sources overwrite.
	for id, workspace := range antigravityHistoryWorkspaces(filepath.Join(cliHome, "history.jsonl")) {
		workspaces[id] = workspace
	}
	// agy caches summaries of conversations it found in other Antigravity
	// surfaces, which is the only readable workspace record IDE ones have.
	if content, err := os.ReadFile(filepath.Join(cliHome, "cache", "conversation_metadata.json")); err == nil {
		var metadata struct {
			Conversations map[string]struct {
				Summary struct {
					WorkspaceURIs []string `json:"WorkspaceURIs"`
				} `json:"summary"`
			} `json:"conversations"`
		}
		if json.Unmarshal(content, &metadata) == nil {
			for id, conversation := range metadata.Conversations {
				for _, uri := range conversation.Summary.WorkspaceURIs {
					if path := fileURIPath(uri); path != "" {
						workspaces[id] = path
						break
					}
				}
			}
		}
	}
	if content, err := os.ReadFile(filepath.Join(cliHome, "cache", "last_conversations.json")); err == nil {
		var latest map[string]string
		if json.Unmarshal(content, &latest) == nil {
			for workspace, id := range latest {
				if id != "" && filepath.IsAbs(workspace) {
					workspaces[id] = workspace
				}
			}
		}
	}
	if content, err := os.ReadFile(filepath.Join(cliHome, "jetbox_summaries_proto.pb")); err == nil {
		for id, workspace := range antigravitySummaryWorkspaces(content) {
			workspaces[id] = workspace
		}
	}
	return workspaces
}

// antigravityHistoryWorkspaces reads the workspace of history lines that name
// their conversation (agy only records the id on some prompt kinds).
func antigravityHistoryWorkspaces(historyPath string) map[string]string {
	file, err := os.Open(historyPath)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	workspaces := map[string]string{}
	_ = scanLines(file, func(line []byte) {
		var entry struct {
			ConversationID string `json:"conversationId"`
			Workspace      string `json:"workspace"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.ConversationID == "" || entry.Workspace == "" {
			return
		}
		workspaces[entry.ConversationID] = entry.Workspace
	})
	return workspaces
}

// antigravitySummaryWorkspaces decodes the summaries map of a
// CascadeTrajectorySummaries message: entry key (1) and value (2), whose
// workspaces (9) carry workspace_folder_absolute_uri (1). Anything that does
// not decode is skipped rather than guessed at.
func antigravitySummaryWorkspaces(message []byte) map[string]string {
	workspaces := map[string]string{}
	for _, entry := range protoBytesFields(message, 1) {
		keys := protoBytesFields(entry, 1)
		values := protoBytesFields(entry, 2)
		if len(keys) != 1 || len(values) != 1 {
			continue
		}
		for _, metadata := range protoBytesFields(values[0], 9) {
			uris := protoBytesFields(metadata, 1)
			if len(uris) == 0 {
				continue
			}
			if path := fileURIPath(string(uris[0])); path != "" {
				workspaces[string(keys[0])] = path
				break
			}
		}
	}
	return workspaces
}

// protoBytesFields returns the length-delimited values of field number in a
// protobuf message, or nil when the message is malformed.
func protoBytesFields(message []byte, number uint64) [][]byte {
	var values [][]byte
	for len(message) > 0 {
		tag, n := protoVarint(message)
		if n == 0 {
			return nil
		}
		message = message[n:]
		switch tag & 7 {
		case 0:
			if _, n = protoVarint(message); n == 0 {
				return nil
			}
			message = message[n:]
		case 1:
			if len(message) < 8 {
				return nil
			}
			message = message[8:]
		case 2:
			length, n := protoVarint(message)
			if n == 0 || length > uint64(len(message)-n) {
				return nil
			}
			value := message[n : n+int(length)]
			message = message[n+int(length):]
			if tag>>3 == number {
				values = append(values, value)
			}
		case 5:
			if len(message) < 4 {
				return nil
			}
			message = message[4:]
		default:
			return nil
		}
	}
	return values
}

// protoVarint decodes a base-128 varint, returning the bytes consumed or 0
// when it is truncated or longer than ten bytes.
func protoVarint(data []byte) (uint64, int) {
	var value uint64
	for index := 0; index < len(data) && index < 10; index++ {
		value |= uint64(data[index]&0x7f) << (7 * index)
		if data[index] < 0x80 {
			return value, index + 1
		}
	}
	return 0, 0
}

// fileURIPath converts a file:// workspace URI into an absolute local path.
func fileURIPath(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "file" || (parsed.Host != "" && parsed.Host != "localhost") {
		return ""
	}
	path := parsed.Path
	// file:///C:/work on Windows parses to /C:/work.
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func deleteAntigravity(row Row) error {
	// agy's conversation summaries live in a SQLite store showagent does not
	// write, so a file-level delete would leave agy listing a ghost.
	return fmt.Errorf("showagent does not delete Antigravity conversations; delete %s from agy's /resume picker", row.ID)
}

// errAntigravityTarget explains why Antigravity is a hand-off source only.
// agy offers no way to create a conversation without a model turn, and in
// print mode that turn can write files when the handed-off text asks it to;
// every per-run way to disable tools either edits the workspace or binds the
// conversation to a tool-less agent for good (checked against agy 1.4.0).
var errAntigravityTarget = errors.New("showagent cannot create Antigravity conversations yet: agy has no import path that avoids a model turn able to run tools; hand Antigravity sessions to another agent instead")

func writeAntigravityConverted(Row, []Turn) (Row, error) {
	return Row{}, errAntigravityTarget
}

// sourceOnlyReason marks Antigravity as readable and resumable, but not a
// target for conversion, branching, or import.
func (antigravityProvider) sourceOnlyReason() error { return errAntigravityTarget }

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
