package restclient

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+"token" || r.Header.Get("X-Test") != "yes" {
			t.Error("missing configured headers")
		}
		if _, err := io.WriteString(w, `{"ok":true}`); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, WithBearerToken("token"), WithHeader("X-Test", "yes"), WithTimeout(time.Second), WithMaxBodySize(11))
	if err != nil {
		t.Fatal(err)
	}
	body, err := client.Fetch(context.Background())
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("Fetch = %s, %v", body, err)
	}
	if client.http == http.DefaultClient || client.http.Timeout != time.Second {
		t.Fatal("client must be owned and configured")
	}
}

func TestBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "alice" || password != "secret" {
			t.Error("invalid basic auth")
		}
	}))
	defer server.Close()
	client, err := New(server.URL, WithBasicAuth("alice", "secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStatusAndSize(t *testing.T) {
	for _, code := range []int{200, 201, 204, 400, 401, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer server.Close()
			client, err := New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Fetch(context.Background())
			var status *StatusError
			if code < 300 {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.As(err, &status) || status.Code != code {
				t.Fatalf("status error = %v", err)
			}
		})
	}
	for _, body := range []string{"1234", "12345"} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.WriteString(w, body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client, err := New(server.URL, WithMaxBodySize(4))
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.Fetch(context.Background())
			if len(body) == 4 && (err != nil || string(got) != body) {
				t.Fatalf("boundary rejected: %q, %v", got, err)
			}
			if len(body) > 4 && (err == nil || got != nil) {
				t.Fatalf("oversized response accepted: %q, %v", got, err)
			}
		})
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client, err := New(server.URL, WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Fetch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := client.Fetch(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout lost: %v", err)
	}
}

func TestTLSAndClientCloning(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, "ok"); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err == nil {
		t.Fatal("untrusted TLS must fail by default")
	}
	originalTransport := &http.Transport{TLSClientConfig: &tls.Config{ServerName: "original"}}
	original := &http.Client{Transport: originalTransport, Timeout: time.Hour}
	client, err = New(server.URL, WithHTTPClient(original), WithInsecureSkipVerify(true), WithTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if original.Timeout != time.Hour || originalTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("mutated supplied client")
	}
	transport := client.http.Transport.(*http.Transport)
	if client.http == original || transport == originalTransport || transport.TLSClientConfig == originalTransport.TLSClientConfig {
		t.Fatal("shared mutable state")
	}
	trusted, err := New(server.URL, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trusted.Fetch(context.Background()); err != nil {
		t.Fatalf("custom roots not preserved: %v", err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed *atomic.Bool
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadErrorWrapping(t *testing.T) {
	want := errors.New("private response details")
	var closed atomic.Bool
	client, err := New("http://example.com", WithHTTPClient(&http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: trackedBody{failingReader{want}, &closed}}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Fetch(context.Background())
	if !errors.Is(err, want) || strings.Contains(err.Error(), "private response details") || !closed.Load() {
		t.Fatalf("read error not safely wrapped or body not closed: %v", err)
	}
}

func (b trackedBody) Close() error { b.closed.Store(true); return nil }

func TestCustomTransportAndBodyClosure(t *testing.T) {
	for _, code := range []int{200, 500} {
		for _, body := range []string{"ok", "oversized"} {
			var closed atomic.Bool
			supplied := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: code, Header: make(http.Header), Body: trackedBody{strings.NewReader(body), &closed}}, nil
			})}
			client, err := New("http://example.com", WithHTTPClient(supplied), WithMaxBodySize(2))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Fetch(context.Background())
			if (err == nil) != (code == http.StatusOK && body == "ok") {
				t.Fatalf("unexpected response error: %v", err)
			}
			if !closed.Load() {
				t.Fatal("response body not closed")
			}
			if _, err := New("http://example.com", WithHTTPClient(supplied), WithInsecureSkipVerify(true)); err == nil {
				t.Fatal("TLS option on opaque transport accepted")
			}
		}
	}
}

func TestErrorsDoNotLeakURLs(t *testing.T) {
	const secret = "TOPSECRET"
	client, err := New("http://example.com?token="+secret, WithHTTPClient(&http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "GET", URL: r.URL.String(), Err: errors.New(secret)}
	})}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background()); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe error: %v", err)
	}
	if _, err := New("http://user:" + secret + "@example.com"); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe URL validation: %v", err)
	}
}

func TestValidationAndDefaults(t *testing.T) {
	for _, raw := range []string{"", "/relative", "ftp://example.com", "https://", "http://" + "user@example.com", "https://example.com/#fragment", "http://[", "http://host:", "http://host:0", "http://host:65536"} {
		if _, err := New(raw); err == nil {
			t.Errorf("accepted URL %q", raw)
		}
	}
	for _, opt := range []Option{nil, WithTimeout(0), WithTimeout(-1), WithMaxBodySize(0), WithMaxBodySize(-1), WithMaxBodySize(1<<63 - 1), WithBearerToken("bad\r\n"), WithBearerToken("bad token"), WithBasicAuth("", "pass"), WithBasicAuth("a:b", "pass"), WithHeader("", "value"), WithHeader("Bad Header", "value"), WithHeader("X-Test", "\x00"), WithHTTPClient(nil)} {
		if _, err := New("https://example.com", opt); err == nil {
			t.Error("accepted invalid option")
		}
	}
	for _, opts := range [][]Option{{WithBearerToken("token"), WithBasicAuth("user", "pass")}, {WithBearerToken("token"), WithHeader("Authorization", "other")}} {
		if _, err := New("https://example.com", opts...); err == nil {
			t.Error("accepted conflicting authentication")
		}
	}
	client, err := New("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if client.http.Timeout != 15*time.Second || client.maxBody != 1048576 || client.http.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
		t.Fatal("incorrect defaults")
	}
}

func TestEmptyAuthOptions(t *testing.T) {
	for _, tc := range []struct {
		opts   []Option
		prefix string
	}{
		{[]Option{WithBearerToken(""), WithBasicAuth("", "")}, ""},
		{[]Option{WithBearerToken("token"), WithBasicAuth("", "")}, "Bearer "},
		{[]Option{WithBearerToken(""), WithBasicAuth("user", "password")}, "Basic "},
	} {
		client, err := New("https://example.com", tc.opts...)
		if err != nil {
			t.Fatal(err)
		}
		auth := client.headers.Get("Authorization")
		_, present := client.headers["Authorization"]
		if tc.prefix == "" && present {
			t.Fatal("empty auth must not emit an Authorization header")
		}
		if tc.prefix != "" && !strings.HasPrefix(auth, tc.prefix) {
			t.Fatal("empty auth option interfered with configured authentication")
		}
	}
}
