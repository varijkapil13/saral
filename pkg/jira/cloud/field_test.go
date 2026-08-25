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

func TestFields_ReadsTheCatalogueASiteSpecificIDIsResolvedThrough(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Fields(t.Context())
	if err != nil {
		t.Fatalf("reading the catalogue: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the catalogue is empty, so no field name can be resolved on this site")
	}

	summary, ok := jira.FieldByName(got, "Summary")
	if !ok {
		t.Fatalf("no field is called Summary: %v", fieldNames(got))
	}
	if summary.ID != "summary" || summary.Custom {
		t.Errorf("Summary reads as %+v, want the system field", summary)
	}
	if summary.Schema.System != "summary" || summary.Schema.Type != "string" {
		t.Errorf("Summary's schema reads as %+v, want the one the site states", summary.Schema)
	}

	// The whole reason this endpoint is called: the ID is site-specific and the
	// name is the only stable handle onto it.
	points, ok := jira.FieldByName(got, "Story Points")
	if !ok {
		t.Fatalf("no field is called Story Points: %v", fieldNames(got))
	}
	switch {
	case !points.Custom:
		t.Errorf("Story Points reads as a system field: %+v", points)
	case points.ID == "" || points.ID == points.Name:
		t.Errorf("Story Points resolved to %q, want the site's own customfield ID", points.ID)
	case points.Schema.CustomID == 0:
		t.Errorf("Story Points carries no customId: %+v", points.Schema)
	case len(points.ClauseNames) == 0:
		t.Error("Story Points carries no clause names, and JQL is written with those")
	}
}

// The catalogue as a German site sends it. A profile is written once and used
// against whatever site is configured, so the name in it is the English display
// name — which is on neither the wire nor a translated screen.
func TestFields_ResolveANameWrittenInEnglishAgainstALocalisedCatalogue(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithFixture(http.MethodGet, fieldPath, "field_localised.json"))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Fields(t.Context())
	if err != nil {
		t.Fatalf("reading the catalogue: %v", err)
	}

	windows, err := jira.ResolveField(got, "Release Windows")
	if err != nil {
		t.Fatalf("resolving the English display name: %v; the catalogue reads %v", err, fieldNames(got))
	}
	if windows.ID != "customfield_10071" {
		t.Errorf("Release Windows resolved to %q, want this site's own ID for it", windows.ID)
	}
	if windows.Name == "Release Windows" {
		t.Error("the catalogue is not the localised one, so this proves nothing")
	}

	points, err := jira.ResolveField(got, "Story Points")
	if err != nil {
		t.Fatalf("resolving an untranslated name: %v", err)
	}
	if points.ID != "customfield_10032" {
		t.Errorf("Story Points resolved to %q", points.ID)
	}

	// Two fields whose display names collapsed into one string under translation.
	var shared *jira.FieldNameError
	if _, err := jira.ResolveField(got, "Restschätzung"); !errors.As(err, &shared) || !shared.Ambiguous() {
		t.Fatalf("resolving a name two fields share gave %v, want an ambiguous *jira.FieldNameError", err)
	}

	forms, err := jira.ResolveField(got, "Intake forms")
	if err != nil {
		t.Fatalf("resolving a field that sends no clause names: %v", err)
	}
	if len(forms.ClauseNames) != 0 {
		t.Errorf("ClauseNames = %v, want none: this field cannot be named in JQL at all", forms.ClauseNames)
	}
}

func TestFields_ReportsARefusalRateLimitAndTransportFailureAsThemselves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opt    jiratest.ServerOption
		assert func(*testing.T, error)
	}{
		{
			name: "the token may not ask",
			opt:  jiratest.WithStatus(http.MethodGet, fieldPath, http.StatusForbidden, ""),
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
			opt:  jiratest.WithRateLimit(http.MethodGet, fieldPath, 30*time.Second),
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
			opt:  jiratest.WithStatus(http.MethodGet, fieldPath, http.StatusInternalServerError, ""),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var broken *jira.TransportError
				if !errors.As(err, &broken) {
					t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
				}
			},
		},
		{
			name: "the answer is not the array this expects",
			opt: jiratest.WithHandler(http.MethodGet, fieldPath, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"values":[]}`))
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
			got, err := c.Fields(t.Context())
			if err == nil {
				t.Fatalf("the failure came back as %d fields", len(got))
			}
			tt.assert(t, err)
		})
	}
}

func TestFields_HonoursACancelledContext(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c, _ := testClient(t, s.URL())
	if _, err := c.Fields(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the cancellation the caller asked for", err)
	}
}

func fieldNames(fields []jira.Field) []string {
	out := make([]string, 0, len(fields))
	for i := range fields {
		out = append(out, fields[i].Name)
	}
	return out
}
