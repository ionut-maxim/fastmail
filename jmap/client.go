package jmap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultSessionURL is Fastmail's JMAP session resource.
const DefaultSessionURL = "https://api.fastmail.com/jmap/session"

// CapabilityCore is the JMAP core capability. [Client.Do] always adds it to
// a request's Using list.
const CapabilityCore = "urn:ietf:params:jmap:core"

// maxErrorBody caps how much of an error response is read.
const maxErrorBody = 64 << 10

// Client talks to one JMAP server as one user. It is safe for concurrent
// use. The session is fetched on first use and cached; it is fetched again
// when a response reports a new session state.
type Client struct {
	token      string
	sessionURL string
	httpClient *http.Client
	userAgent  string

	mu      sync.Mutex
	session *Session
	stale   bool
}

// Option configures a [Client].
type Option func(*Client)

// WithHTTPClient sets the HTTP client used for all requests. The default is
// a client with a 30 second timeout.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithSessionURL sets the JMAP session URL. The default is
// [DefaultSessionURL]; tests point it at a fake server.
func WithSessionURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.sessionURL = u
		}
	}
}

// WithUserAgent sets the User-Agent header sent with every request.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// New returns a client that authenticates with token as a bearer token,
// such as a Fastmail API token.
func New(token string, opts ...Option) (*Client, error) {
	if token == "" {
		return nil, errors.New("jmap: empty token")
	}
	c := &Client{
		token:      token,
		sessionURL: DefaultSessionURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		userAgent:  "github.com/ionut-maxim/fastmail",
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Session returns the JMAP session, fetching it on first use or when the
// cached copy is out of date.
func (c *Client) Session(ctx context.Context) (*Session, error) {
	c.mu.Lock()
	if c.session != nil && !c.stale {
		s := c.session
		c.mu.Unlock()
		return s, nil
	}
	c.mu.Unlock()
	return c.RefreshSession(ctx)
}

// RefreshSession fetches the session from the server and caches it.
func (c *Client) RefreshSession(ctx context.Context) (*Session, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.sessionURL, nil)
	if err != nil {
		return nil, fmt.Errorf("jmap: build session request: %w", err)
	}
	var s Session
	if err := c.send(req, &s); err != nil {
		return nil, err
	}
	if s.APIURL == "" {
		return nil, errors.New("jmap: session has no apiUrl")
	}
	// Resolve URLs relative to the session URL, as RFC 8620 allows.
	if base, err := url.Parse(c.sessionURL); err == nil {
		if ref, err := url.Parse(s.APIURL); err == nil {
			s.APIURL = base.ResolveReference(ref).String()
		}
	}
	c.mu.Lock()
	c.session, c.stale = &s, false
	c.mu.Unlock()
	return &s, nil
}

// AccountID returns the primary account ID for capability, fetching the
// session if needed. It returns an error matching [ErrUnsupportedCapability]
// when the session has no primary account for the capability, which for an
// API token usually means the token was not granted that scope.
func (c *Client) AccountID(ctx context.Context, capability string) (string, error) {
	s, err := c.Session(ctx)
	if err != nil {
		return "", err
	}
	id, ok := s.PrimaryAccounts[capability]
	if !ok || id == "" {
		return "", fmt.Errorf("%w: no primary account for %s", ErrUnsupportedCapability, capability)
	}
	return id, nil
}

// Do sends req to the session's API URL and returns the response. The core
// capability is added to req.Using if missing. It returns a
// [*RequestError] if the server rejects the request as a whole; method-level
// errors are returned by [Response.Decode].
func (c *Client) Do(ctx context.Context, req *Request) (*Response, error) {
	s, err := c.Session(ctx)
	if err != nil {
		return nil, err
	}
	body := *req
	body.Using = withCore(req.Using)
	payload, err := json.Marshal(&body)
	if err != nil {
		return nil, fmt.Errorf("jmap: encode request: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.APIURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("jmap: build request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	var resp Response
	if err := c.send(hreq, &resp); err != nil {
		return nil, err
	}
	resp.methods = make(map[string]string, len(req.Calls))
	for _, call := range req.Calls {
		resp.methods[call.ID] = call.Name
	}
	if resp.SessionState != "" && resp.SessionState != s.State {
		c.mu.Lock()
		if c.session == s {
			c.stale = true
		}
		c.mu.Unlock()
	}
	return &resp, nil
}

// send performs an authenticated request and decodes a 200 JSON body into
// out, or turns any other status into a *RequestError.
func (c *Client) send(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("jmap: %s %s: %w", req.Method, redact(req.URL), err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return requestError(res)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("jmap: decode %s response: %w", redact(req.URL), err)
	}
	return nil
}

func requestError(res *http.Response) error {
	e := &RequestError{Status: res.StatusCode, RetryAfter: retryAfter(res.Header.Get("Retry-After"))}
	body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if mt == "application/problem+json" || mt == "application/json" {
		var p struct {
			Type   string `json:"type"`
			Detail string `json:"detail"`
			Title  string `json:"title"`
			Limit  string `json:"limit"`
		}
		if json.Unmarshal(body, &p) == nil {
			e.Type, e.Detail, e.Limit = p.Type, p.Detail, p.Limit
			if e.Detail == "" {
				e.Detail = p.Title
			}
			return e
		}
	}
	e.Detail = strings.TrimSpace(string(body))
	if len(e.Detail) > 200 {
		e.Detail = e.Detail[:200] + "…"
	}
	return e
}

func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// redact drops the query string and user info from u for error messages.
func redact(u *url.URL) string {
	r := *u
	r.User, r.RawQuery, r.Fragment = nil, "", ""
	return r.String()
}

func withCore(using []string) []string {
	for _, u := range using {
		if u == CapabilityCore {
			return using
		}
	}
	return append([]string{CapabilityCore}, using...)
}
