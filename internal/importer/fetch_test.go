package importer

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func response(status int, contentType, body, location string) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if location != "" {
		header.Set("Location", location)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestFetchShareRevalidatesRedirects(t *testing.T) {
	const secretToken = "redirect-secret-token"
	called := 0
	fetcher := newFetcher(FetchOptions{}, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		return response(http.StatusFound, "text/html", "", "https://evil.example/share/"+secretToken), nil
	}))

	_, err := fetcher.Fetch(context.Background(), "https://chatgpt.com/share/12345678")
	if !errors.Is(err, ErrUnsupportedURL) {
		t.Fatalf("redirect error = %v, want ErrUnsupportedURL", err)
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("redirect error disclosed share token: %v", err)
	}
	var requestErr *url.Error
	if errors.As(err, &requestErr) {
		t.Fatalf("redirect error retained URL-bearing wrapper: %T", requestErr)
	}
	if called != 1 {
		t.Fatalf("transport called %d times, want 1", called)
	}
}

func TestFetchErrorsDoNotDiscloseShareURLs(t *testing.T) {
	const secretToken = "transport-secret-token"
	transportErr := errors.New("connection refused")
	shareURL := "https://chatgpt.com/share/" + secretToken

	tests := []struct {
		name  string
		fetch func(*Fetcher) error
	}{
		{
			name: "share page",
			fetch: func(fetcher *Fetcher) error {
				_, err := fetcher.Fetch(context.Background(), shareURL)
				return err
			},
		},
		{
			name: "snapshot API",
			fetch: func(fetcher *Fetcher) error {
				_, _, err := fetcher.fetchSnapshotJSON(context.Background(), shareURL)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetcher := newFetcher(FetchOptions{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, transportErr
			}))
			err := test.fetch(fetcher)
			if !errors.Is(err, ErrFetch) || !errors.Is(err, transportErr) {
				t.Fatalf("error = %v, want ErrFetch and transport cause", err)
			}
			if strings.Contains(err.Error(), secretToken) {
				t.Fatalf("error disclosed share token: %v", err)
			}
			var requestErr *url.Error
			if errors.As(err, &requestErr) {
				t.Fatalf("error retained URL-bearing wrapper: %T", requestErr)
			}
		})
	}
}

func TestFetchShareAcceptsAllowedRedirectAndBoundsResponse(t *testing.T) {
	called := 0
	fetcher := newFetcher(FetchOptions{MaxResponseBytes: 16}, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		if called == 1 {
			return response(http.StatusFound, "text/html", "", "https://chatgpt.com/share/abcdefgh"), nil
		}
		return response(http.StatusOK, "text/html; charset=utf-8", "<html>ok</html>", ""), nil
	}))

	page, err := fetcher.Fetch(context.Background(), "https://chatgpt.com/share/12345678")
	if err != nil {
		t.Fatal(err)
	}
	if string(page.Body) != "<html>ok</html>" || page.SourceKind != SourceChatGPTShare || called != 2 {
		t.Fatalf("page = %#v, calls = %d", page, called)
	}

	tooLarge := newFetcher(FetchOptions{MaxResponseBytes: 4}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "text/html", "12345", ""), nil
	}))
	if _, err := tooLarge.Fetch(context.Background(), "https://chatgpt.com/share/12345678"); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("size error = %v, want ErrLimitExceeded", err)
	}
}

func TestFetchShareStatusContentTypeCancellationAndRedirectLimit(t *testing.T) {
	tests := []struct {
		name      string
		fetcher   *Fetcher
		ctx       context.Context
		wantError error
	}{
		{
			name: "status",
			fetcher: newFetcher(FetchOptions{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusForbidden, "text/html", "denied", ""), nil
			})),
			ctx: context.Background(), wantError: ErrHTTPStatus,
		},
		{
			name: "content type",
			fetcher: newFetcher(FetchOptions{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, "image/png", "not an image", ""), nil
			})),
			ctx: context.Background(), wantError: ErrUnexpectedContent,
		},
		{
			name: "network cancellation",
			fetcher: newFetcher(FetchOptions{}, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			})),
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			wantError: context.Canceled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.fetcher.Fetch(test.ctx, "https://chatgpt.com/share/12345678"); !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
		})
	}

	redirects := 0
	fetcher := newFetcher(FetchOptions{MaxRedirects: 1, Timeout: time.Second}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		redirects++
		return response(http.StatusFound, "text/html", "", "https://chatgpt.com/share/abcdefgh"), nil
	}))
	if _, err := fetcher.Fetch(context.Background(), "https://chatgpt.com/share/12345678"); !errors.Is(err, ErrRedirectLimit) {
		t.Fatalf("redirect limit error = %v", err)
	}
}

func TestDefaultTransportDoesNotUseEnvironmentProxy(t *testing.T) {
	transport := newSafeTransport()
	if transport.Proxy != nil {
		t.Fatal("safe fetch transport must not use environment proxies")
	}
}

