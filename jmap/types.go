package jmap

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Session is the JMAP session resource (RFC 8620, section 2).
type Session struct {
	// Capabilities maps each server capability to its (raw) settings.
	Capabilities map[string]json.RawMessage `json:"capabilities"`
	// Accounts maps account IDs to the accounts the user can access.
	Accounts map[string]Account `json:"accounts"`
	// PrimaryAccounts maps a capability to the user's main account for it.
	PrimaryAccounts map[string]string `json:"primaryAccounts"`
	// Username is the user's login name.
	Username string `json:"username"`
	// APIURL is where method calls are sent.
	APIURL string `json:"apiUrl"`
	// DownloadURL is the URL template for downloading blobs.
	DownloadURL string `json:"downloadUrl"`
	// UploadURL is the URL template for uploading blobs.
	UploadURL string `json:"uploadUrl"`
	// EventSourceURL is the URL template for push events.
	EventSourceURL string `json:"eventSourceUrl"`
	// State identifies this version of the session.
	State string `json:"state"`
}

// HasCapability reports whether the server offers capability.
func (s *Session) HasCapability(capability string) bool {
	_, ok := s.Capabilities[capability]
	return ok
}

// Account is one account in a [Session].
type Account struct {
	// Name is a user-friendly name for the account.
	Name string `json:"name"`
	// IsPersonal is true if the account belongs to the user.
	IsPersonal bool `json:"isPersonal"`
	// IsReadOnly is true if the account cannot be modified.
	IsReadOnly bool `json:"isReadOnly"`
	// AccountCapabilities maps capabilities to per-account settings.
	AccountCapabilities map[string]json.RawMessage `json:"accountCapabilities"`
}

// Request is a JMAP API request: the capabilities it uses and the method
// calls to run, in order.
type Request struct {
	// Using lists the capabilities the calls need. [Client.Do] adds
	// [CapabilityCore] if it is missing.
	Using []string `json:"using"`
	// Calls are the method calls to run.
	Calls []Invocation `json:"methodCalls"`
	// CreatedIDs optionally maps creation IDs to object IDs from earlier
	// requests.
	CreatedIDs map[string]string `json:"createdIds,omitempty"`
}

// Response is a JMAP API response.
type Response struct {
	// Responses holds one or more responses per method call, in order. Their
	// Args are json.RawMessage values.
	Responses []Invocation `json:"methodResponses"`
	// CreatedIDs maps creation IDs to the IDs the server assigned.
	CreatedIDs map[string]string `json:"createdIds,omitempty"`
	// SessionState is the current session state.
	SessionState string `json:"sessionState"`

	// methods maps call IDs to method names from the request.
	methods map[string]string
}

// Decode finds the response to the method call with callID and decodes its
// arguments into v (v may be nil). If the server answered that call with an
// error, Decode returns a [*MethodError] instead. If several responses share
// the call ID, the first one is used.
func (r *Response) Decode(callID string, v any) error {
	for _, inv := range r.Responses {
		if inv.ID != callID {
			continue
		}
		raw, _ := inv.Args.(json.RawMessage)
		if inv.Name == "error" {
			me := &MethodError{CallID: callID, Method: r.methods[callID]}
			var body struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				return fmt.Errorf("jmap: decode error response: %w", err)
			}
			me.Type, me.Description = body.Type, body.Description
			return me
		}
		if v == nil {
			return nil
		}
		if err := json.Unmarshal(raw, v); err != nil {
			return fmt.Errorf("jmap: decode %s response: %w", inv.Name, err)
		}
		return nil
	}
	return fmt.Errorf("jmap: no response for method call %q", callID)
}

// Invocation is one method call or method response: a method name, its
// arguments and a client-chosen call ID. It is encoded as a three-element
// JSON array.
type Invocation struct {
	// Name is the method name, such as "MaskedEmail/get", or "error" for a
	// method-level error response.
	Name string
	// Args are the method arguments. In requests it is any value that
	// encodes to a JSON object; in responses it is a json.RawMessage.
	Args any
	// ID is the method call ID that ties responses to calls.
	ID string
}

// MarshalJSON encodes the invocation as [name, args, id].
func (i Invocation) MarshalJSON() ([]byte, error) {
	args := i.Args
	if args == nil {
		args = struct{}{}
	}
	return json.Marshal([3]any{i.Name, args, i.ID})
}

// UnmarshalJSON decodes [name, args, id], keeping args as a json.RawMessage.
func (i *Invocation) UnmarshalJSON(b []byte) error {
	var parts []json.RawMessage
	if err := json.Unmarshal(b, &parts); err != nil {
		return err
	}
	if len(parts) != 3 {
		return errors.New("jmap: invocation must have 3 elements")
	}
	if err := json.Unmarshal(parts[0], &i.Name); err != nil {
		return err
	}
	if err := json.Unmarshal(parts[2], &i.ID); err != nil {
		return err
	}
	i.Args = parts[1]
	return nil
}

// ResultReference points an argument at the result of an earlier call in
// the same request (RFC 8620, section 3.7). Put it in the arguments under
// the argument name prefixed with "#", for example "#ids".
type ResultReference struct {
	// ResultOf is the call ID of the earlier method call.
	ResultOf string `json:"resultOf"`
	// Name is the expected response name of that call, such as
	// "Email/query".
	Name string `json:"name"`
	// Path is a JSON Pointer into its arguments, such as "/ids".
	Path string `json:"path"`
}

// SetResponse holds the standard arguments of a /set response. T is the
// object type returned for created and updated objects.
type SetResponse[T any] struct {
	AccountID    string               `json:"accountId"`
	OldState     *string              `json:"oldState"`
	NewState     string               `json:"newState"`
	Created      map[string]T         `json:"created"`
	Updated      map[string]*T        `json:"updated"`
	Destroyed    []string             `json:"destroyed"`
	NotCreated   map[string]*SetError `json:"notCreated"`
	NotUpdated   map[string]*SetError `json:"notUpdated"`
	NotDestroyed map[string]*SetError `json:"notDestroyed"`
}

// Err returns a per-object failure in the response as a
// [*SetError] with its ID and Op filled in, or nil if every operation
// succeeded. Use the NotCreated, NotUpdated and NotDestroyed maps to see all
// failures.
func (r *SetResponse[T]) Err() error {
	for _, g := range []struct {
		op string
		m  map[string]*SetError
	}{{"create", r.NotCreated}, {"update", r.NotUpdated}, {"destroy", r.NotDestroyed}} {
		for id, e := range g.m {
			if e == nil {
				e = &SetError{}
			}
			out := *e
			out.ID, out.Op = id, g.op
			return &out
		}
	}
	return nil
}

// GetResponse holds the standard arguments of a /get response.
type GetResponse[T any] struct {
	AccountID string   `json:"accountId"`
	State     string   `json:"state"`
	List      []T      `json:"list"`
	NotFound  []string `json:"notFound"`
}
