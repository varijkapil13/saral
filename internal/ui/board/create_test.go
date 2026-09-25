package board

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/form"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const storyType = "10301"

// laggingSite hides one issue from the board's sprint read, the way an index
// that has not caught up with a create does.
type laggingSite struct {
	*jiratest.Fake

	mu   sync.Mutex
	hide string
}

func (s *laggingSite) SprintIssues(ctx context.Context, boardID, sprintID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	page, err := s.Fake.SprintIssues(ctx, boardID, sprintID, q)
	s.mu.Lock()
	hide := s.hide
	s.mu.Unlock()
	if hide != "" {
		page.Items = slices.DeleteFunc(page.Items, func(iss jira.Issue) bool { return iss.Key == hide })
	}
	return page, err
}

func made(t *testing.T, f *jiratest.Fake) jira.Issue {
	t.Helper()
	iss, err := f.CreateIssue(context.Background(), jira.IssueInput{
		ProjectKey: "PROJ", IssueTypeID: storyType, Summary: "Made from a board column",
	})
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

// createdIn presses c on a column and answers as the form would once the issue
// exists.
func createdIn(t *testing.T, dr *driver, f *jiratest.Fake, col int) jira.Issue {
	t.Helper()
	dr.moveTo(col, 0)
	dr.key("c")
	if len(dr.pushes) == 0 || dr.pushes[len(dr.pushes)-1].ID != form.ViewID {
		t.Fatalf("c pushed %+v, want the create form", dr.pushes)
	}
	iss := made(t, f)
	dr.send(form.CreatedMsg{Issue: iss, Sprint: dr.m.sprint})
	return iss
}

func TestCreate_CPushesTheFormForTheSprintOnScreen(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(9)), 120, 20)
	dr.moveTo(1, 0)
	dr.key("c")
	if len(dr.pushes) != 1 {
		t.Fatalf("c pushed %d views, want the form", len(dr.pushes))
	}
	pushed, ok := dr.pushes[0].View.(*form.Model)
	if !ok {
		t.Fatalf("c pushed a %T, want the create form", dr.pushes[0].View)
	}
	pushed.Update(kernel.SizeMsg{Width: 100, Height: 20})
	mustContain(t, pushed.View(), "for "+dr.m.sprint.Name)
	if dr.m.creating == nil || dr.m.creating.col != 1 {
		t.Errorf("the board remembers %+v as where the create came from, want column 1", dr.m.creating)
	}
}

func TestCreate_TheNewIssueLandsInTheColumnItWasMadeIn(t *testing.T) {
	t.Parallel()
	for name, col := range map[string]int{
		"the column it is created in":      0,
		"a column a workflow move reaches": 1,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			dr := newDriver(t, testDeps(fake), 120, 20)
			iss := createdIn(t, dr, fake, col)

			if !slices.Contains(dr.column(col), iss.Key) {
				t.Fatalf("column %d holds %v, want %s in it", col, dr.column(col), iss.Key)
			}
			if got := dr.m.selectedKey(); got != iss.Key {
				t.Errorf("the cursor is on %s, want it on the new card", got)
			}
			mustContain(t, dr.lastStatus().Text, iss.Key+" created in "+dr.m.plan.columns[col].name)
			stored, err := fake.Issue(context.Background(), iss.Key)
			if err != nil {
				t.Fatal(err)
			}
			if at, _ := dr.m.plan.columnOf(stored.Status.ID); at != col {
				t.Errorf("the site has %s in %s, want it in column %d", iss.Key, stored.Status.Name, col)
			}
			page, err := fake.SprintIssues(context.Background(), dr.m.plan.boardID, dr.m.sprint.ID, jira.BoardQuery{Fields: []string{"summary"}})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(page.Items, func(i jira.Issue) bool { return i.Key == iss.Key }) {
				t.Errorf("%s is not in the sprint on screen, so the next read would take it off the board", iss.Key)
			}
			if want := 0; col == 0 && countCalls(fake, "Transition") != want {
				t.Errorf("an issue created in its own column made %d transitions", countCalls(fake, "Transition"))
			}
		})
	}
}

// Done needs a resolution on the fake's workflow, so a create in the done
// column goes to the issue pane for the move, as a drop would.
func TestCreate_AMoveThatNeedsAScreenOpensTheIssuePane(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	iss := createdIn(t, dr, fake, 2)
	if last := dr.pushes[len(dr.pushes)-1]; last.ID != "issue" {
		t.Fatalf("the last push is %q, want the issue pane asking for the move", last.ID)
	}
	mustContain(t, dr.statuses[len(dr.statuses)-1].Text, iss.Key+" was created")
	if !slices.Contains(dr.column(0), iss.Key) {
		t.Errorf("the new card is not on the board where it was created: %v", dr.column(0))
	}
}

