package importer

import (
	"errors"
	"net/netip"
	"testing"
)

func TestValidateShareURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		kind SourceKind
		ok   bool
	}{
		{"chatgpt", "https://chatgpt.com/share/abcDEF_123-xyz", SourceChatGPTShare, true},
		{"chatgpt standard port", "https://chatgpt.com:443/share/12345678", SourceChatGPTShare, true},
		{"claude public", "https://claude.ai/share/01234567-89ab-cdef-0123-456789abcdef", SourceClaudeShare, true},
		{"http", "http://chatgpt.com/share/12345678", "", false},
		{"chatgpt private", "https://chatgpt.com/c/12345678", "", false},
		{"chatgpt scheduled task", "https://chatgpt.com/s/12345678", "", false},
		{"lookalike", "https://chatgpt.com.example/share/12345678", "", false},
		{"userinfo", "https://chatgpt.com@evil.example/share/12345678", "", false},
		{"nonstandard port", "https://chatgpt.com:8443/share/12345678", "", false},
		{"extra path", "https://chatgpt.com/share/12345678/extra", "", false},
		{"encoded slash", "https://chatgpt.com/share/12345678%2Fevil", "", false},
		{"query", "https://chatgpt.com/share/12345678?next=https://evil.example", "", false},
		{"claude invite", "https://claude.ai/chat/01234567-89ab-cdef-0123-456789abcdef", "", false},
		{"short token", "https://claude.ai/share/short", "", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kind, _, err := validateShareURL(test.url)
			if test.ok {
				if err != nil || kind != test.kind {
					t.Fatalf("validateShareURL() = %q, %v; want %q, nil", kind, err, test.kind)
				}
				return
			}
			if !errors.Is(err, ErrUnsupportedURL) {
				t.Fatalf("error = %v, want ErrUnsupportedURL", err)
			}
		})
	}
}

func TestRejectNonPublicAddresses(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1",
		"169.254.1.1", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1",
	}
	for _, raw := range blocked {
		t.Run(raw, func(t *testing.T) {
			if err := validatePublicIPs([]netip.Addr{netip.MustParseAddr(raw)}); !errors.Is(err, ErrUnsafeAddress) {
				t.Fatalf("error = %v, want ErrUnsafeAddress", err)
			}
		})
	}
	if err := validatePublicIPs([]netip.Addr{netip.MustParseAddr("93.184.216.34")}); err != nil {
		t.Fatalf("public address rejected: %v", err)
	}
	if err := validatePublicIPs([]netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")}); !errors.Is(err, ErrUnsafeAddress) {
		t.Fatalf("mixed DNS answer error = %v, want ErrUnsafeAddress", err)
	}
}
