package cloud

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for SprintIssues and
// RankIssues. Neither table can agree on which issues a site holds or what
// their keys are, so the properties both must hold are that a sprint with
// issues answers a non-empty page and one with none answers an empty slice
// rather than an error, that a rank with nothing to move sends nothing and
// reports no error, that a refusal before the wire is a *jira.ValidationError,
// and that the typed failures a site can answer with map to the same error type
// on both sides.

type sprintIssueSite interface {
	jira.BoardReader
	jira.SprintReader
	jira.SprintIssueReader
}

type sprintIssueBuilder func(*testing.T) sprintIssueSite

func sprintIssuesFromSite(t *testing.T, opts ...jiratest.ServerOption) sprintIssueSite {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

// conformScrumBoard reads the id of the conformance project's Scrum board off
// the adapter's own listing, so that a test never writes a board id down.
func conformScrumBoard(t *testing.T, r sprintIssueSite) int64 {
	t.Helper()

	boards, err := r.Boards(t.Context(), conformProject)
	if err != nil {
		t.Fatalf("listing the boards on %s: %v", conformProject, err)
	}
	for _, b := range boards {
		if b.Type == jira.BoardScrum {
			return b.ID
		}
	}
	t.Fatalf("%s has no Scrum board among %+v", conformProject, boards)
	return 0
}

// conformActiveSprint reads the id of the board's active sprint off the
// adapter's own listing, for the same reason.
func conformActiveSprint(t *testing.T, r sprintIssueSite, boardID int64) int64 {
	t.Helper()

	page, err := r.Sprints(t.Context(), boardID, jira.SprintActive)
	if err != nil {
		t.Fatalf("listing board %d's sprints: %v", boardID, err)
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking board %d's sprints: %v", boardID, err)
	}
	if len(all) == 0 {
		t.Fatalf("board %d has no active sprint", boardID)
	}
	return all[0].ID
}

// fakeSprintWithIssues seeds the conformance project with issues and moves two
// of them into the board's active sprint, so the fake has a sprint with
// something in it the way the fixture site already does.
func fakeSprintWithIssues(t *testing.T) *jiratest.Fake {
	t.Helper()

	f := conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 5)))
	boardID := conformScrumBoard(t, f)
	sprintID := conformActiveSprint(t, f, boardID)
	if err := f.MoveToSprint(t.Context(), sprintID, []string{conformProject + "-1", conformProject + "-2"}); err != nil {
		t.Fatalf("moving issues into the active sprint: %v", err)
	}
	return f
}

