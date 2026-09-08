package importer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFetchTimeout = 15 * time.Second
	defaultMaxRedirects = 3
)

type FetchOptions struct {
	MaxResponseBytes int64
	Timeout          time.Duration
	MaxRedirects     int
}

type FetchedPage struct {
	SourceKind  SourceKind
	Body        []byte
	ContentType string
}

type Fetcher struct {
	options   FetchOptions
	transport http.RoundTripper
}

func NewFetcher(options FetchOptions) *Fetcher {
	return newFetcher(options, newSafeTransport())
}

func newFetcher(options FetchOptions, transport http.RoundTripper) *Fetcher {
	if options.MaxResponseBytes <= 0 {
		options.MaxResponseBytes = MaxHTMLBytes
	} else if options.MaxResponseBytes > MaxHTMLBytes {
		options.MaxResponseBytes = MaxHTMLBytes
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultFetchTimeout
	} else if options.Timeout > defaultFetchTimeout {
		options.Timeout = defaultFetchTimeout
	}
	if options.MaxRedirects <= 0 {
		options.MaxRedirects = defaultMaxRedirects
	} else if options.MaxRedirects > defaultMaxRedirects {
		options.MaxRedirects = defaultMaxRedirects
	}
	return &Fetcher{options: options, transport: transport}
}

func FetchShare(ctx context.Context, rawURL string, options FetchOptions) (FetchedPage, error) {
	return NewFetcher(options).Fetch(ctx, rawURL)
}

func (fetcher *Fetcher) Fetch(ctx context.Context, rawURL string) (FetchedPage, error) {
	initialKind, parsedURL, err := validateShareURL(rawURL)
	if err != nil {
		return FetchedPage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, fetcher.options.Timeout)
	defer cancel()

	client := &http.Client{
		Transport: fetcher.transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > fetcher.options.MaxRedirects {
				return ErrRedirectLimit
			}
			kind, _, err := validateShareURL(request.URL.String())
			if err != nil {
				return err
			}
			if kind != initialKind {
				return fmt.Errorf("%w: cross-provider redirect", ErrUnsupportedURL)
			}
			return nil
		},
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return FetchedPage{}, fmt.Errorf("%w: %v", ErrFetch, err)
	}
	request.Header.Set("Accept", "text/html, application/xhtml+xml")
	request.Header.Set("User-Agent", "showagent conversation importer")

	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return FetchedPage{}, ctxErr
		}
		err = withoutRequestURL(err)
		if errors.Is(err, ErrUnsupportedURL) || errors.Is(err, ErrRedirectLimit) || errors.Is(err, ErrUnsafeAddress) {
			return FetchedPage{}, err
		}
		return FetchedPage{}, fmt.Errorf("%w: %w", ErrFetch, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return FetchedPage{}, fmt.Errorf("%w: %s", ErrHTTPStatus, response.Status)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return FetchedPage{}, fmt.Errorf("%w: %q", ErrUnexpectedContent, response.Header.Get("Content-Type"))
	}
	if response.ContentLength > fetcher.options.MaxResponseBytes {
		return FetchedPage{}, fmt.Errorf("%w: response exceeds %d bytes", ErrLimitExceeded, fetcher.options.MaxResponseBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, fetcher.options.MaxResponseBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return FetchedPage{}, ctxErr
		}
		return FetchedPage{}, fmt.Errorf("%w: %w", ErrFetch, err)
	}
	if int64(len(body)) > fetcher.options.MaxResponseBytes {
		return FetchedPage{}, fmt.Errorf("%w: response exceeds %d bytes", ErrLimitExceeded, fetcher.options.MaxResponseBytes)
	}
	// The initial request and every redirect target were validated before the
	// transport saw them. Keeping only the kind also avoids retaining a share
	// token in the returned value.
	return FetchedPage{SourceKind: initialKind, Body: body, ContentType: mediaType}, nil
}

func ImportURL(ctx context.Context, rawURL string, options FetchOptions) (Conversation, error) {
	return NewFetcher(options).Import(ctx, rawURL)
}

