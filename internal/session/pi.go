package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Pi coding agent support.
//
// Pi stores one versioned JSONL tree per session. The final entry in the file
// is the active leaf; parentId links select the branch Pi will resume. showagent
// follows that same leaf path so abandoned branches are not previewed or
// transferred to another provider.
const ProviderPi Provider = "pi"

type piProvider struct{}

func (piProvider) Name() Provider      { return ProviderPi }
func (piProvider) DisplayName() string { return "Pi" }
func (piProvider) CommandName() string { return "pi" }
func (piProvider) Home() string        { return defaultPiAgentDir() }

func (piProvider) ScanTarget() ScanTarget {
	return ScanTarget{
		Provider: ProviderPi,
		Path:     defaultPiSessionRoot(),
		EnvVar:   "PI_CODING_AGENT_SESSION_DIR or PI_CODING_AGENT_DIR",
	}
}

func (piProvider) Discover() []Row {
	return discoverPi(defaultPiSessionRoot())
}

func (piProvider) ResumeArgs(row Row, _ ResumeOptions) []string {
	selector := row.File
	if selector == "" {
		selector = row.ID
	}
	return []string{"pi", "--session", selector}
}

func (p piProvider) CompoundArgs(row Row, options ResumeOptions, prompt string) []string {
	return append(p.ResumeArgs(row, options), prompt)
}

func (piProvider) Delete(row Row) error {
	if row.File == "" {
		return errors.New("pi session file is unknown")
	}
	if filepath.Ext(row.File) != ".jsonl" {
		return fmt.Errorf("unexpected Pi session path %q", row.File)
	}
	return os.Remove(row.File)
}

func (piProvider) Transcript(row Row) ([]Turn, error) {
	return piTranscript(row.File)
}

func (piProvider) WriteConverted(source Row, turns []Turn) (Row, error) {
	return writePiConverted(source, turns)
}

type piSessionHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

