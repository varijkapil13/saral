package cloud

import (
	"errors"
	"net/http"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for QuickFilters. The two
// sites cannot agree on which quick filters a board has or what they are
// called — that is theirs to define — so the properties both must hold are
// that a populated answer carries a name and a non-empty JQL fragment on every
// row in position order, that a board with none answers an empty slice rather
// than an error, and that a 404 and a capability refusal read the way every
// other board read's do.

const quickFilterRoute = "/rest/agile/1.0/board/{id}/quickfilter"

type quickFilterBuilder func(*testing.T) jira.BoardReader

func quickFiltersFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.BoardReader {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

func TestQuickFilters_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cloud quickFilterBuilder
		fake  quickFilterBuilder
		run   func(*testing.T, jira.BoardReader)
	}{
		{
			name:  "a populated answer names every quick filter, in position order",
			cloud: func(t *testing.T) jira.BoardReader { return quickFiltersFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformFake(t) },
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				board := firstBoard(t, r)
				got, err := r.QuickFilters(t.Context(), board.ID)
				if err != nil {
					t.Fatalf("reading board %d's quick filters: %v", board.ID, err)
				}
				if len(got) == 0 {
					t.Fatal("this case is about the board that has quick filters, and none came back")
				}
				for i, qf := range got {
					if qf.Name == "" {
						t.Errorf("quick filter %d has no name", i)
					}
					if qf.JQL == "" {
						t.Errorf("quick filter %q has no JQL, and JQL is the whole point of one", qf.Name)
					}
					if i > 0 && got[i-1].Position > qf.Position {
						t.Errorf("quick filter %q at position %d sorts after %q at position %d",
							qf.Name, qf.Position, got[i-1].Name, got[i-1].Position)
					}
				}
			},
		},
		{
			name: "a board with no quick filters answers an empty slice, not an error",
			cloud: func(t *testing.T) jira.BoardReader {
				return quickFiltersFromSite(t, jiratest.WithFixture(http.MethodGet, quickFilterRoute, "board_quickfilters_empty.json"))
			},
			fake: func(t *testing.T) jira.BoardReader {
				return jiratest.New(jiratest.WithProject(conformProject, jiratest.Kanban))
			},
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				board := firstBoard(t, r)
				got, err := r.QuickFilters(t.Context(), board.ID)
				if err != nil {
					t.Fatalf("reading board %d's quick filters: %v", board.ID, err)
				}
				if len(got) != 0 {
					t.Errorf("a board with no quick filters answered %+v", got)
				}
			},
		},
		{
			name: "a board nobody has is a 404 naming the board",
			cloud: func(t *testing.T) jira.BoardReader {
				return quickFiltersFromSite(t, jiratest.WithStatus(http.MethodGet, quickFilterRoute, http.StatusNotFound, ""))
			},
			fake: func(t *testing.T) jira.BoardReader { return conformFake(t) },
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				_, err := r.QuickFilters(t.Context(), 987654)
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
				if missing.Kind != "board" || missing.ID != "987654" {
					t.Errorf("the 404 names %s %s, want board 987654", missing.Kind, missing.ID)
				}
			},
		},
		{
			name: "a refusal names CapBoards rather than reading as a fault",
			cloud: func(t *testing.T) jira.BoardReader {
				return quickFiltersFromSite(t, jiratest.WithStatus(http.MethodGet, quickFilterRoute, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) jira.BoardReader {
				return jiratest.New(
					jiratest.WithProject(conformProject, jiratest.Scrum),
					jiratest.WithCapabilities(jiratest.NoBoards),
				)
			},
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				// Not derived through firstBoard: WithCapabilities(NoBoards)
				// refuses Boards() too, on the fake, so this stays a case about
				// QuickFilters refusing rather than one that never reaches it.
				_, err := r.QuickFilters(t.Context(), 10)
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
				if refused.Capability != jira.CapBoards {
					t.Errorf("the refusal names %q, want %q", refused.Capability, jira.CapBoards)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open quickFilterBuilder
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
