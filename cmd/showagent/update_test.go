package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseAsset(t *testing.T) {
	savedGOOS := currentRuntimeGOOS
	savedArch := currentRuntimeArch
	defer func() {
		currentRuntimeGOOS = savedGOOS
		currentRuntimeArch = savedArch
	}()

	tests := []struct {
		goos       string
		goarch     string
		wantAsset  string
		wantBinary string
		wantErr    bool
	}{
		{"linux", "amd64", "showagent_v0.8.0_linux_amd64.tar.gz", "showagent", false},
		{"darwin", "arm64", "showagent_v0.8.0_darwin_arm64.tar.gz", "showagent", false},
		{"windows", "amd64", "showagent_v0.8.0_windows_amd64.zip", "showagent.exe", false},
		{"windows", "arm64", "", "", true},
		{"plan9", "amd64", "", "", true},
	}
	for _, tt := range tests {
		currentRuntimeGOOS = tt.goos
		currentRuntimeArch = tt.goarch
		asset, binaryName, err := releaseAsset("v0.8.0")
		if tt.wantErr {
			if err == nil {
				t.Fatalf("releaseAsset(%s/%s) err = nil, want error", tt.goos, tt.goarch)
			}
			continue
		}
		if err != nil {
			t.Fatalf("releaseAsset(%s/%s) err = %v", tt.goos, tt.goarch, err)
		}
		if asset != tt.wantAsset || binaryName != tt.wantBinary {
			t.Fatalf("releaseAsset(%s/%s) = %q, %q; want %q, %q", tt.goos, tt.goarch, asset, binaryName, tt.wantAsset, tt.wantBinary)
		}
	}
}

func TestVerifySHA256(t *testing.T) {
	payload := []byte("release archive")
	sum := sha256.Sum256(payload)
	sums := fmt.Sprintf("%x  *showagent_v0.8.0_linux_amd64.tar.gz\n", sum[:])

	if err := verifySHA256(payload, sums, "showagent_v0.8.0_linux_amd64.tar.gz"); err != nil {
		t.Fatalf("verifySHA256 err = %v, want nil", err)
	}
	if err := verifySHA256([]byte("tampered"), sums, "showagent_v0.8.0_linux_amd64.tar.gz"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered verify err = %v, want checksum mismatch", err)
	}
	if err := verifySHA256(payload, sums, "missing.tar.gz"); err == nil || !strings.Contains(err.Error(), "no SHA256SUMS entry") {
		t.Fatalf("missing verify err = %v, want missing entry", err)
	}
}

func TestDownloadBytesEnforcesDeclaredAndStreamingLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/declared":
			w.Header().Set("Content-Length", "20")
			_, _ = w.Write([]byte(strings.Repeat("x", 20)))
		case "/chunked":
			flusher, _ := w.(http.Flusher)
			for range 4 {
				_, _ = w.Write([]byte("1234"))
				if flusher != nil {
					flusher.Flush()
				}
			}
		default:
			_, _ = w.Write([]byte("small"))
		}
	}))
	defer server.Close()

	if data, err := downloadBytes(context.Background(), server.URL+"/small", 10); err != nil || string(data) != "small" {
		t.Fatalf("small download = %q, %v", data, err)
	}
	if _, err := downloadBytes(context.Background(), server.URL+"/declared", 10); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("declared oversize err = %v", err)
	}
	if _, err := downloadBytes(context.Background(), server.URL+"/chunked", 10); err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("streamed oversize err = %v", err)
	}
}

func TestExtractReleaseArchives(t *testing.T) {
	payload := []byte("binary payload")
	tests := []struct {
		name       string
		asset      string
		binaryName string
		archive    []byte
	}{
		{"tar", "showagent_v1.0.0_linux_amd64.tar.gz", "showagent", tarArchive(t, "showagent", payload, int64(len(payload)))},
		{"zip", "showagent_v1.0.0_windows_amd64.zip", "showagent.exe", zipArchive(t, "showagent.exe", payload)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), tt.binaryName)
			if err := extractReleaseBinary(tt.archive, tt.asset, tt.binaryName, destination); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("extracted = %q, want %q", got, payload)
			}
		})
	}
}

func TestExtractTarRejectsOversizedBinary(t *testing.T) {
	archive := tarArchive(t, "showagent", nil, maxBinaryBytes+1)
	err := extractTarBinary(archive, "showagent", filepath.Join(t.TempDir(), "showagent"))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized tar err = %v", err)
	}
}

func TestInstallBinaryIsExecutableAndAtomic(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "bin", "showagent")
	if err := os.WriteFile(source, []byte("new binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installBinary(source, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new binary" {
		t.Fatalf("installed content = %q", content)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("installed mode = %o, want 755", got)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".showagent-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary install files remain: %v, %v", matches, err)
	}
}

