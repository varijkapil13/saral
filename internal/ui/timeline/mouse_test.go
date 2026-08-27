package timeline

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

// pressOn scans the frame the chart would draw and presses the left button in
// the first cell of one of its zones. The manager records a zone on its own
// goroutine, so the zone is waited for rather than assumed.
func pressOn(t *testing.T, d kernel.Deps, dr *driver, name string) {
	t.Helper()

	_ = d.Zones.Scan(dr.m.View())
	id := dr.m.zones.ID(name)
	eventually(t, func() bool { return !d.Zones.Get(id).IsZero() })
	at := d.Zones.Get(id)
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
}

func TestMouse_ClickSelectsTheBarUnderThePointer(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := newDriver(t, d, 120, 24)
	want := dr.m.rows[3].key
	pressOn(t, d, dr, rowZone(want))

	if got := dr.m.selectedKey(); got != want {
		t.Errorf("clicking %s selected %s", want, got)
	}
	if len(dr.pushes) != 0 {
		t.Errorf("one click pushed %d views", len(dr.pushes))
	}
}

// A double-click opens the issue, and only when both clicks are one gesture: a
// second click a minute later is another look, not a decision.
func TestMouse_ADoubleClickOpensTheIssueAndTwoSlowClicksDoNot(t *testing.T) {
	t.Parallel()

	clock := theDay
	d := testDeps(newFake(20))
	d.Now = func() time.Time { return clock }
	dr := newDriver(t, d, 120, 24)
	want := dr.m.rows[2].key

	pressOn(t, d, dr, rowZone(want))
	clock = clock.Add(widget.DoubleClick + time.Second)
	pressOn(t, d, dr, rowZone(want))
	if len(dr.pushes) != 0 {
		t.Fatalf("two clicks a second apart pushed %d views", len(dr.pushes))
	}

	clock = clock.Add(widget.DoubleClick / 4)
	pressOn(t, d, dr, rowZone(want))
	if len(dr.pushes) != 1 {
		t.Fatalf("a double-click pushed %d views, want one", len(dr.pushes))
	}
	if dr.pushes[0].Title != want {
		t.Errorf("the pane pushed is titled %q, want %q", dr.pushes[0].Title, want)
	}
}

// The wheel scrolls the bars without moving the selection, which is what a wheel
// does everywhere else in this program.
func TestMouse_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(200)), 120, 24)
	under := dr.m.selectedKey()
	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})

	if dr.m.top != widget.WheelStep {
		t.Errorf("one notch scrolled to row %d, want %d", dr.m.top, widget.WheelStep)
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the wheel moved the selection from %s to %s", under, got)
	}
}

// A trackpad's horizontal wheel pans the calendar, which is the gesture a chart
// that scrolls in two dimensions owes it.
func TestMouse_TheHorizontalWheelPansTheCalendar(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(200)), 120, 24)
	dr.key("+", "+")
	if dr.m.ax.cols <= dr.m.lay.chart {
		t.Fatal("the whole span fits, so there is nothing to pan")
	}
	dr.m.left = 20
	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelLeft})
	if want := 20 - widget.WheelStep; dr.m.left != want {
		t.Errorf("one notch left moved to column %d, want %d", dr.m.left, want)
	}
	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelRight})
	if dr.m.left != 20 {
		t.Errorf("one notch right moved to column %d, want 20", dr.m.left)
	}
}

// Mouse off means off all the way down: nothing is marked, so a terminal text
// selection has nothing to pick up.
func TestMouse_WithTheManagerOffNothingIsMarked(t *testing.T) {
	t.Parallel()

	d := plainDeps(newFake(20))
	d.Zones = nil
	dr := newDriver(t, d, 120, 24)

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("the frame carries an escape byte with the zone manager off:\n%q", frame)
	}
}

func TestMouse_WithTheManagerOffNoClickHitsAnything(t *testing.T) {
	t.Parallel()

	d := plainDeps(newFake(20))
	d.Zones = nil
	dr := newDriver(t, d, 120, 24)
	under := dr.m.selectedKey()

	for y := range 24 {
		dr.send(tea.MouseClickMsg{X: 4, Y: y, Button: tea.MouseLeft})
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("a click moved the selection from %s to %s with the mouse off", under, got)
	}
	if len(dr.pushes) != 0 {
		t.Errorf("clicks pushed %d views with the mouse off", len(dr.pushes))
	}
}

// While the notes pane is up, a click on the chart underneath reaches nothing:
// selecting a bar nobody can see is a change the user cannot see either.
func TestMouse_NoClickReachesTheChartUnderTheNotes(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := newDriver(t, d, 120, 24)
	want := dr.m.rows[3].key
	_ = d.Zones.Scan(dr.m.View())
	id := dr.m.zones.ID(rowZone(want))
	eventually(t, func() bool { return !d.Zones.Get(id).IsZero() })
	at := d.Zones.Get(id)

	under := dr.m.selectedKey()
	dr.key("n")
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("a click under the notes moved the selection from %s to %s", under, got)
	}
}
