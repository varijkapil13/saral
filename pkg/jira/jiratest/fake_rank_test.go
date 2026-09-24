package jiratest_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// fakeBoardKeyOrder is the order BoardIssues answers a board's issues in, which
// is rank order on a board with a rank field.
func fakeBoardKeyOrder(t *testing.T, c *jiratest.Fake, boardID int64) []string {
	t.Helper()

	page, err := c.BoardIssues(t.Context(), boardID, jira.BoardQuery{Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("BoardIssues: %v", err)
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("collecting board issues: %v", err)
	}
	return fakeKeysOf(all)
}

func fakeActiveSprint(t *testing.T, sprints []jira.Sprint) jira.Sprint {
	t.Helper()

	for _, sp := range sprints {
		if sp.State == jira.SprintActive {
			return sp
		}
	}
	t.Fatalf("no active sprint among %+v", sprints)
	return jira.Sprint{}
}

func TestRankIssues_MovesAnIssueImmediatelyBeforeTheAnchor(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 6)
	board := fakeBoard(t, c)
	before := fakeBoardKeyOrder(t, c, board.ID)
	if len(before) < 2 {
		t.Fatalf("want at least 2 issues on the board, got %v", before)
	}
	anchor, moving := before[0], before[len(before)-1]

	if err := c.RankIssues(t.Context(), []string{moving}, jira.RankBefore(anchor)); err != nil {
		t.Fatalf("ranking: %v", err)
	}

	after := fakeBoardKeyOrder(t, c, board.ID)
	at := slices.Index(after, anchor)
	if at <= 0 || after[at-1] != moving {
		t.Fatalf("order is %v, want %s immediately before %s", after, moving, anchor)
	}
}

func TestRankIssues_MovesAnIssueImmediatelyAfterTheAnchor(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 6)
	board := fakeBoard(t, c)
	before := fakeBoardKeyOrder(t, c, board.ID)
	if len(before) < 2 {
		t.Fatalf("want at least 2 issues on the board, got %v", before)
	}
	anchor, moving := before[len(before)-1], before[0]

	if err := c.RankIssues(t.Context(), []string{moving}, jira.RankAfter(anchor)); err != nil {
		t.Fatalf("ranking: %v", err)
	}

	after := fakeBoardKeyOrder(t, c, board.ID)
	at := slices.Index(after, anchor)
	if at < 0 || at == len(after)-1 || after[at+1] != moving {
		t.Fatalf("order is %v, want %s immediately after %s", after, moving, anchor)
	}
}

func TestRankIssues_AnUnknownKeyIsAPartialFailureAndTheRestStillMove(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 4)
	board := fakeBoard(t, c)
	before := fakeBoardKeyOrder(t, c, board.ID)
	anchor, known := before[0], before[len(before)-1]

	err := c.RankIssues(t.Context(), []string{known, "PROJ-404"}, jira.RankBefore(anchor))
	var partial *jira.PartialRankError
	if !errors.As(err, &partial) {
		t.Fatalf("got %T (%v), want a *jira.PartialRankError", err, err)
	}
	if !slices.Equal(partial.Ranked, []string{known}) {
		t.Errorf("Ranked = %v, want [%s]", partial.Ranked, known)
	}
	if len(partial.Failed) != 1 || partial.Failed[0].Key != "PROJ-404" {
		t.Fatalf("Failed = %+v, want one entry for PROJ-404", partial.Failed)
	}
	if partial.Failed[0].Reason == "" {
		t.Error("the failed entry carries no reason, and the reason is the only thing that says why")
	}

	after := fakeBoardKeyOrder(t, c, board.ID)
	at := slices.Index(after, anchor)
	if at <= 0 || after[at-1] != known {
		t.Errorf("the known issue did not move: order is %v", after)
	}
}

func TestRankIssues_AnUnknownAnchorIsANotFoundError(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 3)
	err := c.RankIssues(t.Context(), []string{"PROJ-1"}, jira.RankBefore("PROJ-999"))

	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "issue" || missing.ID != "PROJ-999" {
		t.Errorf("the 404 names %s %s, want issue PROJ-999", missing.Kind, missing.ID)
	}
}

func TestRankIssues_AFieldIDThatIsNotThisSitesRankFieldIsRefused(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 3)
	err := c.RankIssues(t.Context(), []string{"PROJ-1"}, jira.RankPosition{Before: "PROJ-2", FieldID: "customfield_99999"})

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, named := invalid.For("rankCustomFieldId"); !named {
		t.Errorf("the refusal says %v and does not name rankCustomFieldId", invalid.Fields)
	}
}

func TestSprintIssues_NarrowsToWhatTheSprintHolds(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 6)
	board := fakeBoard(t, c)
	active := fakeActiveSprint(t, fakeSprintsOf(t, c, board.ID))

	whole := fakeBoardKeyOrder(t, c, board.ID)
	if len(whole) < 3 {
		t.Fatalf("want at least 3 issues on the board, got %v", whole)
	}
	moving := whole[:2]
	if err := c.MoveToSprint(t.Context(), active.ID, moving); err != nil {
		t.Fatalf("moving issues into the sprint: %v", err)
	}

	page, err := c.SprintIssues(t.Context(), board.ID, active.ID, jira.BoardQuery{Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("SprintIssues: %v", err)
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("collecting sprint issues: %v", err)
	}
	got := fakeKeysOf(all)
	slices.Sort(got)
	want := slices.Clone(moving)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("SprintIssues = %v, want exactly %v", got, want)
	}
	if len(whole) == len(want) {
		t.Fatal("the whole board and the sprint are the same size, so this proves nothing was narrowed")
	}
}

func TestSprintIssues_AKanbanBoardRefusesItRatherThanAnsweringEmpty(t *testing.T) {
	t.Parallel()

	c := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban), jiratest.WithIssues(jiratest.Gen(3)))
	board := fakeBoard(t, c)

	_, err := c.SprintIssues(t.Context(), board.ID, 1, jira.BoardQuery{Fields: fakeNarrow})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, named := invalid.For("boardId"); !named {
		t.Errorf("the refusal says %v and does not name boardId", invalid.Fields)
	}
}

func TestSprintIssues_AnUnknownSprintIsANotFoundError(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 3)
	board := fakeBoard(t, c)

	_, err := c.SprintIssues(t.Context(), board.ID, 999999, jira.BoardQuery{Fields: fakeNarrow})
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "sprint" || missing.ID != "999999" {
		t.Errorf("the 404 names %s %s, want sprint 999999", missing.Kind, missing.ID)
	}
}

func TestFailNext_PropagatesToRankIssues(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 3)
	c.FailNext(&jira.RateLimitError{RetryAfter: 5 * time.Second})

	err := c.RankIssues(t.Context(), []string{"PROJ-1"}, jira.RankBefore("PROJ-2"))
	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %T (%v), want the queued *jira.RateLimitError", err, err)
	}
	if limited.RetryAfter != 5*time.Second {
		t.Errorf("RetryAfter = %s, want the queued 5s", limited.RetryAfter)
	}
}

func TestFailNext_PropagatesToSprintIssues(t *testing.T) {
	t.Parallel()

	c := fakeNewWithIssues(t, 3)
	board := fakeBoard(t, c)
	active := fakeActiveSprint(t, fakeSprintsOf(t, c, board.ID))
	c.FailNext(&jira.CapabilityError{Reason: "needs the Board permission"})

	_, err := c.SprintIssues(t.Context(), board.ID, active.ID, jira.BoardQuery{Fields: fakeNarrow})
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want the queued *jira.CapabilityError", err, err)
	}
}
