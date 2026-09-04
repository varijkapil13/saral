package cloud

import (
	"errors"
	"net/http"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestQuickFilters_ReadsEveryFilterInPositionOrder(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithFixture(http.MethodGet, quickFilterRoute, "board_quickfilters.json"))
	got, err := c.QuickFilters(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading quick filters: %v", err)
	}
	want := []jira.QuickFilter{
		{ID: 1, Name: "Only My Issues", JQL: "assignee = currentUser()", Description: "Issues assigned to you", Position: 0},
		{ID: 2, Name: "Recently Updated", JQL: "updated >= -7d", Description: "Issues touched in the last week", Position: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d quick filters, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("quick filter %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestQuickFilters_AWellFormedEmptyAnswerIsNotAnError(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithFixture(http.MethodGet, quickFilterRoute, "board_quickfilters_empty.json"))
	got, err := c.QuickFilters(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading quick filters: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want an empty slice", got)
	}
}

func TestQuickFilters_RefusesABoardIDThatIsNotPositive(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t)
	for _, id := range []int64{0, -1} {
		_, err := c.QuickFilters(t.Context(), id)
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("board id %d: got %T (%v), want a *jira.ValidationError", id, err, err)
		}
	}
}

func TestQuickFilters_A404NamesTheBoard(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithStatus(http.MethodGet, quickFilterRoute, http.StatusNotFound, ""))
	_, err := c.QuickFilters(t.Context(), boardTestID)
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "board" {
		t.Errorf("the 404 names kind %q, want board", missing.Kind)
	}
}

// boardJQL is the one piece of new logic BoardIssues and BoardBacklog lean on,
// so it is worth pinning apart from a request round trip.
func TestBoardJQL_CombinesTheSubQueryAndEveryQuickFilterBracketedAndAnded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		q    jira.BoardQuery
		want string
	}{
		{name: "neither set", q: jira.BoardQuery{}, want: ""},
		{
			name: "a sub-query alone",
			q:    jira.BoardQuery{SubQuery: "resolved is EMPTY"},
			want: "(resolved is EMPTY)",
		},
		{
			name: "a quick filter alone",
			q:    jira.BoardQuery{QuickFilters: []string{"assignee = currentUser()"}},
			want: "(assignee = currentUser())",
		},
		{
			name: "a sub-query and two quick filters, in that order",
			q: jira.BoardQuery{
				SubQuery:     "resolved is EMPTY",
				QuickFilters: []string{"assignee = currentUser()", "priority = High"},
			},
			want: "(resolved is EMPTY) AND (assignee = currentUser()) AND (priority = High)",
		},
		{
			name: "blank entries are dropped rather than bracketed into nothing",
			q:    jira.BoardQuery{SubQuery: "  ", QuickFilters: []string{"", "  ", "status = Done"}},
			want: "(status = Done)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := boardJQL(tt.q); got != tt.want {
				t.Errorf("boardJQL(%+v) = %q, want %q", tt.q, got, tt.want)
			}
		})
	}
}

// The composition is only real if BoardIssues actually sends it, so this pins
// the wire request rather than only the pure function above.
func TestBoardIssues_SendsTheSubQueryAndQuickFiltersCombinedAsOneJQLParameter(t *testing.T) {
	t.Parallel()

	c, s := boardClient(t)
	_, err := c.BoardIssues(t.Context(), boardTestID, jira.BoardQuery{
		Fields:       []string{"summary"},
		SubQuery:     "resolved is EMPTY",
		QuickFilters: []string{"assignee = currentUser()"},
	})
	if err != nil {
		t.Fatalf("reading board issues: %v", err)
	}
	got := boardQueryOn(t, s, boardIssuesPath(boardTestID))
	want := "(resolved is EMPTY) AND (assignee = currentUser())"
	if got.Get("jql") != want {
		t.Errorf("jql = %q, want %q", got.Get("jql"), want)
	}
}

func TestBoardIssues_SendsNoJQLParameterWhenNeitherIsSet(t *testing.T) {
	t.Parallel()

	c, s := boardClient(t)
	_, err := c.BoardIssues(t.Context(), boardTestID, jira.BoardQuery{Fields: []string{"summary"}})
	if err != nil {
		t.Fatalf("reading board issues: %v", err)
	}
	got := boardQueryOn(t, s, boardIssuesPath(boardTestID))
	if got.Has("jql") {
		t.Errorf("a jql parameter was sent with neither a sub-query nor a quick filter set: %v", got)
	}
}
