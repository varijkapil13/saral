package cloud

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestEditMeta_DecodesEachFieldWithTheScreenOrderThatOrderPreserved(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()
	c, _ := testClient(t, s.URL())

	got, err := c.EditMeta(t.Context(), "EX-1")
	if err != nil {
		t.Fatalf("reading the edit screen: %v", err)
	}
	if len(got.Fields) != 3 {
		t.Fatalf("got %d fields, want 3: %v", len(got.Fields), fieldMetaIDs(got.Fields))
	}

	points := got.Fields[0]
	if points.Field.ID != "customfield_10032" || points.Field.Name != "Story Points" {
		t.Errorf("field 0 reads as %+v, want Story Points by that id", points)
	}
	if points.Field.Schema.CustomID != 10032 || points.Field.Schema.Type != "number" {
		t.Errorf("Story Points' schema reads as %+v", points.Field.Schema)
	}
	if points.Required {
		t.Error("Story Points reads as required, and the fixture says it is not")
	}

	summary := got.Fields[1]
	if summary.Field.ID != "summary" || !summary.Required {
		t.Errorf("field 1 reads as %+v, want the required Summary field", summary)
	}

	if at, on := got.Order("labels"); !on || at != 2 {
		t.Errorf("Order(labels) = %d, %v, want 2, true", at, on)
	}
	if _, on := got.Order("customfield_99999"); on {
		t.Error("a field this screen never named reads as on it")
	}
}

func TestEditMeta_ReportsARefusalRateLimitAndTransportFailureAsThemselves(t *testing.T) {
	t.Parallel()

	const path = "/rest/api/3/issue/EX-1/editmeta"
	tests := []struct {
		name   string
		opt    jiratest.ServerOption
		assert func(*testing.T, error)
	}{
		{
			name: "the token may not ask",
			opt:  jiratest.WithStatus(http.MethodGet, path, http.StatusForbidden, ""),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
			},
		},
		{
			name: "the site is rate limiting",
			opt:  jiratest.WithRateLimit(http.MethodGet, path, 30*time.Second),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
			},
		},
		{
			name: "the site broke",
			opt:  jiratest.WithStatus(http.MethodGet, path, http.StatusInternalServerError, ""),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var broken *jira.TransportError
				if !errors.As(err, &broken) {
					t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
				}
			},
		},
		{
			name: "the fields object is not the JSON this client expected",
			opt: jiratest.WithHandler(http.MethodGet, path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"fields": [1, 2]}`))
			}),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var broken *jira.TransportError
				if !errors.As(err, &broken) {
					t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(tt.opt)
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			got, err := c.EditMeta(t.Context(), "EX-1")
			if err == nil {
				t.Fatalf("the failure came back as %d fields", len(got.Fields))
			}
			tt.assert(t, err)
		})
	}
}

func TestEditMeta_HonoursACancelledContext(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c, _ := testClient(t, s.URL())
	if _, err := c.EditMeta(ctx, "EX-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the cancellation the caller asked for", err)
	}
}

func TestEditMeta_RefusesAnEmptyKey(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()
	c, _ := testClient(t, s.URL())

	var invalid *jira.ValidationError
	if _, err := c.EditMeta(t.Context(), "  "); !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
}
