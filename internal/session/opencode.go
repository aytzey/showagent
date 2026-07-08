package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OpenCode support.
//
// OpenCode (github.com/anomalyco/opencode) stores sessions in a SQLite
// database, not in per-session files: <data>/opencode.db (or
// opencode-<channel>.db for non-stable channels), where <data> is
// $XDG_DATA_HOME/opencode or ~/.local/share/opencode. The old JSON tree under
// <data>/storage/ is a legacy format that current releases migrate into the
// database and no longer write (verified against the opencode source at
// v1.17.15: packages/core/src/global.ts, packages/core/src/database/
// database.ts, packages/core/src/session/sql.ts).
//
// Instead of parsing SQLite ourselves, every operation shells out to the
// user's own opencode CLI, so schema drift between opencode versions stays
// opencode's problem and showagent never writes into the database directly:
//
//	discovery   opencode db --format json "<SQL>"   (cli/cmd/db.ts)
//	transcript  opencode export <id>                (cli/cmd/export.ts)
//	delete      opencode session delete <id>        (cli/cmd/session.ts)
//	convert-to  opencode import <file>              (cli/cmd/import.ts)
//	resume      opencode --session <id> [--prompt]  (cli/cmd/tui.ts)
//
// Like jcode, the provider only engages when the opencode binary is on PATH,
// and additionally only when the database file exists, so showagent never
// causes opencode to create a fresh data dir. OPENCODE_DATA_HOME overrides
// the data dir for hermetic tests.
const ProviderOpenCode Provider = "opencode"

// opencodeTimeout bounds every opencode CLI invocation so a hung CLI cannot
// wedge discovery or conversion.
const opencodeTimeout = 60 * time.Second

// opencodeSessionQuery lists root sessions newest-first with their first and
// last real user text. Column and JSON field names follow opencode's schema
// (packages/core/src/session/sql.ts: session/message/part tables; message
// and part ids are ascending, so MIN/MAX order by id is chronological).
// Synthetic and ignored text parts are injected context, not user prompts.
const opencodeSessionQuery = `SELECT s.id AS id, s.directory AS directory, s.title AS title, s.time_created AS created, s.time_updated AS updated,
 (SELECT json_extract(p.data,'$.text') FROM part p JOIN message m ON m.id = p.message_id WHERE p.session_id = s.id AND json_extract(m.data,'$.role') = 'user' AND json_extract(p.data,'$.type') = 'text' AND COALESCE(json_extract(p.data,'$.synthetic'),0) = 0 AND COALESCE(json_extract(p.data,'$.ignored'),0) = 0 ORDER BY p.id LIMIT 1) AS first_user,
 (SELECT json_extract(p.data,'$.text') FROM part p JOIN message m ON m.id = p.message_id WHERE p.session_id = s.id AND json_extract(m.data,'$.role') = 'user' AND json_extract(p.data,'$.type') = 'text' AND COALESCE(json_extract(p.data,'$.synthetic'),0) = 0 AND COALESCE(json_extract(p.data,'$.ignored'),0) = 0 ORDER BY p.id DESC LIMIT 1) AS last_user
 FROM session s WHERE s.parent_id IS NULL AND s.time_archived IS NULL ORDER BY s.time_updated DESC`

// opencodeProvider adapts the OpenCode CLI to the provider registry.
type opencodeProvider struct{}

func (opencodeProvider) Name() Provider      { return ProviderOpenCode }
func (opencodeProvider) DisplayName() string { return "OpenCode" }
func (opencodeProvider) CommandName() string { return "opencode" }
func (opencodeProvider) Home() string        { return defaultOpenCodeDataHome() }

func (p opencodeProvider) ScanTarget() ScanTarget {
	target := ScanTarget{
		Provider: ProviderOpenCode,
		Path:     p.Home(),
		EnvVar:   "OPENCODE_DATA_HOME",
	}
	switch {
	case !ProviderCommandAvailable(ProviderOpenCode):
		target.Note = "skipped: opencode CLI not installed"
	case !opencodeDatabaseExists():
		target.Note = "skipped: no opencode database found"
	}
	return target
}

