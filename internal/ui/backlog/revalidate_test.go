package backlog

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
// the Bubble Tea runtime does.
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

// openBacklog builds the real kernel around the backlog root and lets the
// first read settle, exactly as a running program would.
func openBacklog(t *testing.T, d kernel.Deps, w, h int) kernel.Model {
	t.Helper()
	m, err := kernel.New(d, kernel.WithSize(w, h), kernel.WithInitialView(ViewID))
	if err != nil {
		t.Fatal(err)
	}
	m = drainK(t, m, m.Init())
	next, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return drainK(t, next.(kernel.Model), cmd)
}

// firstIssueRow moves the cursor down from wherever it starts until it sits on
// an issue rather than a group header, and reports that issue's key.
func firstIssueRow(t *testing.T, m kernel.Model) (top kernel.Model, key string) {
	t.Helper()
	for range 200 {
		bm, ok := m.Top().(*Model)
		if !ok {
			t.Fatal("the backlog is not on top of the stack")
		}
		if key = bm.underKey(); key != "" {
			return m, key
		}
		m = keysK(t, m, "down")
	}
	t.Fatal("never found an issue row")
	return m, ""
}

// pushIssuePane opens key the way the palette's quick-open does: a Push the
// backlog itself has no direct gesture for, since it never opens issues on
// its own — an issue reached from the backlog is always reached through the
// palette, and this is what that Push looks like once it lands.
func pushIssuePane(t *testing.T, m kernel.Model, d kernel.Deps, f jira.IssueReader, key string) kernel.Model {
	t.Helper()
	iss, err := f.Issue(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	return sendK(t, m, kernel.PushMsg{View: issue.New(d, iss), ID: issue.ViewID, Title: key})
}

// TestBacklog_ARowRevalidatesAfterATransitionLandsInThePushedPane is the demo
// bug: transitioning an issue in a pane pushed over the backlog left its row
// showing the old status until a manual refresh.
func TestBacklog_ARowRevalidatesAfterATransitionLandsInThePushedPane(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	d := paneDeps(t, f)
	m := openBacklog(t, d, 140, 30)
	m, key := firstIssueRow(t, m)

	moves, err := f.Transitions(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	move, at := screenFreeTransition(t, moves)

	m = pushIssuePane(t, m, d, f, key)
	if _, ok := m.Top().(*Model); ok {
		t.Fatal("the push did not put the issue pane on top of the backlog")
	}
	m = keysK(t, m, "t")
	for range at {
		m = keysK(t, m, "down")
	}
	m = keysK(t, m, "enter", "y", "esc")

	bm, ok := m.Top().(*Model)
	if !ok {
		t.Fatal("esc did not pop back to the backlog")
	}
	if bm.underKey() != key {
		t.Errorf("the cursor no longer sits on %s, it sits on %s", key, bm.underKey())
	}
	at2, held := bm.byKey[key]
	if !held {
		t.Fatalf("%s is no longer in the backlog", key)
	}
	got := bm.issues[at2]
	if got.Status.ID != move.To.ID {
		t.Errorf("the row still shows status %+v, want %+v", got.Status, move.To)
	}
}

// TestBacklog_ARowRevalidatesAfterAFieldSaveLandsInThePushedPane covers the
// same bug for a field save that never transitions the issue at all.
func TestBacklog_ARowRevalidatesAfterAFieldSaveLandsInThePushedPane(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	var key string
	for i := 1; i <= 12; i++ {
		iss, err := f.Issue(t.Context(), fmt.Sprintf("PROJ-%d", i))
		if err == nil && iss.Assignee != nil {
			key = iss.Key
			break
		}
	}
	if key == "" {
		t.Fatal("no generated issue carries an assignee")
	}
	f.SetEditMeta(key, jira.FieldMeta{Field: jira.FieldRef{ID: "assignee"}, Name: "Assignee"})

	d := paneDeps(t, f)
	m := openBacklog(t, d, 140, 30)
	m = pushIssuePane(t, m, d, f, key)
	if _, ok := m.Top().(*Model); ok {
		t.Fatal("the push did not put the issue pane on top of the backlog")
	}
	m = sendK(t, m, kernel.BroadcastMsg{Msg: issue.UnassignMsg{}})
	m = keysK(t, m, "s", "esc")

	bm, ok := m.Top().(*Model)
	if !ok {
		t.Fatal("esc did not pop back to the backlog")
	}
	at2, held := bm.byKey[key]
	if !held {
		t.Fatalf("%s is no longer in the backlog", key)
	}
	got := bm.issues[at2]
	if got.Assignee != nil {
		t.Errorf("the row still shows %s assigned, want unassigned", got.Assignee.DisplayName)
	}
}

// TestBacklog_ARevalidateFailureLeavesTheRowAloneAndWarns is the failure path:
// a re-read that comes back 403, rate-limited or transport-broken must not
// crash the backlog and must not touch the row it could not confirm.
func TestBacklog_ARevalidateFailureLeavesTheRowAloneAndWarns(t *testing.T) {
	t.Parallel()

	for name, failure := range map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Browse Projects in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "GET /issue", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(12)
			dr := newDriver(t, testDeps(f), 120, 24)
			var key string
			for range 200 {
				if key = dr.m.underKey(); key != "" {
					break
				}
				dr.key("down")
			}
			if key == "" {
				t.Fatal("never found an issue row")
			}
			at, held := dr.m.byKey[key]
			if !held {
				t.Fatal("the cursor's key is not in byKey")
			}
			before := dr.m.issues[at]

			f.FailNext(failure)
			dr.send(issue.ChangedMsg{Key: key})

			got := dr.m.issues[dr.m.byKey[key]]
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
