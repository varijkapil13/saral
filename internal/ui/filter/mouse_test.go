package filter

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pressOn scans the frame the picker would draw and presses the left button in
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

// A click selects, and a second click on the row already selected does what
// enter does — the gesture the palette and the issue list already answer to.
func TestPicker_ClickingAFacetSelectsItAndClickingItAgainOpensIt(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := newDriver(t, d, 120, 30)

	pressOn(t, d, dr, "facet:priority")
	if dr.m.state != pickFacet {
		t.Fatal("one click opened the facet; it should only have selected it")
	}
	if got := dr.m.facets[dr.m.cursor].facet; got != FacetPriority {
		t.Fatalf("the click selected %q, want priority", got.Label())
	}

	pressOn(t, d, dr, "facet:priority")
	if dr.m.state != pickValue || dr.m.facet != FacetPriority {
		t.Errorf("a second click left the picker on state %d and facet %q", dr.m.state, dr.m.facet.Label())
	}
}

func TestPicker_ClickingAValueTwiceChoosesIt(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := newDriver(t, d, 120, 30)
	dr.pick(FacetPriority)

	pressOn(t, d, dr, "value:priority:10403")
	if _, chose := dr.chosen(); chose {
		t.Fatal("one click chose the value; it should only have selected it")
	}
	pressOn(t, d, dr, "value:priority:10403")

	term, chose := dr.chosen()
	if !chose || term.ID != "10403" {
		t.Errorf("a second click named %+v, want the priority 10403", term)
	}
	if dr.pops != 0 {
		t.Errorf("choosing by pointer closed the picker %d times, want it to stay open", dr.pops)
	}
	if !dr.m.terms.Has(term) {
		t.Error("choosing by pointer did not mark the value in force")
	}
}

// Nothing here strips ANSI, and nothing may: a marker left in a frame with the
// mouse off is what terminal text selection picks up. The facets are the state
// to measure it in, because they draw no text input, and a text input writes
// escapes of its own whatever the theme says.
func TestPicker_WithTheMouseOffTheFrameCarriesNoMarker(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := plainDeps(newFake(20))
	d.Zones = off
	dr := newDriver(t, d, 120, 30)

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "priority") {
		t.Fatalf("the facets did not draw at all:\n%q", frame)
	}
}

// And with the mouse off nothing a click lands on can be hit, because the
// manager recorded no zone to hit.
func TestPicker_WithTheMouseOffNoRowIsHit(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := testDeps(newFake(20))
	d.Zones = off
	dr := newDriver(t, d, 120, 30)
	dr.pick(FacetPriority)

	_ = off.Scan(dr.m.View())
	dr.send(tea.MouseClickMsg{X: 10, Y: 2, Button: tea.MouseLeft})
	dr.send(tea.MouseClickMsg{X: 10, Y: 2, Button: tea.MouseLeft})
	if _, chose := dr.chosen(); chose {
		t.Error("a click chose a value with the mouse off")
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
func TestPicker_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(40))
	dr := newDriver(t, d, 120, 8)
	dr.pick(FacetLabel)
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
