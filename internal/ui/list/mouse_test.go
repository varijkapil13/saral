package list

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// markingZoner is a zoner over a live manager, for a test that wants marked
// output without a whole view around it.
func markingZoner(tb testing.TB) widget.Zoner {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	return widget.NewZoner(mgr)
}

// pointerClock is the clock a double-click is timed against. Winding it forward
// is how a slow click is written; docs/TESTING.md forbids sleeping for one.
type pointerClock struct{ at time.Time }

func (c *pointerClock) now() time.Time        { return c.at }
func (c *pointerClock) after(d time.Duration) { c.at = c.at.Add(d) }
func newPointerClock() *pointerClock {
	return &pointerClock{at: time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)}
}

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

func TestList_ClickingACellNarrowsTheRowsToItAndClickingItAgainClears(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		zone  func(string) string
		key   string
		kind  Facet
		value string
	}{
		"a status chip": {zone: statusZone, key: "PROJ-2", kind: FacetStatus, value: "Shipped"},
		"a type chip":   {zone: typeZone, key: "PROJ-2", kind: FacetType, value: "Chore"},
		"an assignee":   {zone: whoZone, key: "PROJ-2", kind: FacetAssignee, value: "Alan Turing"},
		"nobody at all": {zone: whoZone, key: "PROJ-4", kind: FacetAssignee, value: unassigned},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := testDeps(newFake(20))
			dr := openAll(t, d, 120, 30)
			all := len(dr.m.view)

			pressOn(t, d, dr, tc.zone(tc.key))

			if got := (facet{kind: tc.kind, value: tc.value}); dr.m.facet != got {
				t.Fatalf("the rows are narrowed to %+v, want %+v", dr.m.facet, got)
			}
			if len(dr.m.view) == 0 || len(dr.m.view) >= all {
				t.Fatalf("%d of %d rows survived the narrowing, want some but not all", len(dr.m.view), all)
			}
			for _, at := range dr.m.view {
				iss := &dr.m.issues[at]
				if got := facetValue(iss, tc.kind); got != tc.value {
					t.Errorf("%s is still shown with %s %q, want only %q", iss.Key, tc.kind.label(), got, tc.value)
				}
			}
			mustContain(t, dr.view(), "only "+tc.kind.label()+" \""+tc.value+"\"")

			pressOn(t, d, dr, tc.zone(tc.key))

			if dr.m.facet.on() {
				t.Fatalf("a second click left the rows narrowed to %+v", dr.m.facet)
			}
			if len(dr.m.view) != all {
				t.Errorf("%d rows came back, want all %d", len(dr.m.view), all)
			}
			mustNotContain(t, dr.view(), "only "+tc.kind.label())
		})
	}
}

// The selected row is drawn by styling the whole line at once, around cells
// that already carry their own markers. A style that measured or rewrote those
// markers would put the zone in the wrong place, and the chip on the row the
// user is actually on is the one they are most likely to click.
func TestList_TheChipsOnTheSelectedRowAreStillWhereTheyLook(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := openAll(t, d, 120, 30)
	dr.key("j")
	if got := dr.m.selectedKey(); got != "PROJ-2" {
		t.Fatalf("the cursor is on %q, want PROJ-2", got)
	}

	pressOn(t, d, dr, statusZone("PROJ-2"))

	if got := (facet{kind: FacetStatus, value: "Shipped"}); dr.m.facet != got {
		t.Errorf("clicking the selected row's status narrowed to %+v, want %+v", dr.m.facet, got)
	}
}

// The narrowing and the local filter are two different things being left out at
// once, and the row that survives has to satisfy both.
func TestList_NarrowingAndTheFilterApplyTogether(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(30))
	dr := openAll(t, d, 120, 30)

	pressOn(t, d, dr, statusZone("PROJ-2"))
	dr.key("/")
	dr.typeText("PROJ-2")

	if len(dr.m.view) == 0 {
		t.Fatal("nothing survived a narrowing and a filter that both match PROJ-2")
	}
	for _, at := range dr.m.view {
		iss := &dr.m.issues[at]
		if iss.Status.Name != "Shipped" || !strings.Contains(iss.Key, "PROJ-2") {
			t.Errorf("%s (%s) survived both a status narrowing and a key filter", iss.Key, iss.Status.Name)
		}
	}
}

func TestList_ADoubleClickOpensARowAndTwoDeliberateClicksDoNot(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		gap  time.Duration
		want int
	}{
		"two clicks in one gesture":       {gap: 90 * time.Millisecond, want: 1},
		"two clicks a second apart":       {gap: time.Second, want: 0},
		"two clicks a whole minute apart": {gap: time.Minute, want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			clock := newPointerClock()
			d := testDeps(newFake(20))
			d.Now = clock.now
			dr := openAll(t, d, 120, 30)

			pressOn(t, d, dr, rowZone("PROJ-3"))
			if dr.m.selectedKey() != "PROJ-3" {
				t.Fatalf("the first click left the cursor on %q", dr.m.selectedKey())
			}
			clock.after(tc.gap)
			pressOn(t, d, dr, rowZone("PROJ-3"))

			if got := len(dr.pushes); got != tc.want {
				t.Fatalf("%s opened %d panes, want %d", name, got, tc.want)
			}
			if tc.want == 1 && dr.pushes[0].Title != "PROJ-3" {
				t.Errorf("the pane opened is %q, want PROJ-3", dr.pushes[0].Title)
			}
		})
	}
}

