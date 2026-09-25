package board

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// sprintsOf is the sprints a project's board has, in the fake's own order.
func sprintsOf(t *testing.T, f *jiratest.Fake, project string) (boardID int64, sprints []jira.Sprint) {
	t.Helper()
	ctx := context.Background()
	boards, err := f.Boards(ctx, project)
	if err != nil || len(boards) == 0 {
		t.Fatalf("the fake has no board on %s: %v", project, err)
	}
	page, err := f.Sprints(ctx, boards[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return boards[0].ID, page.Items
}

func sprintIn(t *testing.T, sprints []jira.Sprint, state jira.SprintState) jira.Sprint {
	t.Helper()
	at := slices.IndexFunc(sprints, func(sp jira.Sprint) bool { return sp.State == state })
	if at < 0 {
		t.Fatalf("the fake has no %s sprint", state)
	}
	return sprints[at]
}

// splitAcrossSprints is a Scrum board of twelve issues: the first four in the
// running sprint, the next four in a second one started beside it when both is
// true, and the rest in no sprint at all.
func splitAcrossSprints(t *testing.T, both bool) (f *jiratest.Fake, first, second jira.Sprint) {
	t.Helper()
	gen := jiratest.Gen(12)
	f = jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(gen))
	ctx := context.Background()
	_, sprints := sprintsOf(t, f, "PROJ")
	first = sprintIn(t, sprints, jira.SprintActive)
	if err := f.MoveToSprint(ctx, first.ID, keysOf(gen[:4])); err != nil {
		t.Fatal(err)
	}
	if both {
		future := sprintIn(t, sprints, jira.SprintFuture)
		start, end := time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC), time.Date(2026, time.March, 16, 9, 0, 0, 0, time.UTC)
		if _, err := f.UpdateSprint(ctx, future.ID, jira.SprintPatch{Start: &start, End: &end}); err != nil {
			t.Fatal(err)
		}
		started, err := f.StartSprint(ctx, future.ID)
		if err != nil {
			t.Fatal(err)
		}
		second = started
		if err := f.MoveToSprint(ctx, second.ID, keysOf(gen[4:8])); err != nil {
			t.Fatal(err)
		}
	}
	setupCalls.Store(f, len(f.Calls()))
	return f, first, second
}

func heldKeys(dr *driver) []string {
	out := make([]string, 0, len(dr.m.issues))
	for i := range dr.m.issues {
		out = append(out, dr.m.issues[i].Key)
	}
	slices.Sort(out)
	return out
}

func TestBoard_AScrumBoardShowsItsRunningSprintAndNothingElse(t *testing.T) {
	t.Parallel()
	f, sprint, _ := splitAcrossSprints(t, false)

	dr := newDriver(t, testDeps(f), 120, 20)

	if got, want := heldKeys(dr), []string{"PROJ-1", "PROJ-2", "PROJ-3", "PROJ-4"}; !slices.Equal(got, want) {
		t.Errorf("the board holds %v, want only the running sprint's %v", got, want)
	}
	if n := countCalls(f, "BoardIssues"); n != 0 {
		t.Errorf("a Scrum board read its whole filter %d times; it shows its sprint", n)
	}
	if n := countCalls(f, "SprintIssues"); n == 0 {
		t.Error("the running sprint's issues were never asked for")
	}
	mustContain(t, dr.view(), sprint.Name)
	golden(t, "board_sprint_120x20.golden", dr.view())
}

func TestBoard_AKanbanBoardIsReadWholeWithNoSprint(t *testing.T) {
	t.Parallel()
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban), jiratest.WithIssues(jiratest.Gen(6)))

	dr := newDriver(t, testDeps(f), 120, 20)

	if n := countCalls(f, "BoardIssues"); n == 0 {
		t.Error("a Kanban board never read its cards")
	}
	if n := countCalls(f, "SprintIssues"); n != 0 {
		t.Errorf("a board that runs no sprints asked for a sprint's issues %d times", n)
	}
	if !dr.m.noSprints || dr.m.sprint.ID != 0 {
		t.Errorf("noSprints = %v, sprint = %+v; the 400 is the site saying the board runs none",
			dr.m.noSprints, dr.m.sprint)
	}
}

func TestBoard_TwoRunningSprintsAreChosenBetweenAndTheChoiceIsKept(t *testing.T) {
	t.Parallel()
	f, first, second := splitAcrossSprints(t, true)
	mem := newFakeMemory()
	d := withMemory(testDeps(f), mem)

	dr := newDriver(t, d, 120, 20)

	if dr.m.sprint.ID != first.ID {
		t.Fatalf("the board opened on %q, want the first the site lists", dr.m.sprint.Name)
	}
	mustContain(t, dr.view(), first.Name, "1 of 2 running")
	mustContain(t, dr.lastStatus().Text, "2 sprints are running", "s shows the next")
	golden(t, "board_two_sprints_120x20.golden", dr.view())

	dr.key("s")

	if dr.m.sprint.ID != second.ID {
		t.Fatalf("s left the board on %q, want %q", dr.m.sprint.Name, second.Name)
	}
	if got, want := heldKeys(dr), []string{"PROJ-5", "PROJ-6", "PROJ-7", "PROJ-8"}; !slices.Equal(got, want) {
		t.Errorf("the second sprint's board holds %v, want %v", got, want)
	}
	mustContain(t, dr.view(), second.Name, "2 of 2 running")

	again := newDriver(t, d, 120, 20)
	if again.m.sprint.ID != second.ID {
		t.Errorf("a board opened again is on %q, want the %q it was left on", again.m.sprint.Name, second.Name)
	}
	if again.lastStatus().Text != "" {
		t.Errorf("a board with a sprint already chosen still says %q", again.lastStatus().Text)
	}
}

func TestBoard_ASprintBoardWithNoSprintRunningSaysSo(t *testing.T) {
	t.Parallel()
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(6)))
	ctx := context.Background()
	_, sprints := sprintsOf(t, f, "PROJ")
	if _, err := f.CompleteSprint(ctx, sprintIn(t, sprints, jira.SprintActive).ID); err != nil {
		t.Fatal(err)
	}

	dr := newDriver(t, testDeps(f), 100, 16)

	if len(dr.m.issues) != 0 {
		t.Errorf("a board with no sprint running drew %d cards", len(dr.m.issues))
	}
	golden(t, "empty_nosprint_100x16.golden", dr.view())
}

func TestBoard_SaysWhyTheSprintsCouldNotBeRead(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"a refusal":           &jira.CapabilityError{Reason: "you need Browse Projects on PROJ"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "GET /sprint", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(6)
			dr := newDriver(t, testDeps(sprintsFail{Fake: f, err: err}), 100, 16)

			mustContain(t, dr.view(), "The sprints on this board could not be read.")
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the failure was reported at level %v", got)
			}
			if n := countCalls(f, "BoardIssues"); n != 0 {
				t.Errorf("a board whose sprints could not be read fell back to its whole filter %d times", n)
			}
		})
	}
}

type sprintsFail struct {
	*jiratest.Fake
	err error
}

func (s sprintsFail) Sprints(context.Context, int64, ...jira.SprintState) (jira.Page[jira.Sprint], error) {
	return jira.Page[jira.Sprint]{}, s.err
}
