package widget

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"
	"github.com/varijkapil13/saral/internal/ui/uitest"
)

func clickAt(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// Nothing in this file strips ANSI, and nothing may: a marker left in a frame
// with the mouse off is exactly what stripping hides.
func TestZoner_MarksNothingWhenThereIsNoMouse(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	for name, z := range map[string]Zoner{
		"the zero value, built with no manager": {},
		"a manager disabled by mouse = false":   NewZoner(off),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const row = "PROJ-1  Fix the thing"
			if got := z.Mark("row:PROJ-1", row); got != row {
				t.Errorf("Mark returned %q, want the row unchanged", got)
			}
			lines := []string{"one", "two"}
			marked := z.MarkLines("block", lines)
			if strings.ContainsRune(strings.Join(marked, "\n"), '\x1b') {
				t.Errorf("MarkLines wrote an escape into %q, which terminal text selection picks up", marked)
			}
			if z.Hit("row:PROJ-1", clickAt(0, 0)) {
				t.Error("a click hit a zone that was never marked")
			}
			if z.Enabled() {
				t.Error("Enabled is true with the mouse off")
			}
		})
	}
}

func TestZoner_ResolvesAClickToTheElementItLandedIn(t *testing.T) {
	t.Parallel()

	mgr := zone.New()
	t.Cleanup(mgr.Close)
	z := NewZoner(mgr)

	frame := z.Mark("row:PROJ-1", "PROJ-1  first") + "\n" + z.Mark("row:PROJ-2", "PROJ-2  second")
	if !z.Enabled() {
		t.Fatal("a live manager reports the mouse off")
	}
	uitest.Zone(t, mgr, func() string { return frame }, z.ID("row:PROJ-1"), z.ID("row:PROJ-2"))

	for _, tc := range []struct {
		name  string
		click tea.MouseClickMsg
		want  string
	}{
		{name: "the first row", click: clickAt(3, 0), want: "row:PROJ-1"},
		{name: "the second row", click: clickAt(3, 1), want: "row:PROJ-2"},
		{name: "below every row", click: clickAt(3, 9), want: ""},
		{name: "past the end of a row", click: clickAt(60, 0), want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ""
			for _, name := range []string{"row:PROJ-1", "row:PROJ-2"} {
				if z.Hit(name, tc.click) {
					got = name
					break
				}
			}
			if got != tc.want {
				t.Errorf("a click at (%d,%d) hit %q, want %q", tc.click.X, tc.click.Y, got, tc.want)
			}
		})
	}
}

func TestZoner_MarkedLinesCoverTheWholeBlockAndNotItsLastLine(t *testing.T) {
	t.Parallel()

	mgr := zone.New()
	t.Cleanup(mgr.Close)
	z := NewZoner(mgr)

	lines := z.MarkLines("comment:1", []string{"Ada wrote        ", "the body of it   ", "and signed it off"})
	frame := strings.Join(lines, "\n")
	uitest.Zone(t, mgr, func() string { return frame }, z.ID("comment:1"))

	for _, y := range []int{0, 1, 2} {
		if !z.Hit("comment:1", clickAt(2, y)) {
			t.Errorf("a click on line %d missed the block it was drawn in", y)
		}
	}
	if z.Hit("comment:1", clickAt(2, 3)) {
		t.Error("a click below the block hit it")
	}
}

// Two views drawing the same element name must not answer for each other's
// clicks, which is the whole reason a prefix is minted per instance.
func TestZoner_TwoViewsMarkTheSameNameApart(t *testing.T) {
	t.Parallel()

	mgr := zone.New()
	t.Cleanup(mgr.Close)
	first, second := NewZoner(mgr), NewZoner(mgr)

	if first.ID("row:1") == second.ID("row:1") {
		t.Fatalf("both views mark row:1 as %q, so one view's click resolves in the other", first.ID("row:1"))
	}

	frame := first.Mark("row:1", "the first view's row") + "\n\n" + second.Mark("row:1", "the second view's row")
	uitest.Zone(t, mgr, func() string { return frame }, first.ID("row:1"), second.ID("row:1"))

	click := clickAt(2, 0)
	if !first.Hit("row:1", click) {
		t.Error("the click missed the row of the view that drew it there")
	}
	if second.Hit("row:1", click) {
		t.Error("the click hit the other view's row of the same name")
	}
}
