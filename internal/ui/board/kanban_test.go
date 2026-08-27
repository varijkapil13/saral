package board

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// asked records what the board issue read was asked for. Everything it does not
// intercept is the fake's, so the board really is filled by the port.
type asked struct {
	*jiratest.Fake

	mu      sync.Mutex
	queries []jira.BoardQuery
	boards  []int64
}

func newAsked(f *jiratest.Fake) *asked { return &asked{Fake: f} }

func (a *asked) BoardIssues(ctx context.Context, boardID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	a.mu.Lock()
	a.queries = append(a.queries, q)
	a.boards = append(a.boards, boardID)
	a.mu.Unlock()
	return a.Fake.BoardIssues(ctx, boardID, q)
}

func (a *asked) last() (q jira.BoardQuery, boardID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.queries) == 0 {
		return jira.BoardQuery{}, 0
	}
	return a.queries[len(a.queries)-1], a.boards[len(a.boards)-1]
}

// A Kanban board's configuration carries the condition that decides which
// resolved issues it still shows, and that condition travels with the read as a
// field on the query rather than as a clause this view composes: without it the
// done column is every issue the project ever finished.
func TestBoard_AKanbanBoardLoadsWithItsOwnRuleAboutResolvedIssues(t *testing.T) {
	t.Parallel()
	spy := newAsked(jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Kanban),
		jiratest.WithIssues(jiratest.Gen(12)),
	))
	dr := newDriver(t, testDeps(spy), 120, 20)

	if dr.m.plan.subQuery == "" {
		t.Fatal("this board is a Kanban board and its configuration carries no sub-query")
	}
	if dr.m.failure != nil {
		t.Fatalf("a Kanban board could not be loaded: %v", dr.m.failure)
	}
	if len(dr.m.issues) == 0 {
		t.Fatal("a Kanban board drew no cards")
	}
	query, boardID := spy.last()
	if query.SubQuery != dr.m.plan.subQuery {
		t.Errorf("the read carried the sub-query %q, want the board's own %q", query.SubQuery, dr.m.plan.subQuery)
	}
	if boardID != dr.m.plan.boardID {
		t.Errorf("the read asked board %d, want the one on screen, %d", boardID, dr.m.plan.boardID)
	}
	// The rule hides work resolved long ago, and every resolved issue the
	// generator makes was resolved a year before the fake's clock.
	for i := range dr.m.issues {
		if dr.m.issues[i].Status.Category == jira.CategoryDone {
			t.Errorf("%s is finished and on the board anyway, so the sub-query was not applied",
				dr.m.issues[i].Key)
		}
	}
}

// The read names the fields a card draws and never a wildcard, whatever the
// board estimates in.
func TestBoard_TheReadNamesTheFieldsACardDrawsAndNoWildcard(t *testing.T) {
	t.Parallel()
	spy := newAsked(newFake(12))
	dr := newDriver(t, testDeps(spy), 120, 20)

	query, _ := spy.last()
	if len(query.Fields) == 0 {
		t.Fatal("the read named no field, which asks the endpoint for every field the site has")
	}
	for _, wildcard := range []string{jira.FieldsAll, jira.FieldsNavigable} {
		if slices.Contains(query.Fields, wildcard) {
			t.Errorf("the read asks for %s: %v", wildcard, query.Fields)
		}
	}
	if !slices.Contains(query.Fields, "summary") || !slices.Contains(query.Fields, "status") {
		t.Errorf("the read asks for %v, which is missing what a card is drawn from", query.Fields)
	}
	if dr.m.plan.estimates && !slices.Contains(query.Fields, dr.m.plan.estimate.ID) {
		t.Errorf("the board estimates in %s and the read asks for %v", dr.m.plan.estimate.ID, query.Fields)
	}
}
