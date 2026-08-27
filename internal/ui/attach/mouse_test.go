package attach

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pressOn scans the frame the pane would draw and presses the left button in the
// first cell of one of its zones. The manager records a zone on its own
// goroutine, so the zone is waited for rather than assumed.
func pressOn(t *testing.T, d kernel.Deps, dr *driver, name string) {
	t.Helper()

	_ = d.Zones.Scan(dr.m.View())
	id := dr.m.zones.ID(name)
	eventually(t, func() bool { return !d.Zones.Get(id).IsZero() })
	at := d.Zones.Get(id)
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
}

// A click selects. Opening it takes a real double-click, timed against the
// session's clock, because pointing at a file and pointing at it again a minute
// later is two decisions and not one gesture.
func TestPane_ClickingAFileSelectsItAndDoubleClickingShowsIt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)
	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := testDeps(f)
	d.Now = func() time.Time { return now }
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))
	pdf := dr.m.files[1]

	pressOn(t, d, dr, zoneFile+pdf.ID)
	if dr.m.cursor != 1 {
		t.Fatalf("the click left the cursor on %d, want the row it landed on", dr.m.cursor)
	}
	if got := len(dr.seen.handedOver()); got != 0 {
		t.Fatalf("one click handed the file over %d times", got)
	}

	now = now.Add(2 * time.Minute)
	pressOn(t, d, dr, zoneFile+pdf.ID)
	if got := len(dr.seen.handedOver()); got != 0 {
		t.Fatalf("two clicks two minutes apart were taken for a double-click")
	}

	now = now.Add(100 * time.Millisecond)
	pressOn(t, d, dr, zoneFile+pdf.ID)
	if got := dr.seen.handedOver(); len(got) != 1 {
		t.Errorf("a double-click handed over %v, want the file it landed on", got)
	}
}

// Clicking the preview does what z does, which is the only other way to give it
// the whole pane.
func TestPane_ClickingThePreviewGivesItTheWholePane(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := testDeps(f)
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))

	pressOn(t, d, dr, zonePreview)
	if !dr.m.grown {
		t.Fatal("clicking the preview did not grow it")
	}
	pressOn(t, d, dr, zonePreview)
	if dr.m.grown {
		t.Error("clicking it again did not put the list back")
	}
}

// The wheel scrolls the rows and leaves the selection alone, which is what a
// wheel does everywhere else.
func TestPane_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()

	many := make([]file, 0, 40)
	for i := range 40 {
		many = append(many, file{name: "shot-" + string(rune('a'+i%26)) + ".png", body: "x"})
	}
	f := newFake(3)
	attached(t, f, "PROJ-1", many...)
	dr := newDriver(t, testDeps(f), 120, 20, WithIssue("PROJ-1"))
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

// Nothing here strips ANSI, and nothing may: a marker left in a frame with the
// mouse off is what terminal text selection picks up.
func TestPane_WithTheMouseOffTheFrameCarriesNoMarker(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := plainDeps(f)
	d.Zones = off
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "screenshot.png") {
		t.Fatalf("the list did not draw at all:\n%q", frame)
	}
}

// And with the mouse off nothing a click lands on can be hit, because the manager
// recorded no zone to hit.
func TestPane_WithTheMouseOffNoRowIsHit(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := testDeps(f)
	d.Zones = off
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))

	_ = off.Scan(dr.m.View())
	for range 3 {
		dr.send(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
	}
	if got := len(dr.seen.handedOver()); got != 0 {
		t.Errorf("a click handed a file over %d times with the mouse off", got)
	}
	if dr.m.grown {
		t.Error("a click grew the preview with the mouse off")
	}
}

// A click that lands on nothing this pane drew is not a click on the row the
// cursor happens to be on.
func TestPane_AClickOnNothingChangesNothing(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := testDeps(f)
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))
	_ = d.Zones.Scan(dr.m.View())

	dr.send(tea.MouseClickMsg{X: 200, Y: 200, Button: tea.MouseLeft})
	if dr.m.cursor != 0 || dr.m.grown {
		t.Errorf("a click outside everything moved the cursor to %d (grown=%v)", dr.m.cursor, dr.m.grown)
	}
}

// A right-click is not a menu here, and nothing may make one: kernel.Command has
// no notion of what it applies to.
func TestPane_ARightClickDoesNothing(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := testDeps(f)
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))

	_ = d.Zones.Scan(dr.m.View())
	id := dr.m.zones.ID(zoneFile + dr.m.files[1].ID)
	eventually(t, func() bool { return !d.Zones.Get(id).IsZero() })
	at := d.Zones.Get(id)
	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseRight})

	if dr.m.cursor != 0 {
		t.Errorf("a right-click moved the cursor to %d", dr.m.cursor)
	}
}

// plainDeps draws with a theme that writes no escape sequence of its own, so that
// an escape left in a frame can only be a zone marker.
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
