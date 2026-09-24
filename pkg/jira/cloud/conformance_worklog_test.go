package cloud

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions about time logged on an issue, run against both
// adapters. Worklogs is a third pagination envelope beside search's and
// comment's, so the walk itself is worth conforming and not only the values.

func worklogFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.Worklogger {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

func TestWorklogs_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	t.Run("logging time then reading it back names every domain field", func(t *testing.T) {
		t.Parallel()

		started := time.Date(2026, time.February, 10, 9, 0, 0, 0, time.UTC)
		in := jira.WorklogInput{Spent: 90 * time.Minute, Started: started, Comment: paragraph("Wired the feeder.")}

		for _, adapter := range []struct {
			name string
			open func(*testing.T) jira.Worklogger
		}{
			{name: "cloud", open: func(t *testing.T) jira.Worklogger { return worklogFromSite(t) }},
			{name: "fake", open: func(t *testing.T) jira.Worklogger { return conformFake(t, jiratest.WithIssues(conformOneIssue())) }},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				c := adapter.open(t)
				key := conformIssueKey(adapter.name)
				added, err := c.AddWorklog(t.Context(), key, in)
				if err != nil {
					t.Fatalf("AddWorklog: %v", err)
				}
				if added.ID == "" {
					t.Error("the stored worklog has no id")
				}
				if added.Spent <= 0 {
					t.Errorf("Spent = %v, want a positive amount of time", added.Spent)
				}
				if added.Author.AccountID == "" || added.Author.DisplayName == "" {
					t.Errorf("the author is %+v, want an identified account", added.Author)
				}
				if added.Comment.IsZero() {
					t.Error("the comment did not round-trip")
				}
				if added.Created.IsZero() || added.Updated.IsZero() {
					t.Errorf("Created/Updated are unset: %+v", added)
				}

				page, err := c.Worklogs(t.Context(), key)
				if err != nil {
					t.Fatalf("Worklogs: %v", err)
				}
				got, err := jira.Collect(t.Context(), page, 0)
				if err != nil {
					t.Fatalf("walking the worklogs: %v", err)
				}
				if len(got) == 0 {
					t.Fatal("the issue just logged against carries no worklogs")
				}
			})
		}
	})

	t.Run("an issue with nothing logged is a well-formed empty page", func(t *testing.T) {
		t.Parallel()

		for _, adapter := range []struct {
			name string
			open func(*testing.T) jira.Worklogger
		}{
			{
				name: "cloud",
				open: func(t *testing.T) jira.Worklogger {
					return worklogFromSite(t, jiratest.WithFixture(http.MethodGet, worklogRoute, "worklogs_empty.json"))
				},
			},
			{
				name: "fake",
				open: func(t *testing.T) jira.Worklogger { return conformFake(t, jiratest.WithIssues(conformOneIssue())) },
			},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				page, err := adapter.open(t).Worklogs(t.Context(), conformIssueKey(adapter.name))
				if err != nil {
					t.Fatalf("Worklogs: %v", err)
				}
				if len(page.Items) != 0 {
					t.Errorf("got %d items, want none", len(page.Items))
				}
				if page.HasMore() {
					t.Error("an empty, exhausted page claims another one")
				}
			})
		}
	})

	t.Run("an issue that does not exist is not found", func(t *testing.T) {
		t.Parallel()

		for _, adapter := range []struct {
			name string
			open func(*testing.T) jira.Worklogger
		}{
			{
				name: "cloud",
				open: func(t *testing.T) jira.Worklogger {
					return worklogFromSite(t, jiratest.WithHandler(http.MethodGet, worklogRoute,
						jsonHandler(http.StatusNotFound, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`)))
				},
			},
			{name: "fake", open: func(t *testing.T) jira.Worklogger { return conformFake(t) }},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				_, err := adapter.open(t).Worklogs(t.Context(), "NOSUCH-1")
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
			open func(*testing.T) jira.Worklogger
		}{
			{
				name: "cloud",
				open: func(t *testing.T) jira.Worklogger {
					return worklogFromSite(t, jiratest.WithRateLimit(http.MethodGet, worklogRoute, 30*time.Second))
				},
			},
			{
				name: "fake",
				open: func(t *testing.T) jira.Worklogger {
					f := conformFake(t, jiratest.WithIssues(conformOneIssue()))
					f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
					return f
				},
			},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				c := adapter.open(t)
				_, err := c.Worklogs(t.Context(), conformIssueKey(adapter.name))
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
			})
		}
	})

	t.Run("a capability refusal is a capability refusal", func(t *testing.T) {
		t.Parallel()

		for _, adapter := range []struct {
			name string
			open func(*testing.T) jira.Worklogger
		}{
			{
				name: "cloud",
				open: func(t *testing.T) jira.Worklogger {
					return worklogFromSite(t, jiratest.WithHandler(http.MethodGet, worklogRoute,
						jsonHandler(http.StatusForbidden, `{"errorMessages":["You do not have permission to log work on this issue."]}`)))
				},
			},
			{
				name: "fake",
				open: func(t *testing.T) jira.Worklogger {
					f := conformFake(t, jiratest.WithIssues(conformOneIssue()))
					f.FailNext(&jira.CapabilityError{Reason: "this token cannot log work"})
					return f
				},
			},
		} {
			t.Run(adapter.name, func(t *testing.T) {
				t.Parallel()

				c := adapter.open(t)
				_, err := c.Worklogs(t.Context(), conformIssueKey(adapter.name))
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
			})
		}
	})

	t.Run("both adapters refuse the same input a worklog can never hold", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			in    jira.WorklogInput
			field string
		}{
			{name: "no time spent", in: jira.WorklogInput{Started: time.Now()}, field: "timeSpentSeconds"},
			{name: "no start time", in: jira.WorklogInput{Spent: time.Minute}, field: "started"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				for _, adapter := range []struct {
					name string
					open func(*testing.T) jira.Worklogger
				}{
					{name: "cloud", open: func(t *testing.T) jira.Worklogger { return worklogFromSite(t) }},
					{
						name: "fake",
						open: func(t *testing.T) jira.Worklogger {
							return conformFake(t, jiratest.WithIssues(conformOneIssue()))
						},
					},
				} {
					t.Run(adapter.name, func(t *testing.T) {
						t.Parallel()

						c := adapter.open(t)
						_, err := c.AddWorklog(t.Context(), conformIssueKey(adapter.name), tc.in)
						var invalid *jira.ValidationError
						if !errors.As(err, &invalid) {
							t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
						}
						if _, ok := invalid.For(tc.field); !ok {
							t.Errorf("the failure does not name %s: %v", tc.field, invalid)
						}
					})
				}
			})
		}
	})
}

// conformOneIssue is one issue both adapters can address, under the fake's own
// key since the fixture site answers whatever key the request carried.
func conformOneIssue() []jira.Issue {
	return []jira.Issue{{
		ID:      "20001",
		Key:     conformProject + "-1",
		Project: jira.ProjectRef{Key: conformProject},
		Summary: "Something to log time against",
	}}
}

// conformIssueKey is the key each adapter's own site answers to: the fixture
// server ignores the key on the path and always replays EX-1, and the fake only
// knows the issue it was seeded with.
func conformIssueKey(adapterName string) string {
	if adapterName == "cloud" {
		return testIssueKey
	}
	return conformProject + "-1"
}
