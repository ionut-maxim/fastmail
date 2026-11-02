package maskedemail_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ionut-maxim/fastmail/internal/jmaptest"
	"github.com/ionut-maxim/fastmail/jmap"
	"github.com/ionut-maxim/fastmail/maskedemail"
)

// store is a minimal in-memory MaskedEmail implementation for the fake
// server, following the rules in Fastmail's documentation.
type store struct {
	mu        sync.Mutex
	items     map[string]map[string]any
	order     []string
	next      int
	rateLimit bool
}

func newStore() *store { return &store{items: map[string]map[string]any{}} }

func (s *store) add(m map[string]any) {
	s.items[m["id"].(string)] = m
	s.order = append(s.order, m["id"].(string))
}

func (s *store) get(args json.RawMessage) (string, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var a struct {
		AccountID string   `json:"accountId"`
		IDs       []string `json:"ids"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.AccountID != jmaptest.AccountID {
		return "error", map[string]string{"type": "accountNotFound"}
	}
	ids := a.IDs
	if ids == nil {
		ids = s.order
	}
	list, notFound := []any{}, []string{}
	for _, id := range ids {
		if m, ok := s.items[id]; ok {
			list = append(list, m)
		} else {
			notFound = append(notFound, id)
		}
	}
	return "MaskedEmail/get", map[string]any{"accountId": a.AccountID, "state": "1", "list": list, "notFound": notFound}
}

func (s *store) set(args json.RawMessage) (string, any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var a struct {
		AccountID string                                `json:"accountId"`
		Create    map[string]map[string]any             `json:"create"`
		Update    map[string]map[string]json.RawMessage `json:"update"`
		Destroy   []string                              `json:"destroy"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "error", map[string]string{"type": "invalidArguments", "description": err.Error()}
	}
	if a.AccountID != jmaptest.AccountID {
		return "error", map[string]string{"type": "accountNotFound"}
	}
	res := map[string]any{"accountId": a.AccountID, "oldState": "1", "newState": "2"}
	created, notCreated := map[string]any{}, map[string]any{}
	for cid, p := range a.Create {
		if s.rateLimit {
			notCreated[cid] = map[string]string{"type": "rateLimit", "description": "too many masked emails created"}
			continue
		}
		s.next++
		id := fmt.Sprintf("masked-%d", s.next)
		prefix, _ := p["emailPrefix"].(string)
		if prefix == "" {
			prefix = "random"
		}
		m := map[string]any{
			"id": id, "email": fmt.Sprintf("%s.%d@example.com", prefix, s.next),
			"state": "pending", "forDomain": "", "description": "", "url": nil,
			"createdBy": "test", "createdAt": "2026-01-02T03:04:05Z", "lastMessageAt": nil,
		}
		for k, v := range p {
			if k != "emailPrefix" {
				m[k] = v
			}
		}
		s.add(m)
		// Like the real server, return only server-set properties.
		created[cid] = map[string]any{"id": id, "email": m["email"], "createdBy": "test", "createdAt": m["createdAt"]}
	}
	updated, notUpdated := map[string]any{}, map[string]any{}
	for id, patch := range a.Update {
		m, ok := s.items[id]
		if !ok {
			notUpdated[id] = map[string]string{"type": "notFound"}
			continue
		}
		if string(patch["state"]) == `"pending"` {
			notUpdated[id] = map[string]any{"type": "invalidProperties", "properties": []string{"state"}}
			continue
		}
		for k, v := range patch {
			var x any
			_ = json.Unmarshal(v, &x)
			m[k] = x
		}
		updated[id] = nil
	}
	destroyed, notDestroyed := []string{}, map[string]any{}
	for _, id := range a.Destroy {
		m, ok := s.items[id]
		switch {
		case !ok:
			notDestroyed[id] = map[string]string{"type": "notFound"}
		case m["lastMessageAt"] != nil:
			notDestroyed[id] = map[string]string{"type": "forbidden", "description": "address has received mail"}
		default:
			delete(s.items, id)
			s.order = slices.DeleteFunc(s.order, func(x string) bool { return x == id })
			destroyed = append(destroyed, id)
		}
	}
	for k, v := range map[string]any{"created": created, "notCreated": notCreated, "updated": updated,
		"notUpdated": notUpdated, "destroyed": destroyed, "notDestroyed": notDestroyed} {
		res[k] = v
	}
	return "MaskedEmail/set", res
}

