// Package fastmail is a Go client for Fastmail's JMAP API.
//
// A [Client] bundles a generic JMAP client ([jmap.Client]) with one service
// per supported capability. Today that is Masked Email
// ([maskedemail.Service]); more capabilities can be added as new fields
// without breaking existing code.
//
// Authenticate with a Fastmail API token (Settings → Privacy & Security →
// Manage API tokens) that has the scopes you need, such as Masked Email.
//
//	client, err := fastmail.New(os.Getenv("FASTMAIL_TOKEN"))
//	if err != nil {
//		return err
//	}
//	list, err := client.MaskedEmail.List(ctx, &maskedemail.ListOptions{
//		States: []maskedemail.State{maskedemail.StateEnabled},
//	})
//
// Every call takes a context. Errors are typed; see the [jmap] package for
// [jmap.ErrRateLimit], [jmap.ErrUnauthorized] and the rest.
package fastmail

import (
	"github.com/ionut-maxim/fastmail/jmap"
	"github.com/ionut-maxim/fastmail/maskedemail"
)

// Client is a Fastmail API client. It is safe for concurrent use.
type Client struct {
	// JMAP is the underlying JMAP client, for sessions and raw method calls.
	JMAP *jmap.Client
	// MaskedEmail manages Masked Email addresses.
	MaskedEmail *maskedemail.Service
}

// New returns a client that authenticates with a Fastmail API token. Options
// such as [jmap.WithHTTPClient] and [jmap.WithSessionURL] configure the
// underlying JMAP client. No request is made until the first call.
func New(token string, opts ...jmap.Option) (*Client, error) {
	jc, err := jmap.New(token, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{
		JMAP:        jc,
		MaskedEmail: maskedemail.New(jc),
	}, nil
}