func (opencodeProvider) Discover() []Row {
	return discoverOpenCode()
}

func (opencodeProvider) ResumeArgs(row Row, options ResumeOptions) []string {
	command := []string{"opencode"}
	if options.Dangerous {
		// --auto is opencode's documented auto-approve-permissions flag.
		command = append(command, "--auto")
	}
	return append(command, "--session", row.ID)
}

func (p opencodeProvider) CompoundArgs(row Row, options ResumeOptions, prompt string) []string {
	return append(p.ResumeArgs(row, options), "--prompt", prompt)
}

// Delete shells out to opencode so the removal cascades to the session's
// messages, parts, and child sessions inside the database.
func (opencodeProvider) Delete(row Row) error {
	if _, err := runOpenCode(existingDir(row.CWD), "session", "delete", row.ID); err != nil {
		return fmt.Errorf("opencode session delete failed: %w", err)
	}
	return nil
}

func (opencodeProvider) Transcript(row Row) ([]Turn, error) {
	return opencodeTranscript(row)
}

func (opencodeProvider) WriteConverted(source Row, turns []Turn) (Row, error) {
	return writeOpenCodeConverted(source, turns)
}

// defaultOpenCodeDataHome mirrors opencode's data dir resolution
// ($XDG_DATA_HOME/opencode, else ~/.local/share/opencode) with an
// OPENCODE_DATA_HOME override so tests and unusual setups can relocate it.
func defaultOpenCodeDataHome() string {
	if value := os.Getenv("OPENCODE_DATA_HOME"); value != "" {
		return expandHome(value)
	}
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		return filepath.Join(expandHome(value), "opencode")
	}
	return filepath.Join(homeDir(), ".local", "share", "opencode")
}

// opencodeDatabaseExists reports whether opencode has a session database on
// this machine (opencode.db, or opencode-<channel>.db for channel builds).
// Without one there is nothing to list, and invoking the CLI anyway would
// make it create an empty database as a side effect.
func opencodeDatabaseExists() bool {
	matches, err := filepath.Glob(filepath.Join(defaultOpenCodeDataHome(), "opencode*.db"))
	return err == nil && len(matches) > 0
}

// opencodeSessionEntry is one row of opencodeSessionQuery. Timestamps are
// unix milliseconds, matching opencode's time_created/time_updated columns.
type opencodeSessionEntry struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	Title     string `json:"title"`
	Created   int64  `json:"created"`
	Updated   int64  `json:"updated"`
	FirstUser string `json:"first_user"`
	LastUser  string `json:"last_user"`
}

func discoverOpenCode() []Row {
	if !ProviderCommandAvailable(ProviderOpenCode) || !opencodeDatabaseExists() {
		return nil
	}

	output, err := runOpenCode("", "db", "--format", "json", opencodeSessionQuery)
	if err != nil {
		// Deliberate skip, matching the other providers: discovery shows what
		// it can parse and never fails the whole listing.
		return nil
	}

	var entries []opencodeSessionEntry
	if err := decodeOpenCodeJSON(output, &entries); err != nil {
		return nil
	}

	rows := make([]Row, 0, len(entries))
	for _, entry := range entries {
		if entry.ID == "" {
			continue
		}
		lastAt := entry.Updated
		if lastAt == 0 {
			lastAt = entry.Created
		}
		if lastAt == 0 {
			continue
		}

		firstUser := opencodeUserText(entry.FirstUser)
		lastUser := opencodeUserText(entry.LastUser)
		if firstUser == "" {
			// The title is opencode's own summary of the opening prompt, so it
			// is the next best preview when no plain user text part exists.
			firstUser = cleanText(entry.Title)
		}

		cwd := strings.TrimSpace(entry.Directory)
		if cwd == "" {
			cwd = "(unknown cwd)"
		}

		rows = append(rows, Row{
			Provider:  ProviderOpenCode,
			ID:        entry.ID,
			LastAt:    time.UnixMilli(lastAt),
			CWD:       cwd,
			LaunchCWD: cwd,
			FirstUser: firstUser,
			LastUser:  lastUser,
		})
	}
	return rows
}

