package session

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// ProviderSessions contains one completed provider scan.
type ProviderSessions struct {
	Provider Provider
	Rows     []Row
}

// DiscoverProviders emits each provider as it finishes. The buffer also lets
// discovery finish when a picker exits before consuming every result.
func DiscoverProviders() <-chan ProviderSessions {
	return discoverProviders(registry)
}

func discoverProviders(providers []ProviderImpl) <-chan ProviderSessions {
	results := make(chan ProviderSessions, len(providers))
	var wg sync.WaitGroup
	for _, impl := range providers {
		wg.Go(func() {
			results <- ProviderSessions{Provider: impl.Name(), Rows: impl.Discover()}
		})
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	return results
}

type discoveryStamp struct {
	Size     int64
	Modified int64
	Mode     os.FileMode
}

func stampFile(path string) (discoveryStamp, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return discoveryStamp{}, false
	}
	return discoveryStamp{Size: info.Size(), Modified: info.ModTime().UnixNano(), Mode: info.Mode()}, true
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
		if !hit || entry.Stamp != before || (entry.Valid && entry.Row.File != path) {
			entry.Row, entry.Valid = parse(path)
			entry.Stamp = before
			hit = false
		}
		after, exists := stampFile(path)
		mu.Lock()
		if exists && before == after {
			next[path] = entry
		}
		changed = changed || !hit || !exists || before != after
		mu.Unlock()
		return entry.Row, entry.Valid
	})
	if changed {
		writeDiscoveryCache(key, next)
	}
	return rows
}

func discoveryCachePath(key string) string {
	root, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "showagent", "discovery-v1", fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
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
	return json.NewDecoder(io.LimitReader(file, 64<<20)).Decode(value) == nil
}

func writeDiscoveryCache(key string, value any) {
	path := discoveryCachePath(key)
	if path == "" || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = writeFileAtomic(path, func(file *os.File) error {
		return json.NewEncoder(file).Encode(value)
	})
}
