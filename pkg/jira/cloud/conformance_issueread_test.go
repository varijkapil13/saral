package cloud

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func issueFieldsFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.IssueReader {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

// conformNarrowBody is what a site answers a request for only the summary
// with: no description in it at all, which is what proves the narrowness
// rather than a mask the site never saw.
const conformNarrowBody = `{"id":"20001","key":"` + testIssueKey + `","fields":{"summary":"Narrow read"}}`

func TestIssueFields_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	t.Run("a narrow read carries only what was asked", func(t *testing.T) {
		t.Parallel()

		for _, adapter := range []struct {
			name string
			open func(*testing.T) jira.IssueReader
		}{
			{
				name: "cloud",
				open: func(t *testing.T) jira.IssueReader {
					return issueFieldsFromSite(t, jiratest.WithHandler(http.MethodGet, issueRoute,
						jsonHandler(http.StatusOK, conformNarrowBody)))
				},
			},
			{
				name: "fake",
				open: func(t *testing.T) jira.IssueReader {
					return conformFake(t, jiratest.WithIssues([]jira.Issue{{
						ID: "20001", Key: conformProject + "-1", Project: jira.ProjectRef{Key: conformProject},
						Summary:     "Narrow read",
						Description: paragraph("Not asked for."),
					}}))
				},
			},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				c := adapter.open(t)
				got, err := c.IssueFields(t.Context(), conformIssueKey(adapter.name), []string{"summary"})
				if err != nil {
					t.Fatalf("IssueFields: %v", err)
				}
				if got.Requested.Wide() {
					t.Error("a narrow read reports Requested as wide")
				}
				if !got.Requested.Has("summary") || got.Requested.Len() != 1 {
					t.Errorf("Requested = %v, want exactly [summary]", got.Requested.IDs())
				}
				if !got.Description.IsZero() {
					t.Error("a field nobody asked for reached the caller")
				}
				if got.Summary == "" {
					t.Error("the one field asked for did not reach the caller")
				}
			})
		}
	})

	t.Run("a read naming no field is refused on both, without a request", func(t *testing.T) {
		t.Parallel()

		for _, adapter := range []struct {
			name string
			open func(*testing.T) jira.IssueReader
		}{
			{name: "cloud", open: func(t *testing.T) jira.IssueReader { return issueFieldsFromSite(t) }},
			{name: "fake", open: func(t *testing.T) jira.IssueReader { return conformFake(t) }},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				_, err := adapter.open(t).IssueFields(t.Context(), conformIssueKey(adapter.name), nil)
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
				if _, ok := invalid.For("fields"); !ok {
					t.Errorf("the failure does not name fields: %v", invalid)
				}
			})
		}
	})

	t.Run("an issue that does not exist is not found", func(t *testing.T) {
		t.Parallel()

		for _, adapter := range []struct {
			name string
			open func(*testing.T) jira.IssueReader
		}{
			{
				name: "cloud",
				open: func(t *testing.T) jira.IssueReader {
					return issueFieldsFromSite(t, jiratest.WithHandler(http.MethodGet, issueRoute,
						jsonHandler(http.StatusNotFound, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`)))
				},
			},
			{name: "fake", open: func(t *testing.T) jira.IssueReader { return conformFake(t) }},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				_, err := adapter.open(t).IssueFields(t.Context(), "NOSUCH-1", []string{"summary"})
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
			})
		}
	})

	t.Run("a rate limit is a rate limit", func(t *testing.T) {
		t.Parallel()

		for _, adapter := range []struct {
			name string
			open func(*testing.T) jira.IssueReader
		}{
			{
				name: "cloud",
				open: func(t *testing.T) jira.IssueReader {
					return issueFieldsFromSite(t, jiratest.WithRateLimit(http.MethodGet, issueRoute, 30*time.Second))
				},
			},
			{
				name: "fake",
				open: func(t *testing.T) jira.IssueReader {
					f := conformFake(t, jiratest.WithIssues([]jira.Issue{{
						ID: "20001", Key: conformProject + "-1", Project: jira.ProjectRef{Key: conformProject}, Summary: "x",
					}}))
					f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
					return f
				},
			},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				c := adapter.open(t)
				_, err := c.IssueFields(t.Context(), conformIssueKey(adapter.name), []string{"summary"})
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
			})
		}
	})
}