func (fetcher *Fetcher) Import(ctx context.Context, rawURL string) (Conversation, error) {
	page, pageErr := fetcher.Fetch(ctx, rawURL)
	if pageErr == nil {
		var conversation Conversation
		switch page.SourceKind {
		case SourceChatGPTShare:
			conversation, pageErr = ParseChatGPTHTML(page.Body)
		case SourceClaudeShare:
			conversation, pageErr = ParseClaudeHTML(page.Body)
		default:
			return Conversation{}, ErrUnsupportedURL
		}
		if pageErr == nil {
			return conversation, nil
		}
	}

	// Current public pages load their actual snapshot from a same-origin JSON
	// endpoint. Fetching the page first establishes the provider's normal public
	// access path, then this derived request avoids executing page JavaScript.
	kind, snapshot, snapshotErr := fetcher.fetchSnapshotJSON(ctx, rawURL)
	if snapshotErr == nil {
		if conversation, err := parseShareJSON(snapshot, kind); err == nil {
			return conversation, nil
		} else {
			snapshotErr = err
		}
	}
	if pageErr != nil {
		return Conversation{}, fmt.Errorf("share page: %v; snapshot API: %w", pageErr, snapshotErr)
	}
	return Conversation{}, snapshotErr
}

// fetchSnapshotJSON reads the same public snapshot exposed by the share page.
// The endpoint is derived from an already validated token and host; callers
// cannot choose an arbitrary API path. Redirects are disabled so every network
// destination remains covered by that validation and the safe transport.
func (fetcher *Fetcher) fetchSnapshotJSON(ctx context.Context, rawURL string) (SourceKind, []byte, error) {
	kind, parsedURL, err := validateShareURL(rawURL)
	if err != nil {
		return "", nil, err
	}
	token := strings.TrimPrefix(parsedURL.Path, "/share/")
	switch kind {
	case SourceChatGPTShare:
		parsedURL.Path = "/backend-api/share/" + token
	case SourceClaudeShare:
		parsedURL.Path = "/api/chat_snapshots/" + token
	default:
		return "", nil, ErrUnsupportedURL
	}
	parsedURL.RawPath = ""
	parsedURL.RawQuery = ""
	parsedURL.Fragment = ""

	ctx, cancel := context.WithTimeout(ctx, fetcher.options.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrFetch, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "showagent conversation importer")
	client := &http.Client{
		Transport: fetcher.transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", nil, ctxErr
		}
		err = withoutRequestURL(err)
		if errors.Is(err, ErrUnsafeAddress) {
			return "", nil, err
		}
		return "", nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", nil, fmt.Errorf("%w: %s", ErrHTTPStatus, response.Status)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return "", nil, fmt.Errorf("%w: %q", ErrUnexpectedContent, response.Header.Get("Content-Type"))
	}
	if response.ContentLength > fetcher.options.MaxResponseBytes {
		return "", nil, fmt.Errorf("%w: snapshot exceeds %d bytes", ErrLimitExceeded, fetcher.options.MaxResponseBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, fetcher.options.MaxResponseBytes+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", nil, ctxErr
		}
		return "", nil, fmt.Errorf("%w: %w", ErrFetch, err)
	}
	if int64(len(body)) > fetcher.options.MaxResponseBytes {
		return "", nil, fmt.Errorf("%w: snapshot exceeds %d bytes", ErrLimitExceeded, fetcher.options.MaxResponseBytes)
	}
	return kind, body, nil
}

// withoutRequestURL removes net/http's URL-bearing wrapper while preserving
// the underlying transport or redirect error for errors.Is/errors.As callers.
func withoutRequestURL(err error) error {
	for {
		var requestErr *url.Error
		if !errors.As(err, &requestErr) || requestErr.Err == nil {
			return err
		}
		err = requestErr.Err
	}
}

type ipResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

func newSafeTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           secureDialContext(net.DefaultResolver, dialer.DialContext),
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
	}
}

func secureDialContext(resolver ipResolver, dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid dial address", ErrUnsafeAddress)
		}
		var addresses []netip.Addr
		if literal, err := netip.ParseAddr(host); err == nil {
			addresses = []netip.Addr{literal}
		} else {
			addresses, err = resolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("DNS lookup for %q: %w", host, err)
			}
		}
		if err := validatePublicIPs(addresses); err != nil {
			return nil, err
		}

		var lastErr error
		for _, resolved := range addresses {
			resolved = resolved.Unmap()
			connection, err := dial(ctx, network, net.JoinHostPort(resolved.String(), port))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		return nil, fmt.Errorf("dial failed after %s address(es): %w", strconv.Itoa(len(addresses)), lastErr)
	}
}
