package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLatestReleaseURL = "https://api.github.com/repos/aytzey/showagent/releases/latest"
	defaultReleaseBaseURL   = "https://github.com/aytzey/showagent/releases/download"
	maxReleaseMetadataBytes = 1 << 20
	maxChecksumBytes        = 1 << 20
	maxArchiveBytes         = 64 << 20
	maxBinaryBytes          = 64 << 20
)

var (
	latestReleaseURL   = defaultLatestReleaseURL
	releaseBaseURL     = defaultReleaseBaseURL
	httpClient         = &http.Client{Timeout: 30 * time.Second}
	currentRuntimeGOOS = runtime.GOOS
	currentRuntimeArch = runtime.GOARCH
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func runUpdate(args []string, stdout, stderr io.Writer) int {
	checkOnly := false
	for _, arg := range args {
		switch arg {
		case "--check":
			checkOnly = true
		default:
			return usageError(stderr, fmt.Sprintf("unknown update argument %q", arg))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	latest, err := fetchLatestRelease(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "showagent update: %v\n", err)
		return 1
	}
	current := versionString()
	if !isNewerVersion(latest, current) {
		_, _ = fmt.Fprintf(stdout, "showagent is up to date (%s)\n", current)
		return 0
	}

	_, _ = fmt.Fprintf(stdout, "showagent %s is available (current %s)\n", latest, current)
	if checkOnly {
		return 0
	}
	if err := installRelease(latest, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "showagent update: %v\n", err)
		return 1
	}
	return 0
}

func maybePromptForUpdate(stdin *os.File, stderr io.Writer) (code int, handled bool) {
	if os.Getenv("SHOWAGENT_NO_UPDATE_CHECK") != "" {
		return 0, false
	}
	current := versionString()
	if !strings.HasPrefix(current, "v") {
		return 0, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	latest, err := fetchLatestRelease(ctx)
	if err != nil || !isNewerVersion(latest, current) {
		return 0, false
	}

	_, _ = fmt.Fprintf(stderr, "showagent %s is available (current %s). Install now? [y/N] ", latest, current)
	answer, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil {
		_, _ = fmt.Fprintln(stderr)
		return 0, false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return 0, false
	}

	if err := installRelease(latest, stderr, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "showagent update: %v\n", err)
		return 1, true
	}
	_, _ = fmt.Fprintln(stderr, "showagent updated. Run showagent again to use the new version.")
	return 0, true
}

func fetchLatestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "showagent/"+versionString())
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub releases returned %s", resp.Status)
	}
	var release githubRelease
	if resp.ContentLength > maxReleaseMetadataBytes {
		return "", fmt.Errorf("latest release response is too large (%d bytes)", resp.ContentLength)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseMetadataBytes+1))
	if err != nil {
		return "", err
	}
	if len(payload) > maxReleaseMetadataBytes {
		return "", fmt.Errorf("latest release response exceeded %d bytes", maxReleaseMetadataBytes)
	}
	if err := json.Unmarshal(payload, &release); err != nil {
		return "", err
	}
	if release.TagName == "" {
		return "", errors.New("latest release response has no tag_name")
	}
	return release.TagName, nil
}

func installRelease(tag string, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	asset, binaryName, err := releaseAsset(tag)
	if err != nil {
		return err
	}
	base := strings.TrimRight(releaseBaseURL, "/") + "/" + tag
	_, _ = fmt.Fprintf(stdout, "Downloading %s (%s)...\n", asset, tag)

	archiveData, err := downloadBytes(ctx, base+"/"+asset, maxArchiveBytes)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	sumsData, err := downloadBytes(ctx, base+"/SHA256SUMS", maxChecksumBytes)
	if err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	if err := verifySHA256(archiveData, string(sumsData), asset); err != nil {
		return err
	}

	tmpdir, err := os.MkdirTemp("", "showagent-update-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpdir) }()

	extracted := filepath.Join(tmpdir, binaryName)
	if err := extractReleaseBinary(archiveData, asset, binaryName, extracted); err != nil {
		return err
	}
	target, err := installDestination(binaryName)
	if err != nil {
		return err
	}
	if err := installBinary(extracted, target); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Installed showagent %s to %s\n", tag, target)
	if !pathContains(filepath.Dir(target)) {
		_, _ = fmt.Fprintf(stderr, "Note: %s is not on your PATH\n", filepath.Dir(target))
	}
	return nil
}

func downloadBytes(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "showagent/"+versionString())
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("download is too large (%d bytes; max %d)", resp.ContentLength, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("download exceeded %d bytes", maxBytes)
	}
	return data, nil
}

func releaseAsset(tag string) (asset string, binaryName string, err error) {
	goos := currentRuntimeGOOS
	goarch := currentRuntimeArch
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", "", fmt.Errorf("unsupported architecture %s; download a release manually from https://github.com/aytzey/showagent/releases", goarch)
	}

	binaryName = "showagent"
	extension := ".tar.gz"
	switch goos {
	case "linux", "darwin":
	case "windows":
		if goarch != "amd64" {
			return "", "", fmt.Errorf("automatic update is not published for windows/%s yet", goarch)
		}
		binaryName = "showagent.exe"
		extension = ".zip"
	default:
		return "", "", fmt.Errorf("unsupported OS %s; download a release manually from https://github.com/aytzey/showagent/releases", goos)
	}
	return fmt.Sprintf("showagent_%s_%s_%s%s", tag, goos, goarch, extension), binaryName, nil
}