func TestSprintIssues_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cloud sprintIssueBuilder
		fake  sprintIssueBuilder
		run   func(*testing.T, sprintIssueSite)
	}{
		{
			name:  "an active sprint with issues in it answers a non-empty page",
			cloud: func(t *testing.T) sprintIssueSite { return sprintIssuesFromSite(t) },
			fake:  func(t *testing.T) sprintIssueSite { return fakeSprintWithIssues(t) },
			run: func(t *testing.T, r sprintIssueSite) {
				t.Helper()
				boardID := conformScrumBoard(t, r)
				sprintID := conformActiveSprint(t, r, boardID)
				page, err := r.SprintIssues(t.Context(), boardID, sprintID, jira.BoardQuery{Fields: []string{"summary"}})
				if err != nil {
					t.Fatalf("reading sprint %d's issues: %v", sprintID, err)
				}
				if len(page.Items) == 0 {
					t.Fatal("this case is about the sprint that has issues, and none came back")
				}
				for i, iss := range page.Items {
					if iss.Key == "" {
						t.Errorf("issue %d carries no key", i)
					}
				}
			},
		},
		{
			name: "a sprint that holds nothing answers an empty slice, not an error",
			cloud: func(t *testing.T) sprintIssueSite {
				return sprintIssuesFromSite(t, jiratest.WithFixture(http.MethodGet, sprintIssuesRoute, "board_quickfilters_empty.json"))
			},
			fake: func(t *testing.T) sprintIssueSite { return conformFake(t) },
			run: func(t *testing.T, r sprintIssueSite) {
				t.Helper()
				boardID := conformScrumBoard(t, r)
				sprintID := conformActiveSprint(t, r, boardID)
				page, err := r.SprintIssues(t.Context(), boardID, sprintID, jira.BoardQuery{Fields: []string{"summary"}})
				if err != nil {
					t.Fatalf("reading sprint %d's issues: %v", sprintID, err)
				}
				if len(page.Items) != 0 {
					t.Errorf("a sprint with nothing in it answered %+v", page.Items)
				}
			},
		},
		{
			name:  "a read that names no fields is refused before the site is asked",
			cloud: func(t *testing.T) sprintIssueSite { return sprintIssuesFromSite(t) },
			fake:  func(t *testing.T) sprintIssueSite { return conformFake(t) },
			run: func(t *testing.T, r sprintIssueSite) {
				t.Helper()
				boardID := conformScrumBoard(t, r)
				sprintID := conformActiveSprint(t, r, boardID)
				_, err := r.SprintIssues(t.Context(), boardID, sprintID, jira.BoardQuery{})
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
				if _, named := invalid.For("fields"); !named {
					t.Errorf("the refusal says %v and does not name fields", invalid.Fields)
				}
			},
		},
		{
			// Fixed ids on purpose: WithCapabilities(NoBoards) refuses Boards()
			// on the fake too, so deriving the board through it would test the
			// derivation refusing rather than this one.
			name: "a refusal names CapBoards rather than reading as a fault",
			cloud: func(t *testing.T) sprintIssueSite {
				return sprintIssuesFromSite(t, jiratest.WithStatus(http.MethodGet, sprintIssuesRoute, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) sprintIssueSite {
				return jiratest.New(
					jiratest.WithProject(conformProject, jiratest.Scrum),
					jiratest.WithCapabilities(jiratest.NoBoards),
				)
			},
			run: func(t *testing.T, r sprintIssueSite) {
				t.Helper()
				_, err := r.SprintIssues(t.Context(), 10, 42, jira.BoardQuery{Fields: []string{"summary"}})
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
				if refused.Capability != jira.CapBoards {
					t.Errorf("the refusal names %q, want %q", refused.Capability, jira.CapBoards)
				}
			},
		},
		{
			name: "a sprint nobody has is a not-found",
			cloud: func(t *testing.T) sprintIssueSite {
				return sprintIssuesFromSite(t, jiratest.WithStatus(http.MethodGet, sprintIssuesRoute, http.StatusNotFound, ""))
			},
			fake: func(t *testing.T) sprintIssueSite { return conformFake(t) },
			run: func(t *testing.T, r sprintIssueSite) {
				t.Helper()
				boardID := conformScrumBoard(t, r)
				_, err := r.SprintIssues(t.Context(), boardID, boardID*1000+999, jira.BoardQuery{Fields: []string{"summary"}})
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open sprintIssueBuilder
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

type rankBuilder func(*testing.T) jira.Ranker

func rankFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.Ranker {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c
}

func TestRankIssues_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cloud rankBuilder
		fake  rankBuilder
		run   func(*testing.T, jira.Ranker)
	}{
		{
			name:  "ranking a known issue relative to another reports no error",
			cloud: func(t *testing.T) jira.Ranker { return rankFromSite(t) },
			fake: func(t *testing.T) jira.Ranker {
				return conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 5)))
			},
			run: func(t *testing.T, r jira.Ranker) {
				t.Helper()
				if err := r.RankIssues(t.Context(), []string{conformProject + "-2"}, jira.RankBefore(conformProject+"-1")); err != nil {
					t.Fatalf("ranking: %v", err)
				}
			},
		},
		{
			name:  "ranking no issues sends nothing and reports no error",
			cloud: func(t *testing.T) jira.Ranker { return rankFromSite(t) },
			fake:  func(t *testing.T) jira.Ranker { return conformFake(t) },
			run: func(t *testing.T, r jira.Ranker) {
				t.Helper()
				if err := r.RankIssues(t.Context(), nil, jira.RankBefore(conformProject+"-1")); err != nil {
					t.Fatalf("ranking nothing: %v", err)
				}
			},
		},
		{
			name:  "a rank with no anchor is refused before the site is asked",
			cloud: func(t *testing.T) jira.Ranker { return rankFromSite(t) },
			fake:  func(t *testing.T) jira.Ranker { return conformFake(t) },
			run: func(t *testing.T, r jira.Ranker) {
				t.Helper()
				err := r.RankIssues(t.Context(), []string{conformProject + "-2"}, jira.RankPosition{})
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
			},
		},
		{
			name: "a refusal is a capability answer",
			cloud: func(t *testing.T) jira.Ranker {
				return rankFromSite(t, jiratest.WithStatus(http.MethodPut, rankPath, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) jira.Ranker {
				f := conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 5)))
				f.FailNext(&jira.CapabilityError{Reason: "needs the Schedule Issues permission"})
				return f
			},
			run: func(t *testing.T, r jira.Ranker) {
				t.Helper()
				err := r.RankIssues(t.Context(), []string{conformProject + "-2"}, jira.RankBefore(conformProject+"-1"))
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
				if refused.Reason == "" {
					t.Error("the refusal carries no reason to show the user")
				}
			},
		},
		{
			name: "a rate limit carries the wait the site asked for",
			cloud: func(t *testing.T) jira.Ranker {
				return rankFromSite(t, jiratest.WithRateLimit(http.MethodPut, rankPath, 30*time.Second))
			},
			fake: func(t *testing.T) jira.Ranker {
				f := conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 5)))
				f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
				return f
			},
			run: func(t *testing.T, r jira.Ranker) {
				t.Helper()
				err := r.RankIssues(t.Context(), []string{conformProject + "-2"}, jira.RankBefore(conformProject+"-1"))
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
				if limited.RetryAfter != 30*time.Second {
					t.Errorf("RetryAfter = %s, want 30s", limited.RetryAfter)
				}
			},
		},
		{
			name: "an anchor nobody has is a not-found",
			cloud: func(t *testing.T) jira.Ranker {
				return rankFromSite(t, jiratest.WithStatus(http.MethodPut, rankPath, http.StatusNotFound, ""))
			},
			fake: func(t *testing.T) jira.Ranker {
				return conformFake(t, jiratest.WithIssues(jiratest.GenFor(conformProject, 5)))
			},
			run: func(t *testing.T, r jira.Ranker) {
				t.Helper()
				err := r.RankIssues(t.Context(), []string{conformProject + "-2"}, jira.RankBefore(conformProject+"-999"))
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open rankBuilder
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
