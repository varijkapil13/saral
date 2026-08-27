package backlog

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// at scans the frame the view would draw and reports where one of its zones
// landed. The manager records a zone on its own goroutine, so it is waited for
// rather than assumed.
func at(t *testing.T, d kernel.Deps, dr *driver, name string) (x, y int) {
	t.Helper()
	_ = d.Zones.Scan(dr.m.View())
	id := dr.m.zones.ID(name)
	eventually(t, func() bool { return !d.Zones.Get(id).IsZero() })
	got := d.Zones.Get(id)
	return got.StartX, got.StartY
}

func pressOn(t *testing.T, d kernel.Deps, dr *driver, name string) {
	t.Helper()
	x, y := at(t, d, dr, name)
	dr.send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func TestMouse_ClickingARowSelectsIt(t *testing.T) {
	t.Parallel()
	d := testDeps(seeded(t))
	dr := newDriver(t, d, 120, 24)
	pressOn(t, d, dr, "row:PROJ-9")

	if got := dr.m.zoneOf(dr.m.cursor); got != "row:PROJ-9" {
		t.Errorf("the cursor is on %q after clicking PROJ-9", got)
	}
	if len(dr.m.picked) != 0 {
		t.Errorf("a single click picked %d issues", len(dr.m.picked))
	}
}

// A double-click is the gesture this view advertises on space: nothing here
// opens an issue, so picking is what the second click means.
func TestMouse_DoubleClickingARowPicksIt(t *testing.T) {
	t.Parallel()
	d := testDeps(seeded(t))
	dr := newDriver(t, d, 120, 24)
	pressOn(t, d, dr, "row:PROJ-9")
	pressOn(t, d, dr, "row:PROJ-9")

	if !dr.m.picked["PROJ-9"] {
		t.Error("a double-click on a row did not pick it")
	}
	pressOn(t, d, dr, "row:PROJ-9")
	pressOn(t, d, dr, "row:PROJ-9")
	if dr.m.picked["PROJ-9"] {
		t.Error("a second double-click did not unpick it")
	}
}

// Dragging a row onto a section asks the same question m asks. It is one
// implementation: the pointer reaches the confirm and nothing moves until it is
// answered.
func TestMouse_DraggingARowOntoASectionReachesTheConfirm(t *testing.T) {
	t.Parallel()
	c := newChunker(seeded(t))
	d := testDeps(c)
	dr := newDriver(t, d, 120, 24)

	fromX, fromY := at(t, d, dr, "row:PROJ-1")
	toX, toY := at(t, d, dr, "head:0")
	dr.send(tea.MouseClickMsg{X: fromX, Y: fromY, Button: tea.MouseLeft})
	dr.send(tea.MouseMotionMsg{X: toX, Y: toY, Button: tea.MouseLeft})
	dr.send(tea.MouseReleaseMsg{X: toX, Y: toY, Button: tea.MouseLeft})

	if dr.m.mode != confirming {
		t.Fatalf("a drag onto a sprint left the view in mode %d, want the confirm", dr.m.mode)
	}
	if dr.m.destAt != 0 {
		t.Errorf("the drag chose destination %d, want the section it was dropped on", dr.m.destAt)
	}
	if sprint, backlog := c.calls(); len(sprint)+len(backlog) != 0 {
		t.Fatal("a drag moved an issue without the confirm being answered")
	}
	mustContain(t, dr.view(), "Move 1 issue into ")

	dr.key("y")
	if sprint, _ := c.calls(); len(sprint) != 1 {
		t.Errorf("answering the confirm made %d calls", len(sprint))
	}
}

func TestMouse_ADragThatEndsOnNothingMovesNothing(t *testing.T) {
	t.Parallel()
	c := newChunker(seeded(t))
	d := testDeps(c)
	dr := newDriver(t, d, 120, 24)

	fromX, fromY := at(t, d, dr, "row:PROJ-1")
	dr.send(tea.MouseClickMsg{X: fromX, Y: fromY, Button: tea.MouseLeft})
	dr.send(tea.MouseReleaseMsg{X: 0, Y: 0, Button: tea.MouseLeft})

	if dr.m.mode != browsing {
		t.Errorf("a drag released over the head line left the view in mode %d", dr.m.mode)
	}
	if sprint, backlog := c.calls(); len(sprint)+len(backlog) != 0 {
		t.Error("a drag released over nothing moved an issue")
	}
}

func TestMouse_TheConfirmAndTheCancelAreBothClickable(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		click string
		calls int
	}{
		"going ahead": {click: zoneConfirm, calls: 1},
		"cancelling":  {click: zoneCancel, calls: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newChunker(seeded(t))
			d := testDeps(c)
			dr := newDriver(t, d, 120, 24)
			dr.cursorTo("row:PROJ-1")
			dr.key("space", "m", "enter")

			pressOn(t, d, dr, tc.click)
			if sprint, _ := c.calls(); len(sprint) != tc.calls {
				t.Errorf("clicking %q made %d calls, want %d", tc.click, len(sprint), tc.calls)
			}
			if dr.m.mode != browsing {
				t.Errorf("clicking %q left the view in mode %d", tc.click, dr.m.mode)
			}
		})
	}
}

