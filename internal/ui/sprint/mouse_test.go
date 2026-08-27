package sprint

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pressOn scans the frame the view would draw and presses the left button in
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

func sprintZone(dr *driver, name string) string {
	dr.t.Helper()
	for i := range dr.m.sprints {
		if dr.m.sprints[i].Name == name {
			return dr.m.zoneOf(i)
		}
	}
	dr.t.Fatalf("no sprint called %q is on the list", name)
	return ""
}

// A click selects, and a double-click does what the edit key does — the gesture
// every other list here answers to. It is timed against the session's clock,
// because a click message carries neither a count nor an instant.
func TestSprints_ClickingSelectsAndDoubleClickingOpensTheForm(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake())
	dr := newDriver(t, d, 120, 20)
	dr.key("o")

	pressOn(t, d, dr, sprintZone(dr, "Sprint 1"))
	if dr.m.selected().Name != "Sprint 1" {
		t.Fatalf("the click selected %q", dr.m.selected().Name)
	}
	if dr.m.state == filling {
		t.Fatal("one click opened the form")
	}
	pressOn(t, d, dr, sprintZone(dr, "Sprint 1"))
	if dr.m.state != filling {
		t.Fatalf("a double-click left the view in state %d", dr.m.state)
	}
	if dr.m.form.sprint.Name != "Sprint 1" {
		t.Errorf("the form opened on %q", dr.m.form.sprint.Name)
	}
}

// Both answers to the confirm are clickable, and neither is anything but the
// key it advertises.
func TestSprints_TheConfirmIsAnsweredByPointerAsWellAsByKey(t *testing.T) {
	t.Parallel()

	t.Run("going ahead", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		d := testDeps(f)
		dr := newDriver(t, d, 120, 20)
		dr.onSprint("Sprint 2")
		dr.key("c")
		pressOn(t, d, dr, zoneConfirm)
		if n := countCalls(f, "CompleteSprint"); n != 1 {
			t.Errorf("clicking the confirm completed the sprint %d times, want once", n)
		}
	})

	t.Run("leaving it alone", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		d := testDeps(f)
		dr := newDriver(t, d, 120, 20)
		dr.onSprint("Sprint 2")
		dr.key("c")
		pressOn(t, d, dr, zoneRefuse)
		if n := countCalls(f, "CompleteSprint"); n != 0 {
			t.Errorf("clicking the way out completed the sprint %d times", n)
		}
		if dr.m.state != browsing {
			t.Error("clicking the way out left the confirm up")
		}
	})
}

func TestSprints_ClickingAFormFieldPutsTheCursorInIt(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake())
	dr := newDriver(t, d, 120, 20)
	dr.key("n")
	if dr.m.form.at != fieldName {
		t.Fatalf("the form opened on field %d", dr.m.form.at)
	}
	pressOn(t, d, dr, fieldZone(fieldGoal))
	if dr.m.form.at != fieldGoal {
		t.Errorf("clicking the goal put the cursor on field %d", dr.m.form.at)
	}
	dr.typeText("what this sprint is for")
	if got := dr.m.form.value(fieldGoal); got != "what this sprint is for" {
		t.Errorf("the goal holds %q after typing into the field that was clicked", got)
	}
}

// Nothing here strips ANSI, and nothing may: a marker left in a frame with the
// mouse off is what terminal text selection picks up. The list is the state to
// measure it in, because a text input writes escapes of its own whatever the
// theme says.
func TestSprints_WithTheMouseOffTheFrameCarriesNoMarker(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := plainDeps(newFake())
	d.Zones = off
	dr := newDriver(t, d, 120, 20)

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "Sprint 2") {
		t.Fatalf("the rows did not draw at all:\n%q", frame)
	}
}

// And with the mouse off nothing a click lands on can be hit, because the
// manager recorded no zone to hit.
func TestSprints_WithTheMouseOffNothingIsHit(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	f := newFake()
	d := testDeps(f)
	d.Zones = off
	dr := newDriver(t, d, 120, 20)
	dr.onSprint("Sprint 2")
	dr.key("c")

	_ = off.Scan(dr.m.View())
	dr.send(tea.MouseClickMsg{X: 4, Y: 8, Button: tea.MouseLeft})
	if n := countCalls(f, "CompleteSprint"); n != 0 {
		t.Errorf("a click completed a sprint with the mouse off, %d times", n)
	}
}

// plainDeps draws with a theme that writes no escape sequence of its own, so
// that an escape left in a frame can only be a zone marker.
func plainDeps(client jira.SessionClient) kernel.Deps {
	d := testDeps(client)
	th := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
	plain := lipgloss.NewStyle()
	for _, style := range []*lipgloss.Style{
		&th.Base, &th.Muted, &th.Accent, &th.Danger, &th.Warning, &th.Success, &th.Title,
		&th.Selected, &th.Badge, &th.StaleBadge,
	} {
		*style = plain
	}
	d.Theme = th
	return d
}

// The wheel scrolls the rows and leaves the selection alone, which is what a
// wheel does everywhere else.
func TestSprints_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()

	dr := stock(t, 120, 8, 40)
	under := dr.m.selected().ID

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if dr.m.top == 0 {
		t.Error("the wheel scrolled nothing")
	}
	if dr.m.selected().ID != under {
		t.Error("the wheel moved the selection")
	}

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if dr.m.top != 0 {
		t.Errorf("the wheel is at %d after going down and back up, want 0", dr.m.top)
	}
}
