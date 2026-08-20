package jira_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

var errCause = errors.New("jira_test: the underlying cause")

func TestTypedErrors_RenderTheWordingTheUIShows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "a rate limit that carried a Retry-After",
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/rest/api/3/search/jql"},
			want: "rate limited by Jira, retry in 30s",
		},
		{
			name: "a rate limit whose Retry-After is not a whole second",
			err:  &jira.RateLimitError{RetryAfter: 1500 * time.Millisecond},
			want: "rate limited by Jira, retry in 2s",
		},
		{
			name: "a rate limit with no Retry-After",
			err:  &jira.RateLimitError{},
			want: "rate limited by Jira",
		},
		{
			name: "a capability with a reason from the probe",
			err:  &jira.CapabilityError{Capability: jira.CapPlans, Reason: "the Plans API needs Administer Jira"},
			want: "the Plans API needs Administer Jira",
		},
		{
			name: "a capability with no reason falls back to its key",
			err:  &jira.CapabilityError{Capability: jira.CapBulkMove},
			want: "bulk-move is not available",
		},
		{
			name: "a validation failure with field messages and loose ones",
			err: &jira.ValidationError{
				Fields: []jira.FieldError{
					{Field: "summary", Message: "is required"},
					{Field: "duedate", Message: "is not a date"},
				},
				Messages: []string{"the issue type is not valid for this project"},
			},
			want: "summary: is required; duedate: is not a date; the issue type is not valid for this project",
		},
		{
			name: "a validation failure Jira described in no detail",
			err:  &jira.ValidationError{},
			want: "the request was rejected as invalid",
		},
		{
			name: "a conflict with a detail",
			err:  &jira.ConflictError{Resource: "PROJ-1", Detail: "the sprint was closed"},
			want: "PROJ-1 was modified by someone else: the sprint was closed",
		},
		{
			name: "a conflict with no detail",
			err:  &jira.ConflictError{Resource: "PROJ-1"},
			want: "PROJ-1 was modified by someone else",
		},
		{
			name: "a transport failure with a status and a cause",
			err:  &jira.TransportError{Op: "search", Status: 503, Err: errCause},
			want: "search failed with HTTP 503: jira_test: the underlying cause",
		},
		{
			name: "a transport failure with only a status",
			err:  &jira.TransportError{Op: "search", Status: 502},
			want: "search failed with HTTP 502",
		},
		{
			name: "a transport failure with only a cause",
			err:  &jira.TransportError{Op: "search", Err: errCause},
			want: "search failed: jira_test: the underlying cause",
		},
		{
			name: "a 404, which Jira also uses for hidden resources",
			err:  &jira.NotFoundError{Kind: "issue", ID: "PROJ-1"},
			want: "issue PROJ-1 does not exist, or you cannot see it",
		},
		{
			name: "an auth failure with a reason",
			err:  &jira.AuthError{Reason: "the token was revoked"},
			want: "authentication failed: the token was revoked",
		},
		{
			name: "an auth failure with no reason",
			err:  &jira.AuthError{},
			want: "authentication failed: check the site, email and API token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTypedErrors_SurviveBeingWrapped(t *testing.T) {
	t.Parallel()

	rateLimit := &jira.RateLimitError{RetryAfter: 42 * time.Second}
	capability := &jira.CapabilityError{Capability: jira.CapBoards, Reason: "the project has no board"}
	validation := &jira.ValidationError{Fields: []jira.FieldError{{Field: "summary", Message: "is required"}}}
	conflict := &jira.ConflictError{Resource: "sprint 4"}
	transport := &jira.TransportError{Op: "board configuration", Status: 500, Err: errCause}
	notFound := &jira.NotFoundError{Kind: "board", ID: "17"}
	auth := &jira.AuthError{Reason: "the email does not match the token"}

	tests := []struct {
		name  string
		err   error
		found func(error) bool
	}{
		{"rate limit", rateLimit, func(err error) bool {
			var got *jira.RateLimitError
			return errors.As(err, &got) && got == rateLimit
		}},
		{"capability", capability, func(err error) bool {
			var got *jira.CapabilityError
			return errors.As(err, &got) && got == capability
		}},
		{"validation", validation, func(err error) bool {
			var got *jira.ValidationError
			return errors.As(err, &got) && got == validation
		}},
		{"conflict", conflict, func(err error) bool {
			var got *jira.ConflictError
			return errors.As(err, &got) && got == conflict
		}},
		{"transport", transport, func(err error) bool {
			var got *jira.TransportError
			return errors.As(err, &got) && got == transport
		}},
		{"not found", notFound, func(err error) bool {
			var got *jira.NotFoundError
			return errors.As(err, &got) && got == notFound
		}},
		{"auth", auth, func(err error) bool {
			var got *jira.AuthError
			return errors.As(err, &got) && got == auth
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.found(tt.err) {
				t.Error("errors.As did not find the error in itself")
			}
			wrapped := fmt.Errorf("loading the board: %w", fmt.Errorf("fetching page 2: %w", tt.err))
			if !tt.found(wrapped) {
				t.Errorf("errors.As did not find the error in %q", wrapped)
			}
		})
	}
}