// A click on a cell is a narrowing and never half of a double-click: without
// this the second click on a chip opens the row under it.
func TestList_ClickingACellIsNotHalfOfADoubleClick(t *testing.T) {
	t.Parallel()

	clock := newPointerClock()
	d := testDeps(newFake(20))
	d.Now = clock.now
	dr := openAll(t, d, 120, 30)

	pressOn(t, d, dr, statusZone("PROJ-3"))
	clock.after(50 * time.Millisecond)
	pressOn(t, d, dr, rowZone("PROJ-3"))

	if len(dr.pushes) != 0 {
		t.Errorf("a chip click followed by a row click opened %d panes, want none", len(dr.pushes))
	}
}

// Nothing here strips ANSI, and nothing may: a marker left in a frame with the
// mouse off is what terminal text selection picks up, and stripping is exactly
// what hid it for four batches.
func TestList_WithTheMouseOffTheFrameCarriesNoMarkerAndNoCellNarrows(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := plainDeps(newFake(20))
	d.Zones = off
	dr := openAll(t, d, 120, 30)

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "PROJ-2") {
		t.Fatalf("the rows did not draw at all:\n%q", frame)
	}

	// The terminal reports nothing with the mouse off, but a synthesised click
	// must still miss: the manager is disabled, so it recorded no zone to hit.
	_ = off.Scan(frame)
	dr.send(tea.MouseClickMsg{X: 60, Y: 3, Button: tea.MouseLeft})
	if dr.m.facet.on() {
		t.Errorf("a click narrowed the rows to %+v with the mouse off", dr.m.facet)
	}
	if len(dr.pushes) != 0 {
		t.Errorf("a click opened %d panes with the mouse off", len(dr.pushes))
	}
}

// plainDeps draws with a theme that writes no escape sequence of its own, so
// that an escape left in a frame can only be a zone marker.
func plainDeps(client jira.Client) kernel.Deps {
	d := testDeps(client)
	t := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
	plain := lipgloss.NewStyle()
	for _, style := range []*lipgloss.Style{
		&t.Base, &t.Muted, &t.Accent, &t.Danger, &t.Warning, &t.Success, &t.Title,
		&t.Selected, &t.Badge, &t.StaleBadge,
	} {
		*style = plain
	}
	d.Theme = t
	return d
}

func TestList_ThePaletteNarrowsToTheRowUnderTheCursor(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := openAll(t, d, 120, 30)
	dr.key("j", "j")
	under := dr.m.selectedKey()

	dr.send(FacetMsg{Kind: FacetStatus})

	if !dr.m.facet.on() {
		t.Fatal("the palette narrowed nothing")
	}
	if got, want := dr.m.facet.value, facetValue(dr.m.selectedIssue(), FacetStatus); got != want {
		t.Errorf("the rows are narrowed to %q, want the status of %s, %q", got, under, want)
	}

	// The command is named after what it shows, so running it twice must not
	// undo it the way a second click does.
	dr.send(FacetMsg{Kind: FacetStatus})
	if !dr.m.facet.on() {
		t.Error("running the command twice cleared the narrowing")
	}

	dr.send(FacetMsg{})
	if dr.m.facet.on() {
		t.Errorf("the rows are still narrowed to %+v after being told to show everything", dr.m.facet)
	}
}

func TestList_ThePaletteSaysSoWhenThereIsNoRowToNarrowBy(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(0))
	dr := openAll(t, d, 120, 30)

	dr.send(FacetMsg{Kind: FacetStatus})

	if dr.m.facet.on() {
		t.Fatal("an empty list narrowed itself to a row that does not exist")
	}
	if got := dr.lastStatus().Text; !strings.Contains(got, "no row") {
		t.Errorf("the status line says %q, want it to say there is no row here", got)
	}
}

func TestList_GoldenWhileNarrowedToACell(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(12))
	dr := openAll(t, d, 120, 30)
	pressOn(t, d, dr, statusZone("PROJ-2"))

	golden(t, "list_facet_120x30.golden", dr.view())
}

// The wheel scrolls the rows and leaves the selection alone, which is what a
// wheel does everywhere else.
func TestList_TheWheelScrollsWithoutMovingTheCursorOffTheRowItIsOn(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(80))
	dr := openAll(t, d, 120, 20)
	under := dr.m.selectedKey()

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if dr.m.top == 0 {
		t.Error("the wheel scrolled nothing")
	}
	if got := dr.m.selectedKey(); got != under {
		t.Errorf("the wheel moved the selection to %q, want it left on %q", got, under)
	}

	dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if dr.m.top != 0 {
		t.Errorf("the wheel is at %d after going down and back up, want 0", dr.m.top)
	}
}