// A column no workflow move reaches says so rather than claiming the issue is
// there.
func TestCreate_AColumnNoMoveReachesIsExplained(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.moveTo(1, 0)
	dr.key("c")
	iss := made(t, fake)
	dr.m.plan.columns[1].statuses = []string{"99999"}
	dr.m.plan.byStatus = map[string]int{"10201": 0, "99999": 1, "10203": 2}
	dr.send(form.CreatedMsg{Issue: iss, Sprint: dr.m.sprint})
	got := dr.lastStatus()
	if got.Level != kernel.LevelWarn || !strings.Contains(got.Text, "no workflow move takes it") {
		t.Errorf("the status line says %+v, want the move that does not exist named", got)
	}
	mustContain(t, got.Text, iss.Key+" was created in "+dr.m.plan.columns[0].name)
}

func TestCreate_EachFailureSaysHowFarTheIssueGot(t *testing.T) {
	t.Parallel()
	for name, err := range refusals() {
		for _, step := range []struct {
			name, call, says string
		}{
			{"the sprint move", "MoveToSprint", "waits in the backlog"},
			{"the transition read", "Transitions", "not moved into"},
			{"the transition", "Transition", "not moved into"},
			{"the read back", "IssueFields", "reading it back failed"},
		} {
			t.Run(name+" on "+step.name, func(t *testing.T) {
				t.Parallel()
				fake := newFake(9)
				dr := newDriver(t, testDeps(fake), 120, 20)
				dr.moveTo(1, 0)
				dr.key("c")
				iss := made(t, fake)
				// A nil in the fake's queue lets that call through, so the
				// failure lands on the step named.
				calls := []string{"MoveToSprint", "Transitions", "Transition", "IssueFields"}
				for range slices.Index(calls, step.call) {
					fake.FailNext(nil)
				}
				fake.FailNext(err)
				dr.send(form.CreatedMsg{Issue: iss, Sprint: dr.m.sprint})
				if got := viewCalls(fake); !slices.Contains(got, step.call) {
					t.Fatalf("the landing never made %s: %v", step.call, got)
				}

				got := dr.lastStatus()
				if got.Level != kernel.LevelError {
					t.Errorf("the failure was reported at level %v: %q", got.Level, got.Text)
				}
				mustContain(t, got.Text, iss.Key+" was created, but", step.says)
				mustNotContain(t, got.Text, "created in Doing")
			})
		}
	}
}

// The form broadcasts a refresh as it closes, and the site's index can answer
// it before it has the new issue: the card stays where it landed.
func TestCreate_ARereadThatHasNotCaughtUpKeepsTheNewCard(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	site := &laggingSite{Fake: fake}
	dr := newDriver(t, testDeps(site), 120, 20)
	iss := createdIn(t, dr, fake, 1)
	site.mu.Lock()
	site.hide = iss.Key
	site.mu.Unlock()

	dr.send(kernel.RefreshMsg{})
	if !slices.Contains(dr.column(1), iss.Key) {
		t.Fatalf("a read that lacks %s took it off the board: %v", iss.Key, dr.column(1))
	}

	site.mu.Lock()
	site.hide = ""
	site.mu.Unlock()
	dr.send(kernel.RefreshMsg{})
	n := 0
	for _, key := range dr.column(1) {
		if key == iss.Key {
			n++
		}
	}
	if n != 1 || len(dr.m.landed) != 0 {
		t.Errorf("once the read has it, the board draws it %d times and still keeps %v", n, dr.m.landed)
	}
}

func TestCreate_AReportForABoardNoLongerOnScreenDrawsNothing(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.key("c")
	iss := made(t, fake)
	dr.m.plan.boardID = 777
	dr.send(form.CreatedMsg{Issue: iss, Sprint: dr.m.sprint})
	if dr.m.indexOf(iss.Key) >= 0 {
		t.Errorf("%s was drawn on a board it was not made from", iss.Key)
	}
	mustContain(t, dr.lastStatus().Text, iss.Key+" created")
}

func TestCreate_IsRefusedWithoutABoardOrAConnection(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(nil), 120, 20)
	dr.key("c")
	if len(dr.pushes) != 0 {
		t.Error("c pushed a form with no connection to create with")
	}
	if got := dr.lastStatus(); got.Level != kernel.LevelWarn {
		t.Errorf("the refusal was said at level %v: %q", got.Level, got.Text)
	}
}

// A read that pages brings the new issue on a later page than the first: the
// copy carried over the first page gives way to the one the page brings.
func TestCreate_ALaterPageBringingTheNewCardLeavesOneCopy(t *testing.T) {
	t.Parallel()
	fake := newFake(9, jiratest.WithPageSize(4))
	dr := newDriver(t, testDeps(fake), 120, 20)
	iss := createdIn(t, dr, fake, 1)
	dr.send(kernel.RefreshMsg{})
	n := 0
	for i := range dr.m.issues {
		if dr.m.issues[i].Key == iss.Key {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the board holds %s %d times after a paged read, want once", iss.Key, n)
	}
	if len(dr.m.landed) != 0 {
		t.Errorf("the board still carries %v once the read brought it", dr.m.landed)
	}
}