type fixedResolver struct {
	addresses []netip.Addr
	err       error
}

func (resolver fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return resolver.addresses, resolver.err
}

func TestSecureDialRejectsPrivateDNSAnswerBeforeDial(t *testing.T) {
	dialed := false
	dial := secureDialContext(fixedResolver{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}, func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	})

	if _, err := dial(context.Background(), "tcp", "chatgpt.com:443"); !errors.Is(err, ErrUnsafeAddress) {
		t.Fatalf("dial error = %v, want ErrUnsafeAddress", err)
	}
	if dialed {
		t.Fatal("dial ran after a private DNS answer")
	}
}

func TestSecureDialHandlesAddressAndDialOutcomes(t *testing.T) {
	sentinel := errors.New("resolver unavailable")
	resolverFailure := secureDialContext(fixedResolver{err: sentinel}, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dial ran after resolver failure")
		return nil, nil
	})
	if _, err := resolverFailure(context.Background(), "tcp", "chatgpt.com:443"); !errors.Is(err, sentinel) {
		t.Fatalf("resolver error = %v, want sentinel", err)
	}

	invalidAddress := secureDialContext(fixedResolver{}, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dial ran for malformed address")
		return nil, nil
	})
	if _, err := invalidAddress(context.Background(), "tcp", "missing-port"); !errors.Is(err, ErrUnsafeAddress) {
		t.Fatalf("malformed address error = %v, want ErrUnsafeAddress", err)
	}

	var dialed []string
	dialFailure := errors.New("connection refused")
	resolved := secureDialContext(fixedResolver{addresses: []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946"),
	}}, func(_ context.Context, _ string, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		if len(dialed) == 1 {
			return nil, dialFailure
		}
		return nil, nil
	})
	if _, err := resolved(context.Background(), "tcp", "chatgpt.com:443"); err != nil {
		t.Fatalf("fallback dial: %v", err)
	}
	if len(dialed) != 2 || dialed[0] != "93.184.216.34:443" || dialed[1] != "[2606:2800:220:1:248:1893:25c8:1946]:443" {
		t.Fatalf("dialed addresses = %#v", dialed)
	}

	allFailed := secureDialContext(fixedResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}, func(context.Context, string, string) (net.Conn, error) {
		return nil, dialFailure
	})
	if _, err := allFailed(context.Background(), "tcp", "chatgpt.com:443"); !errors.Is(err, dialFailure) {
		t.Fatalf("all-address dial error = %v, want last dial failure", err)
	}

	literalDialed := ""
	literal := secureDialContext(fixedResolver{err: errors.New("must not resolve a literal")}, func(_ context.Context, _ string, address string) (net.Conn, error) {
		literalDialed = address
		return nil, nil
	})
	if _, err := literal(context.Background(), "tcp", "93.184.216.34:443"); err != nil || literalDialed != "93.184.216.34:443" {
		t.Fatalf("literal dial = %q, %v", literalDialed, err)
	}
}

type failingReadCloser struct {
	err error
}

func (reader failingReadCloser) Read([]byte) (int, error) { return 0, reader.err }
func (failingReadCloser) Close() error                    { return nil }

func TestFetcherRejectsDeclaredSizesAndBodyReadFailures(t *testing.T) {
	readFailure := errors.New("body read failed")
	tests := []struct {
		name      string
		response  *http.Response
		wantError error
	}{
		{
			name: "declared size",
			response: &http.Response{
				StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK),
				Header: http.Header{"Content-Type": []string{"text/html"}},
				Body:   io.NopCloser(strings.NewReader("small")), ContentLength: 32,
			},
			wantError: ErrLimitExceeded,
		},
		{
			name: "body read",
			response: &http.Response{
				StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK),
				Header: http.Header{"Content-Type": []string{"text/html"}},
				Body:   failingReadCloser{err: readFailure},
			},
			wantError: readFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetcher := newFetcher(FetchOptions{MaxResponseBytes: 16}, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return test.response, nil
			}))
			if _, err := fetcher.Fetch(context.Background(), "https://chatgpt.com/share/12345678"); !errors.Is(err, test.wantError) {
				t.Fatalf("Fetch error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestSnapshotFetchRejectsInvalidResponsesWithoutParsing(t *testing.T) {
	readFailure := errors.New("snapshot body read failed")
	tests := []struct {
		name      string
		response  *http.Response
		wantError error
	}{
		{
			name:      "status",
			response:  response(http.StatusNotFound, "application/json", `{}`, ""),
			wantError: ErrHTTPStatus,
		},
		{
			name:      "content type",
			response:  response(http.StatusOK, "text/html", `{}`, ""),
			wantError: ErrUnexpectedContent,
		},
		{
			name: "declared size",
			response: &http.Response{
				StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK),
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   io.NopCloser(strings.NewReader(`{}`)), ContentLength: 32,
			},
			wantError: ErrLimitExceeded,
		},
		{
			name: "body read",
			response: &http.Response{
				StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK),
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   failingReadCloser{err: readFailure},
			},
			wantError: readFailure,
		},
		{
			name:      "actual size",
			response:  response(http.StatusOK, "application/json", `{"long":"payload"}`, ""),
			wantError: ErrLimitExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fetcher := newFetcher(FetchOptions{MaxResponseBytes: 8}, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != "/backend-api/share/12345678" {
					t.Fatalf("snapshot path = %q", request.URL.Path)
				}
				return test.response, nil
			}))
			_, _, err := fetcher.fetchSnapshotJSON(context.Background(), "https://chatgpt.com/share/12345678")
			if !errors.Is(err, test.wantError) {
				t.Fatalf("snapshot error = %v, want %v", err, test.wantError)
			}
		})
	}

	if _, _, err := newFetcher(FetchOptions{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport ran for an unsupported URL")
		return nil, nil
	})).fetchSnapshotJSON(context.Background(), "https://example.com/share/12345678"); !errors.Is(err, ErrUnsupportedURL) {
		t.Fatalf("unsupported snapshot URL error = %v", err)
	}
}

