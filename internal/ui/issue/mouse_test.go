package issue

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// paneClock is what a double-click is timed against here. It is wound forward
// rather than slept on, so a deliberate second click costs no wall time.
type paneClock struct{ at time.Time }

func (c *paneClock) now() time.Time        { return c.at }
func (c *paneClock) after(d time.Duration) { c.at = c.at.Add(d) }

func newPaneClock() *paneClock {
	return &paneClock{at: time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)}
}

// A field editor opened by a click nobody meant is a description handed to
// $EDITOR, so the second click has to be part of the same gesture.
func TestEdit_TwoDeliberateClicksOnARowDoNotOpenIt(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(t, f)
	clock := newPaneClock()
	d.Now = clock.now
	p := newPanel(t, New(d, readIssue(t, f, "PROJ-6"), withDrafts(tempDrafts(t))), 100, 28)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, "PROJ-6")})

	at := p.zoneAt(d, "row:labels")
	p.clickAt(at)
	if got := p.editor().currentCursorRow(); got == nil || got.id != "labels" {
		t.Fatalf("the click did not put the cursor on labels: %+v", got)
	}

	clock.after(time.Second)
	p.clickAt(at)

	if p.editor().stage != sideBrowse {
		t.Error("two clicks a second apart opened the field, so a second look reads as a double-click")
	}
}

// The sidebar has more rows than a short terminal can draw, and until now the
// pane drew them all and let the frame clip: a row past the first few could be
// neither seen nor pointed at.
func TestEdit_TheWheelReachesTheFieldsAShortTerminalClipsOff(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(t, f)
	p := newPanel(t, New(d, readIssue(t, f, "PROJ-6"), withDrafts(tempDrafts(t))), 100, 16)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, "PROJ-6")})

	first := p.frame()
	if !strings.Contains(first, "Summary") {
		t.Fatalf("the first field is not on screen at all:\n%s", first)
	}

	last := len(p.editor().sideRows) - 1
	if box := p.editor().lay.boxes[regionDetails].h; last < box {
		t.Fatalf("this issue's %d sidebar rows all fit in a %d-row box, so this test proves nothing", last+1, box)
	}

	// The wheel targets whatever has the keyboard when it lands outside every
	// region's own zone, so the details region is put there directly — the
	// same as clicking a row would, and without a click's own race against the
	// zone manager's background goroutine.
	p.editor().focus = regionDetails
	down, up := tea.MouseWheelMsg{Button: tea.MouseWheelDown, Y: 1000}, tea.MouseWheelMsg{Button: tea.MouseWheelUp, Y: 1000}
	for range last + 2 {
		p.send(down)
	}
	scrolled := p.frame()
	if strings.Contains(scrolled, "Summary") {
		t.Errorf("the wheel scrolled nothing away:\n%s", scrolled)
	}

	for range last + 4 {
		p.send(up)
	}
	back := p.frame()
	if !strings.Contains(back, "Summary") {
		t.Errorf("the wheel could not get back to the first field:\n%s", back)
	}
	if got := p.editor().tops[regionDetails]; got != 0 {
		t.Errorf("the pane is scrolled to %d after going down and back up, want 0", got)
	}
}

// The keyboard and the wheel have to agree about where the pane is: a cursor
// walked past the bottom of a short terminal used to leave the screen.
func TestEdit_WalkingTheCursorDownBringsItsRowBackOnScreen(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(t, f)
	p := newPanel(t, New(d, readIssue(t, f, "PROJ-6"), withDrafts(tempDrafts(t))), 100, 16)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, "PROJ-6")})
	p.editor().focus = regionDetails

	last := len(p.editor().sideRows) - 1
	for range last {
		p.keys("j")
	}

	if got := p.editor().cursor; got != last {
		t.Fatalf("the cursor is on row %d, want the last one (%d)", got, last)
	}
	if got := p.editor().tops[regionDetails]; got+p.editor().lay.boxes[regionDetails].h <= p.editor().sideRows[last].lineAt {
		t.Errorf("the row under the cursor is off screen: top %d, box %d, row's line %d",
			got, p.editor().lay.boxes[regionDetails].h, p.editor().sideRows[last].lineAt)
	}
}
