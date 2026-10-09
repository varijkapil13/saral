package board

import (
	"context"
	"errors"
	"slices"
	"testing"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// elsewhere is a column PROJ-1 is not in, and the column of the done category,
// whose move asks for a resolution on a screen.
func elsewhere(t *testing.T, f *jiratest.Fake, p Plan) (key string, col, done int) {
	t.Helper()
	iss, err := f.IssueFields(context.Background(), "PROJ-1", []string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	from, _ := p.ColumnOf(iss.Status.ID)
	col = 1
	if from == 1 {
		col = 0
	}
	return "PROJ-1", col, len(p.Columns) - 1
}

func TestIntoColumn_MovesByATransitionThatLandsThere(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 2)
	_, p := boardOf(t, f)
	key, col, done := elsewhere(t, f, p)
	status, err := IntoColumn(context.Background(), f, p, key, col, "")
	if err != nil {
		t.Fatalf("IntoColumn: %v", err)
	}
	if at, _ := p.ColumnOf(status.ID); at != col {
		t.Errorf("the issue landed in %s, column %d, want %d", status.Name, at, col)
	}
	if _, err := IntoColumn(context.Background(), f, p, key, done, ""); !errors.Is(err, ErrScreen) {
		t.Errorf("a move with a required field answered %v, want ErrScreen", err)
	}
	if _, err := IntoColumn(context.Background(), f, p, key, col, "no-such-status"); !errors.Is(err, ErrNoMoveTo) {
		t.Errorf("a status no move reaches answered %v, want ErrNoMoveTo", err)
	}
	if _, err := IntoColumn(context.Background(), f, p, key, len(p.Columns), ""); !errors.Is(err, ErrNoMove) {
		t.Errorf("a column no move reaches answered %v, want ErrNoMove", err)
	}
}

func TestMoves_KeepEachFailureWhole(t *testing.T) {
	t.Parallel()
	for name, want := range refusals() {
		f := fakeOf(jiratest.Scrum, 2)
		_, p := boardOf(t, f)
		key, col, _ := elsewhere(t, f, p)
		ctx := context.Background()
		for use, run := range map[string]func() error{
			"MovesOf":       func() error { _, err := MovesOf(ctx, f, key); return err },
			"Apply":         func() error { return Apply(ctx, f, key, "11") },
			"IntoColumn":    func() error { _, err := IntoColumn(ctx, f, p, key, col, ""); return err },
			"ColumnTargets": func() error { _, err := ColumnTargets(ctx, f, p, key, col); return err },
			"MoveInto":      func() error { return MoveInto(ctx, f, 0, []string{key}) },
		} {
			f.FailNext(want)
			if err := run(); !errors.Is(err, want) {
				t.Errorf("%s from %s: got %v", name, use, err)
			}
		}
		f.FailNextN(1, nil)
		f.FailNext(want)
		if _, err := IntoColumn(ctx, f, p, key, col, ""); !errors.Is(err, want) {
			t.Errorf("%s from the transition itself: got %v", name, err)
		}
	}
}

func TestColumnTargets_KeepsOneTransitionPerStatusInTheColumn(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 2)
	_, p := boardOf(t, f)
	key, col, _ := elsewhere(t, f, p)
	got, err := ColumnTargets(context.Background(), f, p, key, col)
	if err != nil || len(got) == 0 {
		t.Fatalf("ColumnTargets = %v, %v", got, err)
	}
	seen := map[string]bool{}
	for _, tr := range got {
		if at, _ := p.ColumnOf(tr.To.ID); at != col || seen[tr.To.ID] {
			t.Errorf("a target %+v is outside the column or repeats a status", tr)
		}
		seen[tr.To.ID] = true
	}
	twice := []jira.Transition{{ID: "1", To: jira.Status{ID: "a"}}, {ID: "2", To: jira.Status{ID: "a"}}}
	if got := DistinctTargets(twice); len(got) != 1 || got[0].ID != "1" {
		t.Errorf("DistinctTargets = %v, want the first only", got)
	}
}

func TestMoveInto_ZeroIsTheBacklog(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 2)
	b, _ := boardOf(t, f)
	sprints, err := OpenSprints(context.Background(), f, b.ID, 50, jira.SprintActive)
	if err != nil || len(sprints.Open) == 0 {
		t.Fatalf("OpenSprints = %+v, %v", sprints, err)
	}
	if err := MoveInto(context.Background(), f, sprints.Open[0].ID, []string{"PROJ-1"}); err != nil {
		t.Fatalf("into a sprint: %v", err)
	}
	if err := MoveInto(context.Background(), f, 0, []string{"PROJ-1"}); err != nil {
		t.Fatalf("into the backlog: %v", err)
	}
	calls := f.Calls()
	if !slices.Contains(calls, "MoveToSprint") || !slices.Contains(calls, "MoveToBacklog") {
		t.Errorf("the moves made %v", calls)
	}
}

