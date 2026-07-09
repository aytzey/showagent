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
	ID  string `json:"id"`
	CWD string `json:"cwd"`
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
	var rows []Row
	walkJSONL(filepath.Join(codexHome, "sessions"), func(path string) {
		if row, ok := parseCodex(path); ok {
			rows = append(rows, row)
		}
	})
	return rows
}

func parseCodex(path string) (Row, bool) {
	id, cwd, firstUser := scanCodexStart(path)
	if id == "" {
		return Row{}, false
	}

	lastAt, ok := bestTimestamp(path)
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
		LastUser:  scanCodexLastUser(path),
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
				if meta.ID != "" {
					id = meta.ID
				}
				if meta.CWD != "" {
					cwd = meta.CWD
				}
			}
		case "turn_context":
			var context codexTurnContext
			if json.Unmarshal(record.Payload, &context) == nil && context.CWD != "" {
				cwd = context.CWD
			}
		case "response_item":
			if firstUser == "" {
				if role, text := codexMessage(record.Payload); role == "user" && usefulUserText(text) {
					firstUser = text
				}
			}
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

func scanCodexLastUser(path string) string {
	lastUser := ""
	// A reverse-scan error deliberately degrades the preview to empty
	// instead of dropping the row; the forward scan already validated the
	// file well enough to list it.
	_ = reverseLines(path, func(line string) bool {
		var record codexLine
		if err := json.Unmarshal([]byte(line), &record); err != nil || record.Type != "response_item" {
			return true
		}
		role, text := codexMessage(record.Payload)
		if role == "user" && usefulUserText(text) {
			lastUser = text
			return false
		}
		return true
	})
	return lastUser
}

func codexMessage(raw json.RawMessage) (string, string) {
	var payload codexMessagePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", ""
	}
	if payload.Type != "message" {
		return "", ""
	}
	return payload.Role, cleanText(textFromContent(payload.Content))
}
