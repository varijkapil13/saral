package issue

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func openMover(t *testing.T, client jira.Client, iss jira.Issue, w, h int) *panel {
	t.Helper()

	return newPanel(t, NewMove(testDeps(client), iss), w, h)
}

// stubMoves answers with transitions of the test's own, for the screens the
// fake's workflow does not produce.
type stubMoves struct {
	jira.Client

	moves []jira.Transition
}

func (s stubMoves) Transitions(context.Context, string) ([]jira.Transition, error) {
	return slices.Clone(s.moves), nil
}

func (s stubMoves) Transition(context.Context, string, string, jira.IssuePatch) error { return nil }

func moveNames(moves []jira.Transition) []string {
	out := make([]string, 0, len(moves))
	for _, move := range moves {
		out = append(out, move.To.Name)
	}
	slices.Sort(out)
	return out
}

// TestMove_ListsWhatThisIssueCanDoRightNow is why the transitions are read per
// issue: what is available depends on the status the issue is in at the moment
// of asking, so two issues on one site answer differently.
func TestMove_ListsWhatThisIssueCanDoRightNow(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	seen := map[string][]string{}
	for _, key := range []string{"PROJ-1", "PROJ-3"} {
		iss := fullIssue(t, f, key)
		p := openMover(t, f, iss, 100, 28)
		m := p.mover()
		if len(m.moves) == 0 {
			t.Fatalf("%s can do nothing at all", key)
		}
		for _, move := range m.moves {
			if move.To.ID == iss.Status.ID {
				t.Errorf("%s is offered a move to the status it is already in", key)
			}
			if move.ID == "" {
				t.Errorf("%s is offered a move with no id, and a name is what a localised site translates", key)
			}
		}
		seen[key] = moveNames(m.moves)
	}
	if slices.Equal(seen["PROJ-1"], seen["PROJ-3"]) {
		t.Errorf("both issues are offered %v; the moves are read per issue for a reason", seen["PROJ-1"])
	}
}

func TestMove_AsksBeforeItMovesAndSendsTheTransitionId(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	client := record(f)
	iss := fullIssue(t, f, "PROJ-3")
	p := openMover(t, client, iss, 100, 28)

	want := p.mover().moves[0]
	p.keys("enter")

	frame := p.frame()
	if !strings.Contains(frame, "Move PROJ-3 from "+iss.Status.Name+" to "+want.To.Name+"?") {
		t.Fatalf("the confirmation does not say what it is about to do:\n%s", frame)
	}
	if client.writes() != 0 {
		t.Fatal("the move happened before the confirmation was answered")
	}

	p.keys("n")
	if client.writes() != 0 {
		t.Fatal("a key that is not the confirmation moved the issue anyway")
	}

	p.keys("enter", "y")
	move := client.lastMove(t)
	if move.id != want.ID {
		t.Errorf("the move was made as %q, want the id %q", move.id, want.ID)
	}
	if p.pops != 1 {
		t.Errorf("the pane was popped %d times, want once", p.pops)
	}
	if !strings.Contains(p.statusText(), want.To.Name) {
		t.Errorf("status = %q, want it to name the status the issue is now in", p.statusText())
	}
}

func TestMove_FillsARequiredScreenFieldFromTheValuesTheSiteOffers(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	client := record(f)
	iss := fullIssue(t, f, "PROJ-3")
	p := openMover(t, client, iss, 100, 28)

	m := p.mover()
	at := slices.IndexFunc(m.moves, func(tr jira.Transition) bool { return tr.HasScreen })
	if at < 0 {
		t.Fatal("no move on this issue has a screen")
	}
	for range at {
		p.keys("down")
	}
	p.keys("enter")

	if p.mover().stage != moveScreen {
		t.Fatalf("stage = %v, want the screen the move needs", p.mover().stage)
	}
	fields := p.mover().fields
	if len(fields) == 0 {
		t.Fatal("the screen shows no required field")
	}
	first := fields[0].value()
	p.keys("right")
	second := p.mover().fields[0].value()
	if first.ID == second.ID {
		t.Fatalf("the value did not change; the picker offers %d values", len(fields[0].options))
	}

	p.keys("enter")
	if !strings.Contains(p.frame(), second.Label) {
		t.Errorf("the confirmation does not name the value being sent:\n%s", p.frame())
	}
	p.keys("y")

	move := client.lastMove(t)
	sent, ok := move.patch.Fields.ByID(fields[0].meta.Field.ID)
	if !ok {
		t.Fatalf("the screen's answer was not sent: %+v", move.patch)
	}
	if len(sent.Options) != 1 || sent.Options[0].ID != second.ID {
		t.Errorf("sent %+v, want the option id the site offered", sent.Options)
	}
}