type piSessionEntry struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	Message   struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"message"`
}

type piSessionLink struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

type piSession struct {
	Header piSessionHeader
	Path   []piSessionEntry
}

func defaultPiAgentDir() string {
	if value := os.Getenv("PI_CODING_AGENT_DIR"); value != "" {
		return absoluteExpandedPath(value)
	}
	return filepath.Join(homeDir(), ".pi", "agent")
}

// defaultPiSessionRoot returns the directory Pi searches globally. The
// explicit environment variable has the same precedence as Pi's CLI. A global
// settings.json sessionDir is also honored; project-local overrides cannot be
// discovered globally without already knowing every project path.
func defaultPiSessionRoot() string {
	root, _ := piSessionRoot()
	return root
}

func piSessionRoot() (string, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return piSessionRootForCWD(cwd)
}

func piSessionRootForCWD(cwd string) (string, bool) {
	if value := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); value != "" {
		return absoluteExpandedPathFrom(value, cwd), true
	}

	agentDir := defaultPiAgentDir()
	sessionDir := readPiSessionDirSetting(filepath.Join(agentDir, "settings.json"))
	if projectDir := readPiSessionDirSetting(filepath.Join(absoluteExpandedPath(cwd), ".pi", "settings.json")); projectDir != "" {
		sessionDir = projectDir
	}
	if sessionDir != "" {
		return absoluteExpandedPathFrom(sessionDir, cwd), true
	}
	return filepath.Join(agentDir, "sessions"), false
}

func readPiSessionDirSetting(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var settings struct {
		SessionDir string `json:"sessionDir"`
	}
	if json.Unmarshal(content, &settings) != nil {
		return ""
	}
	return strings.TrimSpace(settings.SessionDir)
}

func absoluteExpandedPath(value string) string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return absoluteExpandedPathFrom(value, cwd)
}

func absoluteExpandedPathFrom(value, cwd string) string {
	value = expandHome(strings.TrimSpace(value))
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if absolute, err := filepath.Abs(filepath.Join(cwd, value)); err == nil {
		return absolute
	}
	return filepath.Clean(filepath.Join(cwd, value))
}

func defaultPiSessionDir(cwd string) string {
	root, custom := piSessionRootForCWD(cwd)
	if custom {
		return root
	}
	resolved := absoluteExpandedPath(cwd)
	safePath := strings.TrimLeft(resolved, `/\`)
	safePath = strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(safePath)
	return filepath.Join(root, "--"+safePath+"--")
}

func discoverPi(root string) []Row {
	return parseRowsBounded(jsonlPaths(root), parsePi)
}

func parsePi(path string) (Row, bool) {
	session, err := loadPiSession(path)
	if err != nil || session.Header.ID == "" {
		return Row{}, false
	}
	firstUser, lastUser, hasTransferableTurn := piPreview(session.Path)
	if !hasTransferableTurn {
		return Row{}, false
	}

	lastAt, ok := piLastTimestamp(session)
	if !ok {
		lastAt, ok = fallbackMTime(path)
	}
	if !ok {
		return Row{}, false
	}

	cwd := strings.TrimSpace(session.Header.CWD)
	if cwd == "" {
		cwd = "(unknown cwd)"
	}
	return Row{
		Provider:  ProviderPi,
		ID:        session.Header.ID,
		LastAt:    lastAt,
		CWD:       cwd,
		LaunchCWD: cwd,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, true
}

func loadPiSession(path string) (piSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return piSession{}, err
	}
	defer func() { _ = file.Close() }()

	var result piSession
	byID := map[string]piSessionLink{}
	var leaf piSessionLink
	headerSeen := false
	invalidHeader := false
	if err := scanPiLines(file, func(line []byte) {
		if len(bytes.TrimSpace(line)) == 0 {
			return
		}
		var link piSessionLink
		if err := json.Unmarshal(line, &link); !headerSeen {
			headerSeen = true
			if err != nil || link.Type != "session" || link.ID == "" {
				invalidHeader = true
				return
			}
			result.Header = piSessionHeader{
				Type: link.Type, Version: link.Version, ID: link.ID,
				Timestamp: link.Timestamp, CWD: link.CWD,
			}
			return
		} else if err != nil || invalidHeader {
			return
		}
		if link.Type == "session" {
			return
		}
		if link.ID == "" {
			return
		}
		leaf = link
		byID[link.ID] = link
	}); err != nil {
		return piSession{}, err
	}
	if invalidHeader || result.Header.Type != "session" || result.Header.ID == "" {
		return piSession{}, fmt.Errorf("%s is not a Pi session file", path)
	}
	if result.Header.Version < 2 {
		_, entries, err := loadPiEntries(file, nil)
		if err != nil {
			return piSession{}, err
		}
		result.Path = entries
		return result, nil
	}
	if leaf.ID == "" {
		return result, nil
	}

	current := leaf
	var pathIDs []string
	seen := make(map[string]bool, len(byID))
	for current.ID != "" && !seen[current.ID] {
		seen[current.ID] = true
		pathIDs = append(pathIDs, current.ID)
		if current.ParentID == "" {
			break
		}
		parent, ok := byID[current.ParentID]
		if !ok {
			break
		}
		current = parent
	}
	slices.Reverse(pathIDs)
	entries, _, err := loadPiEntries(file, seen)
	if err != nil {
		return piSession{}, err
	}
	for _, id := range pathIDs {
		if entry, ok := entries[id]; ok {
			result.Path = append(result.Path, entry)
		}
	}
	return result, nil
}

func scanPiLines(file *os.File, visit func([]byte)) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scanBufferMax)
	for scanner.Scan() {
		visit(scanner.Bytes())
	}
	return scanner.Err()
}

func loadPiEntries(file *os.File, wanted map[string]bool) (map[string]piSessionEntry, []piSessionEntry, error) {
	entries := make(map[string]piSessionEntry, len(wanted))
	var legacy []piSessionEntry
	err := scanPiLines(file, func(line []byte) {
		var link piSessionLink
		if json.Unmarshal(line, &link) != nil || link.Type == "session" {
			return
		}
		if wanted != nil && !wanted[link.ID] {
			return
		}
		var entry piSessionEntry
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		if wanted == nil {
			legacy = append(legacy, entry)
			return
		}
		entries[entry.ID] = entry
	})
	return entries, legacy, err
}

func piLastTimestamp(session piSession) (time.Time, bool) {
	for index := len(session.Path) - 1; index >= 0; index-- {
		if timestamp, ok := parseTimestamp(session.Path[index].Timestamp); ok {
			return timestamp, true
		}
	}
	return parseTimestamp(session.Header.Timestamp)
}

func piTurns(entries []piSessionEntry) []Turn {
	turns := make([]Turn, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "message" {
			continue
		}
		role := entry.Message.Role
		text := cleanTranscriptText(textFromContent(entry.Message.Content))
		if !keepTranscriptTurn(role, text) {
			continue
		}
		turns = append(turns, Turn{Role: role, Text: text})
	}
	return turns
}

func piPreview(entries []piSessionEntry) (firstUser, lastUser string, hasTransferableTurn bool) {
	for _, entry := range entries {
		if entry.Type != "message" {
			continue
		}
		role := entry.Message.Role
		if role == "assistant" && hasTransferableTurn {
			continue
		}
		text := cleanTranscriptText(textFromContent(entry.Message.Content))
		if !keepTranscriptTurn(role, text) {
			continue
		}
		hasTransferableTurn = true
		if role == "user" {
			preview := cleanPreviewText(text)
			if preview == "" {
				continue
			}
			if firstUser == "" {
				firstUser = preview
			}
			lastUser = preview
		}
	}
	return firstUser, lastUser, hasTransferableTurn
}

func piTranscript(path string) ([]Turn, error) {
	session, err := loadPiSession(path)
	if err != nil {
		return nil, err
	}
	return piTurns(session.Path), nil
}

func writePiConverted(source Row, turns []Turn) (Row, error) {
	cwd := strings.TrimSpace(source.CWD)
	if cwd == "" || strings.HasPrefix(cwd, "(") {
		return Row{}, errors.New("pi conversion needs a known workspace directory")
	}

	sessionID, err := newUUID()
	if err != nil {
		return Row{}, err
	}
	now := time.Now().UTC()
	directory := defaultPiSessionDir(cwd)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Row{}, err
	}
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(now.Format(time.RFC3339Nano))
	path := filepath.Join(directory, stamp+"_"+sessionID+".jsonl")

	lastAt := now
	if err := writeFileAtomic(path, func(file *os.File) error {
		encoder := json.NewEncoder(file)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(map[string]any{
			"type":      "session",
			"version":   3,
			"id":        sessionID,
			"timestamp": now.Format(time.RFC3339Nano),
			"cwd":       cwd,
		}); err != nil {
			return err
		}

		var parentID any
		for index, turn := range turns {
			entryID, err := newPiEntryID()
			if err != nil {
				return err
			}
			entryTime := now.Add(time.Duration(index+1) * time.Millisecond)
			lastAt = entryTime
			message := map[string]any{
				"role":      turn.Role,
				"content":   turn.Text,
				"timestamp": entryTime.UnixMilli(),
			}
			if turn.Role == "assistant" {
				message["content"] = []map[string]any{{"type": "text", "text": turn.Text}}
				message["api"] = "openai-completions"
				message["provider"] = "showagent"
				message["model"] = "converted-session"
				message["usage"] = map[string]any{
					"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
					"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
				}
				message["stopReason"] = "stop"
			}
			if err := encoder.Encode(map[string]any{
				"type":      "message",
				"id":        entryID,
				"parentId":  parentID,
				"timestamp": entryTime.Format(time.RFC3339Nano),
				"message":   message,
			}); err != nil {
				return err
			}
			parentID = entryID
		}
		return nil
	}); err != nil {
		return Row{}, err
	}

	firstUser, lastUser := userPreviewFromTurns(turns)
	return Row{
		Provider:  ProviderPi,
		ID:        sessionID,
		LastAt:    lastAt,
		CWD:       cwd,
		LaunchCWD: cwd,
		File:      path,
		FirstUser: firstUser,
		LastUser:  lastUser,
	}, nil
}

func newPiEntryID() (string, error) {
	return randomHex(4)
}
