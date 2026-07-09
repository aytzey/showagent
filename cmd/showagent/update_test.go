package main

import (
	"crypto/sha256"
	"fmt"
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
