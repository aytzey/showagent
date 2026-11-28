package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type codexTrimSnapshot struct {
	beforeTurn string
	history    [sha256.Size]byte
}

type codexNativeThread struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	CWD         string `json:"cwd"`
	HistoryMode string `json:"historyMode"`
	Ephemeral   bool   `json:"ephemeral"`
	Status      struct {
		Type string `json:"type"`
	} `json:"status"`
}

type codexNativeResult struct {
	Thread codexNativeThread `json:"thread"`
}

type codexNativeHistory struct {
	turnIDs      []string
	starts       []int
	conversation []Turn
	digest       [sha256.Size]byte
}

func startCodexHistoryClient(ctx context.Context, cwd string) (*codexImportRPCClient, error) {
	client, err := startCodexImportClient(ctx, cwd)
	if err != nil {
		return nil, err
	}
	if err := client.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "showagent-history", "version": "1"},
	}, nil); err != nil {
		client.close()
		return nil, err
	}
	if err := client.notify("initialized"); err != nil {
		client.close()
		return nil, err
	}
	return client, nil
}

func readCodexNativeThread(ctx context.Context, client *codexImportRPCClient, id string) (codexNativeThread, error) {
	var result codexNativeResult
	err := client.call(ctx, "thread/read", map[string]string{"threadId": id}, &result)
	if err == nil && (result.Thread.ID != id || result.Thread.Path == "" || result.Thread.Ephemeral) {
		err = errors.New("the Codex app-server returned a different or nonpersistent thread")
	}
	return result.Thread, err
}

