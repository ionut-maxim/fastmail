// Package maskedemail manages Fastmail Masked Email addresses through the
// MaskedEmail JMAP extension (capability [Capability]).
//
// A masked email is a generated address that forwards to the user's inbox.
// Each one has a [State]:
//
//   - [StatePending]: just created. It becomes enabled when it receives its
//     first message. Fastmail deletes pending addresses 24 hours after
//     creation and does not show them in its UI, so create addresses as
//     [StateEnabled] unless you only need one tentatively.
//   - [StateEnabled]: mail is delivered normally.
//   - [StateDisabled]: mail is accepted but goes straight to the trash.
//   - [StateDeleted]: mail is bounced.
//
// An address can never go back to pending. To stop using an address, disable
// it (or set it to deleted) with [Service.Update]; [Service.Delete] only works
// for addresses that never received mail.
//
// The API token needs the Masked Email scope.
package maskedemail

import (
	"encoding/json"
	"strings"
	"time"
)

// Capability is the JMAP capability for Masked Email.
const Capability = "https://www.fastmail.com/dev/maskedemail"

// State is the state of a masked email address.
type State string

// The states a masked email address can be in.
const (
	StatePending  State = "pending"
	StateEnabled  State = "enabled"
	StateDisabled State = "disabled"
	StateDeleted  State = "deleted"
)

// Valid reports whether s is one of the known states.
func (s State) Valid() bool {
	switch s {
	case StatePending, StateEnabled, StateDisabled, StateDeleted:
		return true
	}
	return false
}

// MaskedEmail is one masked email address.
type MaskedEmail struct {
	// ID identifies the address. Set by the server.
	ID string `json:"id"`
	// Email is the generated address. Set by the server.
	Email string `json:"email"`
	// State is the address's state.
	State State `json:"state"`
	// ForDomain is the origin of the site the address is for, such as
	// "https://example.com" (scheme and host only).
	ForDomain string `json:"forDomain"`
	// Description is a short user-supplied description, or empty.
	Description string `json:"description"`
	// URL is a deep link back to the creating app's record for this
	// address, or empty.
	URL string `json:"url"`
	// CreatedBy names the client that created the address. Set by the
	// server.
	CreatedBy string `json:"createdBy"`
	// CreatedAt is when the address was created. Set by the server.
	CreatedAt time.Time `json:"createdAt"`
	// LastMessageAt is when the address last received mail, or nil if it
	// never has. Set by the server.
	LastMessageAt *time.Time `json:"lastMessageAt"`
}

// UnmarshalJSON decodes a MaskedEmail, accepting null for string fields and
// both RFC 3339 ("2006-01-02T15:04:05Z") and space-separated
// ("2006-01-02 15:04:05", UTC) timestamps. A timestamp it cannot parse is
// left as the zero value rather than failing the whole response.
func (m *MaskedEmail) UnmarshalJSON(b []byte) error {
	var w struct {
		ID            *string `json:"id"`
		Email         *string `json:"email"`
		State         *State  `json:"state"`
		ForDomain     *string `json:"forDomain"`
		Description   *string `json:"description"`
		URL           *string `json:"url"`
		CreatedBy     *string `json:"createdBy"`
		CreatedAt     *string `json:"createdAt"`
		LastMessageAt *string `json:"lastMessageAt"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*m = MaskedEmail{
		ID:          deref(w.ID),
		Email:       deref(w.Email),
		State:       deref(w.State),
		ForDomain:   deref(w.ForDomain),
		Description: deref(w.Description),
		URL:         deref(w.URL),
		CreatedBy:   deref(w.CreatedBy),
	}
	if t, ok := parseTime(deref(w.CreatedAt)); ok {
		m.CreatedAt = t
	}
	if t, ok := parseTime(deref(w.LastMessageAt)); ok {
		m.LastMessageAt = &t
	}
	return nil
}

// merge copies the non-zero fields of src into m. The server only returns
// server-set or changed properties after a create.
func (m *MaskedEmail) merge(src *MaskedEmail) {
	if src == nil {
		return
	}
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&m.ID, src.ID)
	set(&m.Email, src.Email)
	set(&m.ForDomain, src.ForDomain)
	set(&m.Description, src.Description)
	set(&m.URL, src.URL)
	set(&m.CreatedBy, src.CreatedBy)
	if src.State != "" {
		m.State = src.State
	}
	if !src.CreatedAt.IsZero() {
		m.CreatedAt = src.CreatedAt
	}
	if src.LastMessageAt != nil {
		m.LastMessageAt = src.LastMessageAt
	}
}

var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// host returns the lower-cased host of an origin or domain such as
// "https://www.example.com/", "www.example.com" or "example.com:443".
func host(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 && !strings.Contains(s[i:], "]") {
		s = s[:i]
	}
	return strings.TrimSuffix(s, ".")
}

// matchDomain reports whether the address's forDomain is filter or a
// subdomain of it, comparing hosts only.
func matchDomain(forDomain, filter string) bool {
	h, f := host(forDomain), host(filter)
	if f == "" {
		return true
	}
	return h == f || strings.HasSuffix(h, "."+f)
}
