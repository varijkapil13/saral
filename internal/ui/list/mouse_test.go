package list

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
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

// Clicking a cell asks the site again rather than narrowing the rows already
// loaded, and it asks by the id the row carries: a display name is localised,
// two statuses on one project can share one, and a pass over what is loaded
// cannot reach an issue this session never fetched.
func TestList_ClickingACellAsksTheSiteForThatValueAndClickingItAgainDropsIt(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		zone  func(string) string
		key   string
		kind  filter.Facet
		label string
		jql   string
	}{
		"a status chip": {
			zone: statusZone, key: "PROJ-2", kind: filter.FacetStatus, label: "Shipped",
			jql: `project = "PROJ" AND status = "10203" ORDER BY updated DESC`,
		},
		"a type chip": {
			zone: typeZone, key: "PROJ-2", kind: filter.FacetType, label: "Chore",
			jql: `project = "PROJ" AND issuetype = "10303" ORDER BY updated DESC`,
		},
		"an assignee": {
			zone: whoZone, key: "PROJ-2", kind: filter.FacetAssignee, label: "Alan Turing",
			jql: `project = "PROJ" AND assignee = "acct-alan" ORDER BY updated DESC`,
		},
		"nobody at all": {
			zone: whoZone, key: "PROJ-4", kind: filter.FacetAssignee, label: unassigned,
			jql: `project = "PROJ" AND assignee IS EMPTY ORDER BY updated DESC`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := testDeps(newFake(20))
			dr := openAll(t, d, 120, 30)

			pressOn(t, d, dr, tc.zone(tc.key))

			if got := dr.m.jql; got != tc.jql {
				t.Fatalf("the click asked for %q, want %q", got, tc.jql)
			}
			if len(dr.m.terms) != 1 || dr.m.terms[0].Facet != tc.kind || dr.m.terms[0].Label != tc.label {
				t.Fatalf("the terms in force are %+v, want one %s named %q", dr.m.terms, tc.kind.Label(), tc.label)
			}
			mustContain(t, dr.view(), tc.kind.Label()+" \""+tc.label+"\"")

			// The chip is the one way off a term that is there whatever the
			// search came back with, which a cell of a row is not.
			pressOn(t, d, dr, termZone(0))

			if len(dr.m.terms) != 0 {
				t.Fatalf("clicking the chip left %+v in force", dr.m.terms)
			}
			if got := dr.m.jql; got != allUpdated {
				t.Errorf("dropping the term asked for %q, want the whole project again: %q", got, allUpdated)
			}
			mustNotContain(t, dr.view(), tc.kind.Label()+" \""+tc.label+"\"")
		})
	}
}

// The same cell twice is the gesture docs/UX.md promises: put it on, take it
// off again.
func TestList_ClickingTheSameCellTwiceDropsTheTerm(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := openAll(t, d, 120, 30)

	pressOn(t, d, dr, statusZone("PROJ-2"))
	if len(dr.m.terms) != 1 {
		t.Fatalf("the first click put %+v in force, want one term", dr.m.terms)
	}
	pressOn(t, d, dr, statusZone("PROJ-2"))

	if len(dr.m.terms) != 0 {
		t.Errorf("a second click on the same cell left %+v in force", dr.m.terms)
	}
	if got := dr.m.jql; got != allUpdated {
		t.Errorf("the search is %q, want the whole project again: %q", got, allUpdated)
	}
}

// The rows really do come back narrowed, for a facet the fake's JQL knows.
func TestList_ATermNarrowsTheRowsTheSiteAnswersWith(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := openAll(t, d, 120, 30)
	all := len(dr.m.view)

	pressOn(t, d, dr, statusZone("PROJ-2"))

	if len(dr.m.view) == 0 || len(dr.m.view) >= all {
		t.Fatalf("%d of %d rows came back, want some but not all", len(dr.m.view), all)
	}
	for _, at := range dr.m.view {
		if got := dr.m.issues[at].Status.ID; got != "10203" {
			t.Errorf("%s came back with status %q, want only 10203", dr.m.issues[at].Key, got)
		}
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

	if len(dr.m.terms) != 1 || dr.m.terms[0].ID != "10203" {
		t.Errorf("clicking the selected row's status put %+v in force, want the status 10203", dr.m.terms)
	}
}

// A term and the local filter are two different things being left out at once:
// the term is what the site was asked, and the filter is what is kept of the
// answer. A row has to survive both.
func TestList_ATermAndTheFilterApplyTogether(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(30))
	dr := openAll(t, d, 120, 30)

	pressOn(t, d, dr, statusZone("PROJ-2"))
	dr.key("/")
	dr.typeText("PROJ-2")

	if len(dr.m.view) == 0 {
		t.Fatal("nothing survived a status term and a filter that both match PROJ-2")
	}
	for _, at := range dr.m.view {
		iss := &dr.m.issues[at]
		if iss.Status.ID != "10203" || !strings.Contains(iss.Key, "PROJ-2") {
			t.Errorf("%s (%s) survived both a status term and a key filter", iss.Key, iss.Status.Name)
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
	if len(dr.m.terms) != 0 {
		t.Errorf("a click put %+v in force with the mouse off", dr.m.terms)
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
	want := dr.m.selectedIssue().Status.ID

	dr.send(FacetMsg{Kind: filter.FacetStatus})

	if len(dr.m.terms) != 1 || dr.m.terms[0].ID != want {
		t.Fatalf("the palette put %+v in force, want the status %q of the row under the cursor", dr.m.terms, want)
	}

	dr.send(FacetMsg{})
	if len(dr.m.terms) != 0 {
		t.Errorf("%+v is still in force after being told to drop every filter", dr.m.terms)
	}
	if got := dr.m.jql; got != allUpdated {
		t.Errorf("dropping every filter asked for %q, want %q", got, allUpdated)
	}
}

func TestList_ThePaletteSaysSoWhenThereIsNoRowToNarrowBy(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(0))
	dr := openAll(t, d, 120, 30)

	dr.send(FacetMsg{Kind: filter.FacetStatus})

	if len(dr.m.terms) != 0 {
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

	golden(t, "list_terms_120x30.golden", dr.view())
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
