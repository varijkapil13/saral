package board

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// A Kanban board's configuration carries the condition that decides which
// resolved issues it still shows, and that condition is part of the query the
// board runs: without it the done column is every issue the project ever
// finished.
//
// This test fails against pkg/jira/jiratest, and the failure is the fake's and
// not the view's: Fake.BoardConfig hands out
//
//	resolved >= -14d OR resolved is EMPTY
//
// and Fake.Search cannot parse it — its JQL subset knows neither the resolved
// field nor the >= operator, and refuses anything outside the subset rather than
// matching everything. So a Kanban board cannot be loaded through the fake at
// all, and every Kanban path above the port is untestable here. It is left
// failing rather than written around, because a test that passed by dropping the
// sub-query would be a test that hid the rule.
func TestBoard_AKanbanBoardLoadsWithItsOwnRuleAboutResolvedIssues(t *testing.T) {
	t.Parallel()
	fake := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Kanban),
		jiratest.WithIssues(jiratest.Gen(12)),
	)
	dr := newDriver(t, testDeps(fake), 120, 20)

	if dr.m.plan.subQuery == "" {
		t.Fatal("this board is a Kanban board and its configuration carries no sub-query")
	}
	if dr.m.failure != nil {
		t.Fatalf("a Kanban board cannot be loaded through the fake:\n\t%v\n\tquery: %s\n"+
			"Either Fake.Search learns the sub-query Fake.BoardConfig hands out, or the port grows the "+
			"board-issue read the Agile API has — which is the endpoint that applies a board's filter and "+
			"its sub-query server-side, and the reason this view has to rebuild the set out of JQL at all.",
			dr.m.failure, dr.m.plan.jql("PROJ"))
	}
	if len(dr.m.issues) == 0 {
		t.Error("a Kanban board drew no cards")
	}
}
