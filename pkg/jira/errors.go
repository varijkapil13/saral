package jira

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// RateLimitError reports that Jira refused the request under its cost-based rate
// limit. RetryAfter carries the value of the Retry-After header when the server
// sent one, and is zero when it did not.
type RateLimitError struct {
	RetryAfter time.Duration
	Endpoint   string
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited by Jira, retry in %s", e.RetryAfter.Round(time.Second))
	}
	return "rate limited by Jira"
}

// CapabilityError reports that the site or the token cannot do this, and why.
// It is the typed form of a 403: an answer about what is possible, not a fault.
type CapabilityError struct {
	Capability CapabilityKey
	Reason     string
}

func (e *CapabilityError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("%s is not available", e.Capability)
	}
	return e.Reason
}

// FieldError is one field-scoped message out of a validation failure.
type FieldError struct {
	Field   string
	Message string
}

// ValidationError reports that Jira rejected the values in a request. Fields
// holds the per-field messages in the order Jira returned them so a form can
// annotate the offending widgets; Messages holds the ones with no field.
type ValidationError struct {
	Fields   []FieldError
	Messages []string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields)+len(e.Messages))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+": "+f.Message)
	}
	parts = append(parts, e.Messages...)
	if len(parts) == 0 {
		return "the request was rejected as invalid"
	}
	return strings.Join(parts, "; ")
}

// For returns the message attached to a field, if there is one.
func (e *ValidationError) For(field string) (string, bool) {
	for _, f := range e.Fields {
		if f.Field == field {
			return f.Message, true
		}
	}
	return "", false
}

// ConflictError reports that the resource changed underneath us and the write
// was refused. The caller is expected to offer reload-and-reapply.
type ConflictError struct {
	Resource string
	Detail   string
}

func (e *ConflictError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s was modified by someone else", e.Resource)
	}
	return fmt.Sprintf("%s was modified by someone else: %s", e.Resource, e.Detail)
}

// TransportError reports that the request produced no usable answer: the
// connection failed, it timed out, or the server returned 5xx. Status is the
// HTTP status when there was one and zero otherwise. Callers keep showing
// cached data and retry in the background.
type TransportError struct {
	Op     string
	Status int
	Err    error
}

func (e *TransportError) Error() string {
	switch {
	case e.Status != 0 && e.Err != nil:
		return fmt.Sprintf("%s failed with HTTP %d: %v", e.Op, e.Status, e.Err)
	case e.Status != 0:
		return fmt.Sprintf("%s failed with HTTP %d", e.Op, e.Status)
	default:
		return fmt.Sprintf("%s failed: %v", e.Op, e.Err)
	}
}

func (e *TransportError) Unwrap() error { return e.Err }

// NotFoundError reports a 404, which for Jira also covers "exists but you may
// not see it" — the API deliberately does not distinguish the two.
type NotFoundError struct {
	Kind string
	ID   string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %s does not exist, or you cannot see it", e.Kind, e.ID)
}

// AuthError reports that the credentials were rejected (401), which for an API
// token means the token is wrong, revoked, or paired with the wrong email.
type AuthError struct {
	Reason string
}

func (e *AuthError) Error() string {
	if e.Reason == "" {
		return "authentication failed: check the site, email and API token"
	}
	return "authentication failed: " + e.Reason
}

// RetryAfter reports how long to wait before retrying, if err is a rate limit.
func RetryAfter(err error) (time.Duration, bool) {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return rl.RetryAfter, true
	}
	return 0, false
}

// Reason returns the human-readable explanation to put in front of the user for
// any error from this package, and reports whether err was one of its typed
// kinds. It never returns an empty string for a non-nil error.
func Reason(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var (
		rl   *RateLimitError
		capa *CapabilityError
		ve   *ValidationError
		ce   *ConflictError
		te   *TransportError
		nf   *NotFoundError
		ae   *AuthError
	)
	switch {
	case errors.As(err, &rl), errors.As(err, &capa), errors.As(err, &ve),
		errors.As(err, &ce), errors.As(err, &te), errors.As(err, &nf), errors.As(err, &ae):
		return err.Error(), true
	}
	return err.Error(), false
}
