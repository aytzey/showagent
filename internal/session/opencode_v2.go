package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var openCodeVersionPattern = regexp.MustCompile(`^(?:opencode[ \t]+)?v?([0-9]+)\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?$`)

func openCodeMajorVersion(ctx context.Context, dir string) (int, error) {
	output, err := runOpenCodeCLIContext(ctx, "opencode", dir, "--version")
	if err != nil {
		return 0, fmt.Errorf("detect opencode version: %w", err)
	}
	match := openCodeVersionPattern.FindStringSubmatch(strings.TrimSpace(string(output)))
	if match == nil {
		return 0, fmt.Errorf("unrecognized opencode --version output")
	}
	major, err := strconv.Atoi(match[1])
	if err != nil || major > 2 {
		return 0, fmt.Errorf("unsupported opencode major version %s", match[1])
	}
	return major, nil
}

type openCode2Session struct {
	ID       string `json:"id"`
	ParentID string `json:"parentID,omitempty"`
	Title    string `json:"title,omitempty"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Time struct {
		Created  int64 `json:"created"`
		Updated  int64 `json:"updated"`
		Archived int64 `json:"archived,omitempty"`
	} `json:"time"`
}

type openCode2Message struct {
	ID      string  `json:"id"`
	Type    string  `json:"type"`
	Text    *string `json:"text,omitempty"`
	Content []struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	} `json:"content,omitempty"`
}

func openCode2Read(ctx context.Context, path string, target any) error {
	output, err := runOpenCodeCLIContext(ctx, "opencode", "", "api", "GET", path)
	if err != nil {
		return err
	}
	if err := decodeOpenCodeJSON(output, target); err != nil {
		return fmt.Errorf("parse opencode 2.x API response: %w", err)
	}
	return nil
}

func discoverOpenCode2(ctx context.Context) ([]opencodeSessionEntry, error) {
	query := url.Values{"limit": {"100"}, "order": {"desc"}, "parentID": {"null"}, "archived": {"false"}}
	var entries []opencodeSessionEntry
	seen := make(map[string]bool)
	for {
		var page struct {
			Data   []openCode2Session    `json:"data"`
			Cursor struct{ Next string } `json:"cursor"`
		}
		if err := openCode2Read(ctx, "/api/session?"+query.Encode(), &page); err != nil {
			return nil, err
		}
		if page.Data == nil {
			return nil, fmt.Errorf("opencode 2.x session list is missing data")
		}
		for _, session := range page.Data {
			if session.ID == "" || session.ParentID != "" || session.Time.Archived != 0 {
				continue
			}
			entry := opencodeSessionEntry{
				ID: session.ID, Directory: session.Location.Directory, Title: session.Title,
				Created: session.Time.Created, Updated: session.Time.Updated,
			}
			var err error
			entry.FirstUser, err = openCode2UserPreview(ctx, session.ID, "asc")
			if err != nil {
				return nil, err
			}
			entry.LastUser, err = openCode2UserPreview(ctx, session.ID, "desc")
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
		if page.Cursor.Next == "" {
			return entries, nil
		}
		if seen[page.Cursor.Next] {
			return nil, fmt.Errorf("opencode 2.x session list repeated a pagination cursor")
		}
		seen[page.Cursor.Next] = true
		query = url.Values{"limit": {"100"}, "cursor": {page.Cursor.Next}}
	}
}

func openCode2UserPreview(ctx context.Context, id, order string) (string, error) {
	query := url.Values{"limit": {"1"}, "type": {"user"}, "order": {order}}
	var page struct {
		Data []openCode2Message `json:"data"`
	}
	if err := openCode2Read(ctx, "/api/session/"+url.PathEscape(id)+"/message?"+query.Encode(), &page); err != nil {
		return "", err
	}
	if page.Data == nil {
		return "", fmt.Errorf("opencode 2.x user messages are missing data")
	}
	if len(page.Data) == 0 {
		return "", nil
	}
	if page.Data[0].Type != "user" || page.Data[0].Text == nil {
		return "", fmt.Errorf("opencode 2.x user message is missing its type or text")
	}
	return *page.Data[0].Text, nil
}

func openCode2Transcript(row Row) ([]Turn, error) {
	output, err := runOpenCodeCLI("opencode", existingDir(row.CWD), "session", "export", row.ID)
	if err != nil {
		return nil, fmt.Errorf("opencode session export failed: %w", err)
	}
	var data struct {
		Messages []openCode2Message `json:"messages"`
	}
	if err := decodeOpenCodeJSON(output, &data); err != nil {
		return nil, fmt.Errorf("parse opencode 2.x export for %s: %w", row.ID, err)
	}
	if data.Messages == nil {
		return nil, fmt.Errorf("opencode 2.x export is missing messages")
	}
	var turns []Turn
	for _, message := range data.Messages {
		if message.Type == "" {
			return nil, fmt.Errorf("opencode 2.x export contains a message without a type")
		}
		var text string
		if message.Type == "user" {
			if message.Text == nil {
				return nil, fmt.Errorf("opencode 2.x user message is missing text")
			}
			text = *message.Text
		}
		if message.Type == "assistant" {
			if message.Content == nil {
				return nil, fmt.Errorf("opencode 2.x assistant message is missing content")
			}
			var parts []string
			for _, part := range message.Content {
				if part.Type == "" || (part.Type == "text" && part.Text == nil) {
					return nil, fmt.Errorf("opencode 2.x assistant content is missing its type or text")
				}
				if part.Type == "text" {
					parts = append(parts, *part.Text)
				}
			}
			text = strings.Join(parts, "\n")
		}
		if message.Type != "user" && message.Type != "assistant" {
			continue
		}
		text = cleanTranscriptText(text)
		if keepTranscriptTurn(message.Type, text) {
			turns = append(turns, Turn{Role: message.Type, Text: text})
		}
	}
	return turns, nil
}

func writeOpenCode2Converted(source Row, turns []Turn, dir string) (Row, error) {
	now := time.Now()
	sessionID, err := opencodeID("ses", true, now)
	if err != nil {
		return Row{}, err
	}
	messages := make([]map[string]any, 0, len(turns))
	for index, turn := range turns {
		at := now.Add(time.Duration(index+1) * time.Millisecond)
		id, err := opencodeID("msg", false, at)
		if err != nil {
			return Row{}, err
		}
		message := map[string]any{
			"id": id, "type": turn.Role, "time": map[string]any{"created": at.UnixMilli()},
		}
		if turn.Role == "user" {
			message["text"] = turn.Text
		} else {
			message["agent"] = "build"
			message["model"] = map[string]any{"providerID": "showagent", "id": "converted-transcript"}
			message["content"] = []map[string]any{{"type": "text", "text": turn.Text}}
			message["time"] = map[string]any{"created": at.UnixMilli(), "completed": at.UnixMilli()}
		}
		messages = append(messages, message)
	}
	firstUser, lastUser := userPreviewFromTurns(turns)
	updated := now.Add(time.Duration(len(turns)) * time.Millisecond)
	payload := map[string]any{
		"info": map[string]any{
			"id": sessionID, "projectID": "showagent", "title": opencodeTitle(source, firstUser),
			"location": map[string]any{"directory": dir},
			"time":     map[string]any{"created": now.UnixMilli(), "updated": updated.UnixMilli()},
			"cost":     0,
			"tokens":   map[string]any{"input": 0, "output": 0, "reasoning": 0, "cache": map[string]any{"read": 0, "write": 0}},
		},
		"messages": messages,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return Row{}, err
	}
	file, err := os.CreateTemp("", "showagent-opencode-*.json")
	if err != nil {
		return Row{}, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return Row{}, err
	}
	if err := file.Close(); err != nil {
		return Row{}, err
	}
	output, err := runOpenCodeCLI("opencode", dir, "session", "import", file.Name())
	if err != nil {
		return Row{}, fmt.Errorf("opencode session import failed: %w", err)
	}
	if !strings.Contains(string(output), "Imported session: "+sessionID) {
		return Row{}, fmt.Errorf("opencode session import did not confirm the session: %s", strings.TrimSpace(string(output)))
	}
	return Row{Provider: ProviderOpenCode, ID: sessionID, LastAt: updated, CWD: source.CWD,
		LaunchCWD: dir, FirstUser: firstUser, LastUser: lastUser}, nil
}
