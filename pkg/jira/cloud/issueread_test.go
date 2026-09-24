package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const issueFieldsAnswer = `{"id":"10001","key":"` + testIssueKey + `","fields":{"summary":"Something to do"}}`

func query(t *testing.T, raw string) url.Values {
	t.Helper()

	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("reading the query sent: %v", err)
	}
	return v
}

func TestIssueFields_AsksForTheFieldsNamedJoinedByCommas(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, issueRoute,
		jsonHandler(http.StatusOK, issueFieldsAnswer)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.IssueFields(t.Context(), testIssueKey, []string{"summary", "status", "assignee"}); err != nil {
		t.Fatalf("IssueFields: %v", err)
	}

	sent := sentTo(t, s, http.MethodGet, "/rest/api/3/issue/"+testIssueKey)
	if got := query(t, sent.Query).Get("fields"); got != "summary,status,assignee" {
		t.Errorf("fields = %q, want the three fields joined by commas in the order they were asked", got)
	}
	if query(t, sent.Query).Has("expand") {
		t.Errorf("query = %q, a read of only system fields does not need the schema expanded", sent.Query)
	}
}

func TestIssueFields_ExpandsSchemaOnlyWhenACustomFieldIsNamed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fields     []string
		wantExpand bool
	}{
		{name: "only system fields", fields: []string{"summary", "status"}, wantExpand: false},
		{name: "a custom field among them", fields: []string{"summary", "customfield_10010"}, wantExpand: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, issueRoute,
				jsonHandler(http.StatusOK, issueFieldsAnswer)))
			defer s.Close()

			c, _ := testClient(t, s.URL())
			if _, err := c.IssueFields(t.Context(), testIssueKey, tc.fields); err != nil {
				t.Fatalf("IssueFields: %v", err)
			}
			sent := sentTo(t, s, http.MethodGet, "/rest/api/3/issue/"+testIssueKey)
			got := query(t, sent.Query).Has("expand")
			if got != tc.wantExpand {
				t.Errorf("expand present = %v, want %v for %v", got, tc.wantExpand, tc.fields)
			}
		})
	}
}

func TestIssueFields_RequestedMaskEqualsWhatWasAsked(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, issueRoute,
		jsonHandler(http.StatusOK, issueFieldsAnswer)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.IssueFields(t.Context(), testIssueKey, []string{"status", "summary"})
	if err != nil {
		t.Fatalf("IssueFields: %v", err)
	}
	if got.Requested.Wide() {
		t.Error("a narrow read reports Requested as wide")
	}
	want := []string{"status", "summary"}
	if ids := got.Requested.IDs(); len(ids) != len(want) || !got.Requested.Has("status") || !got.Requested.Has("summary") {
		t.Errorf("Requested = %v, want exactly %v", ids, want)
	}
}

func TestIssueFields_RefusesAnEmptyFieldListWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		fields []string
	}{
		{name: "nil", fields: nil},
		{name: "blank entries", fields: []string{"", "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer()
			defer s.Close()

			c, _ := testClient(t, s.URL())
			_, err := c.IssueFields(t.Context(), testIssueKey, tc.fields)

			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, ok := invalid.For("fields"); !ok {
				t.Errorf("the failure does not name fields: %v", invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests for a read that names no field", served)
			}
		})
	}
}

func TestIssueFields_A404NamesTheIssue(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, issueRoute,
		jsonHandler(http.StatusNotFound, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.IssueFields(t.Context(), testIssueKey, []string{"summary"})

	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "issue" || missing.ID != testIssueKey {
		t.Errorf("the failure names %s %s rather than the issue", missing.Kind, missing.ID)
	}
}

func TestIssueFields_A403IsACapabilityAnswer(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, issueRoute,
		jsonHandler(http.StatusForbidden, `{"errorMessages":["You do not have permission to see this issue."]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.IssueFields(t.Context(), testIssueKey, []string{"summary"})

	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
}

func TestIssueFields_A429CarriesTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithRateLimit(http.MethodGet, issueRoute, 30*time.Second))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.IssueFields(t.Context(), testIssueKey, []string{"summary"})

	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
	}
	if limited.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", limited.RetryAfter)
	}
}

func TestIssueFields_ABadGatewayIsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, issueRoute,
		jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.IssueFields(t.Context(), testIssueKey, []string{"summary"})

	var down *jira.TransportError
	if !errors.As(err, &down) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if down.Status != http.StatusBadGateway {
		t.Errorf("Status = %d, want %d", down.Status, http.StatusBadGateway)
	}
}

func TestIssueFields_ReturnsTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, issueRoute, func(_ http.ResponseWriter, r *http.Request) {
		announce()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer closeServer(t, s)
	defer letGo()

	c, _ := testClient(t, s.URL())
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	go func() {
		_, err := c.IssueFields(ctx, testIssueKey, []string{"summary"})
		failed <- err
	}()

	receive(t, "the request to reach the site", arrived)
	cancel()
	if err := receive(t, "the cancelled call to come back", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
}
