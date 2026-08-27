package board

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestBoardMouse_ClickingACardSelectsItAndADoubleClickOpensIt(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(24))
	dr := newDriver(t, d, 120, 20)

	pressOn(t, d, dr, cardZone("PROJ-4"))
	if got := dr.m.selectedKey(); got != "PROJ-4" {
		t.Fatalf("the click selected %q, want PROJ-4", got)
	}
	if len(dr.pushes) != 0 {
		t.Fatal("one click opened the issue; it should only have selected it")
	}

	pressOn(t, d, dr, cardZone("PROJ-4"))
	if len(dr.pushes) != 1 {
		t.Fatalf("%d views were pushed by a double-click, want the detail pane", len(dr.pushes))
	}
	if got := dr.pushes[0].Title; got != "PROJ-4" {
		t.Errorf("the double-click opened %q", got)
	}
}

// A column's strip runs from its caption to the rule under the grid, so a card
// can be dropped on an empty column — which is the column a card is most often
// dropped on.
func TestBoardMouse_ACardCanBeDroppedOnAColumnWithNothingInIt(t *testing.T) {
	t.Parallel()
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
		{Name: "Nothing here", StatusIDs: []string{"10202"}},
	}}
	d, dr := stocked(t, cfg, []jira.Issue{
		{Key: "PROJ-1", Summary: "one", Status: jira.Status{ID: "10201", Name: "Triage"}},
	}, 100, 16)
	if dr.m.columnLen(1) != 0 {
		t.Fatal("the second column is not the empty one")
	}

	from := zoneOf(t, d, dr, cardZone("PROJ-1"))
	onto := zoneOf(t, d, dr, colZone(1))
	// Halfway down the empty column, where no card was ever drawn.
	y := onto.StartY + (onto.EndY-onto.StartY)/2
	dr.send(tea.MouseClickMsg{X: from.StartX, Y: from.StartY, Button: tea.MouseLeft})
	dr.send(tea.MouseMotionMsg{X: onto.StartX + 1, Y: y, Button: tea.MouseLeft})

	if dr.m.card == nil {
		t.Fatal("dragging into an empty column did not take the card off the board")
	}
	if dr.m.card.target != 1 {
		t.Errorf("the card is aimed at column %d, want the empty one", dr.m.card.target)
	}
	mustContain(t, dr.view(), "move PROJ-1 from Triage to Nothing here")
}

// A press and a release inside one card is a click and not a move: nothing is
// asked of the site, and the card stays where it is.
func TestBoardMouse_APressAndReleaseOnOneCardIsNotAMove(t *testing.T) {
	t.Parallel()
	fake := newFake(24)
	d := testDeps(fake)
	dr := newDriver(t, d, 120, 20)
	at := zoneOf(t, d, dr, cardZone("PROJ-6"))
	before := len(fake.Calls())

	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
	dr.send(tea.MouseReleaseMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})

	if dr.m.card != nil {
		t.Error("a click took a card off the board")
	}
	if got := fake.Calls()[before:]; len(got) != 0 {
		t.Errorf("a click asked the site for %v", got)
	}
	if got := dr.column(0); !slices.Contains(got, "PROJ-6") {
		t.Errorf("the first column holds %v", got)
	}
}

// A key ends a gesture the pointer is in the middle of, so a card is never left
// following a pointer nobody is watching.
func TestBoardMouse_AKeyEndsADragTheReleaseNeverArrivedFor(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(24))
	dr := newDriver(t, d, 120, 20)
	at := zoneOf(t, d, dr, cardZone("PROJ-3"))

	dr.send(tea.MouseClickMsg{X: at.StartX, Y: at.StartY, Button: tea.MouseLeft})
	if !dr.m.drag.Active() {
		t.Fatal("pressing on a card grabbed nothing")
	}
	dr.key("j")
	if dr.m.drag.Active() {
		t.Error("a keypress left the pointer still holding a card")
	}
}

// The wheel scrolls the grid and leaves the selection alone, which is what a
// wheel does everywhere else.
func TestBoardMouse_TheWheelScrollsWithoutMovingTheSelection(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(60)), 120, 10)
	under := dr.m.selectedKey()

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if dr.m.rowTop == 0 {
		t.Error("the wheel scrolled nothing")
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the wheel moved the selection to %q, want it left on %q", got, under)
	}

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if dr.m.rowTop != 0 {
		t.Errorf("the grid is at row %d after going down and back up, want 0", dr.m.rowTop)
	}
}

// Nothing here strips ANSI, and nothing may: a marker left in a frame with the
// mouse off is what terminal text selection picks up.
func TestBoardMouse_WithTheMouseOffTheFrameCarriesNoMarker(t *testing.T) {
	t.Parallel()
	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := plainDeps(newFake(24))
	d.Zones = off
	dr := newDriver(t, d, 120, 20)

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "PROJ-3") {
		t.Fatalf("the board did not draw at all:\n%q", frame)
	}
}

// And with the mouse off nothing a click lands on can be hit, because the
// manager recorded no zone to hit.
func TestBoardMouse_WithTheMouseOffNothingIsHit(t *testing.T) {
	t.Parallel()
	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := testDeps(newFake(24))
	d.Zones = off
	dr := newDriver(t, d, 120, 20)
	under := dr.m.selectedKey()

	_ = off.Scan(dr.m.View())
	for _, y := range []int{2, 5, 9} {
		dr.send(tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
		dr.send(tea.MouseMotionMsg{X: 60, Y: y, Button: tea.MouseLeft})
		dr.send(tea.MouseReleaseMsg{X: 60, Y: y, Button: tea.MouseLeft})
	}
	if dr.m.card != nil {
		t.Error("a card was taken off the board with the mouse off")
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("a click with the mouse off moved the selection to %q", got)
	}
	if len(dr.pushes) != 0 {
		t.Error("a click with the mouse off opened an issue")
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
