// Package jmaptest is an in-process fake JMAP server for tests.
package jmaptest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Token is the bearer token the fake server accepts.
const Token = "test-token"

// AccountID is the primary account ID the fake session advertises for every
// capability in Server.Capabilities.
const AccountID = "u1234"

// Handler answers one method call. It returns the response name (usually
// the method name, or "error") and arguments.
type Handler func(args json.RawMessage) (name string, resp any)

// Server is a fake JMAP server. Configure it before the first request.
type Server struct {
	*httptest.Server

	// Capabilities the session advertises, each with AccountID as the
	// primary account. The core capability is always included.
	Capabilities []string
	// SessionState is reported in the session and in API responses.
	SessionState string
	// APIStatus, when non-zero, makes the API endpoint fail with this HTTP
	// status and APIBody (with APIContentType).
	APIStatus      int
	APIBody        string
	APIContentType string
	// RetryAfter is sent as Retry-After with APIStatus, if set.
	RetryAfter string

	mu       sync.Mutex
	handlers map[string]Handler
	requests []Request
	sessions int
}

// Request is a decoded API request the server received.
type Request struct {
	Using []string `json:"using"`
	Calls []struct {
		Name string
		Args json.RawMessage
		ID   string
	} `json:"-"`
	Raw json.RawMessage `json:"-"`
}

// New starts a fake server that is closed when the test ends.
func New(t testing.TB, capabilities ...string) *Server {
	s := &Server{Capabilities: capabilities, SessionState: "s1", handlers: map[string]Handler{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jmap/session", s.session)
	mux.HandleFunc("POST /jmap/api/", s.api)
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// SessionURL is the URL to pass to jmap.WithSessionURL.
func (s *Server) SessionURL() string { return s.URL + "/jmap/session" }

// Handle registers h for method.
func (s *Server) Handle(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// SetSessionState changes the session state the server reports.
func (s *Server) SetSessionState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SessionState = state
}

// Requests returns the API requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// SessionFetches returns how many times the session was fetched.
func (s *Server) SessionFetches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") != "Bearer "+Token {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Authorization header not a valid format"))
		return false
	}
	return true
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	s.mu.Lock()
	s.sessions++
	state := s.SessionState
	s.mu.Unlock()
	caps := map[string]any{"urn:ietf:params:jmap:core": map[string]any{"maxCallsInRequest": 50}}
	primary := map[string]string{}
	for _, c := range s.Capabilities {
		caps[c] = map[string]any{}
		primary[c] = AccountID
	}
	writeJSON(w, map[string]any{
		"capabilities": caps,
		"accounts": map[string]any{AccountID: map[string]any{
			"name": "user@example.com", "isPersonal": true, "isReadOnly": false,
			"accountCapabilities": caps,
		}},
		"primaryAccounts": primary,
		"username":        "user@example.com",
		// Relative, to exercise URL resolution.
		"apiUrl":         "/jmap/api/",
		"downloadUrl":    s.URL + "/jmap/download/{accountId}/{blobId}/{name}?type={type}",
		"uploadUrl":      s.URL + "/jmap/upload/{accountId}/",
		"eventSourceUrl": s.URL + "/jmap/event/",
		"state":          state,
	})
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	if s.APIStatus != 0 {
		if s.RetryAfter != "" {
			w.Header().Set("Retry-After", s.RetryAfter)
		}
		w.Header().Set("Content-Type", s.APIContentType)
		w.WriteHeader(s.APIStatus)
		_, _ = w.Write([]byte(s.APIBody))
		return
	}
	var body struct {
		Using []string            `json:"using"`
		Calls [][]json.RawMessage `json:"methodCalls"`
	}
	raw := json.RawMessage{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil || json.Unmarshal(raw, &body) != nil {
		problem(w, http.StatusBadRequest, "urn:ietf:params:jmap:error:notJSON", "")
		return
	}
	req := Request{Using: body.Using, Raw: raw}
	var out [][]any
	for _, c := range body.Calls {
		var name, id string
		if len(c) != 3 || json.Unmarshal(c[0], &name) != nil || json.Unmarshal(c[2], &id) != nil {
			problem(w, http.StatusBadRequest, "urn:ietf:params:jmap:error:notRequest", "")
			return
		}
		req.Calls = append(req.Calls, struct {
			Name string
			Args json.RawMessage
			ID   string
		}{name, c[1], id})
		s.mu.Lock()
		h := s.handlers[name]
		s.mu.Unlock()
		if h == nil {
			out = append(out, []any{"error", map[string]string{"type": "unknownMethod"}, id})
			continue
		}
		rn, resp := h(c[1])
		out = append(out, []any{rn, resp, id})
	}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	state := s.SessionState
	s.mu.Unlock()
	writeJSON(w, map[string]any{"methodResponses": out, "sessionState": state})
}

func problem(w http.ResponseWriter, status int, typ, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": typ, "status": status, "detail": detail})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
