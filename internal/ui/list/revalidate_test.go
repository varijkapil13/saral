package list

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// screenFreeTransition is a move that lands without a screen, so choosing it
// goes straight to the confirmation rather than a required-fields form this
// test has no business filling in.
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

// firstAssignedIssue is the first generated issue that carries an assignee,
// since unassigning one that already has none dirties nothing.
func firstAssignedIssue(t *testing.T, f *jiratest.Fake, n int) jira.Issue {
	t.Helper()
	for i := 1; i <= n; i++ {
		iss, err := f.Issue(t.Context(), fmt.Sprintf("PROJ-%d", i))
		if err == nil && iss.Assignee != nil {
			return iss
		}
	}
	t.Fatal("no generated issue carries an assignee")
	return jira.Issue{}
}

// selectOn scrolls the list until the cursor lands on key, however this
// fixture happened to order it.
func selectOn(t *testing.T, m kernel.Model, key string) kernel.Model {
	t.Helper()
	for range 200 {
		lm, ok := m.Top().(*Model)
		if !ok {
			t.Fatal("the list is not on top of the stack")
		}
		if lm.selectedKey() == key {
			return m
		}
		m = keys(t, m, "down")
	}
	t.Fatalf("could not scroll the list onto %s", key)
	return m
}

func issueAt(t *testing.T, lm *Model, key string) jira.Issue {
	t.Helper()
	at := slices.IndexFunc(lm.issues, func(iss jira.Issue) bool { return iss.Key == key })
	if at < 0 {
		t.Fatalf("%s is no longer among the list's rows", key)
	}
	return lm.issues[at]
}

// paneDeps is testDeps with its own drafts directory. An issue pane keeps
// unsent text under kernel.Deps.DraftRoot, which falls back to the real
// config directory when DraftsDir is empty; two parallel tests pushing a pane
// over the same fixture key would otherwise see each other's drafts.
func paneDeps(t *testing.T, f jira.Client) kernel.Deps {
	t.Helper()
	d := testDeps(f)
	d.DraftsDir = t.TempDir()
	return d
}

// TestList_ARowRevalidatesAfterATransitionLandsInThePushedPane is the demo bug:
// opening an issue, transitioning it in the pane, and going back left the list
// showing the old status until a manual refresh. The row must now revalidate
// on its own, without a full reload, and the cursor must stay where it was.
func TestList_ARowRevalidatesAfterATransitionLandsInThePushedPane(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	m := startAll(t, paneDeps(t, f), 120, 30)

	lm := m.Top().(*Model)
	key := lm.selectedKey()
	cursorBefore := lm.cursor

	moves, err := f.Transitions(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	move, at := screenFreeTransition(t, moves)

	m = keys(t, m, "enter")
	if _, ok := m.Top().(*Model); ok {
		t.Fatal("enter did not push the issue pane over the list")
	}
	m = keys(t, m, "t")
	for range at {
		m = keys(t, m, "down")
	}
	m = keys(t, m, "enter", "y", "esc")

	lm, ok := m.Top().(*Model)
	if !ok {
		t.Fatal("esc did not pop back to the list")
	}
	if lm.cursor != cursorBefore {
		t.Errorf("the cursor moved from %d to %d", cursorBefore, lm.cursor)
	}
	got := issueAt(t, lm, key)
	if got.Status.ID != move.To.ID {
		t.Errorf("the row still shows status %+v, want %+v", got.Status, move.To)
	}
	mustContain(t, frame(m), move.To.Name)
}

// TestList_ARowRevalidatesAfterAFieldSaveLandsInThePushedPane covers the same
// bug for a field save that never transitions the issue at all.
func TestList_ARowRevalidatesAfterAFieldSaveLandsInThePushedPane(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	iss := firstAssignedIssue(t, f, 20)
	f.SetEditMeta(iss.Key, jira.FieldMeta{Field: jira.FieldRef{ID: "assignee"}, Name: "Assignee"})
	m := startAll(t, paneDeps(t, f), 120, 30)
	m = selectOn(t, m, iss.Key)

	lm := m.Top().(*Model)
	cursorBefore := lm.cursor

	m = keys(t, m, "enter")
	if _, ok := m.Top().(*Model); ok {
		t.Fatal("enter did not push the issue pane over the list")
	}
	m = send(t, m, kernel.BroadcastMsg{Msg: issue.UnassignMsg{}})
	m = keys(t, m, "s", "esc")

	lm, ok := m.Top().(*Model)
	if !ok {
		t.Fatal("esc did not pop back to the list")
	}
	if lm.cursor != cursorBefore {
		t.Errorf("the cursor moved from %d to %d", cursorBefore, lm.cursor)
	}
	got := issueAt(t, lm, iss.Key)
	if got.Assignee != nil {
		t.Errorf("the row still shows %s assigned, want unassigned", got.Assignee.DisplayName)
	}
}

// TestList_ARevalidateFailureLeavesTheRowAloneAndWarns is the failure path: a
// re-read that comes back 403, rate-limited or transport-broken must not crash
// the list and must not touch the row it could not confirm.
func TestList_ARevalidateFailureLeavesTheRowAloneAndWarns(t *testing.T) {
	t.Parallel()

	for name, failure := range map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Browse Projects in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "GET /issue", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(10)
			dr := openAll(t, testDeps(f), 120, 30)
			key := dr.m.selectedKey()
			before := issueAt(t, dr.m, key)

			f.FailNext(failure)
			dr.send(issue.ChangedMsg{Key: key})

			got := issueAt(t, dr.m, key)
			if got.Status.ID != before.Status.ID || got.Summary != before.Summary {
				t.Errorf("the row changed after a failed revalidate: %+v, want %+v", got, before)
			}
			reason, _ := jira.Reason(failure)
			status := dr.lastStatus()
			if status.Level != kernel.LevelWarn || !strings.Contains(status.Text, reason) {
				t.Errorf("status = %+v, want a warning naming %q", status, reason)
			}
		})
	}
}
