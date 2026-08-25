package issue

import (
	"strconv"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// The sidebar and the full screen are one model. A fresh one would read the
// thread again, land on the first comment and lose whatever was half written, so
// esc would not come back to where the reader was.
func TestComments_TheSidebarAndTheFullScreenAreOneModel(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-4", "The conversation the sidebar is already showing.")
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-4")), 120, 30)
	mustContain(t, p.frame(), "The conversation the sidebar")

	p.keys("C")
	if len(p.pushes) != 1 {
		t.Fatalf("C asked for %d views to be pushed, want one", len(p.pushes))
	}
	if got := p.pushes[0].ID; got != comment.ViewID {
		t.Errorf("the thread was pushed under %q, want %q so the footer is the thread's own", got, comment.ViewID)
	}
	if p.pushes[0].View != p.pane(t).thread {
		t.Error("C pushed a different model from the one the sidebar holds")
	}

	// Pressing it again while it is up does nothing rather than stacking a
	// second copy of the same model.
	p.keys("C")
	if len(p.pushes) != 1 {
		t.Errorf("C pushed the thread %d times", len(p.pushes))
	}
}

// The kernel gave the thread the whole screen on the way there, so coming back
// has to put its box back — otherwise the sidebar draws the top-left corner of a
// full-screen frame.
func TestComments_ComingBackPutsTheThreadBackInItsBox(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-5", "Something to come back to.")
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-5")), 120, 30)
	inBox := p.frame()

	p.keys("C")
	// The kernel resizes the pushed view and blurs the pane under it.
	p.send(kernel.FocusMsg{})
	p.pane(t).thread.Update(kernel.SizeMsg{Width: 120, Height: 30})
	p.send(kernel.FocusMsg{Focused: true})

	if got := p.frame(); got != inBox {
		t.Errorf("the thread came back at the wrong size:\n--- want ---\n%s\n--- got ---\n%s", inBox, got)
	}
}

// The palette's write, edit and delete arrive as broadcasts, and the sidebar is
// no place for an editor: the thread goes full screen and is handed the message
// there.
func TestComments_ThePaletteWriteGoesFullScreenFirst(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	addComment(t, f, "PROJ-6", "Something to edit.")
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-6")), 120, 30)

	p.send(comment.WriteMsg{})

	if len(p.pushes) != 1 {
		t.Fatalf("writing a comment from the palette pushed %d views, want the thread", len(p.pushes))
	}
	thread := newPanel(t, p.pushes[0].View, 120, 30)
	mustContain(t, thread.frame(), "ctrl+s")
}

// Once it is full screen the thread is on the kernel's stack and hears the
// broadcast itself, so the pane must not push it again.
func TestComments_APaletteWriteWhileTheThreadIsUpIsLeftToTheThread(t *testing.T) {
	t.Parallel()

	f := newFake(12)
	p := newPanel(t, New(testDeps(f), seedOf(t, f, "PROJ-7")), 120, 30)
	p.keys("C")
	p.send(comment.WriteMsg{})

	if len(p.pushes) != 1 {
		t.Errorf("the pane pushed the thread %d times", len(p.pushes))
	}
}

// A motion aimed at the thread is handed over as a keypress, so every entry in
// the table has to be one the kernel can spell and the thread can answer.
func TestThread_EveryMotionSpellsAStrokeItAnswers(t *testing.T) {
	t.Parallel()

	for at, want := range threadStrokes {
		if want == "" {
			t.Errorf("motion %d has no stroke in the thread's keymap", at)
			continue
		}
		if got := threadSteps[at].String(); got != want {
			t.Errorf("motion %d is spelt %q and arrives as %q", at, want, got)
		}
	}

	f := newFake(12)
	for i := range 12 {
		addComment(t, f, "PROJ-8", "Comment "+strconv.Itoa(i+1)+", worth a couple of lines of somebody's day.")
	}
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-8"), 120, 30)
	dr.key("tab", "tab")
	if dr.m.focus != regionComments {
		t.Fatalf("two tabs left the keyboard on region %d", dr.m.focus)
	}

	// The thread opens on the newest comment, so every motion that answers here
	// is one that goes back up it. G comes back to where it started, which is
	// how each of them is measured against the same place.
	dr.key("G")
	newest := dr.m.thread.View()
	for _, k := range []string{"k", "pgup", "ctrl+u", "home"} {
		dr.key(k)
		if dr.m.thread.View() == newest {
			t.Errorf("%s left the thread on the comment it opened on, so the stroke is not one it answers", k)
		}
		dr.key("G")
		if dr.m.thread.View() != newest {
			t.Errorf("G did not come back to the newest comment after %s", k)
		}
	}
}
