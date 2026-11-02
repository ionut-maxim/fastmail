// Package jmap is a small, generic JMAP (RFC 8620) client.
//
// It discovers the JMAP session, sends method calls to the session's API URL
// and maps request-level, method-level and /set errors to typed Go errors.
// Capability-specific packages, such as
// [github.com/ionut-maxim/fastmail/maskedemail], are built on top of it.
//
// A request is a list of method calls ([Invocation]) plus the capabilities it
// uses. The core capability is always added for you:
//
//	c, err := jmap.New(token)
//	if err != nil {
//		return err
//	}
//	accountID, err := c.AccountID(ctx, "https://www.fastmail.com/dev/maskedemail")
//	if err != nil {
//		return err
//	}
//	resp, err := c.Do(ctx, &jmap.Request{
//		Using: []string{"https://www.fastmail.com/dev/maskedemail"},
//		Calls: []jmap.Invocation{{
//			Name: "MaskedEmail/get",
//			Args: map[string]any{"accountId": accountID, "ids": nil},
//			ID:   "0",
//		}},
//	})
//	if err != nil {
//		return err
//	}
//	var out struct {
//		List []json.RawMessage `json:"list"`
//	}
//	err = resp.Decode("0", &out)
//
// # Errors
//
// Errors are typed so callers can tell them apart with [errors.As] and
// [errors.Is]:
//
//   - [*RequestError]: the whole request was rejected (HTTP status other than
//     200, often with an RFC 7807 problem body).
//   - [*MethodError]: one method call returned an "error" response.
//   - [*SetError]: a /set call could not create, update or destroy one object.
//
// All three match [ErrRateLimit] when the server is rate limiting, and
// [ErrUnauthorized] and [ErrNotFound] where that applies.
package jmap
