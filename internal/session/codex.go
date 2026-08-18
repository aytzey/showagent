package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// codexProvider adapts the Codex CLI's session store (rollout-*.jsonl files
// under ~/.codex/sessions) to the provider registry.
type codexProvider struct{}

func (codexProvider) Name() Provider      { return ProviderCodex }
func (codexProvider) DisplayName() string { return "Codex" }
func (codexProvider) CommandName() string { return "codex" }
func (codexProvider) Home() string        { return defaultCodexHome() }

func (p codexProvider) ScanTarget() ScanTarget {
	return ScanTarget{
		Provider: ProviderCodex,
		Path:     filepath.Join(p.Home(), "sessions"),
		EnvVar:   "CODEX_HOME",
	}
}

func (p codexProvider) Discover() []Row {
	return discoverCodex(p.Home())
}

func (codexProvider) ResumeArgs(row Row, options ResumeOptions) []string {
	command := []string{"codex", "resume"}
	if options.Dangerous {
		command = append(command, "--dangerously-bypass-approvals-and-sandbox")
	}
	return append(command, row.ID)
}

func (p codexProvider) CompoundArgs(row Row, options ResumeOptions, prompt string) []string {
	return append(p.ResumeArgs(row, options), prompt)
}

// Delete shells out to the codex CLI so codex can keep its own bookkeeping
// (history metadata) consistent instead of us unlinking the rollout file.
func (codexProvider) Delete(row Row) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, "codex", "delete", "--force", row.ID)
	if info, err := os.Stat(row.CWD); err == nil && info.IsDir() {
		command.Dir = row.CWD
	}
	if output, err := command.CombinedOutput(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("codex delete timed out after 30s: %w", ctx.Err())
		}
		return fmt.Errorf("codex delete failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (codexProvider) Transcript(row Row) ([]Turn, error) {
	return codexTranscript(row.File)
}

func (codexProvider) WriteConverted(source Row, turns []Turn) (Row, error) {
	return writeCodexConverted(source, turns)
}

type codexLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID             string `json:"id"`
	CWD            string `json:"cwd"`
	ThreadSource   string `json:"thread_source"`
	ParentThreadID string `json:"parent_thread_id"`
}

type codexTurnContext struct {
	CWD string `json:"cwd"`
}

type codexMessagePayload struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func discoverCodex(codexHome string) []Row {
	// Multica keeps daemon-managed Codex conversations in per-agent/issue
	// stores beside the user's ordinary sessions so native Codex does not index
	// thousands of task threads. Showagent is the explicit cross-agent history
	// browser, so include both roots here and make those local task contexts
	// available for branch/convert/transcript handoff as well.
	paths := jsonlPaths(filepath.Join(codexHome, "sessions"))
	paths = append(paths, jsonlPaths(filepath.Join(codexHome, "multica-sessions"))...)
	return parseRowsBounded(paths, parseCodex)
}

func parseCodex(path string) (Row, bool) {
	id, cwd, firstUser := scanCodexStart(path)
	if id == "" {
		return Row{}, false
	}

	tail := scanCodexTail(path)
	if tail.cwd != "" {
		cwd = tail.cwd
	}
	lastAt, ok := tail.lastAt, tail.hasTimestamp
	if !ok {
		lastAt, ok = fallbackMTime(path)
	}
	if !ok {
		return Row{}, false
	}

	if cwd == "" {
		cwd = "(unknown cwd)"
	}

	return Row{
		Provider:  ProviderCodex,
		ID:        id,
		LastAt:    lastAt,
		CWD:       cwd,
		LaunchCWD: cwd,
		File:      path,
		FirstUser: firstUser,
		LastUser:  tail.lastUser,
	}, true
}

func scanCodexStart(path string) (string, string, string) {
	id := sessionIDFromPath(path)
	cwd := ""
	firstUser := ""

	file, err := os.Open(path)
	if err != nil {
		return "", "", ""
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scanBufferMax)
	metaSeen := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var record codexLine
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		switch record.Type {
		case "session_meta":
			var meta codexSessionMeta
			if json.Unmarshal(record.Payload, &meta) == nil {
				if meta.ParentThreadID != "" || (meta.ThreadSource != "" && meta.ThreadSource != "user") {
					return "", "", ""
				}
				metaSeen = true
				if meta.ID != "" {
					id = meta.ID
				}
				if meta.CWD != "" {
					cwd = meta.CWD
				}
			}
		case "response_item":
			if firstUser == "" {
				if role, text := codexMessage(record.Payload); role == "user" && usefulUserText(text) {
					firstUser = cleanPreviewText(text)
				}
			}
		}
		if metaSeen && firstUser != "" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		// Deliberate skip: a file we cannot scan to the end (read error, or
		// a single line beyond scanBufferMax) is dropped from discovery
		// instead of being shown half-parsed. Conversion paths report the
		// same condition as an error.
		return "", "", ""
	}

	return id, cwd, firstUser
}

type codexTail struct {
	cwd          string
	lastUser     string
	lastAt       time.Time
	hasTimestamp bool
}

// scanCodexTail collects all tail-owned metadata in one reverse pass. Modern
// Codex files place turn_context and timestamped response records near the
// end, so even a 100 MiB rollout normally costs one 64 KiB block instead of
// three full-file scans.
func scanCodexTail(path string) codexTail {
	var tail codexTail
	_ = reverseLines(path, func(line string) bool {
		if !tail.hasTimestamp {
			tail.lastAt, tail.hasTimestamp = timestampFromLine(line)
		}

		var record codexLine
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return true
		}
		switch record.Type {
		case "turn_context":
			if tail.cwd == "" {
				var turnContext codexTurnContext
				if json.Unmarshal(record.Payload, &turnContext) == nil {
					tail.cwd = strings.TrimSpace(turnContext.CWD)
				}
			}
		case "response_item":
			if tail.lastUser == "" {
				role, text := codexMessage(record.Payload)
				if role == "user" && usefulUserText(text) {
					tail.lastUser = cleanPreviewText(text)
				}
			}
		}
		return !tail.hasTimestamp || tail.cwd == "" || tail.lastUser == ""
	})
	return tail
}

func codexMessage(raw json.RawMessage) (string, string) {
	var payload codexMessagePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", ""
	}
	if payload.Type != "message" {
		return "", ""
	}
	return payload.Role, cleanTranscriptText(textFromContent(payload.Content))
}
