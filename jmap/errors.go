package jmap

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sentinel errors that the typed errors in this package match with
// [errors.Is].
var (
	// ErrUnauthorized means the token was missing, invalid, expired or lacks
	// access (HTTP 401 or 403).
	ErrUnauthorized = errors.New("jmap: unauthorized")

	// ErrRateLimit means the server is rate limiting the caller, either for
	// the whole request (HTTP 429) or for one method call or object (a
	// "rateLimit" error type).
	ErrRateLimit = errors.New("jmap: rate limited")

	// ErrNotFound means an object or account does not exist.
	ErrNotFound = errors.New("jmap: not found")

	// ErrUnsupportedCapability means the session does not offer the
	// capability, or has no primary account for it.
	ErrUnsupportedCapability = errors.New("jmap: capability not supported")
)

// Error types defined by RFC 8620 and used by Fastmail that callers commonly
// check for. The Type field of [MethodError] and [SetError] holds one of
// these or another server-defined value.
const (
	TypeRateLimit         = "rateLimit"
	TypeNotFound          = "notFound"
	TypeForbidden         = "forbidden"
	TypeInvalidArguments  = "invalidArguments"
	TypeInvalidProperties = "invalidProperties"
	TypeAccountNotFound   = "accountNotFound"
	TypeUnknownMethod     = "unknownMethod"
	TypeServerFail        = "serverFail"
	TypeServerUnavailable = "serverUnavailable"
)

// Request-level problem types from RFC 8620, section 3.6.1.
const (
	ProblemUnknownCapability = "urn:ietf:params:jmap:error:unknownCapability"
	ProblemNotJSON           = "urn:ietf:params:jmap:error:notJSON"
	ProblemNotRequest        = "urn:ietf:params:jmap:error:notRequest"
	ProblemLimit             = "urn:ietf:params:jmap:error:limit"
)

// RequestError is returned when the server rejects a whole request or the
// session lookup with an HTTP status other than 200.
type RequestError struct {
	// Status is the HTTP status code.
	Status int
	// Type is the RFC 7807 problem type, such as [ProblemUnknownCapability],
	// or empty when the body was not a problem document.
	Type string
	// Detail is the server's human-readable explanation, if any.
	Detail string
	// Limit names the limit that was exceeded when Type is [ProblemLimit].
	Limit string
	// RetryAfter is the delay the server asked for in a Retry-After header,
	// or zero.
	RetryAfter time.Duration
}

func (e *RequestError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "jmap: request failed: HTTP %d", e.Status)
	if e.Type != "" {
		fmt.Fprintf(&b, " %s", e.Type)
	}
	if e.Limit != "" {
		fmt.Fprintf(&b, " (limit %s)", e.Limit)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, ": %s", e.Detail)
	}
	return b.String()
}

// Is reports whether e matches one of the sentinel errors.
func (e *RequestError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == 401 || e.Status == 403
	case ErrRateLimit:
		return e.Status == 429 || strings.HasSuffix(e.Type, ":"+TypeRateLimit)
	case ErrUnsupportedCapability:
		return e.Type == ProblemUnknownCapability
	}
	return false
}

// MethodError is a method-level error: the server answered one method call
// with an "error" response (RFC 8620, section 3.6.2).
type MethodError struct {
	// Method is the name of the method that failed, such as
	// "MaskedEmail/get".
	Method string
	// CallID is the method call ID the error answers.
	CallID string
	// Type is the error type, such as [TypeInvalidArguments].
	Type string
	// Description is the server's explanation, if any.
	Description string
}

func (e *MethodError) Error() string {
	msg := fmt.Sprintf("jmap: %s failed: %s", e.methodName(), e.Type)
	if e.Description != "" {
		msg += ": " + e.Description
	}
	return msg
}

func (e *MethodError) methodName() string {
	if e.Method == "" {
		return "method call " + e.CallID
	}
	return e.Method
}

// Is reports whether e matches one of the sentinel errors.
func (e *MethodError) Is(target error) bool {
	switch target {
	case ErrRateLimit:
		return e.Type == TypeRateLimit
	case ErrNotFound:
		return e.Type == TypeAccountNotFound
	}
	return false
}

// SetError reports why a /set call could not create, update or destroy one
// object (RFC 8620, section 5.3).
type SetError struct {
	// ID is the object ID, or the creation ID for a failed create.
	ID string
	// Op is "create", "update" or "destroy".
	Op string
	// Type is the error type, such as [TypeRateLimit] or [TypeNotFound].
	Type string `json:"type"`
	// Description is the server's explanation, if any.
	Description string `json:"description,omitempty"`
	// Properties lists the offending properties when Type is
	// [TypeInvalidProperties].
	Properties []string `json:"properties,omitempty"`
}

func (e *SetError) Error() string {
	msg := fmt.Sprintf("jmap: %s %s failed: %s", e.Op, e.ID, e.Type)
	if len(e.Properties) > 0 {
		msg += " (" + strings.Join(e.Properties, ", ") + ")"
	}
	if e.Description != "" {
		msg += ": " + e.Description
	}
	return msg
}

// Is reports whether e matches one of the sentinel errors.
func (e *SetError) Is(target error) bool {
	switch target {
	case ErrRateLimit:
		return e.Type == TypeRateLimit
	case ErrNotFound:
		return e.Type == TypeNotFound
	}
	return false
}
