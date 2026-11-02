package maskedemail

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/ionut-maxim/fastmail/jmap"
)

// Service calls the MaskedEmail methods for the primary Masked Email account
// of a [jmap.Client]. Create one with [New]. It is safe for concurrent use.
type Service struct {
	c *jmap.Client
}

// New returns a Service that uses c.
func New(c *jmap.Client) *Service {
	return &Service{c: c}
}

// ListOptions filters the result of [Service.List]. Filtering happens on
// the client, since MaskedEmail/get has no filter arguments. The zero value
// lists every address, including deleted ones.
type ListOptions struct {
	// States keeps only addresses in one of these states. Empty means any
	// state.
	States []State
	// ForDomain keeps only addresses created for this site or one of its
	// subdomains. It may be an origin ("https://example.com") or a bare
	// domain ("example.com"); only the host is compared, case-insensitively.
	ForDomain string
}

// List returns the account's masked email addresses that match opts (which
// may be nil), in the order the server returns them.
func (s *Service) List(ctx context.Context, opts *ListOptions) ([]MaskedEmail, error) {
	all, err := s.get(ctx, nil)
	if err != nil {
		return nil, err
	}
	if opts == nil || (len(opts.States) == 0 && opts.ForDomain == "") {
		return all.List, nil
	}
	out := make([]MaskedEmail, 0, len(all.List))
	for _, m := range all.List {
		if len(opts.States) > 0 && !slices.Contains(opts.States, m.State) {
			continue
		}
		if !matchDomain(m.ForDomain, opts.ForDomain) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// Get returns the masked email with id. It returns an error matching
// [jmap.ErrNotFound] if there is none.
func (s *Service) Get(ctx context.Context, id string) (*MaskedEmail, error) {
	if id == "" {
		return nil, errors.New("maskedemail: empty id")
	}
	res, err := s.get(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	for i := range res.List {
		if res.List[i].ID == id {
			return &res.List[i], nil
		}
	}
	return nil, fmt.Errorf("maskedemail: %s: %w", id, jmap.ErrNotFound)
}

// CreateParams describes a new masked email address. All fields are
// optional.
type CreateParams struct {
	// State is the initial state. The server default is [StatePending],
	// which Fastmail deletes after 24 hours unless the address receives
	// mail; use [StateEnabled] for an address that should stay.
	State State `json:"state,omitempty"`
	// ForDomain is the origin of the site the address is for, scheme and
	// host only, such as "https://example.com".
	ForDomain string `json:"forDomain,omitempty"`
	// Description is a short user-supplied description. Fastmail asks
	// integrators not to put their product name or the site's domain here.
	Description string `json:"description,omitempty"`
	// URL is a deep link back to your app's record for this address. It
	// should not contain the site's domain.
	URL string `json:"url,omitempty"`
	// EmailPrefix becomes the start of the generated address: at most 64
	// characters of a-z, 0-9 and underscore. It is only used on create and
	// is never returned.
	EmailPrefix string `json:"emailPrefix,omitempty"`
}

var prefixRE = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

func (p *CreateParams) validate() error {
	if p.State != "" && !p.State.Valid() {
		return fmt.Errorf("maskedemail: invalid state %q", p.State)
	}
	if p.EmailPrefix != "" && !prefixRE.MatchString(p.EmailPrefix) {
		return fmt.Errorf("maskedemail: email prefix %q must be 1-64 characters of a-z, 0-9 and _", p.EmailPrefix)
	}
	return nil
}

// Create creates a masked email address and returns it with its generated
// ID and address. Fastmail rate-limits creation; when the limit is hit the
// error matches [jmap.ErrRateLimit].
func (s *Service) Create(ctx context.Context, p CreateParams) (*MaskedEmail, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	const cid = "new"
	res, err := s.set(ctx, map[string]any{"create": map[string]CreateParams{cid: p}})
	if err != nil {
		return nil, err
	}
	created, ok := res.Created[cid]
	if !ok {
		return nil, errors.New("maskedemail: create returned no object")
	}
	m := &MaskedEmail{State: p.State, ForDomain: p.ForDomain, Description: p.Description, URL: p.URL}
	if m.State == "" {
		m.State = StatePending
	}
	m.merge(&created)
	return m, nil
}

// UpdateParams lists the properties to change. Nil fields are left as they
// are.
type UpdateParams struct {
	// State moves the address to a new state. It cannot be set back to
	// [StatePending].
	State *State
	// ForDomain replaces the site origin.
	ForDomain *string
	// Description replaces the description.
	Description *string
	// URL replaces the deep link; an empty string clears it.
	URL *string
}

func (p *UpdateParams) patch() (map[string]any, error) {
	patch := map[string]any{}
	if p.State != nil {
		if !p.State.Valid() || *p.State == StatePending {
			return nil, fmt.Errorf("maskedemail: cannot update state to %q", *p.State)
		}
		patch["state"] = *p.State
	}
	if p.ForDomain != nil {
		patch["forDomain"] = *p.ForDomain
	}
	if p.Description != nil {
		patch["description"] = *p.Description
	}
	if p.URL != nil {
		if *p.URL == "" {
			patch["url"] = nil
		} else {
			patch["url"] = *p.URL
		}
	}
	if len(patch) == 0 {
		return nil, errors.New("maskedemail: nothing to update")
	}
	return patch, nil
}

// Update changes the masked email with id and returns it as stored after
// the change. Disabling ([StateDisabled]) is the normal way to stop using an
// address. It returns an error matching [jmap.ErrNotFound] if there is no
// such address.
func (s *Service) Update(ctx context.Context, id string, p UpdateParams) (*MaskedEmail, error) {
	if id == "" {
		return nil, errors.New("maskedemail: empty id")
	}
	patch, err := p.patch()
	if err != nil {
		return nil, err
	}
	accountID, err := s.c.AccountID(ctx, Capability)
	if err != nil {
		return nil, err
	}
	// Update and read back in one round trip.
	resp, err := s.c.Do(ctx, &jmap.Request{
		Using: []string{Capability},
		Calls: []jmap.Invocation{
			{Name: "MaskedEmail/set", ID: "set", Args: map[string]any{
				"accountId": accountID,
				"update":    map[string]any{id: patch},
			}},
			{Name: "MaskedEmail/get", ID: "get", Args: map[string]any{
				"accountId": accountID,
				"ids":       []string{id},
			}},
		},
	})
	if err != nil {
		return nil, err
	}
	var set jmap.SetResponse[MaskedEmail]
	if err := resp.Decode("set", &set); err != nil {
		return nil, err
	}
	if err := set.Err(); err != nil {
		return nil, err
	}
	var get jmap.GetResponse[MaskedEmail]
	if err := resp.Decode("get", &get); err != nil {
		return nil, err
	}
	for i := range get.List {
		if get.List[i].ID == id {
			return &get.List[i], nil
		}
	}
	return nil, fmt.Errorf("maskedemail: %s: %w", id, jmap.ErrNotFound)
}

// Delete permanently destroys the masked email with id. Fastmail only allows
// this for addresses that have never received mail; for any other address
// the server refuses and the error is a [*jmap.SetError] (typically of type
// [jmap.TypeForbidden]). To stop using an address that has received mail,
// call [Service.Update] with [StateDisabled] (mail goes to the trash) or
// [StateDeleted] (mail bounces) instead.
func (s *Service) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("maskedemail: empty id")
	}
	_, err := s.set(ctx, map[string]any{"destroy": []string{id}})
	return err
}

func (s *Service) get(ctx context.Context, ids []string) (*jmap.GetResponse[MaskedEmail], error) {
	accountID, err := s.c.AccountID(ctx, Capability)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"accountId": accountID, "ids": ids}
	var out jmap.GetResponse[MaskedEmail]
	if err := s.call(ctx, "MaskedEmail/get", args, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Service) set(ctx context.Context, args map[string]any) (*jmap.SetResponse[MaskedEmail], error) {
	accountID, err := s.c.AccountID(ctx, Capability)
	if err != nil {
		return nil, err
	}
	args["accountId"] = accountID
	var out jmap.SetResponse[MaskedEmail]
	if err := s.call(ctx, "MaskedEmail/set", args, &out); err != nil {
		return nil, err
	}
	if err := out.Err(); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Service) call(ctx context.Context, method string, args any, out any) error {
	resp, err := s.c.Do(ctx, &jmap.Request{
		Using: []string{Capability},
		Calls: []jmap.Invocation{{Name: method, Args: args, ID: "0"}},
	})
	if err != nil {
		return err
	}
	return resp.Decode("0", out)
}
