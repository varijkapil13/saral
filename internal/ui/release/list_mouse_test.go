package release

import (
	"testing"

	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/widget"
)

// A click selects a version, and a double-click opens the release screen over
// it — the same thing enter does. The pair is timed, so pointing at a version
// and pointing at it again a minute later only selects it twice.
func TestReleases_ClickingAVersionSelectsItAndDoubleClickingOpensTheRelease(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(8))
	dr := listOf(t, d, 120, 20)

	pressOn(t, d, dr, rowZone(threeOh))
	if got := dr.list().selectedID(); got != threeOh {
		t.Fatalf("one click selected %q", got)
	}
	if _, pushed := dr.pushed(); pushed {
		t.Fatal("one click opened the release screen; it should only have selected the row")
	}

	pressOn(t, d, dr, rowZone(threeOh))
	push, pushed := dr.pushed()
	if !pushed {
		t.Fatal("a double-click on the row opened nothing")
	}
	if push.ID != FlowViewID {
		t.Errorf("a double-click pushed %q", push.ID)
	}
}

// Two clicks the double-click window apart are two clicks, not a gesture. The
// clock is injected, so this winds forward rather than sleeping.
func TestReleases_TwoSlowClicksOnlySelect(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(8))
	now := d.Now()
	d.Now = func() time.Time { return now }
	dr := listOf(t, d, 120, 20)

	pressOn(t, d, dr, rowZone(threeOh))
	now = now.Add(2 * widget.DoubleClick)
	pressOn(t, d, dr, rowZone(threeOh))

	if _, pushed := dr.pushed(); pushed {
		t.Error("two clicks a minute apart opened a release screen")
	}
}

// The wheel scrolls the rows and leaves the selection alone, which is what a
// wheel does everywhere else.
func TestReleases_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 120, 8)
	stock(dr, manyVersions(60))
	under := dr.list().cursor

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if dr.list().top == 0 {
		t.Error("the wheel scrolled nothing")
	}
	if dr.list().cursor != under {
		t.Errorf("the wheel moved the selection to %d", dr.list().cursor)
	}

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if dr.list().top != 0 {
		t.Errorf("the wheel is at %d after going down and back up", dr.list().top)
	}
}

// With the mouse off nothing a click lands on can be hit, because the manager
// recorded no zone to hit.
func TestReleases_WithTheMouseOffNoRowIsHit(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := testDeps(newFake(8))
	d.Zones = off
	dr := listOf(t, d, 120, 20)

	_ = off.Scan(dr.m.View())
	dr.send(tea.MouseClickMsg{X: 4, Y: 3, Button: tea.MouseLeft})
	dr.send(tea.MouseClickMsg{X: 4, Y: 3, Button: tea.MouseLeft})
	if _, pushed := dr.pushed(); pushed {
		t.Error("a click opened a release screen with the mouse off")
	}
}

// A click while the editor is open belongs to the editor, not to the rows
// behind it.
func TestReleases_AClickDoesNotMoveTheCursorWhileAVersionIsBeingTyped(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(8))
	dr := listOf(t, d, 120, 24)
	dr.key("n")
	dr.typeText("4.0")

	pressOn(t, d, dr, rowZone(threeOh))
	if got := dr.list().selectedID(); got == threeOh {
		t.Error("a click moved the cursor out from under an editor that is still open")
	}
	if dr.list().mode != editing {
		t.Error("a click closed the editor")
	}
}
