package importer

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

var shareTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,160}$`)

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// ValidateShareURL accepts only public conversation URL shapes that have a
// dedicated parser. In particular, normal ChatGPT /c and scheduled-task /s
// URLs are not share pages.
func ValidateShareURL(rawURL string) (SourceKind, error) {
	kind, _, err := validateShareURL(rawURL)
	return kind, err
}

func validateShareURL(rawURL string) (SourceKind, *url.URL, error) {
	if rawURL == "" || strings.TrimSpace(rawURL) != rawURL {
		return "", nil, ErrUnsupportedURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" {
		return "", nil, fmt.Errorf("%w: malformed URL", ErrUnsupportedURL)
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" {
		return "", nil, fmt.Errorf("%w: HTTPS without user information is required", ErrUnsupportedURL)
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", nil, fmt.Errorf("%w: query strings and fragments are not accepted", ErrUnsupportedURL)
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return "", nil, fmt.Errorf("%w: nonstandard ports are not accepted", ErrUnsupportedURL)
	}
	if parsed.RawPath != "" || strings.Contains(parsed.EscapedPath(), "%") {
		return "", nil, fmt.Errorf("%w: encoded paths are not accepted", ErrUnsupportedURL)
	}

	host := strings.ToLower(parsed.Hostname())
	var kind SourceKind
	switch host {
	case "chatgpt.com":
		kind = SourceChatGPTShare
	case "claude.ai":
		kind = SourceClaudeShare
	default:
		return "", nil, fmt.Errorf("%w: host %q is not allowed", ErrUnsupportedURL, host)
	}
	if host != parsed.Hostname() && !strings.EqualFold(host, parsed.Hostname()) {
		return "", nil, ErrUnsupportedURL
	}

	const prefix = "/share/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return "", nil, fmt.Errorf("%w: path is not a public conversation share", ErrUnsupportedURL)
	}
	token := strings.TrimPrefix(parsed.Path, prefix)
	if !shareTokenPattern.MatchString(token) {
		return "", nil, fmt.Errorf("%w: invalid share token", ErrUnsupportedURL)
	}
	return kind, parsed, nil
}

func validatePublicIPs(addresses []netip.Addr) error {
	if len(addresses) == 0 {
		return fmt.Errorf("%w: DNS returned no addresses", ErrUnsafeAddress)
	}
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() || address.IsMulticast() {
			return fmt.Errorf("%w: %s", ErrUnsafeAddress, address)
		}
		for _, prefix := range nonPublicPrefixes {
			if prefix.Contains(address) {
				return fmt.Errorf("%w: %s", ErrUnsafeAddress, address)
			}
		}
	}
	return nil
}