func TestMove_SaysWhyAMoveItCannotCompleteCannotBeMadeHere(t *testing.T) {
	t.Parallel()

	client := stubMoves{Client: newFake(4), moves: []jira.Transition{{
		ID:        "77",
		Name:      "Escalate",
		To:        jira.Status{ID: "10999", Name: "Escalated", Category: jira.CategoryInProgress},
		HasScreen: true,
		Fields: []jira.FieldMeta{{
			Field:    jira.FieldRef{ID: "customfield_13999", Name: "Escalation owner"},
			Name:     "Escalation owner",
			Required: true,
		}},
	}}}
	p := openMover(t, client, jira.Issue{Key: "PROJ-1", Status: jira.Status{Name: "Triage"}}, 100, 28)

	p.keys("enter")
	if p.mover().stage != moveScreen {
		t.Fatalf("stage = %v, want the screen", p.mover().stage)
	}
	frame := p.frame()
	if !strings.Contains(frame, "Escalation owner") {
		t.Errorf("the pane does not name the field it cannot fill:\n%s", frame)
	}

	p.keys("enter")
	if p.mover().stage == moveConfirm {
		t.Fatal("the pane offered to make a move it cannot complete")
	}
	if !strings.Contains(p.statusText(), "in the browser") {
		t.Errorf("status = %q, want it to say where this move can be made", p.statusText())
	}
	if !strings.Contains(p.frame(), "nothing to choose from") {
		t.Errorf("the row does not say why it is empty:\n%s", p.frame())
	}
}

func TestMove_LeavesAnOptionalScreenFieldAlone(t *testing.T) {
	t.Parallel()

	client := stubMoves{Client: newFake(4), moves: []jira.Transition{{
		ID:        "78",
		Name:      "Park",
		To:        jira.Status{ID: "10998", Name: "Parked", Category: jira.CategoryToDo},
		HasScreen: true,
		Fields: []jira.FieldMeta{{
			Field:         jira.FieldRef{ID: "customfield_13998", Name: "Reason"},
			Name:          "Reason",
			AllowedValues: []jira.Option{{ID: "1", Label: "Waiting"}},
		}},
	}}}
	recorded := record(client)
	p := openMover(t, recorded, jira.Issue{Key: "PROJ-1", Status: jira.Status{Name: "Triage"}}, 100, 28)

	p.keys("enter")
	if p.mover().stage != moveConfirm {
		t.Fatalf("stage = %v; a screen whose fields are all optional needs no answers", p.mover().stage)
	}
	p.keys("y")

	if got := recorded.lastMove(t).patch; got.Fields.Len() != 0 {
		t.Errorf("the move sent %v; an optional field nobody filled is not a value to invent", got.Fields.IDs())
	}
}

func TestMove_ClickingAMovePicksItAndClickingItAgainChoosesIt(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	p := newPanel(t, NewMove(d, fullIssue(t, f, "PROJ-6")), 100, 28)

	want := p.mover().moves[1]
	at := p.zoneAt(d, "move:"+want.ID)
	p.clickAt(at)
	if got := p.mover().moves[p.mover().cursor].ID; got != want.ID {
		t.Fatalf("the click put the cursor on %s, want %s", got, want.ID)
	}

	p.clickAt(at)
	if p.mover().stage == moveList {
		t.Error("a second click on the move under the cursor did not choose it")
	}
}

func TestMove_ReportsEveryWayReadingTheMovesCanFail(t *testing.T) {
	t.Parallel()

	for _, tc := range failureCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(6)
			iss := fullIssue(t, f, "PROJ-3")
			f.FailNext(tc.err)
			p := openMover(t, f, iss, 100, 28)

			if !strings.Contains(p.statusText(), tc.want) {
				t.Errorf("status = %q, want the error's own wording (%q)", p.statusText(), tc.want)
			}
			if len(p.mover().moves) != 0 {
				t.Error("the pane is offering moves it never read")
			}
		})
	}
}

func TestMove_ReportsEveryWayTheMoveItselfCanFail(t *testing.T) {
	t.Parallel()

	for _, tc := range failureCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(6)
			iss := fullIssue(t, f, "PROJ-3")
			p := openMover(t, f, iss, 100, 28)

			f.FailNext(tc.err)
			p.keys("enter", "y")

			if !strings.Contains(p.statusText(), tc.want) {
				t.Errorf("status = %q, want the error's own wording (%q)", p.statusText(), tc.want)
			}
			if p.pops != 0 {
				t.Error("the pane closed itself over a move that did not happen")
			}
			after := fullIssue(t, f, "PROJ-3")
			if after.Status.ID != iss.Status.ID {
				t.Errorf("the issue is now %s; the move failed", after.Status.Name)
			}
		})
	}
}

// TestMove_StopsReadingWhenThePaneIsClosed is the contract every view here
// keeps: a pane nobody is looking at cancels what it asked for rather than
// leaving a request running for an answer that has nowhere to go.
func TestMove_StopsReadingWhenThePaneIsClosed(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	iss := fullIssue(t, f, "PROJ-3")
	view := NewMove(testDeps(f), iss)
	view, _ = view.Update(kernel.SizeMsg{Width: 100, Height: 28})
	cmd := view.Init()
	if _, closed := view.Update(kernel.FocusMsg{Focused: false}); closed != nil {
		t.Fatal("closing the pane asked for more work")
	}

	failed, ok := cmd().(editFailedMsg)
	if !ok {
		t.Fatalf("the read came back as %T, want the failure a cancelled context produces", cmd())
	}
	if !errors.Is(failed.err, context.Canceled) {
		t.Errorf("err = %v, want the context's own error", failed.err)
	}
}

type failureCase struct {
	name string
	err  error
	want string
}

func failureCases() []failureCase {
	return []failureCase{
		{"a refusal", &jira.CapabilityError{Reason: "you need Transition Issues in this project"}, "Transition Issues"},
		{"a rate limit", &jira.RateLimitError{RetryAfter: 30 * time.Second}, "retry in 30s"},
		{"a transport failure", &jira.TransportError{Op: "GET /transitions", Err: errors.New("connection reset")}, "connection reset"},
	}
}