func verifySHA256(data []byte, sums string, asset string) error {
	expected, err := checksumForAsset(sums, asset)
	if err != nil {
		return err
	}
	actualBytes := sha256.Sum256(data)
	actual := fmt.Sprintf("%x", actualBytes[:])
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", asset, expected, actual)
	}
	return nil
}

func checksumForAsset(sums string, asset string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == asset {
			if len(fields[0]) != sha256.Size*2 {
				return "", fmt.Errorf("invalid SHA256SUMS entry for %s", asset)
			}
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no SHA256SUMS entry for %s", asset)
}

func extractReleaseBinary(archiveData []byte, asset string, binaryName string, destination string) error {
	if strings.HasSuffix(asset, ".zip") {
		return extractZipBinary(archiveData, binaryName, destination)
	}
	return extractTarBinary(archiveData, binaryName, destination)
}

func extractZipBinary(archiveData []byte, binaryName string, destination string) error {
	reader, err := zip.NewReader(bytes.NewReader(archiveData), int64(len(archiveData)))
	if err != nil {
		return err
	}
	for _, entry := range reader.File {
		if filepath.Base(entry.Name) != binaryName {
			continue
		}
		if entry.UncompressedSize64 > maxBinaryBytes {
			return fmt.Errorf("%s is too large after extraction (%d bytes)", binaryName, entry.UncompressedSize64)
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		defer func() { _ = source.Close() }()
		return writeExtractedBinary(source, destination)
	}
	return fmt.Errorf("%s not found in release archive", binaryName)
}

func extractTarBinary(archiveData []byte, binaryName string, destination string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archiveData))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != binaryName {
			continue
		}
		if header.Size < 0 || header.Size > maxBinaryBytes {
			return fmt.Errorf("%s is too large after extraction (%d bytes)", binaryName, header.Size)
		}
		return writeExtractedBinary(reader, destination)
	}
	return fmt.Errorf("%s not found in release archive", binaryName)
}

func writeExtractedBinary(source io.Reader, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(source, maxBinaryBytes+1))
	if copyErr != nil {
		_ = out.Close()
		return copyErr
	}
	if written > maxBinaryBytes {
		_ = out.Close()
		return fmt.Errorf("extracted binary exceeds %d bytes", maxBinaryBytes)
	}
	return out.Close()
}

func installDestination(binaryName string) (string, error) {
	if installDir := os.Getenv("SHOWAGENT_INSTALL_DIR"); installDir != "" {
		return filepath.Join(expandInstallHome(installDir), binaryName), nil
	}
	if executable, err := os.Executable(); err == nil && strings.EqualFold(filepath.Base(executable), binaryName) {
		if manager := managedInstall(executable); manager != "" {
			return "", fmt.Errorf("this copy is managed by %s; update it with %s", manager, managedUpdateCommand(manager))
		}
		if dirWritable(filepath.Dir(executable)) {
			return executable, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory; set SHOWAGENT_INSTALL_DIR")
	}
	return filepath.Join(home, ".local", "bin", binaryName), nil
}

func installBinary(source string, destination string) error {
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create install dir %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".showagent-*")
	if err != nil {
		return fmt.Errorf("create temp install file in %s: %w", dir, err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	input, err := os.Open(source)
	if err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := io.Copy(temp, input); err != nil {
		_ = input.Close()
		_ = temp.Close()
		return err
	}
	if err := input.Close(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceInstalledFile(tempName, destination); err != nil {
		return fmt.Errorf("install to %s: %w", destination, err)
	}
	if runtime.GOOS != "windows" {
		if directory, err := os.Open(dir); err == nil {
			_ = directory.Sync()
			_ = directory.Close()
		}
	}
	return nil
}

func managedInstall(executable string) string {
	path := strings.ToLower(filepath.ToSlash(executable))
	switch {
	case strings.Contains(path, "/cellar/showagent/") || strings.Contains(path, "/homebrew/cellar/showagent/"):
		return "Homebrew"
	case strings.HasPrefix(path, "/nix/store/"):
		return "Nix"
	case strings.HasPrefix(path, "/snap/"):
		return "Snap"
	default:
		return ""
	}
}

func managedUpdateCommand(manager string) string {
	switch manager {
	case "Homebrew":
		return "brew upgrade aytzey/tap/showagent"
	case "Nix":
		return "your Nix profile or flake"
	case "Snap":
		return "snap refresh showagent"
	default:
		return "the package manager that installed showagent"
	}
}

func dirWritable(dir string) bool {
	file, err := os.CreateTemp(dir, ".showagent-write-test-*")
	if err != nil {
		return false
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
	return true
}

func pathContains(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == dir {
			return true
		}
	}
	return false
}

func expandInstallHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func isNewerVersion(candidate, current string) bool {
	candidateParts, ok := parseVersion(candidate)
	if !ok {
		return false
	}
	currentParts, ok := parseVersion(current)
	if !ok {
		return true
	}
	for i := range candidateParts.numbers {
		if candidateParts.numbers[i] != currentParts.numbers[i] {
			return candidateParts.numbers[i] > currentParts.numbers[i]
		}
	}
	return currentParts.prerelease && !candidateParts.prerelease
}

type semanticVersion struct {
	numbers    [3]int
	prerelease bool
}

func parseVersion(value string) (semanticVersion, bool) {
	var result semanticVersion
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if cut := strings.IndexByte(value, '+'); cut >= 0 {
		value = value[:cut]
	}
	if cut := strings.IndexByte(value, '-'); cut >= 0 {
		result.prerelease = true
		value = value[:cut]
	}
	fields := strings.Split(value, ".")
	if len(fields) != len(result.numbers) {
		return result, false
	}
	for i, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return result, false
		}
		result.numbers[i] = number
	}
	return result, true
}
