package move

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// pressOn scans the frame the wizard would draw and presses the left button in
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

// One click selects and a real double-click does what enter does. The pair is
// timed against the injected clock, which is fixed here, so two clicks in one
// test are one gesture.
func TestMove_ClickingAnIssueTypeSelectsItAndADoubleClickChoosesIt(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	d := testDeps(f)
	dr := newDriver(t, d, 120, 24, WithIssues(iss))
	dr.typeKey("OTHER")

	at := 2
	name := "type:" + dr.m.vocab[at].Type.ID
	pressOn(t, d, dr, name)
	if dr.m.step != stepType {
		t.Fatal("one click walked past the issue type; it should only have selected one")
	}
	if dr.m.cursor != at {
		t.Fatalf("the click selected row %d, want %d", dr.m.cursor, at)
	}

	pressOn(t, d, dr, name)
	if dr.m.step != stepStatus {
		t.Errorf("a double-click left the wizard on step %d rather than the remap", dr.m.step)
	}
	if dr.m.typeAt != at {
		t.Errorf("the type that was chosen is %d, want %d", dr.m.typeAt, at)
	}
}

func TestMove_ClickingAProjectSelectsItAndADoubleClickLooksItUp(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	d := testDeps(f)
	dr := newDriver(t, d, 120, 24, WithIssues(iss))
	if len(dr.m.found) == 0 {
		t.Fatal("nothing to click on")
	}

	name := "project:" + dr.m.found[0]
	pressOn(t, d, dr, name)
	if dr.m.step != stepTarget {
		t.Fatal("one click looked the project up")
	}
	pressOn(t, d, dr, name)
	if dr.m.step != stepType {
		t.Errorf("a double-click left the wizard on step %d", dr.m.step)
	}
}

// Nothing here strips ANSI, and nothing may: a marker left in a frame with the
// mouse off is what terminal text selection picks up.
func TestMove_WithTheMouseOffTheFrameCarriesNoMarker(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := plainDeps(f)
	d.Zones = off
	dr := newDriver(t, d, 120, 24, WithIssues(iss))
	dr.typeKey("OTHER")

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "Story") {
		t.Fatalf("the issue types did not draw at all:\n%q", frame)
	}
}

// And with the mouse off nothing a click lands on can be hit, because the
// manager recorded no zone to hit.
func TestMove_WithTheMouseOffNoRowIsHit(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := testDeps(f)
	d.Zones = off
	dr := newDriver(t, d, 120, 24, WithIssues(iss))
	dr.typeKey("OTHER")

	_ = off.Scan(dr.m.View())
	dr.send(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
	dr.send(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
	if dr.m.step != stepType {
		t.Errorf("a click chose an issue type with the mouse off: step %d", dr.m.step)
	}
}

// The wheel scrolls the rows and leaves the selection alone, which is what a
// wheel does everywhere else.
func TestMove_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 120, 6, WithIssues(iss))
	dr.typeKey("OTHER")
	under := dr.m.cursor

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if dr.m.top == 0 {
		t.Error("the wheel scrolled nothing")
	}
	if dr.m.cursor != under {
		t.Errorf("the wheel moved the selection to %d, want it left on %d", dr.m.cursor, under)
	}
	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if dr.m.top != 0 {
		t.Errorf("the wheel is at %d after going down and back up, want 0", dr.m.top)
	}
}

// A click on the confirm screen may not submit: the keys and the palette are the
// only ways to answer it, and a stray double-click on a row of issue keys is not
// an answer.
func TestMove_ClickingTheConfirmScreenSubmitsNothing(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	d := testDeps(f)
	dr := newDriver(t, d, 120, 24, WithIssues(iss))
	dr.walkTo("OTHER")

	_ = d.Zones.Scan(dr.m.View())
	for range 4 {
		dr.send(tea.MouseClickMsg{X: 4, Y: 9, Button: tea.MouseLeft})
	}
	if n := countCalls(f, "BulkMove"); n != 0 {
		t.Errorf("clicking the confirm screen submitted %d moves", n)
	}
	if dr.m.step != stepConfirm {
		t.Errorf("clicking the confirm screen left the wizard on step %d", dr.m.step)
	}
}