func opencodeUserText(value string) string {
	text := cleanText(value)
	if !usefulUserText(text) {
		return ""
	}
	return text
}

// opencodeExportData is the shape of `opencode export <id>`: the session info
// plus every message with its parts (cli/cmd/export.ts).
type opencodeExportData struct {
	Messages []struct {
		Info struct {
			Role string `json:"role"`
		} `json:"info"`
		Parts []struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			Synthetic bool   `json:"synthetic"`
			Ignored   bool   `json:"ignored"`
		} `json:"parts"`
	} `json:"messages"`
}

func opencodeTranscript(row Row) ([]Turn, error) {
	output, err := runOpenCode(existingDir(row.CWD), "export", row.ID)
	if err != nil {
		return nil, fmt.Errorf("opencode export failed: %w", err)
	}

	var data opencodeExportData
	if err := decodeOpenCodeJSON(output, &data); err != nil {
		return nil, fmt.Errorf("parse opencode export for %s: %w", row.ID, err)
	}

	var turns []Turn
	for _, message := range data.Messages {
		var parts []string
		for _, part := range message.Parts {
			if part.Type != "text" || part.Synthetic || part.Ignored {
				continue
			}
			parts = append(parts, part.Text)
		}
		text := cleanText(strings.Join(parts, "\n"))
		if !keepTranscriptTurn(message.Info.Role, text) {
			continue
		}
		turns = append(turns, Turn{Role: message.Info.Role, Text: text})
	}
	return turns, nil
}