func TestMouse_ClickingADestinationChoosesIt(t *testing.T) {
	t.Parallel()
	c := newChunker(seeded(t))
	d := testDeps(c)
	dr := newDriver(t, d, 120, 24)
	dr.cursorTo("row:PROJ-1")
	dr.key("space", "m")

	last := len(dr.m.groups) - 1
	pressOn(t, d, dr, destZone(last))
	if dr.m.destAt != last {
		t.Fatalf("clicking the last destination left it on %d", dr.m.destAt)
	}
	if dr.m.mode != choosing {
		t.Fatal("the first click on a destination went past the chooser")
	}
	pressOn(t, d, dr, destZone(last))
	if dr.m.mode != confirming {
		t.Errorf("clicking the chosen destination again left the view in mode %d", dr.m.mode)
	}
}

func TestMouse_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(60)), 100, 20)
	cursor, top := dr.m.cursor, dr.m.top
	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})

	if dr.m.cursor != cursor {
		t.Errorf("the wheel moved the cursor from %d to %d", cursor, dr.m.cursor)
	}
	if dr.m.top == top {
		t.Errorf("the wheel did not scroll: the pane is still at %d", top)
	}
	for range 100 {
		dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}
	if want := max(len(dr.m.rows)-dr.m.rowsHeight(), 0); dr.m.top != want {
		t.Errorf("the wheel scrolled past the end to %d, want %d", dr.m.top, want)
	}
}

// Mouse off means off all the way down: nothing is marked into the frame, so
// there is nothing for a text selection to pick up and nothing for a click to
// resolve to.
func TestMouse_WithTheManagerDisabledNothingIsMarkedAndNothingIsHit(t *testing.T) {
	t.Parallel()
	mgr := zone.New()
	mgr.SetEnabled(false)
	t.Cleanup(mgr.Close)

	d := plainDeps(seeded(t), mgr)
	dr := newDriver(t, d, 120, 24)

	frame := dr.m.View()
	if strings.ContainsRune(frame, 0x1b) {
		t.Errorf("a frame drawn with the mouse off carries an escape byte:\n%q", frame)
	}
	before := dr.m.cursor
	for y := range 24 {
		dr.send(tea.MouseClickMsg{X: 4, Y: y, Button: tea.MouseLeft})
	}
	if dr.m.cursor != before {
		t.Errorf("a click resolved to a row with the mouse off: the cursor moved to %d", dr.m.cursor)
	}
	if len(dr.m.picked) != 0 {
		t.Errorf("a click picked %d issues with the mouse off", len(dr.m.picked))
	}
}

// A key ends a gesture the pointer is in the middle of, so a drag nobody is
// watching cannot land somewhere the keyboard has since moved.
func TestMouse_AKeyEndsADragInProgress(t *testing.T) {
	t.Parallel()
	c := newChunker(seeded(t))
	d := testDeps(c)
	dr := newDriver(t, d, 120, 24)

	fromX, fromY := at(t, d, dr, "row:PROJ-1")
	toX, toY := at(t, d, dr, "head:0")
	dr.send(tea.MouseClickMsg{X: fromX, Y: fromY, Button: tea.MouseLeft})
	dr.key("j")
	dr.send(tea.MouseReleaseMsg{X: toX, Y: toY, Button: tea.MouseLeft})

	if dr.m.mode != browsing {
		t.Errorf("a drag a key had ended still reached the confirm")
	}
}

// Zone ids are never freed, so a frame drawn a thousand times must not mint one
// per draw.
func TestMouse_MarksOneZonePerRowRatherThanOnePerFrame(t *testing.T) {
	t.Parallel()
	d := testDeps(seeded(t))
	dr := newDriver(t, d, 120, 24)
	names := make(map[string]bool)
	for range 20 {
		for i := range dr.m.rows {
			names[dr.m.zoneOf(i)] = true
		}
		_ = dr.m.View()
	}
	if len(names) != len(dr.m.rows) {
		t.Errorf("%d rows produced %d zone names", len(dr.m.rows), len(names))
	}
	for i := range dr.m.rows {
		if got := dr.m.zoneOf(i); got == "" || strings.Contains(got, strconv.Itoa(dr.m.styles.gen)+":frame") {
			t.Errorf("row %d is marked %q", i, got)
		}
	}
}
