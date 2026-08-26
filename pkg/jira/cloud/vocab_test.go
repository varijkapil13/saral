package cloud

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	vocabProject      = "EX"
	vocabStatusesPath = "/rest/api/3/project/{key}/statuses"
)

func TestIssueTypeStatuses_ReadsEachTypesOwnWorkflow(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.IssueTypeStatuses(t.Context(), vocabProject)
	if err != nil {
		t.Fatalf("reading the statuses in %s: %v", vocabProject, err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d issue types, want the three the project has", len(got))
	}

	byName := make(map[string][]jira.Status, len(got))
	for _, entry := range got {
		if entry.Type.ID == "" {
			t.Errorf("%q came back with no id, and an id is the only thing that identifies it", entry.Type.Name)
		}
		byName[entry.Type.Name] = entry.Statuses
	}
	// Two types in one project run different workflows, so "every status here"
	// is a union and never one type's list.
	if len(byName["Task"]) == len(byName["Bug"]) {
		t.Errorf("Task and Bug both list %d statuses, and the fixture gives them different workflows", len(byName["Task"]))
	}
	if !slices.ContainsFunc(got, func(e jira.IssueTypeStatuses) bool { return e.Type.Subtask }) {
		t.Error("no subtask type came back, and the answer covers every type in the project")
	}
}

// A status name identifies nothing: it is localised, and a team-managed project
// mints project-scoped statuses that reuse the stock names. Two ids under one
// name is the shape that proves an id-keyed reader from a name-keyed one.
func TestIssueTypeStatuses_CarriesTwoIdsUnderOneDisplayName(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.IssueTypeStatuses(t.Context(), vocabProject)
	if err != nil {
		t.Fatalf("reading the statuses in %s: %v", vocabProject, err)
	}

	ids := make(map[string]map[string]bool)
	for _, entry := range got {
		for _, status := range entry.Statuses {
			if ids[status.Name] == nil {
				ids[status.Name] = make(map[string]bool)
			}
			ids[status.Name][status.ID] = true
			if status.Category == jira.CategoryUnknown {
				t.Errorf("%s (%s) has no category, and the category is the only property that is the same on every site",
					status.Name, status.ID)
			}
		}
	}
	if len(ids["In Review"]) < 2 {
		t.Errorf(`"In Review" resolved to %v, want the two distinct ids one site can hold under one name`, ids["In Review"])
	}
}

func TestIssueTypeStatuses_RefusesToAskWithoutAProject(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	for _, key := range []string{"", "   "} {
		got, err := c.IssueTypeStatuses(t.Context(), key)
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("asking for %q gave %+v, %T (%v); want a *jira.ValidationError", key, got, err, err)
		}
		if _, named := invalid.For("projectKey"); !named {
			t.Errorf("the refusal does not name projectKey: %v", invalid)
		}
	}
	if n := len(s.Requests()); n != 0 {
		t.Errorf("the site was asked %d times for a project nobody named", n)
	}
}

func TestPriorities_KeepsTheSitesOwnOrderAndItsOwnNames(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Priorities(t.Context())
	if err != nil {
		t.Fatalf("reading the priorities: %v", err)
	}

	names := make([]string, 0, len(got))
	for _, p := range got {
		if p.ID == "" {
			t.Errorf("%q came back with no id", p.Name)
		}
		names = append(names, p.Name)
	}
	// Ranking order, which is neither alphabetical nor anything a client can
	// derive: an administrator can add a priority and put it anywhere.
	if want := []string{"Highest", "High", "Medium", "Someday"}; !slices.Equal(names, want) {
		t.Errorf("got %v, want %v in the site's own order", names, want)
	}
}

func TestLabels_WalksEveryPageOfBareStrings(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	page, err := c.Labels(t.Context())
	if err != nil {
		t.Fatalf("reading the labels: %v", err)
	}
	if total, ok := page.Count(); !ok || total != 5 {
		t.Errorf("the first page reports %d labels (stated %v), want the 5 the site claims", total, ok)
	}

	got, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the labels: %v", err)
	}
	want := []string{"cache-layer", "prüfung", "release-blocker", "wire-shape", "優先度"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if n := peopleCount(s, vocabLabelPath); n != 2 {
		t.Errorf("the label endpoint was called %d times, want one per page", n)
	}
}

