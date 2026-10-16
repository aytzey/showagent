package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
const ProviderMiMo Provider = "mimocode"

// opencodeTimeout bounds every opencode CLI invocation so a hung CLI cannot
// wedge discovery or conversion.
const opencodeTimeout = 60 * time.Second

const (
	maxOpenCodeOutput = 64 << 20
	maxOpenCodeError  = 1 << 20
)

// opencodeSessionQuery lists root sessions newest-first with their first and
// last real user text. Column and JSON field names follow opencode's schema
// (packages/core/src/session/sql.ts: session/message/part tables; message
// and part ids are ascending, so MIN/MAX order by id is chronological).
// Synthetic and ignored text parts are injected context, not user prompts.
// shortcut: MiMo previews stop at 200 characters to stay under its CLI output
// flush limit; paginate the query if the session count makes JSON exceed it.
const opencodeSessionQuery = `SELECT s.id AS id, s.directory AS directory, s.title AS title, s.time_created AS created, s.time_updated AS updated,
 (SELECT substr(json_extract(p.data,'$.text'),1,200) FROM part p JOIN message m ON m.id = p.message_id WHERE p.session_id = s.id AND json_extract(m.data,'$.role') = 'user' AND json_extract(p.data,'$.type') = 'text' AND COALESCE(json_extract(p.data,'$.synthetic'),0) = 0 AND COALESCE(json_extract(p.data,'$.ignored'),0) = 0 ORDER BY p.id LIMIT 1) AS first_user,
 (SELECT substr(json_extract(p.data,'$.text'),1,200) FROM part p JOIN message m ON m.id = p.message_id WHERE p.session_id = s.id AND json_extract(m.data,'$.role') = 'user' AND json_extract(p.data,'$.type') = 'text' AND COALESCE(json_extract(p.data,'$.synthetic'),0) = 0 AND COALESCE(json_extract(p.data,'$.ignored'),0) = 0 ORDER BY p.id DESC LIMIT 1) AS last_user
 FROM session s WHERE s.parent_id IS NULL AND s.time_archived IS NULL ORDER BY s.time_updated DESC`

// sessionQuery bounds previews only for MiMo; OpenCode keeps whole prompts so
// search, list --json and MCP filtering still see every word.
func (p opencodeProvider) sessionQuery() string {
	if p.mimo {
		return opencodeSessionQuery
	}
	return strings.ReplaceAll(opencodeSessionQuery, "substr(json_extract(p.data,'$.text'),1,200)", "json_extract(p.data,'$.text')")
}

// opencodeProvider adapts the OpenCode CLI to the provider registry.
// MiMoCode is an OpenCode fork with the same CLI import/export format and its
// own binary and database. Keep both on the same adapter.
type opencodeProvider struct{ mimo bool }

func (p opencodeProvider) Name() Provider {
	if p.mimo {
		return ProviderMiMo
	}
	return ProviderOpenCode
}
func (p opencodeProvider) DisplayName() string {
	if p.mimo {
		return "MiMoCode"
	}
	return "OpenCode"
}
func (p opencodeProvider) CommandName() string {
	if p.mimo {
		return "mimo"
	}
	return "opencode"
}
func (p opencodeProvider) Home() string {
	if p.mimo {
		return defaultMiMoDataHome()
	}
	return defaultOpenCodeDataHome()
}
func (p opencodeProvider) envVar() string {
	if p.mimo {
		return "MIMOCODE_HOME"
	}
	return "OPENCODE_DATA_HOME"
}

func (p opencodeProvider) ScanTargets() []ScanTarget {
	target := ScanTarget{
		Provider: p.Name(),
		Path:     p.Home(),
		EnvVar:   p.envVar(),
	}
	switch {
	case !ProviderCommandAvailable(p.Name()):
		target.Note = "skipped: " + p.CommandName() + " CLI not installed"
	case !p.databaseExists():
		target.Note = "skipped: no " + p.CommandName() + " database found"
	}
	return []ScanTarget{target}
}

