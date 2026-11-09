package session

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	cache, err := os.MkdirTemp("", "showagent-session-cache-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("XDG_CACHE_HOME", cache)
	code := m.Run()
	_ = os.RemoveAll(cache)
	os.Exit(code)
}

func TestDiscoveryCacheReusesUnchangedFilesAndRefreshesEdits(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	writeFile(t, path, "first")
	var calls atomic.Int32
	parse := func(path string) (Row, bool) {
		calls.Add(1)
		data, err := os.ReadFile(path)
		return Row{Provider: ProviderCodex, ID: string(data), File: path}, err == nil
	}
	for range 2 {
		rows := parseRowsCached(root, []string{path}, parse)
		if len(rows) != 1 || rows[0].ID != "first" {
			t.Fatalf("discovery lost an unchanged session: %#v", rows)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("unchanged session was reparsed %d times, want 1", got)
	}

	// Same-length rewrites must still invalidate the saved summary.
	writeFile(t, path, "other")
	at := time.Now().Add(time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	rows := parseRowsCached(root, []string{path}, parse)
	if len(rows) != 1 || rows[0].ID != "other" || calls.Load() != 2 {
		t.Fatalf("edited session stayed stale: rows=%#v calls=%d", rows, calls.Load())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if rows := parseRowsCached(root, []string{path}, parse); len(rows) != 0 {
		t.Fatalf("deleted session reappeared from the cache: %#v", rows)
	}
}

func TestOpenCodeDiscoveryCachesIdleDatabaseAndRefreshesWAL(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	home := opencodeFixtureHome(t)
	withFakeOpenCode(t, home)
	fixture := `[{"id":"ses_cached","directory":"/work/cache","created":1,"updated":2,"first_user":"hello","last_user":"first"}]`
	writeFile(t, filepath.Join(home, "sessions.json"), fixture)
	provider := opencodeProvider{}
	for range 2 {
		if rows := provider.Discover(); len(rows) != 1 || rows[0].LastUser != "first" {
			t.Fatalf("idle database listing changed: %#v", rows)
		}
	}
	log, err := os.ReadFile(filepath.Join(home, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(log), "db --format json"); got != 1 {
		t.Fatalf("idle database launched its CLI %d times, want 1", got)
	}
	writeFile(t, filepath.Join(home, "sessions.json"), strings.Replace(fixture, `"last_user":"first"`, `"last_user":"fresh"`, 1))
	writeFile(t, filepath.Join(home, "opencode.db-wal"), "new committed message")
	if rows := provider.Discover(); len(rows) != 1 || rows[0].LastUser != "fresh" {
		t.Fatalf("WAL update did not invalidate database cache: %#v", rows)
	}
	if err := os.Remove(filepath.Join(home, "opencode.db")); err != nil {
		t.Fatal(err)
	}
	if rows := provider.Discover(); len(rows) != 0 {
		t.Fatalf("removed database reappeared from the cache: %#v", rows)
	}
}

func TestDiscoveryCacheRecoversFromCorruptionAndUnavailableDirectory(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, codexSourceFixture)
	key := "cache-recovery"
	for _, corrupt := range []string{"{broken", `{"missing":{"Valid":true}},oops`} {
		writeFile(t, discoveryCachePath(key), corrupt)
		if rows := parseRowsCached(key, []string{path}, parseCodex); len(rows) != 1 {
			t.Fatalf("a corrupt derived cache hid the source session: %#v", rows)
		}
	}
	info, err := os.Stat(discoveryCachePath(key))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache containing session previews must be private: %v %v", info, err)
	}
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	writeFile(t, blocked, "blocked")
	t.Setenv("XDG_CACHE_HOME", blocked)
	if rows := parseRowsCached(key, []string{path}, parseCodex); len(rows) != 1 {
		t.Fatalf("an unwritable cache hid the source session: %#v", rows)
	}
}

func TestDiscoveryDoesNotCacheAFileThatChangesDuringParsing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, "first")
	var calls atomic.Int32
	parse := func(path string) (Row, bool) {
		data, err := os.ReadFile(path)
		if calls.Add(1) == 1 {
			writeFile(t, path, "updated")
		}
		return Row{ID: string(data), File: path}, err == nil
	}
	parseRowsCached(path, []string{path}, parse)
	rows := parseRowsCached(path, []string{path}, parse)
	if len(rows) != 1 || rows[0].ID != "updated" || calls.Load() != 2 {
		t.Fatalf("a concurrent write was cached under the wrong fingerprint: rows=%#v calls=%d", rows, calls.Load())
	}
}