func TestTypedErrors_AreNotMistakenForOneAnother(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("searching: %w", &jira.NotFoundError{Kind: "issue", ID: "PROJ-1"})

	var conflict *jira.ConflictError
	if errors.As(wrapped, &conflict) {
		t.Errorf("errors.As found a *ConflictError in %q", wrapped)
	}
	var rateLimit *jira.RateLimitError
	if errors.As(wrapped, &rateLimit) {
		t.Errorf("errors.As found a *RateLimitError in %q", wrapped)
	}
	var notFound *jira.NotFoundError
	if !errors.As(wrapped, &notFound) {
		t.Errorf("errors.As did not find the *NotFoundError in %q", wrapped)
	}
}

func TestTransportError_UnwrapsToItsCause(t *testing.T) {
	t.Parallel()

	err := &jira.TransportError{Op: "search", Status: 500, Err: errCause}
	if !errors.Is(err, errCause) {
		t.Errorf("errors.Is(%v, errCause) = false, want true", err)
	}
	if got := errors.Unwrap(err); !errors.Is(got, errCause) {
		t.Errorf("Unwrap() = %v, want errCause", got)
	}
	if got := errors.Unwrap(&jira.TransportError{Op: "search", Status: 500}); got != nil {
		t.Errorf("Unwrap() = %v, want nil when there was no cause", got)
	}
}

func TestValidationError_ForFindsTheMessageAttachedToAField(t *testing.T) {
	t.Parallel()

	err := &jira.ValidationError{
		Fields: []jira.FieldError{
			{Field: "summary", Message: "is required"},
			{Field: "customfield_10016", Message: "must be a number"},
		},
		Messages: []string{"you do not have permission to set the assignee"},
	}

	tests := []struct {
		name  string
		field string
		want  string
		found bool
	}{
		{name: "a field Jira complained about", field: "summary", want: "is required", found: true},
		{name: "a custom field Jira complained about", field: "customfield_10016", want: "must be a number", found: true},
		{name: "a field Jira accepted", field: "description", found: false},
		{name: "the empty field name", field: "", found: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := err.For(tt.field)
			if ok != tt.found || got != tt.want {
				t.Errorf("For(%q) = (%q, %t), want (%q, %t)", tt.field, got, ok, tt.want, tt.found)
			}
		})
	}
}

func TestRetryAfter_AnswersOnlyForARateLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		err   error
		want  time.Duration
		limit bool
	}{
		{
			name:  "a rate limit that carried a Retry-After",
			err:   &jira.RateLimitError{RetryAfter: 90 * time.Second},
			want:  90 * time.Second,
			limit: true,
		},
		{
			name:  "a wrapped rate limit",
			err:   fmt.Errorf("searching: %w", &jira.RateLimitError{RetryAfter: 5 * time.Second}),
			want:  5 * time.Second,
			limit: true,
		},
		{
			name:  "a rate limit with no Retry-After is still a rate limit",
			err:   &jira.RateLimitError{},
			limit: true,
		},
		{name: "another typed error", err: &jira.AuthError{}, limit: false},
		{name: "a plain error", err: errCause, limit: false},
		{name: "no error at all", err: nil, limit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := jira.RetryAfter(tt.err)
			if ok != tt.limit || got != tt.want {
				t.Errorf("RetryAfter(%v) = (%s, %t), want (%s, %t)", tt.err, got, ok, tt.want, tt.limit)
			}
		})
	}
}

func TestReason_ExplainsEveryErrorAndSaysWhichAreOurs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		err   error
		want  string
		typed bool
	}{
		{
			name:  "a rate limit",
			err:   &jira.RateLimitError{RetryAfter: 30 * time.Second},
			want:  "rate limited by Jira, retry in 30s",
			typed: true,
		},
		{
			name:  "a capability",
			err:   &jira.CapabilityError{Capability: jira.CapPlans, Reason: "needs Administer Jira"},
			want:  "needs Administer Jira",
			typed: true,
		},
		{
			name:  "a validation failure",
			err:   &jira.ValidationError{Fields: []jira.FieldError{{Field: "summary", Message: "is required"}}},
			want:  "summary: is required",
			typed: true,
		},
		{
			name:  "a conflict",
			err:   &jira.ConflictError{Resource: "PROJ-1"},
			want:  "PROJ-1 was modified by someone else",
			typed: true,
		},
		{
			name:  "a transport failure",
			err:   &jira.TransportError{Op: "search", Status: 503},
			want:  "search failed with HTTP 503",
			typed: true,
		},
		{
			name:  "a 404",
			err:   &jira.NotFoundError{Kind: "issue", ID: "PROJ-1"},
			want:  "issue PROJ-1 does not exist, or you cannot see it",
			typed: true,
		},
		{
			name:  "an auth failure",
			err:   &jira.AuthError{},
			want:  "authentication failed: check the site, email and API token",
			typed: true,
		},
		{
			name:  "a wrapped typed error keeps the wrapper's wording",
			err:   fmt.Errorf("loading issues: %w", &jira.NotFoundError{Kind: "issue", ID: "PROJ-1"}),
			want:  "loading issues: issue PROJ-1 does not exist, or you cannot see it",
			typed: true,
		},
		{
			name:  "an error from somewhere else is still explained",
			err:   errCause,
			want:  "jira_test: the underlying cause",
			typed: false,
		},
		{name: "no error at all", err: nil, want: "", typed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := jira.Reason(tt.err)
			if got != tt.want || ok != tt.typed {
				t.Errorf("Reason(%v) = (%q, %t), want (%q, %t)", tt.err, got, ok, tt.want, tt.typed)
			}
			if tt.err != nil && got == "" {
				t.Error("Reason returned an empty string for a non-nil error")
			}
		})
	}
}
