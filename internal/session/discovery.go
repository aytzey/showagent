package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// ProviderSessions contains one completed provider scan.
type ProviderSessions struct {
	Provider Provider
	Rows     []Row
}

// DiscoverProviders emits each provider as it finishes.
func DiscoverProviders() <-chan ProviderSessions {
	return discoverProviders(registry)
}

// Discovery owns a provider scan and its read-only CLI processes.
type Discovery struct {
	Results <-chan ProviderSessions
	cancel  context.CancelFunc
	native  sync.WaitGroup
}

// StartDiscovery starts a scan that can be stopped before the picker resumes
// another agent or exits. Stop waits for native CLI cleanup, not local parsers.
func StartDiscovery() *Discovery { return startDiscovery(registry) }

func (d *Discovery) Stop() {
	if d != nil {
		d.cancel()
		d.native.Wait()
	}
}

type contextualDiscoverer interface {
	DiscoverContext(context.Context) []Row
}

func discoverProviders(providers []ProviderImpl) <-chan ProviderSessions {
	return startDiscovery(providers).Results
}

func startDiscovery(providers []ProviderImpl) *Discovery {
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan ProviderSessions, len(providers))
	discovery := &Discovery{Results: results, cancel: cancel}
	var wg sync.WaitGroup
	for _, impl := range providers {
		native, cancellable := impl.(contextualDiscoverer)
		if cancellable {
			discovery.native.Add(1)
		}
		wg.Go(func() {
			var rows []Row
			if cancellable {
				defer discovery.native.Done()
				rows = native.DiscoverContext(ctx)
			} else {
				rows = impl.Discover()
			}
			select {
			case results <- ProviderSessions{Provider: impl.Name(), Rows: rows}:
			case <-ctx.Done():
			}
		})
	}
	go func() {
		wg.Wait()
		cancel()
		close(results)
	}()
	return discovery
}

type discoveryStamp struct {
	Size     int64
	Modified int64
	Mode     os.FileMode
	Identity string
}

func stampFile(path string) (discoveryStamp, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return discoveryStamp{}, false
	}
	return discoveryStamp{Size: info.Size(), Modified: info.ModTime().UnixNano(), Mode: info.Mode(), Identity: discoveryFileIdentity(path, info)}, true
}

type cachedDiscoveryRow struct {
	Stamp discoveryStamp
	Row   Row
	Valid bool
}

func parseRowsCached(key string, paths []string, parse func(string) (Row, bool)) []Row {
	var previous map[string]cachedDiscoveryRow
	if !readDiscoveryCache(key, &previous) {
		previous = nil
	}
	next := make(map[string]cachedDiscoveryRow, len(paths))
	changed := len(previous) != len(paths)
	var mu sync.Mutex
	rows := parseRowsBounded(paths, func(path string) (Row, bool) {
		before, exists := stampFile(path)
		if !exists {
			mu.Lock()
			changed = true
			mu.Unlock()
			return Row{}, false
		}
		entry, hit := previous[path]
		// Claude's fallback launch directory depends on which old workspaces
		// still exist, so only its directly observed project match is reusable.
		if hit && entry.Valid && entry.Row.Provider == ProviderClaude && claudeProjectDir(entry.Row.LaunchCWD) != filepath.Base(filepath.Dir(path)) {
			hit = false
		}
		if !hit || before.Identity == "" || entry.Stamp != before || (entry.Valid && entry.Row.File != path) {
			entry.Row, entry.Valid = parse(path)
			entry.Stamp = before
			hit = false
		}
		after, exists := stampFile(path)
		mu.Lock()
		if exists && before.Identity != "" && before == after {
			next[path] = entry
		}
		changed = changed || !hit || !exists || before != after
		mu.Unlock()
		return entry.Row, entry.Valid
	})
	if changed {
		for path, entry := range next {
			if stamp, exists := stampFile(path); !exists || stamp != entry.Stamp {
				delete(next, path)
			}
		}
		writeDiscoveryCache(key, next)
	}
	return rows
}

const discoveryCacheVersion = 3
const maxDiscoveryCacheBytes = 64 << 20

var discoveryCacheDir = os.UserCacheDir

func discoveryCachePath(key string) string {
	root, err := discoveryCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "showagent", fmt.Sprintf("discovery-v%d", discoveryCacheVersion), fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
}

func discoveryCacheKey(provider, root string) string {
	abs, _ := filepath.Abs(root)
	resolved, _ := filepath.EvalSymlinks(abs)
	return provider + ":" + abs + ":" + resolved
}

func readDiscoveryCache(key string, value any) bool {
	file, err := os.Open(discoveryCachePath(key))
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxDiscoveryCacheBytes || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return false
	}
	var envelope struct {
		Version int
		Data    json.RawMessage
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxDiscoveryCacheBytes+1))
	if decoder.Decode(&envelope) != nil || envelope.Version != discoveryCacheVersion {
		return false
	}
	var extra any
	return decoder.Decode(&extra) == io.EOF && json.Unmarshal(envelope.Data, value) == nil
}

func writeDiscoveryCache(key string, value any) {
	path := discoveryCachePath(key)
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = writeFileAtomic(path, func(file *os.File) error {
		return json.NewEncoder(file).Encode(struct {
			Version int
			Data    any
		}{Version: discoveryCacheVersion, Data: value})
	})
}

func removeDiscoveryPreview(row Row, impl ProviderImpl) {
	var roots []string
	switch row.Provider {
	case ProviderCodex:
		for _, target := range codexScanTargets(impl.Home()) {
			roots = append(roots, target.Path)
		}
	case ProviderClaude:
		roots = []string{impl.Home()}
	case ProviderOpenCode, ProviderMiMo:
		key := discoveryCacheKey(string(row.Provider)+":db", impl.Home())
		var cached opencodeDiscoveryCache
		if !readDiscoveryCache(key, &cached) {
			_ = os.Remove(discoveryCachePath(key))
			return
		}
		kept := cached.Entries[:0]
		for _, entry := range cached.Entries {
			if entry.ID != row.ID {
				kept = append(kept, entry)
			}
		}
		cached.Entries = kept
		writeDiscoveryCache(key, cached)
		return
	}
	for _, root := range roots {
		key := discoveryCacheKey(string(row.Provider), root)
		var cached map[string]cachedDiscoveryRow
		if !readDiscoveryCache(key, &cached) {
			_ = os.Remove(discoveryCachePath(key))
			continue
		}
		if _, exists := cached[row.File]; exists {
			delete(cached, row.File)
			writeDiscoveryCache(key, cached)
		}
	}
}