func TestLand_PutsACreatedIssueInItsSprintAndColumn(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 2)
	b, p := boardOf(t, f)
	key, col, done := elsewhere(t, f, p)
	created, err := f.IssueFields(context.Background(), key, []string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	sprints, _ := OpenSprints(context.Background(), f, b.ID, 50, jira.SprintActive)
	got := Land(context.Background(), f, Landing{
		Created: created, Sprint: sprints.Open[0].ID, Col: col, Plan: p, Fields: []string{"summary", "status"}, OnBoard: true,
	})
	if got.Err != nil || !got.Read || got.NoMove || got.Screen != nil {
		t.Fatalf("Land = %+v", got)
	}
	if at, _ := p.ColumnOf(got.Issue.Status.ID); at != col || got.Issue.Summary == "" {
		t.Errorf("the landed issue is %+v, want it read back in column %d", got.Issue, col)
	}
	moved, _ := f.IssueFields(context.Background(), key, []string{"status"})
	screened := Land(context.Background(), f, Landing{Created: moved, Col: done, Plan: p, OnBoard: true})
	if screened.Screen == nil || !screened.Read {
		t.Errorf("a column behind a screen landed as %+v", screened)
	}
	nowhere := Land(context.Background(), f, Landing{Created: moved, Col: len(p.Columns), Plan: p, OnBoard: true})
	if !nowhere.NoMove {
		t.Errorf("a column no move reaches landed as %+v", nowhere)
	}
	off := Land(context.Background(), f, Landing{Created: moved, Col: col, Plan: p})
	if off.Read || off.Err != nil {
		t.Errorf("a landing on a board no longer on screen went on to %+v", off)
	}
}

func TestLand_NamesTheStepThatFailed(t *testing.T) {
	t.Parallel()
	for name, want := range refusals() {
		for skip, step := range []LandStep{LandSprint, LandMove, LandRead} {
			f := fakeOf(jiratest.Scrum, 2)
			b, p := boardOf(t, f)
			key, col, _ := elsewhere(t, f, p)
			created, _ := f.IssueFields(context.Background(), key, []string{"status"})
			sprints, _ := OpenSprints(context.Background(), f, b.ID, 50, jira.SprintActive)
			calls := []int{0, 1, 3}[skip]
			if calls > 0 {
				f.FailNextN(calls, nil)
			}
			f.FailNext(want)
			got := Land(context.Background(), f, Landing{
				Created: created, Sprint: sprints.Open[0].ID, Col: col, Plan: p, Fields: []string{"summary"}, OnBoard: true,
			})
			if got.Step != step || !errors.Is(got.Err, want) {
				t.Errorf("%s at %d: got step %d, %v", name, step, got.Step, got.Err)
			}
		}
	}
}

func TestSettle_MovesIntoTheSprintAndReadsBack(t *testing.T) {
	t.Parallel()
	f := fakeOf(jiratest.Scrum, 2)
	b, _ := boardOf(t, f)
	sprints, _ := OpenSprints(context.Background(), f, b.ID, 50, jira.SprintActive)
	ctx := context.Background()
	got := Settle(ctx, f, f, appquery.NewSearch(f), nil, appquery.ListProjection(), "PROJ-1", sprints.Open[0].ID)
	if got.MoveErr != nil || !got.Read || got.ReadErr != nil || got.Issue.Key != "PROJ-1" {
		t.Errorf("Settle = %+v", got)
	}
	unread := Settle(ctx, f, nil, nil, nil, appquery.Projection{}, "PROJ-1", 0)
	if unread.Read || unread.MoveErr != nil {
		t.Errorf("a settle with no reader went on to %+v", unread)
	}
	for name, want := range refusals() {
		f.FailNext(want)
		if got := Settle(ctx, f, f, nil, []string{"summary"}, appquery.Projection{}, "PROJ-1", sprints.Open[0].ID); !errors.Is(got.MoveErr, want) || got.ReadErr != nil {
			t.Errorf("%s from the move: %+v", name, got)
		}
		f.FailNext(want)
		if got := Settle(ctx, f, f, nil, []string{"summary"}, appquery.Projection{}, "PROJ-1", 0); !errors.Is(got.ReadErr, want) {
			t.Errorf("%s from the read: %+v", name, got)
		}
	}
}
