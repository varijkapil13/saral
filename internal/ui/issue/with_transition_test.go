package issue

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

type failingMoves struct {
	jira.Client

	err error
}

func (s failingMoves) Transitions(context.Context, string) ([]jira.Transition, error) {
	return nil, s.err
}

func transitionsOf(t *testing.T, f jira.Mover, key string) []jira.Transition {
	t.Helper()
	moves, err := f.Transitions(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return moves
}

func TestWithTransition_OpensOnTheScreenOfTheMoveItWasGiven(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	rec := record(f)
	iss := readIssue(t, f, "PROJ-3")
	moves := transitionsOf(t, f, iss.Key)
	at := slices.IndexFunc(moves, func(tr jira.Transition) bool { return len(requiredFields(tr)) > 0 })
	if at < 0 {
		t.Fatal("no move on this issue needs a screen; this fixture cannot show one")
	}
	want := moves[at]

	p := newPanel(t, New(testDeps(t, rec), iss, WithTransition(want.ID), withDrafts(tempDrafts(t))), 100, 30)

	m := p.editor()
	if m.pick == nil || m.pick.kind != rkStatus {
		t.Fatal("the pane did not open its status picker")
	}
	if m.pick.move == nil || m.pick.move.ID != want.ID {
		t.Fatalf("the picker chose %+v, want the move %q it was opened for", m.pick.move, want.ID)
	}
	if m.pick.confirming || len(m.pick.fields) == 0 {
		t.Error("a move with a required field went past its screen")
	}
	if rec.writes() != 0 {
		t.Error("the move was applied before its screen was filled in")
	}
	if frame := p.frame(); !strings.Contains(frame, want.To.Name) {
		t.Errorf("the frame does not name where the move goes:\n%s", frame)
	}
}

func TestWithTransition_AScreenFreeMoveGoesStraightToTheQuestion(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	iss := readIssue(t, f, "PROJ-3")
	moves := transitionsOf(t, f, iss.Key)
	at := slices.IndexFunc(moves, func(tr jira.Transition) bool { return len(requiredFields(tr)) == 0 })
	if at < 0 {
		t.Fatal("every move on this issue needs a screen")
	}

	p := newPanel(t, New(testDeps(t, record(f)), iss, WithTransition(moves[at].ID), withDrafts(tempDrafts(t))), 100, 30)

	if m := p.editor(); m.pick == nil || !m.pick.confirming {
		t.Error("a move with nothing to fill in did not go straight to its confirmation")
	}
}

func TestWithTransition_AMoveNoLongerOfferedSaysSoAndLeavesTheList(t *testing.T) {
	t.Parallel()

	f := newFake(6)
	iss := readIssue(t, f, "PROJ-3")

	p := newPanel(t, New(testDeps(t, record(f)), iss, WithTransition("no-such-move"), withDrafts(tempDrafts(t))), 100, 30)

	m := p.editor()
	if m.pick == nil {
		t.Fatal("the status picker did not open")
	}
	if m.pick.move != nil {
		t.Errorf("a move the site no longer offers was chosen: %+v", m.pick.move)
	}
	if !strings.Contains(m.pick.fail, "no longer offered") {
		t.Errorf("the picker says %q, want it to say the move is gone", m.pick.fail)
	}
	if len(m.pick.moves) == 0 {
		t.Error("the moves that are offered were not listed")
	}
}

func TestWithTransition_ReportsEveryWayReadingTheMovesCanFail(t *testing.T) {
	t.Parallel()

	for _, tc := range failureCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(6)
			iss := readIssue(t, f, "PROJ-3")
			client := failingMoves{Client: f, err: tc.err}

			p := newPanel(t, New(testDeps(t, client), iss, WithTransition("31"), withDrafts(tempDrafts(t))), 100, 30)

			if !strings.Contains(p.statusText(), tc.want) {
				t.Errorf("status = %q, want it to carry %q", p.statusText(), tc.want)
			}
			if m := p.editor(); m.pick != nil && m.pick.move != nil {
				t.Error("a move was chosen although the moves could not be read")
			}
		})
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
