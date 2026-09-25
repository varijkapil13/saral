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

// heldDriver is newDriver with every page after the first kept in flight.
func heldDriver(t *testing.T, d kernel.Deps, w, h int) *driver {
	t.Helper()
	view, ok := New(d).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	dr := &driver{t: t, m: view, holdPages: true}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.send(kernel.FocusMsg{Focused: true})
	dr.run(dr.m.Init())
	return dr
}

// transitionInto is the transition of key that the board lands in col.
func transitionInto(t *testing.T, dr *driver, f jira.Mover, key string, col int) []jira.Transition {
	t.Helper()
	moves, err := f.Transitions(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := dr.m.moveInto(moves, col); !found {
		t.Fatalf("no transition takes %s into column %d", key, col)
	}
	return moves
}

func TestBoard_ARefusedMovePutsTheCardBackAndLeavesTheBoardFresh(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"a 400":               &jira.ValidationError{Messages: []string{"PROJ-3 cannot move while it is blocked"}},
		"a 403":               &jira.CapabilityError{Reason: "you need Transition Issues in this project"},
		"a 409":               &jira.ConflictError{Resource: "PROJ-3", Detail: "PROJ-3 was changed by someone else"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "POST /transitions", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			dr := newDriver(t, testDeps(fake), 120, 20)
			moves := transitionInto(t, dr, fake, "PROJ-3", 1)

			dr.key("m", "l")
			fake.FailNext(err)
			dr.send(movesMsg{gen: dr.m.moveGen, key: "PROJ-3", column: 1, moves: moves})

			if dr.m.card != nil || dr.m.moving {
				t.Error("the card is still in hand after the site refused the move")
			}
			if got := dr.column(0); !slices.Contains(got, "PROJ-3") {
				t.Errorf("the first column holds %v, want PROJ-3 put back", got)
			}
			if dr.m.stale {
				t.Error("one refused move badged the whole board stale")
			}
			reason, _ := jira.Reason(err)
			mustContain(t, dr.lastStatus().Text, reason)
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
			if name == "a 403" {
				golden(t, "refused_move_120x20.golden", dr.view())
			}
		})
	}
}

func TestBoard_ACardThatCouldNotBeReadForItsMovesGoesBack(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)

	dr.key("m", "l")
	fake.FailNext(&jira.CapabilityError{Reason: "you need Transition Issues in this project"})
	dr.key("enter")

	if dr.m.card != nil || dr.m.moving {
		t.Error("the card is still in hand")
	}
	if dr.m.stale {
		t.Error("a refused read of one card's moves badged the board stale")
	}
	mustContain(t, dr.lastStatus().Text, "Transition Issues")
}

// A move lands while the rest of the board is still being read: the walk goes
// on, the card lands where it was dropped, and nothing is read from the top.
func TestBoard_AMoveWhileLaterPagesLoadKeepsTheWalkAndTheCursor(t *testing.T) {
	t.Parallel()
	fake := newFake(24, jiratest.WithPageSize(5))
	dr := heldDriver(t, testDeps(fake), 120, 20)
	if len(dr.heldPages) == 0 || !dr.m.more {
		t.Fatal("the board read everything at once, so no page is in flight to keep")
	}
	if got := dr.column(0); len(got) == 0 {
		t.Fatal("the first page put nothing in the first column")
	}
	key := dr.column(0)[0]
	transitionInto(t, dr, fake, key, 1)
	pagesBefore := countCalls(fake, "SprintIssues")

	dr.key("m", "l", "enter")

	if got := dr.column(1); !slices.Contains(got, key) {
		t.Fatalf("the second column holds %v, want %s landed in it", got, key)
	}
	if got := dr.m.selectedKey(); got != key {
		t.Errorf("the cursor is on %q after the move, want it still on %s", got, key)
	}
	if n := countCalls(fake, "SprintIssues"); n != pagesBefore {
		t.Errorf("a landed move read the board again (%d reads, was %d)", n, pagesBefore)
	}
	if n := countCalls(fake, "IssueFields"); n != 1 {
		t.Errorf("the moved card was read back %d times, want once", n)
	}

	dr.release()

	if got := len(dr.m.issues); got != 24 {
		t.Errorf("the board holds %d cards once the walk finished, want all 24; the move cut the walk short", got)
	}
	if dr.m.more {
		t.Error("the board still says it has more to read")
	}
	if got := dr.column(1); !slices.Contains(got, key) {
		t.Errorf("%s left the column it was moved to once the rest arrived: %v", key, got)
	}
}

type rereadFails struct {
	*jiratest.Fake
}

func (rereadFails) IssueFields(context.Context, string, []string) (jira.Issue, error) {
	return jira.Issue{}, &jira.TransportError{Op: "GET /issue", Err: errors.New("connection reset")}
}

func TestBoard_AMovedCardThatCannotBeReadBackStaysWhereItLanded(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(rereadFails{Fake: fake}), 120, 20)

	dr.key("m", "l", "enter")

	if got := dr.column(1); !slices.Contains(got, "PROJ-3") {
		t.Errorf("the second column holds %v, want PROJ-3 where the move put it", got)
	}
	mustContain(t, dr.lastStatus().Text, "PROJ-3 moved", "connection reset")
}