func (p opencodeProvider) Discover() []Row {
	return discoverOpenCode(p)
}

func (p opencodeProvider) ResumeArgs(row Row, options ResumeOptions) []string {
	command := []string{p.CommandName()}
	if options.Dangerous {
		if p.mimo {
			command = append(command, "--dangerously-skip-permissions")
		} else {
			command = append(command, "--auto")
		}
	}
	return append(command, "--session", row.ID)
}

func (p opencodeProvider) CompoundArgs(row Row, options ResumeOptions, prompt string) []string {
	return append(p.ResumeArgs(row, options), "--prompt", prompt)
}

// Delete shells out to opencode so the removal cascades to the session's
// messages, parts, and child sessions inside the database.
func (p opencodeProvider) Delete(row Row) error {
	if _, err := runOpenCodeCLI(p.CommandName(), existingDir(row.CWD), "session", "delete", row.ID); err != nil {
		return fmt.Errorf("%s session delete failed: %w", p.CommandName(), err)
	}
	return nil
}

func (p opencodeProvider) Transcript(row Row) ([]Turn, error) {
	return opencodeTranscript(p.CommandName(), row)
}

func (p opencodeProvider) WriteConverted(source Row, turns []Turn) (Row, error) {
	return writeOpenCodeConverted(p, source, turns)
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

func defaultMiMoDataHome() string {
	if value := os.Getenv("MIMOCODE_HOME"); value != "" {
		return filepath.Join(value, "data")
	}
	if value := os.Getenv("XDG_DATA_HOME"); value != "" {
		return filepath.Join(expandHome(value), "mimocode")
	}
	return filepath.Join(homeDir(), ".local", "share", "mimocode")
}

// Avoid invoking a CLI with no database: discovery must not create an empty store.
func (p opencodeProvider) databaseExists() bool {
	name := p.CommandName()
	if p.mimo {
		name = "mimocode"
	}
	matches, err := filepath.Glob(filepath.Join(p.Home(), name+"*.db"))
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

func discoverOpenCode(p opencodeProvider) []Row {
	if !ProviderCommandAvailable(p.Name()) || !p.databaseExists() {
		return nil
	}

	output, err := runOpenCodeCLI(p.CommandName(), "", "db", "--format", "json", p.sessionQuery())
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
			firstUser = cleanPreviewText(entry.Title)
		}

		cwd := strings.TrimSpace(entry.Directory)
		if cwd == "" {
			cwd = "(unknown cwd)"
		}

		rows = append(rows, Row{
			Provider:  p.Name(),
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
	text := cleanPreviewText(value)
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

func opencodeTranscript(command string, row Row) ([]Turn, error) {
	if command == "mimo" {
		return mimoTranscript(row)
	}
	output, err := runOpenCodeCLI(command, existingDir(row.CWD), "export", row.ID)
	if err != nil {
		return nil, fmt.Errorf("%s export failed: %w", command, err)
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
		text := cleanTranscriptText(strings.Join(parts, "\n"))
		if !keepTranscriptTurn(message.Info.Role, text) {
			continue
		}
		turns = append(turns, Turn{Role: message.Info.Role, Text: text})
	}
	return turns, nil
}

// MiMoCode's export can exit successfully after truncating large JSON output.
// Query only transferable text parts, paging below its db command's output cap.
func mimoTranscript(row Row) ([]Turn, error) {
	const pageSize = 64
	id := strings.ReplaceAll(row.ID, "'", "''")
	var turns []Turn
	var messageID, role string
	var parts []string
	flush := func() {
		text := cleanTranscriptText(strings.Join(parts, "\n"))
		if keepTranscriptTurn(role, text) {
			turns = append(turns, Turn{Role: role, Text: text})
		}
	}

	for offset, limit := 0, pageSize; ; {
		query := fmt.Sprintf(`SELECT m.id AS message_id, json_extract(m.data,'$.role') AS role, json_extract(p.data,'$.text') AS text
FROM message m JOIN part p ON p.message_id=m.id
WHERE m.session_id='%s' AND p.session_id='%s' AND json_extract(p.data,'$.type')='text'
AND COALESCE(json_extract(p.data,'$.synthetic'),0)=0 AND COALESCE(json_extract(p.data,'$.ignored'),0)=0
ORDER BY m.id,p.id LIMIT %d OFFSET %d`, id, id, limit, offset)
		output, err := runOpenCodeCLI("mimo", existingDir(row.CWD), "db", "--format", "json", query)
		if err != nil {
			return nil, fmt.Errorf("read MiMoCode transcript for %s: %w", row.ID, err)
		}
		var page []struct {
			MessageID string `json:"message_id"`
			Role      string `json:"role"`
			Text      string `json:"text"`
		}
		if err := decodeOpenCodeJSON(output, &page); err != nil {
			if limit == 1 {
				return nil, fmt.Errorf("parse MiMoCode transcript for %s: %w", row.ID, err)
			}
			// ponytail: a single text part can still exceed MiMo's CLI output cap;
			// read SQLite directly if that becomes a real case.
			limit = max(1, limit/2)
			continue
		}
		for _, item := range page {
			if messageID != "" && item.MessageID != messageID {
				flush()
				parts = nil
			}
			messageID, role = item.MessageID, item.Role
			parts = append(parts, item.Text)
		}
		offset += len(page)
		if len(page) < limit {
			break
		}
	}
	if messageID != "" {
		flush()
	}
	return turns, nil
}

// writeOpenCodeConverted synthesizes a session in opencode's export format
// and hands it to `opencode import`, which validates it and writes it into
// the database under the project of the working directory it runs in. The
// import CLI reports failures on stdout with a zero exit code, so success is
// confirmed by its "Imported session: <id>" acknowledgement.
func writeOpenCodeConverted(p opencodeProvider, source Row, turns []Turn) (Row, error) {
	dir, err := launchDir(source.CWD)
	if err != nil {
		return Row{}, fmt.Errorf("%s conversion needs the session workspace: %w", p.CommandName(), err)
	}
	if dir == "" {
		return Row{}, fmt.Errorf("%s conversion needs a known workspace directory", p.CommandName())
	}
	modelProviderID, modelID := "showagent", "converted-transcript"
	if p.mimo {
		modelProviderID, modelID, err = mimoDefaultModel(dir)
		if err != nil {
			return Row{}, err
		}
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
			info["model"] = map[string]any{"providerID": modelProviderID, "modelID": modelID}
		} else {
			parentID := parent
			if parentID == "" {
				parentID = messageID
			}
			info["parentID"] = parentID
			info["modelID"] = modelID
			info["providerID"] = modelProviderID
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

	file, err := os.CreateTemp("", "showagent-"+p.CommandName()+"-*.json")
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

	output, err := runOpenCodeCLI(p.CommandName(), dir, "import", file.Name())
	if err != nil {
		return Row{}, fmt.Errorf("%s import failed: %w", p.CommandName(), err)
	}
	if !strings.Contains(string(output), "Imported session: "+sessionID) {
		return Row{}, fmt.Errorf("%s import did not confirm the session: %s", p.CommandName(), strings.TrimSpace(string(output)))
	}

	return Row{
		Provider:  p.Name(),
		ID:        sessionID,
		LastAt:    updatedAt,
		CWD:       source.CWD,
		LaunchCWD: source.CWD,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, nil
}

// MiMoCode resumes with the last imported user message's model. Ask its CLI
// for the resolved default so JSONC, project config, and auth stay MiMo's job.
func mimoDefaultModel(dir string) (string, string, error) {
	output, err := runOpenCodeCLI("mimo", dir, "debug", "config")
	if err != nil {
		return "", "", fmt.Errorf("read MiMoCode default model: %w", err)
	}
	var config struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(output, &config); err != nil {
		return "", "", fmt.Errorf("parse MiMoCode default model: %w", err)
	}
	providerID, modelID, ok := strings.Cut(config.Model, "/")
	if !ok || providerID == "" || modelID == "" {
		return "", "", fmt.Errorf("MiMoCode needs a configured default model before handover")
	}
	return providerID, modelID, nil
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
//
// stdout and stderr are drained in their own goroutines via explicit pipes,
// because discovery outputs can run into multiple megabytes (each session's
// first/last user-message previews are included in the JSON). Setting
// command.Stdout/Stderr to an io.Writer and letting exec.Run() drain the
// pipes itself loses the tail of large outputs — see issue #10.
func runOpenCode(dir string, args ...string) ([]byte, error) {
	return runOpenCodeCLI("opencode", dir, args...)
}

func runOpenCodeCLI(binary, dir string, args ...string) ([]byte, error) {
	output, err := runProviderCLI(opencodeTimeout, binary, dir, nil, args...)
	if err != nil {
		return nil, err
	}
	return output, nil
}

// runProviderCLI runs a provider CLI in dir (when set) with optional stdin
// and a timeout. It returns whatever stdout was captured even when the
// command fails, so callers can read a structured error the CLI printed.
func runProviderCLI(timeout time.Duration, binary, dir string, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	command := exec.CommandContext(ctx, binary, args...)
	if dir != "" {
		command.Dir = dir
	}
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}

	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%s %s stdout pipe: %w", binary, strings.Join(args, " "), err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("%s %s stderr pipe: %w", binary, strings.Join(args, " "), err)
	}

	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("%s %s: %w", binary, strings.Join(args, " "), err)
	}

	var stdout cappedBuffer
	stdout.max = maxOpenCodeOutput
	var stderr cappedBuffer
	stderr.max = maxOpenCodeError
	var drain sync.WaitGroup
	drain.Add(2)
	go func() {
		defer drain.Done()
		_, _ = io.Copy(&stdout, stdoutPipe)
	}()
	go func() {
		defer drain.Done()
		_, _ = io.Copy(&stderr, stderrPipe)
	}()
	drain.Wait()
	waitErr := command.Wait()

	if stdout.Len() > maxOpenCodeOutput {
		return nil, fmt.Errorf("%s %s: command output exceeds %d bytes", binary, strings.Join(args, " "), maxOpenCodeOutput)
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return stdout.Bytes(), fmt.Errorf("%s %s: %w: %s", binary, strings.Join(args, " "), waitErr, detail)
	}
	return stdout.Bytes(), nil
}

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(payload []byte) (int, error) {
	remaining := b.max - b.Len()
	if remaining <= 0 {
		return 0, fmt.Errorf("command output exceeds %d bytes", b.max)
	}
	if len(payload) <= remaining {
		return b.Buffer.Write(payload)
	}
	written, _ := b.Buffer.Write(payload[:remaining])
	return written, fmt.Errorf("command output exceeds %d bytes", b.max)
}

// decodeOpenCodeJSON unmarshals the first JSON payload in CLI output,
// tolerating banner or notice lines the CLI may print before it.
func decodeOpenCodeJSON(output []byte, value any) error {
	offset := 0
	for _, line := range bytes.SplitAfter(output, []byte{'\n'}) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			offset += len(line)
			continue
		}
		leading := len(line) - len(bytes.TrimLeft(line, " \t\r\n"))
		lineOffset := offset + leading
		offset += len(line)
		if trimmed[0] == '[' || trimmed[0] == '{' {
			return json.NewDecoder(bytes.NewReader(output[lineOffset:])).Decode(value)
		}
	}
	return errors.New("no JSON payload in opencode output")
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
