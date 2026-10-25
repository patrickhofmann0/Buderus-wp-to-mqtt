// Package restclient fetches bounded API responses.
package restclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultTimeout           = 15 * time.Second
	DefaultMaxBodySize int64 = 1 << 20
)

type Client struct {
	url     string
	http    *http.Client
	headers http.Header
	maxBody int64
}

type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("api returned http status %d", e.Code) }

type options struct {
	timeout               time.Duration
	maxBody               int64
	headers               http.Header
	token, user, password string
	basic                 bool
	client                *http.Client
	insecure              bool
}

type Option func(*options) error

func WithTimeout(timeout time.Duration) Option {
	return func(o *options) error {
		if timeout <= 0 {
			return errors.New("timeout must be positive")
		}
		o.timeout = timeout
		return nil
	}
}

func WithMaxBodySize(size int64) Option {
	return func(o *options) error {
		if size <= 0 || size == int64(^uint64(0)>>1) {
			return errors.New("maximum body size must be between 1 and maxint64-1")
		}
		o.maxBody = size
		return nil
	}
}

func WithBearerToken(token string) Option {
	return func(o *options) error {
		if !validHeaderValue(token) || strings.ContainsAny(token, " \t") {
			return errors.New("invalid bearer token")
		}
		o.token = token
		return nil
	}
}

func WithBasicAuth(user, password string) Option {
	return func(o *options) error {
		if user == "" && password == "" {
			return nil
		}
		if user == "" || strings.ContainsRune(user, ':') || hasControl(user) || hasControl(password) {
			return errors.New("invalid basic authentication")
		}
		o.user, o.password, o.basic = user, password, true
		return nil
	}
}

func WithHeader(name, value string) Option {
	return func(o *options) error {
		if !validHeaderName(name) || !validHeaderValue(value) {
			return errors.New("invalid http header")
		}
		o.headers.Set(name, value)
		return nil
	}
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, func(c rune) bool { return c < 32 || c == 127 }) >= 0
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range []byte(name) {
		allowed := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))
		if !allowed {
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for _, c := range []byte(value) {
		if c == 127 || (c < 32 && c != '\t') {
			return false
		}
	}
	return true
}

func WithHTTPClient(client *http.Client) Option {
	return func(o *options) error {
		if client == nil {
			return errors.New("http client must not be nil")
		}
		o.client = client
		return nil
	}
}

func WithInsecureSkipVerify(insecure bool) Option {
	return func(o *options) error { o.insecure = insecure; return nil }
}

func New(rawURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || !validPort(u) {
		return nil, errors.New("api url must be an absolute http(s) url without credentials or fragment")
	}
	o := options{timeout: DefaultTimeout, maxBody: DefaultMaxBodySize, headers: make(http.Header)}
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("nil client option")
		}
		if err := opt(&o); err != nil {
			return nil, fmt.Errorf("configure api client: %w", err)
		}
	}
	if (o.basic && o.token != "") || ((o.basic || o.token != "") && o.headers.Get("Authorization") != "") {
		return nil, errors.New("conflicting authentication options")
	}
	client := &http.Client{}
	if o.client != nil {
		*client = *o.client
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if t, ok := transport.(*http.Transport); ok {
		cloned := t.Clone()
		if cloned.TLSClientConfig == nil {
			cloned.TLSClientConfig = &tls.Config{}
		}
		cloned.TLSClientConfig.InsecureSkipVerify = o.insecure // Explicit opt-in for private appliances.
		client.Transport = cloned
	} else if o.insecure {
		return nil, errors.New("insecure tls requires an http transport")
	}
	client.Timeout = o.timeout
	if o.token != "" {
		o.headers.Set("Authorization", "Bearer "+o.token)
	}
	if o.basic {
		req := &http.Request{Header: o.headers}
		req.SetBasicAuth(o.user, o.password)
	}
	return &Client{url: u.String(), http: client, headers: o.headers.Clone(), maxBody: o.maxBody}, nil
}

func validPort(u *url.URL) bool {
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		return err == nil && number > 0 && number <= 65535
	}
	return true
}

func (c *Client) Fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("create api request: %w", safeError(err))
	}
	req.Header = c.headers.Clone()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch api: %w", safeError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{Code: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read api response: %w", safeError(err))
	}
	if int64(len(body)) > c.maxBody {
		return nil, errors.New("api response exceeds maximum body size")
	}
	return body, nil
}

// net/http wraps transport errors with the full URL, which can contain secrets.
func safeError(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return safeError(u.Err)
	}
	return &redactedError{cause: err}
}

type redactedError struct{ cause error }

func (e *redactedError) Error() string { return "request failed" }
func (e *redactedError) Unwrap() error { return e.cause }
