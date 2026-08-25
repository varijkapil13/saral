package issue

import (
	"runtime"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
)

// regionZone draws the pane, hands the frame to the zone manager the way the
// kernel does, and returns where a region ended up. The manager records on a
// goroutine of its own, so the zone is looked for until it appears.
func regionZone(t *testing.T, dr *driver, d kernel.Deps, r region) *zone.ZoneInfo {
	t.Helper()

	id := dr.m.zones.ID(zoneNames[r])
	deadline := time.Now().Add(10 * time.Second)
	for {
		d.Zones.Scan(dr.m.View())
		if at := d.Zones.Get(id); !at.IsZero() {
			return at
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing on screen is marked %q", id)
		}
		runtime.Gosched()
	}
}

// Each region is a rectangle the pointer resolves through, so a click moves the
// keyboard to whichever one it landed in and the wheel scrolls that one rather
// than the focused one. Coordinate arithmetic cannot do this: a mouse position
// is where it is on the terminal and a view is never told where its frame begins.
func TestRegions_AClickMovesTheKeyboardAndTheWheelScrollsWhatIsUnderThePointer(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	full := readIssue(t, f, "PROJ-3")
	full.Description = longDoc(60)
	dr := newDriver(t, d, seedOf(t, f, "PROJ-3"), 120, 30)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})

	fields := regionZone(t, dr, d, regionDetails)
	dr.send(tea.MouseClickMsg{X: fields.StartX + 2, Y: fields.StartY + 1, Button: tea.MouseLeft})
	if dr.m.focus != regionDetails {
		t.Fatalf("a click in the fields left the keyboard on region %d", dr.m.focus)
	}

	desc := regionZone(t, dr, d, regionDesc)
	for range 2 {
		dr.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown, X: desc.StartX + 2, Y: desc.StartY + 1})
	}
	switch {
	case dr.m.tops[regionDesc] == 0:
		t.Error("the wheel over the description scrolled nothing")
	case dr.m.tops[regionDetails] != 0:
		t.Error("the wheel over the description scrolled the fields, which had the keyboard")
	case dr.m.focus != regionDetails:
		t.Error("the wheel moved the keyboard as well as the pane under the pointer")
	}
}

// An expand's own line is a click target, because the key opens all of them at
// once and a reader pointing at one means that one.
func TestRegions_AClickOnAnExpandOpensThatOne(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	full := readIssue(t, f, "PROJ-6")
	full.Description = twoFoldDoc()
	dr := newDriver(t, d, seedOf(t, f, "PROJ-6"), 120, 30)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})

	mustContain(t, dr.view(), "How we tested it", "What is left")
	mustNotContain(t, dr.view(), "Twice on staging", "The German site")

	id := dr.m.zones.ID(foldZone(1))
	deadline := time.Now().Add(10 * time.Second)
	var at *zone.ZoneInfo
	for at.IsZero() {
		d.Zones.Scan(dr.m.View())
		at = d.Zones.Get(id)
		if time.Now().After(deadline) {
			t.Fatalf("nothing on screen is marked %q", id)
		}
		runtime.Gosched()
	}
	dr.send(tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseLeft})

	mustContain(t, dr.view(), "The German site")
	mustNotContain(t, dr.view(), "Twice on staging")
}

func twoFoldDoc() adf.Doc {
	return adf.NewDoc(expandNode("How we tested it", "Twice on staging, once in production."),
		expandNode("What is left", "The German site has not been checked."))
}

func expandNode(title, body string) adf.Node {
	n := adf.NewNode("expand", adf.NewNode("paragraph", adf.NewText(body)))
	n.Attrs = adf.Attrs{"title": title}
	return n
}