func readCodexNativeHistory(ctx context.Context, client *codexImportRPCClient, id string) (codexNativeHistory, error) {
	var history codexNativeHistory
	hash := sha256.New()
	cursor := ""
	seenTurns := map[string]bool{}
	for {
		params := map[string]any{"threadId": id, "itemsView": "full", "sortDirection": "asc", "limit": 12}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data       []json.RawMessage `json:"data"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := client.call(ctx, "thread/turns/list", params, &page); err != nil {
			return codexNativeHistory{}, err
		}
		for _, raw := range page.Data {
			var turn struct {
				ID    string `json:"id"`
				Items []struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Content any    `json:"content"`
				} `json:"items"`
			}
			if json.Unmarshal(raw, &turn) != nil || turn.ID == "" || seenTurns[turn.ID] {
				return codexNativeHistory{}, errors.New("the Codex app-server returned ambiguous native turns")
			}
			seenTurns[turn.ID] = true
			history.turnIDs = append(history.turnIDs, turn.ID)
			history.starts = append(history.starts, len(history.conversation))
			_, _ = hash.Write(raw)
			for _, item := range turn.Items {
				var role, text string
				switch item.Type {
				case "userMessage":
					role, text = "user", textFromContent(item.Content)
				case "agentMessage":
					role, text = "assistant", item.Text
				}
				text = cleanTranscriptText(text)
				if keepTranscriptTurn(role, text) {
					history.conversation = append(history.conversation, Turn{Role: role, Text: text})
				}
			}
		}
		if page.NextCursor == "" {
			copy(history.digest[:], hash.Sum(nil))
			return history, nil
		}
		if page.NextCursor == cursor || len(page.Data) == 0 {
			return codexNativeHistory{}, errors.New("native Codex turn pagination did not advance")
		}
		cursor = page.NextCursor
	}
}

func readCodexTrimFile(path string) ([]byte, trimRevision, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, trimRevision{}, err
	}
	defer func() { _ = file.Close() }()
	return readTrimSnapshot(path, file)
}

func planCodexTrim(row Row, path string, options HandoffOptions) (TrimPlan, error) {
	if options.ThroughUser > 0 {
		return TrimPlan{}, errors.New("use --before-user to trim whole native Codex turns")
	}
	if options.BeforeUser == 1 {
		return TrimPlan{}, errors.New("keep at least one user message in the session")
	}
	source, revision, err := readCodexTrimFile(path)
	if err != nil {
		return TrimPlan{}, err
	}
	ctx, cancel := importContext(context.Background())
	defer cancel()
	client, err := startCodexHistoryClient(ctx, existingDir(row.resumeCWD()))
	if err != nil {
		return TrimPlan{}, err
	}
	defer client.close()
	thread, err := readCodexNativeThread(ctx, client, row.ID)
	if err != nil {
		return TrimPlan{}, err
	}
	if thread.HistoryMode != "paginated" {
		return TrimPlan{}, errors.New("trimming requires Codex thread/revert support and a session with paginated history")
	}
	if !sameImportPath(thread.Path, path) || thread.Status.Type == "active" {
		return TrimPlan{}, ErrTrimChanged
	}
	history, err := readCodexNativeHistory(ctx, client, row.ID)
	if err != nil {
		return TrimPlan{}, err
	}
	plan, err := codexTrimBoundary(row, options, history)
	if err != nil {
		return TrimPlan{}, err
	}
	if !codexIsRevisionPath(path) {
		local, err := readCodexTranscript(bytes.NewReader(source))
		if err != nil {
			return TrimPlan{}, err
		}
		// Showagent numbers user messages from the rollout. Native projection
		// must agree exactly before using that number as a whole-turn cutoff.
		if !slices.Equal(local, history.conversation) {
			return TrimPlan{}, errors.New("native Codex turns do not match this rollout's numbered messages")
		}
	}
	if stamp, ok := stampFile(path); !ok || stamp != revision.stamp {
		return TrimPlan{}, ErrTrimChanged
	}
	plan.revision = revision
	return plan, nil
}

func codexTrimBoundary(row Row, options HandoffOptions, history codexNativeHistory) (TrimPlan, error) {
	var turns recordedTrimTurns
	for index, turn := range history.conversation {
		turns.add(turn.Role, turn.Text, index, index+1)
	}
	cut, result, err := turns.cut(options)
	if err != nil {
		return TrimPlan{}, err
	}
	// A native turn may include earlier chat items. Removing a whole such
	// turn would delete more than the selected boundary describes.
	index := slices.Index(history.starts, cut)
	if index <= 0 || (index+1 < len(history.starts) && history.starts[index+1] == cut) {
		return TrimPlan{}, errors.New("this user message does not start an unambiguous native Codex turn")
	}
	return TrimPlan{
		Row: row, Options: options, Boundary: newUserMessage(options.BeforeUser, result.keptTurns+1, result.boundaryText),
		KeptUserMessages: result.keptUsers, RemovedUserMessages: result.removedUsers,
		KeptTurns: result.keptTurns, RemovedTurns: result.removedTurns,
		target: row, options: options,
		codex: codexTrimSnapshot{beforeTurn: history.turnIDs[index], history: history.digest},
	}, nil
}

func applyCodexTrim(plan TrimPlan, path string) (Row, error) {
	row := plan.target
	release := acquireCodexImportPathLock(path)
	defer release()
	_, revision, err := readCodexTrimFile(path)
	if err != nil || revision != plan.revision {
		return Row{}, errors.Join(ErrTrimChanged, err)
	}
	ctx, cancel := importContext(context.Background())
	defer cancel()
	cwd, err := launchDir(row.resumeCWD())
	if err != nil {
		return Row{}, err
	}
	client, err := startCodexHistoryClient(ctx, cwd)
	if err != nil {
		return Row{}, err
	}
	defer client.close()

	thread, err := readCodexNativeThread(ctx, client, row.ID)
	if err != nil {
		return Row{}, err
	}
	if !sameImportPath(thread.Path, path) || thread.HistoryMode != "paginated" || thread.Status.Type == "active" {
		return Row{}, ErrTrimChanged
	}
	var resumed codexNativeResult
	if stamp, ok := stampFile(path); !ok || stamp != revision.stamp {
		return Row{}, ErrTrimChanged
	}
	if err := client.call(ctx, "thread/resume", map[string]any{"threadId": row.ID, "excludeTurns": true}, &resumed); err != nil {
		return Row{}, fmt.Errorf("cannot acquire the native Codex session; close it elsewhere first: %w", err)
	}
	if resumed.Thread.ID != row.ID || !sameImportPath(resumed.Thread.Path, path) || resumed.Thread.Status.Type == "active" {
		return Row{}, ErrTrimChanged
	}
	history, err := readCodexNativeHistory(ctx, client, row.ID)
	if err != nil || history.digest != plan.codex.history {
		return Row{}, errors.Join(ErrTrimChanged, err)
	}
	var reverted codexNativeResult
	if err := client.call(ctx, "thread/revert", map[string]string{"threadId": row.ID, "beforeTurnId": plan.codex.beforeTurn}, &reverted); err != nil {
		return Row{}, err
	}
	retained, err := readCodexNativeHistory(ctx, client, row.ID)
	if err != nil {
		return Row{}, fmt.Errorf("the Codex thread was reverted, but its retained history could not be read; rescan before retrying: %w", err)
	}
	cut := slices.Index(history.turnIDs, plan.codex.beforeTurn)
	if cut < 1 || reverted.Thread.ID != row.ID || !slices.Equal(retained.turnIDs, history.turnIDs[:cut]) ||
		!slices.Equal(retained.conversation, history.conversation[:history.starts[cut]]) {
		return Row{}, errors.New("the Codex thread was reverted, but the retained history could not be verified; rescan before retrying")
	}
	updated, err := rowFromCodexRevision(row, reverted.Thread, retained.conversation)
	if err != nil {
		return Row{}, fmt.Errorf("the Codex thread was reverted, but its updated rollout could not be resolved: %w", err)
	}
	removeDiscoveryPreview(row, codexProvider{})
	return updated, nil
}

func rowFromCodexRevision(base Row, thread codexNativeThread, turns []Turn) (Row, error) {
	base.File = codexRevisionFile(thread.Path)
	adapter, _ := sessionTrimAdapter(base)
	if strings.HasSuffix(base.File, ".jsonl.zst") {
		acceptPlain := adapter.acceptPath
		adapter.acceptPath = func(relative string) bool { return acceptPlain(strings.TrimSuffix(relative, ".zst")) }
	}
	path, err := trimPath(base, adapter)
	if err != nil || codexRevisionThreadID(path) != base.ID {
		return Row{}, errors.Join(errors.New("the Codex app-server did not return a local native revision rollout"), err)
	}
	updated, ok := codexRevisionRow(path)
	if !ok || updated.ID != base.ID {
		return Row{}, errors.New("the Codex app-server returned a revision without the original session identity")
	}
	updated.FirstUser, updated.LastUser = userPreviewFromTurns(turns)
	if thread.CWD != "" {
		updated.CWD, updated.LaunchCWD = normalizeImportPath(thread.CWD), normalizeImportPath(thread.CWD)
	}
	return updated, nil
}

func codexIsRevisionPath(path string) bool {
	return codexRevisionThreadID(path) != ""
}

func codexRevisionThreadID(path string) string {
	stem, valid := strings.CutSuffix(strings.TrimSuffix(filepath.Base(path), ".zst"), ".jsonl")
	base, revision, ok := strings.Cut(stem, "_")
	if !valid || !ok || !strings.HasPrefix(base, "rollout-") || len(revision) != 36 || !sessionIDPattern.MatchString(revision) {
		return ""
	}
	matched := sessionIDPattern.FindStringSubmatch(base + ".jsonl")
	if len(matched) != 2 {
		return ""
	}
	return matched[1]
}

// The native API can return a logical .jsonl path after the rollout is
// compressed. Follow Codex's preference for a plain file when both exist.
func codexRevisionFile(path string) string {
	path = normalizeImportPath(path)
	if !codexIsRevisionPath(path) {
		return path
	}
	plain := strings.TrimSuffix(path, ".zst")
	if _, err := os.Stat(plain); !errors.Is(err, os.ErrNotExist) {
		return plain
	}
	compressed := plain + ".zst"
	if info, err := os.Stat(compressed); err == nil && info.Mode().IsRegular() {
		return normalizeImportPath(compressed)
	}
	return path
}

func codexRevisionRow(path string) (Row, bool) {
	if !strings.HasSuffix(path, ".jsonl.zst") {
		return parseCodex(path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Row{}, false
	}
	return Row{Provider: ProviderCodex, ID: codexRevisionThreadID(path), File: path, LastAt: info.ModTime(), CWD: "(unknown cwd)"}, true
}

func codexRevisionTranscript(row Row) ([]Turn, error) {
	ctx, cancel := importContext(context.Background())
	defer cancel()
	client, err := startCodexHistoryClient(ctx, existingDir(row.resumeCWD()))
	if err != nil {
		return nil, err
	}
	defer client.close()
	thread, err := readCodexNativeThread(ctx, client, row.ID)
	if err != nil {
		return nil, err
	}
	if !sameImportPath(codexRevisionFile(thread.Path), row.File) {
		return nil, ErrTrimChanged
	}
	history, err := readCodexNativeHistory(ctx, client, row.ID)
	return history.conversation, err
}

// Native revert keeps the old rollout to supply retained offsets to the new
// revision. A directory scan must expose only the current thread identity.
func discoverCodexRevisions(parent context.Context, root string) []Row {
	paths := sessionPaths(root, func(path string) bool { return filepath.Ext(path) == ".jsonl" || codexIsRevisionPath(path) })
	revisions := make(map[string]string)
	for _, path := range paths {
		if id := codexRevisionThreadID(path); id != "" && path > revisions[id] {
			revisions[id] = path
		}
	}
	ordinary := paths[:0]
	for _, path := range paths {
		if codexIsRevisionPath(path) || revisions[sessionIDFromPath(path)] != "" {
			continue
		}
		ordinary = append(ordinary, path)
	}
	active := parseRowsCached(discoveryCacheKey("codex", root), ordinary, parseCodex)
	if len(revisions) == 0 {
		return active
	}
	key := discoveryCacheKey("codex:revisions", root)
	var previous map[string]cachedDiscoveryRow
	_ = readDiscoveryCache(key, &previous)
	updated := make(map[string]cachedDiscoveryRow, len(revisions))

	ctx, cancel := importContext(parent)
	defer cancel()
	client, err := startCodexHistoryClient(ctx, "")
	if err == nil {
		defer client.close()
	}
	for id, fallback := range revisions {
		if parent.Err() != nil {
			return active
		}
		// On a missing or failed native reader, keep the revision visible by
		// ID without displaying stale messages from any retired rollout.
		if client != nil {
			if thread, err := readCodexNativeThread(ctx, client, id); err == nil {
				if entry, valid := cachedCodexRevision(ctx, client, id, thread, previous); valid {
					active = append(active, entry.Row)
					if entry.Valid {
						updated[entry.Row.File] = entry
					}
					continue
				}
			}
		}
		if row, ok := codexRevisionRow(codexRevisionFile(fallback)); ok && row.ID == id {
			row.FirstUser, row.LastUser = "", ""
			active = append(active, row)
		}
	}
	if parent.Err() == nil && !maps.Equal(previous, updated) {
		writeDiscoveryCache(key, updated)
	}
	return active
}

func cachedCodexRevision(ctx context.Context, client *codexImportRPCClient, id string, thread codexNativeThread, previous map[string]cachedDiscoveryRow) (cachedDiscoveryRow, bool) {
	path := codexRevisionFile(thread.Path)
	stamp, exists := stampFile(path)
	if entry, ok := previous[path]; exists && stamp.Identity != "" && ok && entry.Valid && entry.Stamp == stamp &&
		entry.Row.File == path && entry.Row.ID == id && entry.Row.Provider == ProviderCodex {
		return entry, true
	}
	history, err := readCodexNativeHistory(ctx, client, id)
	if err != nil {
		return cachedDiscoveryRow{}, false
	}
	row, err := rowFromCodexRevision(Row{Provider: ProviderCodex, ID: id}, thread, history.conversation)
	if err != nil {
		return cachedDiscoveryRow{}, false
	}
	end, ok := stampFile(path)
	return cachedDiscoveryRow{Row: row, Stamp: stamp, Valid: exists && ok && stamp.Identity != "" && end == stamp}, true
}
