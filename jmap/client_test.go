package jmap_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ionut-maxim/fastmail/internal/jmaptest"
	"github.com/ionut-maxim/fastmail/jmap"
)

const capTest = "urn:example:test"

func newClient(t *testing.T, srv *jmaptest.Server, token string) *jmap.Client {
	t.Helper()
	c, err := jmap.New(token, jmap.WithSessionURL(srv.SessionURL()), jmap.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewEmptyToken(t *testing.T) {
	if _, err := jmap.New(""); err == nil {
		t.Fatal("want error for empty token")
	}
}

func TestSession(t *testing.T) {
	srv := jmaptest.New(t, capTest)
	c := newClient(t, srv, jmaptest.Token)
	ctx := context.Background()

	s, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := srv.URL + "/jmap/api/"; s.APIURL != want {
		t.Errorf("APIURL = %q, want %q (resolved)", s.APIURL, want)
	}
	if !s.HasCapability(capTest) || !s.HasCapability(jmap.CapabilityCore) {
		t.Errorf("capabilities = %v", s.Capabilities)
	}
	if s.Accounts[jmaptest.AccountID].Name == "" {
		t.Errorf("accounts = %v", s.Accounts)
	}

	id, err := c.AccountID(ctx, capTest)
	if err != nil || id != jmaptest.AccountID {
		t.Fatalf("AccountID = %q, %v", id, err)
	}
	if _, err := c.Session(ctx); err != nil {
		t.Fatal(err)
	}
	if n := srv.SessionFetches(); n != 1 {
		t.Errorf("session fetched %d times, want 1 (cached)", n)
	}

	_, err = c.AccountID(ctx, "urn:example:missing")
	if !errors.Is(err, jmap.ErrUnsupportedCapability) {
		t.Errorf("AccountID(missing) err = %v, want ErrUnsupportedCapability", err)
	}
}

func TestSessionUnauthorized(t *testing.T) {
	srv := jmaptest.New(t, capTest)
	c := newClient(t, srv, "wrong")
	_, err := c.Session(context.Background())
	var re *jmap.RequestError
	if !errors.As(err, &re) || re.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want *RequestError 401", err)
	}
	if !errors.Is(err, jmap.ErrUnauthorized) {
		t.Errorf("err does not match ErrUnauthorized")
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestDo(t *testing.T) {
	srv := jmaptest.New(t, capTest)
	srv.Handle("Test/echo", func(args json.RawMessage) (string, any) {
		return "Test/echo", args
	})
	c := newClient(t, srv, jmaptest.Token)
	ctx := context.Background()

	resp, err := c.Do(ctx, &jmap.Request{
		Using: []string{capTest},
		Calls: []jmap.Invocation{
			{Name: "Test/echo", Args: map[string]any{"n": 1}, ID: "a"},
			{Name: "Test/echo", Args: map[string]any{
				"#n": jmap.ResultReference{ResultOf: "a", Name: "Test/echo", Path: "/n"},
			}, ID: "b"},
			{Name: "Test/nope", ID: "c"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests", len(reqs))
	}
	if want := []string{jmap.CapabilityCore, capTest}; !slices.Equal(reqs[0].Using, want) {
		t.Errorf("using = %v, want %v", reqs[0].Using, want)
	}
	if got := string(reqs[0].Calls[1].Args); got != `{"#n":{"resultOf":"a","name":"Test/echo","path":"/n"}}` {
		t.Errorf("result reference encoded as %s", got)
	}
	if got := string(reqs[0].Calls[2].Args); got != `{}` {
		t.Errorf("nil args encoded as %s, want {}", got)
	}

	var out struct{ N int }
	if err := resp.Decode("a", &out); err != nil || out.N != 1 {
		t.Errorf("Decode(a) = %+v, %v", out, err)
	}

	err = resp.Decode("c", nil)
	var me *jmap.MethodError
	if !errors.As(err, &me) {
		t.Fatalf("Decode(c) err = %v, want *MethodError", err)
	}
	if me.Type != jmap.TypeUnknownMethod || me.Method != "Test/nope" || me.CallID != "c" {
		t.Errorf("MethodError = %+v", me)
	}

	if err := resp.Decode("zzz", nil); err == nil {
		t.Error("Decode(unknown id) want error")
	}
}

func TestSessionRefreshOnStateChange(t *testing.T) {
	srv := jmaptest.New(t, capTest)
	srv.Handle("Test/echo", func(json.RawMessage) (string, any) { return "Test/echo", map[string]any{} })
	c := newClient(t, srv, jmaptest.Token)
	ctx := context.Background()
	req := &jmap.Request{Calls: []jmap.Invocation{{Name: "Test/echo", ID: "0"}}}

	if _, err := c.Do(ctx, req); err != nil {
		t.Fatal(err)
	}
	srv.SetSessionState("s2")
	if _, err := c.Do(ctx, req); err != nil {
		t.Fatal(err)
	}
	if n := srv.SessionFetches(); n != 1 {
		t.Fatalf("session fetches = %d, want 1", n)
	}
	s, err := c.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.State != "s2" || srv.SessionFetches() != 2 {
		t.Errorf("session state %q after %d fetches, want s2 after 2", s.State, srv.SessionFetches())
	}
}

func TestMethodErrors(t *testing.T) {
	tests := []struct {
		typ      string
		sentinel error
	}{
		{jmap.TypeRateLimit, jmap.ErrRateLimit},
		{jmap.TypeAccountNotFound, jmap.ErrNotFound},
		{jmap.TypeInvalidArguments, nil},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			srv := jmaptest.New(t, capTest)
			srv.Handle("Test/fail", func(json.RawMessage) (string, any) {
				return "error", map[string]string{"type": tt.typ, "description": "nope"}
			})
			c := newClient(t, srv, jmaptest.Token)
			resp, err := c.Do(context.Background(), &jmap.Request{Calls: []jmap.Invocation{{Name: "Test/fail", ID: "0"}}})
			if err != nil {
				t.Fatal(err)
			}
			err = resp.Decode("0", nil)
			var me *jmap.MethodError
			if !errors.As(err, &me) || me.Type != tt.typ || me.Description != "nope" {
				t.Fatalf("err = %#v", err)
			}
			for _, s := range []error{jmap.ErrRateLimit, jmap.ErrNotFound, jmap.ErrUnauthorized} {
				if got, want := errors.Is(err, s), s == tt.sentinel; got != want {
					t.Errorf("errors.Is(%v) = %v, want %v", s, got, want)
				}
			}
		})
	}
}

func TestRequestErrors(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		retryAfter  string
		wantType    string
		wantLimit   string
		wantRetry   time.Duration
		sentinel    error
	}{
		{
			name: "unknown capability", status: 400, contentType: "application/problem+json",
			body:     `{"type":"urn:ietf:params:jmap:error:unknownCapability","status":400,"detail":"no such capability"}`,
			wantType: jmap.ProblemUnknownCapability, sentinel: jmap.ErrUnsupportedCapability,
		},
		{
			name: "limit", status: 400, contentType: "application/problem+json",
			body:     `{"type":"urn:ietf:params:jmap:error:limit","limit":"maxCallsInRequest","status":400}`,
			wantType: jmap.ProblemLimit, wantLimit: "maxCallsInRequest",
		},
		{
			name: "rate limited", status: 429, contentType: "text/plain", body: "slow down",
			retryAfter: "7", wantRetry: 7 * time.Second, sentinel: jmap.ErrRateLimit,
		},
		{
			name: "forbidden", status: 403, contentType: "text/plain", body: "forbidden",
			sentinel: jmap.ErrUnauthorized,
		},
		{
			name: "server error", status: 503, contentType: "text/html", body: "<h1>down</h1>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := jmaptest.New(t, capTest)
			srv.APIStatus, srv.APIContentType, srv.APIBody, srv.RetryAfter = tt.status, tt.contentType, tt.body, tt.retryAfter
			c := newClient(t, srv, jmaptest.Token)
			_, err := c.Do(context.Background(), &jmap.Request{Calls: []jmap.Invocation{{Name: "Test/x", ID: "0"}}})
			var re *jmap.RequestError
			if !errors.As(err, &re) {
				t.Fatalf("err = %v, want *RequestError", err)
			}
			if re.Status != tt.status || re.Type != tt.wantType || re.Limit != tt.wantLimit || re.RetryAfter != tt.wantRetry {
				t.Errorf("RequestError = %+v", re)
			}
			for _, s := range []error{jmap.ErrRateLimit, jmap.ErrUnauthorized, jmap.ErrUnsupportedCapability, jmap.ErrNotFound} {
				if got, want := errors.Is(err, s), s == tt.sentinel; got != want {
					t.Errorf("errors.Is(%v) = %v, want %v", s, got, want)
				}
			}
		})
	}
}

func TestSetResponseErr(t *testing.T) {
	var r jmap.SetResponse[struct{}]
	if err := r.Err(); err != nil {
		t.Fatalf("empty response Err = %v", err)
	}
	raw := `{"notCreated":{"k1":{"type":"rateLimit","description":"too many"}}}`
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	err := r.Err()
	var se *jmap.SetError
	if !errors.As(err, &se) || se.ID != "k1" || se.Op != "create" || se.Type != jmap.TypeRateLimit {
		t.Fatalf("Err = %#v", err)
	}
	if !errors.Is(err, jmap.ErrRateLimit) || errors.Is(err, jmap.ErrNotFound) {
		t.Errorf("sentinel matching wrong for %v", err)
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("Error() = %q", err)
	}
}

func TestInvocationJSON(t *testing.T) {
	var inv jmap.Invocation
	if err := json.Unmarshal([]byte(`["Foo/get",{"a":1},"x"]`), &inv); err != nil {
		t.Fatal(err)
	}
	if inv.Name != "Foo/get" || inv.ID != "x" || string(inv.Args.(json.RawMessage)) != `{"a":1}` {
		t.Errorf("decoded %+v", inv)
	}
	b, err := json.Marshal(inv)
	if err != nil || string(b) != `["Foo/get",{"a":1},"x"]` {
		t.Errorf("round trip = %s, %v", b, err)
	}
	if err := json.Unmarshal([]byte(`["a","b"]`), &inv); err == nil {
		t.Error("want error for 2-element invocation")
	}
}