func TestManagedInstallDetection(t *testing.T) {
	tests := map[string]string{
		"/opt/homebrew/Cellar/showagent/0.9.0/bin/showagent":              "Homebrew",
		"/home/linuxbrew/.linuxbrew/Cellar/showagent/0.9.0/bin/showagent": "Homebrew",
		"/nix/store/abc-showagent/bin/showagent":                          "Nix",
		"/snap/showagent/current/showagent":                               "Snap",
		"/home/u/.local/bin/showagent":                                    "",
	}
	for path, want := range tests {
		if got := managedInstall(path); got != want {
			t.Fatalf("managedInstall(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestInstallReleaseEndToEnd(t *testing.T) {
	const tag = "v1.2.3"
	const asset = "showagent_v1.2.3_linux_amd64.tar.gz"
	payload := []byte("release binary")
	archive := tarArchive(t, "showagent", payload, int64(len(payload)))
	sum := sha256.Sum256(archive)
	sums := fmt.Sprintf("%x  %s\n", sum[:], asset)
	server := releaseServer(t, tag, asset, archive, sums)

	savedBase := releaseBaseURL
	savedGOOS := currentRuntimeGOOS
	savedArch := currentRuntimeArch
	t.Cleanup(func() {
		releaseBaseURL = savedBase
		currentRuntimeGOOS = savedGOOS
		currentRuntimeArch = savedArch
	})
	releaseBaseURL = server.URL
	currentRuntimeGOOS = "linux"
	currentRuntimeArch = "amd64"
	installDir := filepath.Join(t.TempDir(), "bin")
	t.Setenv("SHOWAGENT_INSTALL_DIR", installDir)

	var stdout, stderr bytes.Buffer
	if err := installRelease(tag, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(installDir, "showagent"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, payload) {
		t.Fatalf("installed payload = %q, want %q", installed, payload)
	}
	if !strings.Contains(stdout.String(), "Installed showagent v1.2.3") {
		t.Fatalf("stdout missing install confirmation: %s", stdout.String())
	}
}

func TestMaybePromptForUpdateInstallsAcceptedRelease(t *testing.T) {
	const tag = "v1.2.3"
	const asset = "showagent_v1.2.3_linux_amd64.tar.gz"
	payload := []byte("prompted update")
	archive := tarArchive(t, "showagent", payload, int64(len(payload)))
	sum := sha256.Sum256(archive)
	server := releaseServer(t, tag, asset, archive, fmt.Sprintf("%x  %s\n", sum[:], asset))

	savedLatest := latestReleaseURL
	savedBase := releaseBaseURL
	savedVersion := version
	savedGOOS := currentRuntimeGOOS
	savedArch := currentRuntimeArch
	t.Cleanup(func() {
		latestReleaseURL = savedLatest
		releaseBaseURL = savedBase
		version = savedVersion
		currentRuntimeGOOS = savedGOOS
		currentRuntimeArch = savedArch
	})
	latestReleaseURL = server.URL + "/latest"
	releaseBaseURL = server.URL
	version = "v1.0.0"
	currentRuntimeGOOS = "linux"
	currentRuntimeArch = "amd64"
	installDir := filepath.Join(t.TempDir(), "bin")
	t.Setenv("SHOWAGENT_INSTALL_DIR", installDir)

	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if _, err := stdin.WriteString("yes\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code, handled := maybePromptForUpdate(stdin, &stderr)
	if code != 0 || !handled {
		t.Fatalf("prompt result = code %d handled %v: %s", code, handled, stderr.String())
	}
	if content, err := os.ReadFile(filepath.Join(installDir, "showagent")); err != nil || !bytes.Equal(content, payload) {
		t.Fatalf("prompted install = %q, %v", content, err)
	}
}

func TestInstallHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHOWAGENT_INSTALL_DIR", "~/custom-bin")
	destination, err := installDestination("showagent")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "custom-bin", "showagent")
	if destination != want {
		t.Fatalf("destination = %q, want %q", destination, want)
	}
	if !dirWritable(home) {
		t.Fatal("temporary home should be writable")
	}
	t.Setenv("PATH", strings.Join([]string{"/bin", filepath.Join(home, "custom-bin")}, string(os.PathListSeparator)))
	if !pathContains(filepath.Join(home, "custom-bin")) || pathContains(filepath.Join(home, "missing")) {
		t.Fatal("PATH detection returned the wrong result")
	}
	if got := managedUpdateCommand("Homebrew"); !strings.Contains(got, "brew upgrade") {
		t.Fatalf("Homebrew update command = %q", got)
	}
	if got := managedUpdateCommand("Nix"); !strings.Contains(got, "Nix") {
		t.Fatalf("Nix update command = %q", got)
	}
	if got := managedUpdateCommand("Snap"); !strings.Contains(got, "snap refresh") {
		t.Fatalf("Snap update command = %q", got)
	}
}

func TestInstallDestinationFallsBackToUserLocalBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHOWAGENT_INSTALL_DIR", "")
	destination, err := installDestination("showagent")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "bin", "showagent"); destination != want {
		t.Fatalf("destination = %q, want %q", destination, want)
	}
}

func TestRunUpdateHandlesCurrentNetworkUsageAndInstallErrors(t *testing.T) {
	savedLatest := latestReleaseURL
	savedVersion := version
	savedGOOS := currentRuntimeGOOS
	t.Cleanup(func() {
		latestReleaseURL = savedLatest
		version = savedVersion
		currentRuntimeGOOS = savedGOOS
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
	}))
	defer server.Close()

	latestReleaseURL = server.URL + "/latest"
	version = "v1.2.3"
	var stdout, stderr bytes.Buffer
	if code := runUpdate(nil, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "up to date") {
		t.Fatalf("current update = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	latestReleaseURL = server.URL + "/error"
	stdout.Reset()
	stderr.Reset()
	if code := runUpdate([]string{"--check"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "503") {
		t.Fatalf("network failure = %d, %q", code, stderr.String())
	}

	latestReleaseURL = server.URL + "/latest"
	version = "v1.0.0"
	currentRuntimeGOOS = "plan9"
	stdout.Reset()
	stderr.Reset()
	if code := runUpdate(nil, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "unsupported OS") {
		t.Fatalf("unsupported install = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stderr.Reset()
	if code := runUpdate([]string{"--bogus"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "unknown update argument") {
		t.Fatalf("bad update argument = %d, %q", code, stderr.String())
	}
}

func TestMaybePromptForUpdateDeclinesAndSkipsDevelopmentBuilds(t *testing.T) {
	savedLatest := latestReleaseURL
	savedVersion := version
	t.Cleanup(func() {
		latestReleaseURL = savedLatest
		version = savedVersion
	})

	version = "dev"
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if code, handled := maybePromptForUpdate(stdin, &bytes.Buffer{}); code != 0 || handled {
		t.Fatalf("development prompt = %d/%v", code, handled)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v2.0.0"}`))
	}))
	defer server.Close()
	latestReleaseURL = server.URL
	version = "v1.0.0"
	if err := os.WriteFile(stdin.Name(), []byte("no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code, handled := maybePromptForUpdate(stdin, &stderr); code != 0 || handled || !strings.Contains(stderr.String(), "Install now") {
		t.Fatalf("declined prompt = %d/%v, %q", code, handled, stderr.String())
	}
}

func TestMaybePromptForUpdateCanBeDisabled(t *testing.T) {
	t.Setenv("SHOWAGENT_NO_UPDATE_CHECK", "1")
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	if code, handled := maybePromptForUpdate(stdin, &bytes.Buffer{}); code != 0 || handled {
		t.Fatalf("disabled prompt = code %d handled %v", code, handled)
	}
}

func TestFetchLatestReleaseRejectsHTTPAndEmptyPayloads(t *testing.T) {
	saved := latestReleaseURL
	t.Cleanup(func() { latestReleaseURL = saved })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			http.Error(w, "nope", http.StatusBadGateway)
			return
		}
		if r.URL.Path == "/oversized" {
			flusher, _ := w.(http.Flusher)
			_, _ = w.Write([]byte(`{"tag_name":"v1.2.3","padding":"`))
			if flusher != nil {
				flusher.Flush()
			}
			_, _ = io.WriteString(w, strings.Repeat("x", maxReleaseMetadataBytes+1))
			_, _ = w.Write([]byte(`"}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"missing tag"}`))
	}))
	defer server.Close()

	latestReleaseURL = server.URL + "/error"
	if _, err := fetchLatestRelease(context.Background()); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("HTTP error = %v", err)
	}
	latestReleaseURL = server.URL + "/empty"
	if _, err := fetchLatestRelease(context.Background()); err == nil || !strings.Contains(err.Error(), "tag_name") {
		t.Fatalf("missing tag error = %v", err)
	}
	latestReleaseURL = server.URL + "/oversized"
	if _, err := fetchLatestRelease(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("oversized metadata error = %v", err)
	}
}

func TestExtractReleaseBinaryReportsMissingEntry(t *testing.T) {
	tests := []struct {
		asset   string
		archive []byte
	}{
		{"release.tar.gz", tarArchive(t, "other", []byte("x"), 1)},
		{"release.zip", zipArchive(t, "other.exe", []byte("x"))},
	}
	for _, tt := range tests {
		err := extractReleaseBinary(tt.archive, tt.asset, "showagent", filepath.Join(t.TempDir(), "showagent"))
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("missing entry in %s = %v", tt.asset, err)
		}
	}
}

func releaseServer(t *testing.T, tag, asset string, archive []byte, sums string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, tag)
		case "/" + tag + "/" + asset:
			_, _ = w.Write(archive)
		case "/" + tag + "/SHA256SUMS":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func tarArchive(t *testing.T, name string, payload []byte, declaredSize int64) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: declaredSize, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if len(payload) > 0 {
		if _, err := tw.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	// An intentionally oversized declared size has no body. The reader rejects
	// the header before reading it, while Close would correctly complain.
	if declaredSize <= int64(len(payload)) {
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	_ = tw.Flush()
	_ = gz.Close()
	return output.Bytes()
}

func zipArchive(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	zw := zip.NewWriter(&output)
	entry, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