// The endpoint takes no query and ignores one sent anyway — a narrowed request
// answered byte-identically to the unnarrowed one — so sending one would teach a
// caller that the site is filtering when it is not.
func TestLabels_SendsNoQueryBecauseTheEndpointIgnoresOne(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.Labels(t.Context()); err != nil {
		t.Fatalf("reading the labels: %v", err)
	}

	asked := peopleQueryOn(t, s, vocabLabelPath)
	if _, sent := asked["query"]; sent {
		t.Errorf("a query was sent (%q), and this endpoint does not narrow on one", asked.Encode())
	}
	if asked.Get("maxResults") == "" {
		t.Error("no maxResults was sent, so the page size comes from the site and moves when it does")
	}
}

func TestVocabulary_ReportsARefusalRateLimitAndTransportFailureAsThemselves(t *testing.T) {
	t.Parallel()

	calls := map[string]struct {
		path string
		run  func(context.Context, *Client) error
	}{
		"the statuses in a project": {
			path: vocabStatusesPath,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.IssueTypeStatuses(ctx, vocabProject)
				return err
			},
		},
		"the site's priorities": {
			path: vocabPriorityPath,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Priorities(ctx)
				return err
			},
		},
		"the site's labels": {
			path: vocabLabelPath,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Labels(ctx)
				return err
			},
		},
	}

	failures := map[string]struct {
		opt    func(path string) jiratest.ServerOption
		assert func(*testing.T, error)
	}{
		"the token may not ask": {
			opt: func(path string) jiratest.ServerOption {
				return jiratest.WithStatus(http.MethodGet, path, http.StatusForbidden, "")
			},
			assert: func(t *testing.T, err error) {
				t.Helper()
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
			},
		},
		"the site is rate limiting": {
			opt: func(path string) jiratest.ServerOption {
				return jiratest.WithRateLimit(http.MethodGet, path, 30*time.Second)
			},
			assert: func(t *testing.T, err error) {
				t.Helper()
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
				if limited.RetryAfter != 30*time.Second {
					t.Errorf("RetryAfter = %s, want the 30s the site asked for", limited.RetryAfter)
				}
			},
		},
		"there is no such project": {
			opt: func(path string) jiratest.ServerOption {
				return jiratest.WithStatus(http.MethodGet, path, http.StatusNotFound, "")
			},
			assert: func(t *testing.T, err error) {
				t.Helper()
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
			},
		},
		"the site broke": {
			opt: func(path string) jiratest.ServerOption {
				return jiratest.WithStatus(http.MethodGet, path, http.StatusInternalServerError, "")
			},
			assert: peopleWantTransport,
		},
		"the answer is not JSON": {
			opt: func(path string) jiratest.ServerOption {
				return peopleAnswering(path, "<html>a proxy answered instead</html>")
			},
			assert: peopleWantTransport,
		},
		"the answer is the wrong JSON": {
			opt: func(path string) jiratest.ServerOption {
				return peopleAnswering(path, `"a string where a body should be"`)
			},
			assert: peopleWantTransport,
		},
	}

	for name, call := range calls {
		for failure, tt := range failures {
			t.Run(name+"/"+failure, func(t *testing.T) {
				t.Parallel()

				s := jiratest.NewServer(tt.opt(call.path))
				defer s.Close()

				c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
				err := call.run(t.Context(), c)
				if err == nil {
					t.Fatal("the failure came back as an answer")
				}
				tt.assert(t, err)
			})
		}
	}
}

func TestVocabulary_HonoursACancelledContext(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, vocabStatusesPath, func(w http.ResponseWriter, r *http.Request) {
		announce()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer closeServer(t, s)
	defer letGo()

	ctx, cancel := context.WithCancel(t.Context())
	c, _ := testClient(t, s.URL())

	errs := make(chan error, 1)
	go func() {
		_, err := c.IssueTypeStatuses(ctx, vocabProject)
		errs <- err
	}()

	receive(t, "the request to reach the site", arrived)
	cancel()

	if err := receive(t, "the cancelled call to come back", errs); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the caller's own cancellation", err)
	}
}
