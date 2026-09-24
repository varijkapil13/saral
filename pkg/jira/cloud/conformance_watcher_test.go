package cloud

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for who watches an issue.
// The two sites cannot agree on who is watching what, so the properties asserted
// are that a populated list never names more people than its own count, that no
// row that reaches a caller is blank, and that every typed refusal reads the
// same regardless of which site produced it.

type watcherBuilder func(*testing.T) jira.WatcherManager

func watchersFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.WatcherManager {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c
}

func conformFakeWithWatchableIssue(t *testing.T) *jiratest.Fake {
	t.Helper()
	return conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 1)))
}

func conformNoBlankWatcher(t *testing.T, got jira.WatcherList) {
	t.Helper()

	if len(got.People) > got.Count {
		t.Errorf("got %d named watchers for a count of %d; People cannot outnumber Count", len(got.People), got.Count)
	}
	for _, u := range got.People {
		if u.AccountID == "" || u.DisplayName == "" {
			t.Errorf("a watcher with nothing on it reached the caller: %+v", u)
		}
	}
}

func TestWatchers_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cloud watcherBuilder
		fake  watcherBuilder
		run   func(*testing.T, jira.WatcherManager)
	}{
		{
			name:  "a populated list names who is watching, in the count",
			cloud: func(t *testing.T) jira.WatcherManager { return watchersFromSite(t) },
			fake: func(t *testing.T) jira.WatcherManager {
				f := conformFakeWithWatchableIssue(t)
				if err := f.Watch(t.Context(), conformProject+"-1", ""); err != nil {
					t.Fatalf("seeding a watcher: %v", err)
				}
				return f
			},
			run: func(t *testing.T, c jira.WatcherManager) {
				t.Helper()
				got, err := c.Watchers(t.Context(), testIssueKey)
				if err != nil {
					t.Fatalf("reading who watches: %v", err)
				}
				if got.Count == 0 {
					t.Fatal("this case is about a watched issue, and the count came back zero")
				}
				if len(got.People) == 0 {
					t.Fatal("this case is about a populated list, and nobody was named")
				}
				conformNoBlankWatcher(t, got)
			},
		},
		{
			name: "a well-formed empty or hidden list is still a count with nobody named",
			cloud: func(t *testing.T) jira.WatcherManager {
				return watchersFromSite(t, jiratest.WithFixture(http.MethodGet, watchersRoute, "watchers_hidden.json"))
			},
			fake: func(t *testing.T) jira.WatcherManager { return conformFakeWithWatchableIssue(t) },
			run: func(t *testing.T, c jira.WatcherManager) {
				t.Helper()
				got, err := c.Watchers(t.Context(), testIssueKey)
				if err != nil {
					t.Fatalf("reading who watches: %v", err)
				}
				if len(got.People) != 0 {
					t.Errorf("this case is about nobody being named, and %+v was", got.People)
				}
				conformNoBlankWatcher(t, got)
			},
		},
		{
			name: "an issue nobody has is a 404",
			cloud: func(t *testing.T) jira.WatcherManager {
				return watchersFromSite(t, jiratest.WithStatus(http.MethodGet, watchersRoute, http.StatusNotFound, ""))
			},
			fake: func(t *testing.T) jira.WatcherManager { return conformFake(t) },
			run: func(t *testing.T, c jira.WatcherManager) {
				t.Helper()
				_, err := c.Watchers(t.Context(), "NOSUCH-1")
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
			},
		},
		{
			name: "a rate limit reads as one on both sites",
			cloud: func(t *testing.T) jira.WatcherManager {
				return watchersFromSite(t, jiratest.WithRateLimit(http.MethodGet, watchersRoute, 30*time.Second))
			},
			fake: func(t *testing.T) jira.WatcherManager {
				f := conformFakeWithWatchableIssue(t)
				f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
				return f
			},
			run: func(t *testing.T, c jira.WatcherManager) {
				t.Helper()
				_, err := c.Watchers(t.Context(), testIssueKey)
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
				if limited.RetryAfter != 30*time.Second {
					t.Errorf("got a wait of %s, want the 30s the header asked for", limited.RetryAfter)
				}
			},
		},
		{
			name:  "watching an issue succeeds for a known account",
			cloud: func(t *testing.T) jira.WatcherManager { return watchersFromSite(t) },
			fake:  func(t *testing.T) jira.WatcherManager { return conformFakeWithWatchableIssue(t) },
			run: func(t *testing.T, c jira.WatcherManager) {
				t.Helper()
				if err := c.Watch(t.Context(), testIssueKey, ""); err != nil {
					t.Fatalf("watching as self: %v", err)
				}
			},
		},
		{
			name:  "removing nobody is refused before either site is asked",
			cloud: func(t *testing.T) jira.WatcherManager { return watchersFromSite(t) },
			fake:  func(t *testing.T) jira.WatcherManager { return conformFakeWithWatchableIssue(t) },
			run: func(t *testing.T, c jira.WatcherManager) {
				t.Helper()
				err := c.Unwatch(t.Context(), conformProject+"-1", "")
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
				if _, named := invalid.For("accountId"); !named {
					t.Errorf("the refusal does not name accountId: %v", invalid)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open watcherBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				tt.run(t, adapter.open(t))
			})
		}
	}
}
