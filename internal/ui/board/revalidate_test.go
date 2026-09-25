package board

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// drainK runs a command tree to exhaustion against the real kernel, the way
// the Bubble Tea runtime does, unlike settle's depth-bounded recursion — a
// push, a read, a transition and a revalidate chain several hops deeper than
// settle's tests need.
func drainK(t *testing.T, m kernel.Model, cmd tea.Cmd) kernel.Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 4000 {
			t.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		updated, follow := m.Update(msg)
		model, ok := updated.(kernel.Model)
		if !ok {
			t.Fatal("Update did not return a kernel.Model")
		}
		m = model
		queue = append(queue, follow)
	}
	return m
}

func sendK(t *testing.T, m kernel.Model, msg tea.Msg) kernel.Model {
	t.Helper()
	next, cmd := m.Update(msg)
	model, ok := next.(kernel.Model)
	if !ok {
		t.Fatal("Update did not return a kernel.Model")
	}
	return drainK(t, model, cmd)
}

func keysK(t *testing.T, m kernel.Model, strokes ...string) kernel.Model {
	t.Helper()
	for _, s := range strokes {
		m = sendK(t, m, keyPress(s))
	}
	return m
}

// paneDeps is testDeps with its own drafts directory. An issue pane keeps
// unsent text under kernel.Deps.DraftRoot, which falls back to the real
// config directory when DraftsDir is empty; two parallel tests pushing a pane
// over the same fixture key would otherwise see each other's drafts.
func paneDeps(t *testing.T, f jira.SessionClient) kernel.Deps {
	t.Helper()
	d := testDeps(f)
	d.DraftsDir = t.TempDir()
	return d
}

func screenFreeTransition(t *testing.T, moves []jira.Transition) (move jira.Transition, at int) {
	t.Helper()
	at = slices.IndexFunc(moves, func(tr jira.Transition) bool {
		for i := range tr.Fields {
			if tr.Fields[i].Required {
				return false
			}
		}
		return true
	})
	if at < 0 {
		t.Fatal("this fixture issue has no screen-free transition to move it through")
	}
	return moves[at], at
}

// TestBoard_ARowRevalidatesAfterATransitionLandsInThePushedPane is the demo
// bug: opening a card, transitioning it in the pushed issue pane and going
// back left the board showing the old column until a manual refresh.
func TestBoard_ARowRevalidatesAfterATransitionLandsInThePushedPane(t *testing.T) {
	t.Parallel()

	f := newFake(9)
	m, err := kernel.New(paneDeps(t, f), kernel.WithSize(140, 30), kernel.WithInitialView(ViewID))
	if err != nil {
		t.Fatal(err)
	}
	m = drainK(t, m, m.Init())
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = drainK(t, next.(kernel.Model), cmd)

	bm := m.Top().(*Model)
	iss := bm.issueAt(bm.curCol, bm.curRow)
	if iss == nil {
		t.Fatal("no card sits under the cursor")
	}
	key := iss.Key

	moves, err := f.Transitions(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	move, at := screenFreeTransition(t, moves)

	m = keysK(t, m, "enter")
	if _, ok := m.Top().(*Model); ok {
		t.Fatal("enter did not push the issue pane over the board")
	}
	m = keysK(t, m, "t")
	for range at {
		m = keysK(t, m, "down")
	}
	m = keysK(t, m, "enter", "y", "esc")

	bm, ok := m.Top().(*Model)
	if !ok {
		t.Fatalf("esc did not pop back to the board, top is %T", m.Top())
	}
	if bm.selectedKey() != key {
		t.Errorf("the cursor no longer sits on %s, it sits on %s", key, bm.selectedKey())
	}
	got := bm.byKey(key)
	if got == nil {
		t.Fatalf("%s is no longer on the board", key)
	}
	if got.Status.ID != move.To.ID {
		t.Errorf("the card still shows status %+v, want %+v", got.Status, move.To)
	}
}

// TestBoard_ARowRevalidatesAfterAFieldSaveLandsInThePushedPane covers the same
// bug for a field save that never transitions the issue at all.
func TestBoard_ARowRevalidatesAfterAFieldSaveLandsInThePushedPane(t *testing.T) {
	t.Parallel()

	f := newFake(9)
	for i := 1; i <= 9; i++ {
		f.SetEditMeta(fmt.Sprintf("PROJ-%d", i), jira.FieldMeta{Field: jira.FieldRef{ID: "assignee"}, Name: "Assignee"})
	}
	m, err := kernel.New(paneDeps(t, f), kernel.WithSize(140, 30), kernel.WithInitialView(ViewID))
	if err != nil {
		t.Fatal(err)
	}
	m = drainK(t, m, m.Init())
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	m = drainK(t, next.(kernel.Model), cmd)

	bm := m.Top().(*Model)
	iss := bm.issueAt(bm.curCol, bm.curRow)
	if iss == nil || iss.Assignee == nil {
		t.Fatal("the card under the cursor is not a fixture assignee can be dropped from")
	}
	key := iss.Key

	m = keysK(t, m, "enter")
	if _, ok := m.Top().(*Model); ok {
		t.Fatal("enter did not push the issue pane over the board")
	}
	m = sendK(t, m, kernel.BroadcastMsg{Msg: issue.UnassignMsg{}})
	m = keysK(t, m, "s", "esc")

	bm, ok := m.Top().(*Model)
	if !ok {
		t.Fatal("esc did not pop back to the board")
	}
	if bm.selectedKey() != key {
		t.Errorf("the cursor no longer sits on %s, it sits on %s", key, bm.selectedKey())
	}
	got := bm.byKey(key)
	if got == nil {
		t.Fatalf("%s is no longer on the board", key)
	}
	if got.Assignee != nil {
		t.Errorf("the card still shows %s assigned, want unassigned", got.Assignee.DisplayName)
	}
}

// TestBoard_ARevalidateFailureLeavesTheCardAloneAndWarns is the failure path:
// a re-read that comes back 403, rate-limited or transport-broken must not
// crash the board and must not touch the card it could not confirm.
func TestBoard_ARevalidateFailureLeavesTheCardAloneAndWarns(t *testing.T) {
	t.Parallel()

	for name, failure := range map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Browse Projects in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "GET /issue", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(9)
			dr := newDriver(t, testDeps(f), 120, 20)
			key := dr.m.issueAt(dr.m.curCol, dr.m.curRow).Key
			before := *dr.m.byKey(key)

			f.FailNext(failure)
			dr.send(issue.ChangedMsg{Key: key})

			got := dr.m.byKey(key)
			if got == nil {
				t.Fatalf("%s is gone from the board after a failed revalidate", key)
			}
			if got.Status.ID != before.Status.ID || got.Summary != before.Summary {
				t.Errorf("the card changed after a failed revalidate: %+v, want %+v", got, before)
			}
			reason, _ := jira.Reason(failure)
			status := dr.lastStatus()
			if status.Level != kernel.LevelWarn || !strings.Contains(status.Text, reason) {
				t.Errorf("status = %+v, want a warning naming %q", status, reason)
			}
		})
	}
}
