package comment

import (
	"runtime"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// clickOn presses the left button in the middle of a zone. Zones are recorded
// on the manager's own goroutine as a side effect of scanning a drawn frame, so
// the zone is looked for until it appears rather than assumed to be there.
func clickOn(t *testing.T, d kernel.Deps, dr *driver, name string) {
	t.Helper()

	_ = d.Zones.Scan(dr.m.View())
	id := dr.m.zonePrefix + name
	eventually(t, "the zone "+id+" to be recorded", func() bool {
		return !d.Zones.Get(id).IsZero()
	})
	at := d.Zones.Get(id)
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
	}
}

func TestThread_ClickingACommentSelectsItAndClickingItAgainEditsIt(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	first := comment(t, f, "PROJ-1", "The older one.")
	comment(t, f, "PROJ-1", "The newer one.")
	d := testDeps(t, f)
	dr := newDriver(t, d, "PROJ-1", 100, 24)

	if dr.m.cursor != 1 {
		t.Fatalf("the cursor opened on %d, want the newest comment", dr.m.cursor)
	}
	clickOn(t, d, dr, zoneComment+first.ID)
	if dr.m.cursor != 0 {
		t.Fatalf("a click left the cursor on %d, want the comment that was clicked", dr.m.cursor)
	}

	clickOn(t, d, dr, zoneComment+first.ID)
	if dr.m.mode != writing || dr.m.editing != first.ID {
		t.Errorf("a second click on the selected comment did not open the editor on it")
	}
}

func TestThread_ClickingWriteOpensTheEditorOnANewComment(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "Something to reply to.")
	d := testDeps(t, f)
	dr := newDriver(t, d, "PROJ-1", 100, 24)

	clickOn(t, d, dr, zoneWrite)

	if dr.m.mode != writing || dr.m.editing != "" {
		t.Errorf("clicking write did not open the editor on a new comment")
	}
}

func TestThread_ClickingDeleteAsksRatherThanDeletes(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	comment(t, f, "PROJ-1", "The one that must not vanish on a click.")
	d := testDeps(t, f)
	dr := newDriver(t, d, "PROJ-1", 100, 24)

	clickOn(t, d, dr, zoneDelete)

	if dr.m.mode != confirming {
		t.Fatal("clicking delete did not put the confirmation up")
	}
	if got := countCalls(f, "DeleteComment"); got != 0 {
		t.Fatalf("clicking delete removed %d comments without asking", got)
	}

	clickOn(t, d, dr, zoneConfirm)
	if got := countCalls(f, "DeleteComment"); got != 1 {
		t.Errorf("clicking the confirmation deleted %d comments, want 1", got)
	}
}

func TestThread_ClickingTheEditorsOwnActionsSendsAndPutsAside(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	d := testDeps(t, f)
	dr := newDriver(t, d, "PROJ-1", 100, 24)

	dr.key("a")
	dr.typeText("Sent with the mouse.")
	clickOn(t, d, dr, zoneSend)

	if dr.m.mode != browsing {
		t.Fatal("clicking send left the editor open")
	}
	if got := countCalls(f, "AddComment"); got != 1 {
		t.Fatalf("clicking send stored %d comments, want 1", got)
	}

	dr.key("a")
	dr.typeText("Put aside with the mouse.")
	clickOn(t, d, dr, zoneCancel)

	if dr.m.mode != browsing {
		t.Error("clicking the cancel hint left the editor open")
	}
	dr.key("a")
	if got := dr.m.editor.Value(); got != "Put aside with the mouse." {
		t.Errorf("the draft did not survive the click: %q", got)
	}
}