func setup(t *testing.T) (*maskedemail.Service, *store, *jmaptest.Server) {
	t.Helper()
	srv := jmaptest.New(t, maskedemail.Capability)
	st := newStore()
	srv.Handle("MaskedEmail/get", st.get)
	srv.Handle("MaskedEmail/set", st.set)
	c, err := jmap.New(jmaptest.Token, jmap.WithSessionURL(srv.SessionURL()), jmap.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	st.add(map[string]any{
		"id": "m1", "email": "shop.abc@example.com", "state": "enabled",
		"forDomain": "https://shop.example.com", "description": "Shop", "url": nil,
		"createdBy": "test", "createdAt": "2025-03-01 10:00:00", "lastMessageAt": "2025-03-02T11:00:00Z",
	})
	st.add(map[string]any{
		"id": "m2", "email": "news.def@example.com", "state": "disabled",
		"forDomain": "https://example.org", "description": "", "url": "app://item/2",
		"createdBy": "test", "createdAt": "2025-04-01T10:00:00Z", "lastMessageAt": nil,
	})
	st.add(map[string]any{
		"id": "m3", "email": "old.ghi@example.com", "state": "deleted",
		"forDomain": "https://example.com", "description": nil, "url": nil,
		"createdBy": "test", "createdAt": "2025-05-01T10:00:00Z", "lastMessageAt": nil,
	})
	return maskedemail.New(c), st, srv
}

func ids(list []maskedemail.MaskedEmail) []string {
	out := []string{}
	for _, m := range list {
		out = append(out, m.ID)
	}
	return out
}

func TestList(t *testing.T) {
	svc, _, srv := setup(t)
	ctx := context.Background()

	tests := []struct {
		name string
		opts *maskedemail.ListOptions
		want []string
	}{
		{"all", nil, []string{"m1", "m2", "m3"}},
		{"enabled", &maskedemail.ListOptions{States: []maskedemail.State{maskedemail.StateEnabled}}, []string{"m1"}},
		{"active", &maskedemail.ListOptions{States: []maskedemail.State{maskedemail.StateEnabled, maskedemail.StateDisabled}}, []string{"m1", "m2"}},
		{"domain covers subdomains", &maskedemail.ListOptions{ForDomain: "example.com"}, []string{"m1", "m3"}},
		{"origin", &maskedemail.ListOptions{ForDomain: "https://Shop.Example.com/"}, []string{"m1"}},
		{"domain and state", &maskedemail.ListOptions{ForDomain: "example.com", States: []maskedemail.State{maskedemail.StateDeleted}}, []string{"m3"}},
		{"no match", &maskedemail.ListOptions{ForDomain: "example.net"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.List(ctx, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids(got), tt.want) {
				t.Errorf("ids = %v, want %v", ids(got), tt.want)
			}
		})
	}

	req := srv.Requests()[0]
	if !slices.Contains(req.Using, maskedemail.Capability) || req.Calls[0].Name != "MaskedEmail/get" {
		t.Errorf("request = using %v, call %s", req.Using, req.Calls[0].Name)
	}
	if got := string(req.Calls[0].Args); got != `{"accountId":"u1234","ids":null}` {
		t.Errorf("args = %s", got)
	}
}

func TestGet(t *testing.T) {
	svc, _, _ := setup(t)
	ctx := context.Background()

	m, err := svc.Get(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	wantCreated := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	wantLast := time.Date(2025, 3, 2, 11, 0, 0, 0, time.UTC)
	if m.Email != "shop.abc@example.com" || m.State != maskedemail.StateEnabled ||
		m.ForDomain != "https://shop.example.com" || m.Description != "Shop" || m.URL != "" ||
		!m.CreatedAt.Equal(wantCreated) || m.LastMessageAt == nil || !m.LastMessageAt.Equal(wantLast) {
		t.Errorf("Get(m1) = %+v", m)
	}

	m, err = svc.Get(ctx, "m3")
	if err != nil {
		t.Fatal(err)
	}
	if m.Description != "" || m.LastMessageAt != nil {
		t.Errorf("Get(m3) = %+v", m)
	}

	if _, err := svc.Get(ctx, "nope"); !errors.Is(err, jmap.ErrNotFound) {
		t.Errorf("Get(nope) err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(ctx, ""); err == nil {
		t.Error("Get(\"\") want error")
	}
}

func TestCreate(t *testing.T) {
	svc, _, srv := setup(t)
	ctx := context.Background()

	m, err := svc.Create(ctx, maskedemail.CreateParams{
		State:       maskedemail.StateEnabled,
		ForDomain:   "https://example.com",
		Description: "Newsletter",
		URL:         "app://item/9",
		EmailPrefix: "news_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "masked-1" || m.Email != "news_1.1@example.com" || m.State != maskedemail.StateEnabled ||
		m.ForDomain != "https://example.com" || m.Description != "Newsletter" || m.URL != "app://item/9" ||
		m.CreatedBy != "test" || m.CreatedAt.IsZero() {
		t.Errorf("Create = %+v", m)
	}
	reqs := srv.Requests()
	got := string(reqs[len(reqs)-1].Calls[0].Args)
	want := `{"accountId":"u1234","create":{"new":{"state":"enabled","forDomain":"https://example.com","description":"Newsletter","url":"app://item/9","emailPrefix":"news_1"}}}`
	if got != want {
		t.Errorf("args =\n%s\nwant\n%s", got, want)
	}

	// Defaults: pending, nothing else sent.
	m, err = svc.Create(ctx, maskedemail.CreateParams{})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != maskedemail.StatePending {
		t.Errorf("default state = %q", m.State)
	}
	reqs = srv.Requests()
	if got := string(reqs[len(reqs)-1].Calls[0].Args); got != `{"accountId":"u1234","create":{"new":{}}}` {
		t.Errorf("args = %s", got)
	}
}

func TestCreateValidation(t *testing.T) {
	svc, _, srv := setup(t)
	for _, p := range []maskedemail.CreateParams{
		{EmailPrefix: "Has-Upper"},
		{EmailPrefix: string(make([]byte, 65))},
		{State: "bogus"},
	} {
		if _, err := svc.Create(context.Background(), p); err == nil {
			t.Errorf("Create(%+v) want error", p)
		}
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("sent %d requests for invalid input", n)
	}
}

func TestCreateRateLimit(t *testing.T) {
	svc, st, _ := setup(t)
	st.mu.Lock()
	st.rateLimit = true
	st.mu.Unlock()
	_, err := svc.Create(context.Background(), maskedemail.CreateParams{State: maskedemail.StateEnabled})
	if !errors.Is(err, jmap.ErrRateLimit) {
		t.Fatalf("err = %v, want ErrRateLimit", err)
	}
	var se *jmap.SetError
	if !errors.As(err, &se) || se.Op != "create" || se.Type != jmap.TypeRateLimit {
		t.Errorf("SetError = %+v", se)
	}
}

func TestUpdate(t *testing.T) {
	svc, _, srv := setup(t)
	ctx := context.Background()
	disabled := maskedemail.StateDisabled
	desc, empty := "Renamed", ""

	m, err := svc.Update(ctx, "m2", maskedemail.UpdateParams{State: &disabled, Description: &desc, URL: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != maskedemail.StateDisabled || m.Description != "Renamed" || m.URL != "" || m.Email != "news.def@example.com" {
		t.Errorf("Update = %+v", m)
	}
	reqs := srv.Requests()
	req := reqs[len(reqs)-1]
	if len(req.Calls) != 2 || req.Calls[0].Name != "MaskedEmail/set" || req.Calls[1].Name != "MaskedEmail/get" {
		t.Fatalf("calls = %+v", req.Calls)
	}
	if got := string(req.Calls[0].Args); got != `{"accountId":"u1234","update":{"m2":{"description":"Renamed","state":"disabled","url":null}}}` {
		t.Errorf("set args = %s", got)
	}

	fd := "https://example.net"
	m, err = svc.Update(ctx, "m1", maskedemail.UpdateParams{ForDomain: &fd})
	if err != nil || m.ForDomain != fd {
		t.Errorf("Update forDomain = %+v, %v", m, err)
	}

	if _, err := svc.Update(ctx, "nope", maskedemail.UpdateParams{Description: &desc}); !errors.Is(err, jmap.ErrNotFound) {
		t.Errorf("Update(nope) err = %v, want ErrNotFound", err)
	}
}

func TestUpdateValidation(t *testing.T) {
	svc, _, srv := setup(t)
	pending, bogus := maskedemail.StatePending, maskedemail.State("bogus")
	for _, p := range []maskedemail.UpdateParams{{}, {State: &pending}, {State: &bogus}} {
		if _, err := svc.Update(context.Background(), "m1", p); err == nil {
			t.Errorf("Update(%+v) want error", p)
		}
	}
	if _, err := svc.Update(context.Background(), "", maskedemail.UpdateParams{State: &bogus}); err == nil {
		t.Error("Update(\"\") want error")
	}
	if n := len(srv.Requests()); n != 0 {
		t.Errorf("sent %d requests for invalid input", n)
	}
}

func TestDelete(t *testing.T) {
	svc, _, srv := setup(t)
	ctx := context.Background()

	if err := svc.Delete(ctx, "m2"); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if got := string(reqs[len(reqs)-1].Calls[0].Args); got != `{"accountId":"u1234","destroy":["m2"]}` {
		t.Errorf("args = %s", got)
	}
	if _, err := svc.Get(ctx, "m2"); !errors.Is(err, jmap.ErrNotFound) {
		t.Errorf("m2 still there: %v", err)
	}

	// m1 has received mail: the server refuses.
	err := svc.Delete(ctx, "m1")
	var se *jmap.SetError
	if !errors.As(err, &se) || se.Op != "destroy" || se.ID != "m1" || se.Type != jmap.TypeForbidden {
		t.Errorf("Delete(m1) err = %v", err)
	}

	if err := svc.Delete(ctx, "nope"); !errors.Is(err, jmap.ErrNotFound) {
		t.Errorf("Delete(nope) err = %v, want ErrNotFound", err)
	}
}

func TestNoMaskedEmailScope(t *testing.T) {
	srv := jmaptest.New(t) // no Masked Email capability
	c, err := jmap.New(jmaptest.Token, jmap.WithSessionURL(srv.SessionURL()), jmap.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = maskedemail.New(c).List(context.Background(), nil)
	if !errors.Is(err, jmap.ErrUnsupportedCapability) {
		t.Errorf("err = %v, want ErrUnsupportedCapability", err)
	}
}

func TestMethodErrorPropagates(t *testing.T) {
	svc, _, srv := setup(t)
	srv.Handle("MaskedEmail/get", func(json.RawMessage) (string, any) {
		return "error", map[string]string{"type": "serverFail"}
	})
	_, err := svc.List(context.Background(), nil)
	var me *jmap.MethodError
	if !errors.As(err, &me) || me.Method != "MaskedEmail/get" || me.Type != jmap.TypeServerFail {
		t.Errorf("err = %v", err)
	}
}