func TestPublicFetchWrappersRejectUnsupportedURLs(t *testing.T) {
	if _, err := FetchShare(context.Background(), "https://example.com/share/12345678", FetchOptions{}); !errors.Is(err, ErrUnsupportedURL) {
		t.Fatalf("FetchShare error = %v", err)
	}
	if _, err := ImportURL(context.Background(), "https://example.com/share/12345678", FetchOptions{}); !errors.Is(err, ErrUnsupportedURL) {
		t.Fatalf("ImportURL error = %v", err)
	}
	if fetcher := NewFetcher(FetchOptions{MaxResponseBytes: MaxHTMLBytes + 1, Timeout: time.Hour, MaxRedirects: 100}); fetcher.options.MaxResponseBytes != MaxHTMLBytes || fetcher.options.Timeout != defaultFetchTimeout || fetcher.options.MaxRedirects != defaultMaxRedirects {
		t.Fatalf("NewFetcher did not cap options: %#v", fetcher.options)
	}
}

func TestFetcherImportParsesTheFetchedSnapshot(t *testing.T) {
	body := `<script type="application/json">{"conversation":{"title":"Shared greeting","current_node":"a1","mapping":{"a1":{"parent":null,"message":{"id":"a1","author":{"role":"assistant"},"content":{"parts":["hello"]}}}}}}</script>`
	fetcher := newFetcher(FetchOptions{}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, "text/html", body, ""), nil
	}))

	conversation, err := fetcher.Import(context.Background(), "https://chatgpt.com/share/12345678")
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation.Messages) != 1 || conversation.Messages[0].Text != "hello" {
		t.Fatalf("conversation = %#v", conversation)
	}
}

func TestFetcherImportReadsProviderSnapshotEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		shareURL  string
		wantPath  string
		body      string
		wantText  string
		wantTitle string
	}{
		{
			name:      "ChatGPT",
			shareURL:  "https://chatgpt.com/share/66e6ce92-d844-8013-ba02-6907eb708caf",
			wantPath:  "/backend-api/share/66e6ce92-d844-8013-ba02-6907eb708caf",
			body:      `{"title":"Shared plan","current_node":"u1","mapping":{"u1":{"parent":null,"message":{"id":"m1","author":{"role":"user"},"content":{"parts":["live API question"]}}}}}`,
			wantText:  "live API question",
			wantTitle: "Shared plan",
		},
		{
			name:      "Claude",
			shareURL:  "https://claude.ai/share/e294f513-fdfa-4ce7-8c79-781385841023",
			wantPath:  "/api/chat_snapshots/e294f513-fdfa-4ce7-8c79-781385841023",
			body:      `{"uuid":"e294f513-fdfa-4ce7-8c79-781385841023","snapshot_name":"Shared research","chat_messages":[{"uuid":"m1","sender":"human","text":"live API question"}]}`,
			wantText:  "live API question",
			wantTitle: "Shared research",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			fetcher := newFetcher(FetchOptions{}, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if strings.HasPrefix(request.URL.Path, "/share/") {
					return response(http.StatusOK, "text/html", "<html><body>client-rendered conversation</body></html>", ""), nil
				}
				if request.URL.Path != test.wantPath {
					t.Fatalf("request path = %q, want %q", request.URL.Path, test.wantPath)
				}
				if request.Header.Get("Accept") != "application/json" {
					t.Fatalf("Accept = %q", request.Header.Get("Accept"))
				}
				return response(http.StatusOK, "application/json; charset=utf-8", test.body, ""), nil
			}))

			conversation, err := fetcher.Import(context.Background(), test.shareURL)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || conversation.Title != test.wantTitle || len(conversation.Messages) != 1 || conversation.Messages[0].Text != test.wantText {
				t.Fatalf("calls=%d conversation=%#v", calls, conversation)
			}
		})
	}
}
