package form

import (
	"testing"
	"time"
)

// formClock is the clock the double-click is timed against, wound forward
// rather than slept on.
type formClock struct{ at time.Time }

func (c *formClock) now() time.Time        { return c.at }
func (c *formClock) after(d time.Duration) { c.at = c.at.Add(d) }

func newFormClock() *formClock {
	return &formClock{at: time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)}
}

// Two clicks a second apart are two decisions. Bubble Tea reports no click
// count, and the rule this replaces — a second click on the row already
// selected — could not tell the difference.
func TestForm_TwoDeliberateClicksOnARowDoNotOpenItsEditor(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	clock := newFormClock()
	d.Now = clock.now
	dr := openOn(t, d, 100, 24, fakeStory)
	dr.m.moveTo(0)

	at := 2
	dr.send(clickIn(t, d, dr.m, dr.m.View(), dr.m.rowZone(at)))
	if dr.m.cursor != at {
		t.Fatalf("the cursor is on row %d, want the row that was clicked", dr.m.cursor)
	}

	clock.after(time.Second)
	dr.send(clickIn(t, d, dr.m, dr.m.View(), dr.m.rowZone(at)))

	if dr.m.edit != editNone {
		t.Error("two clicks a second apart opened the field's editor")
	}
}

func TestForm_TwoDeliberateClicksOnAValueDoNotTakeIt(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	clock := newFormClock()
	d.Now = clock.now
	dr := openOn(t, d, 100, 24, fakeStory)
	dr.focus("priority")
	dr.key("enter")

	dr.send(clickIn(t, d, dr.m, dr.m.View(), dr.m.choiceZone(1)))
	if dr.m.pick != 1 {
		t.Fatalf("the picker is on value %d, want the one that was clicked", dr.m.pick)
	}

	clock.after(2 * time.Second)
	dr.send(clickIn(t, d, dr.m, dr.m.View(), dr.m.choiceZone(1)))

	if got := dr.field("priority").picked; len(got) != 0 {
		t.Errorf("two clicks two seconds apart took %+v", got)
	}
}
