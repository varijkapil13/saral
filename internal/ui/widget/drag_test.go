package widget

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func motion(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func release(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func TestDrag_ReportsHowFarThePointerHasComeFromThePress(t *testing.T) {
	t.Parallel()

	var d Drag
	if !d.Start("divider", clickAt(40, 10)) {
		t.Fatal("a press on the divider started no drag")
	}
	if !d.Active() || d.ID() != "divider" {
		t.Fatalf("the drag is active=%v on %q", d.Active(), d.ID())
	}

	dx, dy, ok := d.Move(motion(46, 12))
	if !ok || dx != 6 || dy != 2 {
		t.Errorf("moving to (46,12) reported (%d,%d,%v), want (6,2,true)", dx, dy, ok)
	}
	if x, y := d.At(); x != 46 || y != 12 {
		t.Errorf("the pointer is at (%d,%d), want (46,12)", x, y)
	}

	dx, dy, ok = d.Move(motion(30, 10))
	if !ok || dx != -10 || dy != 0 {
		t.Errorf("moving back to (30,10) reported (%d,%d,%v), want (-10,0,true)", dx, dy, ok)
	}

	dx, dy, ok = d.Release(release(31, 10))
	if !ok || dx != -9 || dy != 0 {
		t.Errorf("releasing at (31,10) reported (%d,%d,%v), want (-9,0,true)", dx, dy, ok)
	}
	if d.Active() || d.ID() != "" {
		t.Errorf("the drag is still active=%v on %q after a release", d.Active(), d.ID())
	}
}

// A pointer that leaves the divider it grabbed is still dragging it: a release
// far outside the original bounds completes the gesture rather than dropping it.
func TestDrag_ReleasingOutsideTheElementStillEndsTheDrag(t *testing.T) {
	t.Parallel()

	var d Drag
	d.Start("divider", clickAt(40, 10))

	dx, dy, ok := d.Release(release(200, -4))
	if !ok {
		t.Fatal("a release well outside the divider ended nothing")
	}
	if dx != 160 || dy != -14 {
		t.Errorf("the release reported (%d,%d), want (160,-14)", dx, dy)
	}
	if d.Active() {
		t.Error("the drag is still active after a release outside the element")
	}
}

func TestDrag_IgnoresWhatWasNeverPressed(t *testing.T) {
	t.Parallel()

	for name, act := range map[string]func(d *Drag) (int, int, bool){
		"a motion with nothing held":     func(d *Drag) (int, int, bool) { return d.Move(motion(10, 10)) },
		"a release with nothing pressed": func(d *Drag) (int, int, bool) { return d.Release(release(10, 10)) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var d Drag
			if dx, dy, ok := act(&d); ok || dx != 0 || dy != 0 {
				t.Errorf("%s reported (%d,%d,%v), want no drag", name, dx, dy, ok)
			}
			if d.Active() {
				t.Errorf("%s left a drag under way", name)
			}
		})
	}
}

func TestDrag_APressOnNothingDraggableStartsNothing(t *testing.T) {
	t.Parallel()

	var d Drag
	if d.Start("", clickAt(5, 5)) {
		t.Error("a press that hit no element started a drag")
	}
	if d.Active() {
		t.Error("a press that hit no element left a drag under way")
	}
}

func TestDrag_ASecondPressGrabsTheNewElement(t *testing.T) {
	t.Parallel()

	var d Drag
	d.Start("divider", clickAt(40, 10))
	d.Move(motion(48, 10))
	d.Start("other", clickAt(12, 4))

	if d.ID() != "other" {
		t.Fatalf("the drag is on %q, want the element pressed last", d.ID())
	}
	dx, dy, _ := d.Move(motion(14, 4))
	if dx != 2 || dy != 0 {
		t.Errorf("the delta is measured from (%d,%d), want it measured from the second press", dx, dy)
	}
}

func TestDrag_CancelDropsTheGesture(t *testing.T) {
	t.Parallel()

	var d Drag
	d.Start("divider", clickAt(40, 10))
	d.Cancel()

	if d.Active() || d.ID() != "" {
		t.Fatalf("cancel left the drag active=%v on %q", d.Active(), d.ID())
	}
	if _, _, ok := d.Release(release(50, 10)); ok {
		t.Error("a release after cancel applied a drag nobody was making")
	}
}