// writeOpenCodeConverted synthesizes a session in opencode's export format
// and hands it to `opencode import`, which validates it and writes it into
// the database under the project of the working directory it runs in. The
// import CLI reports failures on stdout with a zero exit code, so success is
// confirmed by its "Imported session: <id>" acknowledgement.
func writeOpenCodeConverted(source Row, turns []Turn) (Row, error) {
	dir, err := launchDir(source.CWD)
	if err != nil {
		return Row{}, fmt.Errorf("opencode conversion needs the session workspace: %w", err)
	}
	if dir == "" {
		return Row{}, errors.New("opencode conversion needs a known workspace directory")
	}

	now := time.Now()
	sessionID, err := opencodeID("ses", true, now)
	if err != nil {
		return Row{}, err
	}
	slugSuffix, err := randomHex(4)
	if err != nil {
		return Row{}, err
	}

	messages := make([]map[string]any, 0, len(turns))
	parent := ""
	for index, turn := range turns {
		at := now.Add(time.Duration(index+1) * time.Millisecond)
		messageID, err := opencodeID("msg", false, at)
		if err != nil {
			return Row{}, err
		}
		partID, err := opencodeID("prt", false, at)
		if err != nil {
			return Row{}, err
		}

		info := map[string]any{
			"id":        messageID,
			"sessionID": sessionID,
			"role":      turn.Role,
			"time":      map[string]any{"created": at.UnixMilli()},
		}
		if turn.Role == "user" {
			info["agent"] = "build"
			info["model"] = map[string]any{"providerID": "showagent", "modelID": "converted-transcript"}
		} else {
			parentID := parent
			if parentID == "" {
				parentID = messageID
			}
			info["parentID"] = parentID
			info["modelID"] = "converted-transcript"
			info["providerID"] = "showagent"
			info["mode"] = "build"
			info["agent"] = "build"
			info["path"] = map[string]any{"cwd": source.CWD, "root": source.CWD}
			info["cost"] = 0
			info["tokens"] = map[string]any{
				"input": 0, "output": 0, "reasoning": 0,
				"cache": map[string]any{"read": 0, "write": 0},
			}
		}

		messages = append(messages, map[string]any{
			"info": info,
			"parts": []map[string]any{{
				"id":        partID,
				"sessionID": sessionID,
				"messageID": messageID,
				"type":      "text",
				"text":      turn.Text,
			}},
		})
		parent = messageID
	}

	firstUser, lastUser := userPreviewFromTurns(turns)
	updatedAt := now.Add(time.Duration(len(turns)) * time.Millisecond)
	payload := map[string]any{
		"info": map[string]any{
			"id":   sessionID,
			"slug": "showagent-" + slugSuffix,
			// projectID and directory are replaced by `opencode import` with
			// the project resolved from its working directory.
			"projectID": "showagent",
			"directory": source.CWD,
			"title":     opencodeTitle(source, firstUser),
			"version":   "showagent",
			"time": map[string]any{
				"created": now.UnixMilli(),
				"updated": updatedAt.UnixMilli(),
			},
		},
		"messages": messages,
	}

	content, err := json.Marshal(payload)
	if err != nil {
		return Row{}, err
	}

	file, err := os.CreateTemp("", "showagent-opencode-*.json")
	if err != nil {
		return Row{}, fmt.Errorf("create opencode import file: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return Row{}, fmt.Errorf("write opencode import file: %w", err)
	}
	if err := file.Close(); err != nil {
		return Row{}, fmt.Errorf("close opencode import file: %w", err)
	}

	output, err := runOpenCode(dir, "import", file.Name())
	if err != nil {
		return Row{}, fmt.Errorf("opencode import failed: %w", err)
	}
	if !strings.Contains(string(output), "Imported session: "+sessionID) {
		return Row{}, fmt.Errorf("opencode import did not confirm the session: %s", strings.TrimSpace(string(output)))
	}

	return Row{
		Provider:  ProviderOpenCode,
		ID:        sessionID,
		LastAt:    updatedAt,
		CWD:       source.CWD,
		LaunchCWD: source.CWD,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, nil
}

func opencodeTitle(source Row, firstUser string) string {
	title := strings.TrimSpace(firstUser)
	if title == "" {
		return "Converted from " + string(source.Provider) + " via showagent"
	}
	runes := []rune(title)
	if len(runes) > 80 {
		return string(runes[:80]) + "…"
	}
	return title
}

const opencodeIDAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// opencodeID mirrors opencode's identifier scheme (packages/schema/src/
// identifier.ts): prefix + "_" + 12 hex chars encoding unixMillis*0x1000+seq
// (bitwise NOT of that value for descending ids, which sessions use) + 14
// random base62 chars. showagent mints at most one id per timestamp, so the
// per-millisecond sequence number is fixed at 1.
func opencodeID(prefix string, descending bool, at time.Time) (string, error) {
	value := uint64(at.UnixMilli())*0x1000 + 1
	if descending {
		value = ^value
	}
	suffix := make([]byte, 14)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	for i, b := range suffix {
		suffix[i] = opencodeIDAlphabet[int(b)%len(opencodeIDAlphabet)]
	}
	return fmt.Sprintf("%s_%012x%s", prefix, value&0xFFFFFFFFFFFF, suffix), nil
}

// runOpenCode invokes the opencode CLI, returning its stdout. dir is the
// working directory ("" inherits showagent's). Every call is bounded by
// opencodeTimeout so a wedged CLI cannot hang the picker.
func runOpenCode(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opencodeTimeout)
	defer cancel()

	command := exec.CommandContext(ctx, "opencode", args...)
	if dir != "" {
		command.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("opencode %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return stdout.Bytes(), nil
}

// decodeOpenCodeJSON unmarshals the first JSON payload in CLI output,
// tolerating banner or notice lines the CLI may print before it.
func decodeOpenCodeJSON(output []byte, value any) error {
	index := bytes.IndexAny(output, "[{")
	if index < 0 {
		return errors.New("no JSON payload in opencode output")
	}
	return json.Unmarshal(output[index:], value)
}

// existingDir returns cwd when it is a real directory, and "" otherwise, for
// CLI calls that prefer running inside the session's workspace but must not
// fail when it is gone.
func existingDir(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || strings.HasPrefix(cwd, "(") {
		return ""
	}
	if info, err := os.Stat(cwd); err == nil && info.IsDir() {
		return cwd
	}
	return ""
}