func TestCachedMiMoCopiesRecheckOriginalAndRedactSavedPreviews(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	home := t.TempDir()
	t.Setenv("MIMOCODE_HOME", home)
	writeFile(t, filepath.Join(home, "data", "mimocode.db"), "fixture")
	original := filepath.Join(home, "original.jsonl")
	writeFile(t, original, "original")
	withFakeOpenCode(t, home)
	bin := os.Getenv("PATH")
	if err := os.Symlink(filepath.Join(bin, "opencode"), filepath.Join(bin, "mimo")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "sessions.json"), `[{"id":"ses_copy","created":1,"updated":2,"title":"password hidden-title","first_user":"password hidden-first","last_user":"password hidden-last","import_source":"`+original+`"}]`)
	provider := opencodeProvider{mimo: true}
	if rows := provider.Discover(); len(rows) != 0 {
		t.Fatalf("MiMo copy was shown alongside its original: %#v", rows)
	}
	cache, err := os.ReadFile(discoveryCachePath(discoveryCacheKey("mimocode:db", provider.Home())))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hidden-title", "hidden-first", "hidden-last"} {
		if strings.Contains(string(cache), secret) {
			t.Fatal("database cache retained a raw secret in a preview")
		}
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if rows := provider.Discover(); len(rows) != 1 || rows[0].ID != "ses_copy" {
		t.Fatalf("cached MiMo copy disappeared after its original was removed: %#v", rows)
	}
	log, _ := os.ReadFile(filepath.Join(home, "calls.log"))
	if strings.Count(string(log), "db --format json") != 1 {
		t.Fatal("checking an original file reran the database CLI")
	}
}

type controlledDiscoveryProvider struct {
	ProviderImpl
	name  Provider
	ready <-chan struct{}
}

func (p controlledDiscoveryProvider) Name() Provider { return p.name }
func (p controlledDiscoveryProvider) Discover() []Row {
	<-p.ready
	return []Row{{Provider: p.name, ID: "ready"}}
}

func TestDiscoveryEmitsReadyProviderWithoutWaitingForSlowProvider(t *testing.T) {
	fast, slow := make(chan struct{}), make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(slow) }) })
	close(fast)
	results := discoverProviders([]ProviderImpl{
		controlledDiscoveryProvider{name: ProviderCodex, ready: slow},
		controlledDiscoveryProvider{name: ProviderClaude, ready: fast},
	})
	select {
	case result := <-results:
		if result.Provider != ProviderClaude {
			t.Fatalf("first completed provider = %s, want claude", result.Provider)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ready provider was held behind an unfinished provider")
	}
	release.Do(func() { close(slow) })
	if result := <-results; result.Provider != ProviderCodex {
		t.Fatalf("late provider was lost: %#v", result)
	}
	if _, ok := <-results; ok {
		t.Fatal("discovery stream did not close after all providers completed")
	}
}

func TestClaudeDiscoveryRefreshesFallbackWhenWorkspaceAvailabilityChanges(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	first, fallback := filepath.Join(root, "first"), filepath.Join(root, "fallback")
	if err := os.Mkdir(fallback, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "projects", "-a-lossy-old-project-slug", "session.jsonl")
	writeFile(t, path, `{"type":"user","sessionId":"session","cwd":"`+first+`","timestamp":"2026-06-01T09:00:00Z","message":{"role":"user","content":"first"}}
{"type":"user","sessionId":"session","cwd":"`+fallback+`","timestamp":"2026-06-01T10:00:00Z","message":{"role":"user","content":"latest"}}
{"type":"assistant","sessionId":"session","cwd":"`+filepath.Join(root, "unavailable-tail")+`","timestamp":"2026-06-01T11:00:00Z","message":{"role":"assistant","content":"done"}}`)
	rows := parseRowsCached(root, []string{path}, parseClaude)
	if len(rows) != 1 || rows[0].LaunchCWD != fallback {
		t.Fatalf("missing workspace did not use an existing fallback: %#v", rows)
	}
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	rows = parseRowsCached(root, []string{path}, parseClaude)
	if len(rows) != 1 || rows[0].LaunchCWD != first {
		t.Fatalf("cached fallback ignored a restored workspace: %#v", rows)
	}
}

func BenchmarkCachedDiscoveryLargeHistory(b *testing.B) {
	b.Setenv("XDG_CACHE_HOME", b.TempDir())
	root := b.TempDir()
	path := filepath.Join(root, "session.jsonl")
	padding := `{"type":"event_msg","payload":{"type":"token_count","padding":"` + strings.Repeat("x", 4096) + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(codexSourceFixture+strings.Repeat(padding, 2048)), 0o600); err != nil {
		b.Fatal(err)
	}
	parseRowsCached(root, []string{path}, parseCodex)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rows := parseRowsCached(root, []string{path}, parseCodex)
		if len(rows) != 1 || rows[0].LastUser != "build the feature" {
			b.Fatal("cached discovery lost the last user message")
		}
	}
}
