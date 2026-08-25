package widget

import (
	"testing"
	"time"
)

// fakeClock is the injected clock docs/TESTING.md asks for: a double-click is
// timed by winding it forward, never by sleeping.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time       { return c.at }
func (c *fakeClock) tick(d time.Duration) { c.at = c.at.Add(d) }

func TestClicks_TellsADoubleClickFromTwoDeliberateOnes(t *testing.T) {
	t.Parallel()

	type click struct {
		id    string
		after time.Duration
		want  bool
	}
	for name, sequence := range map[string][]click{
		"two clicks inside the window are a double": {
			{id: "row:1"},
			{id: "row:1", after: 120 * time.Millisecond, want: true},
		},
		"two clicks a second apart are two clicks": {
			{id: "row:1"},
			{id: "row:1", after: time.Second},
		},
		"a click on the edge of the window is still a double": {
			{id: "row:1"},
			{id: "row:1", after: DoubleClick - time.Millisecond, want: true},
		},
		"a click on the window's far edge is not": {
			{id: "row:1"},
			{id: "row:1", after: DoubleClick},
		},
		"the second click has to be on the same element": {
			{id: "row:1"},
			{id: "row:2", after: 50 * time.Millisecond},
		},
		"a double click is consumed, so the third click is a single": {
			{id: "row:1"},
			{id: "row:1", after: 50 * time.Millisecond, want: true},
			{id: "row:1", after: 50 * time.Millisecond},
			{id: "row:1", after: 50 * time.Millisecond, want: true},
		},
		"a click elsewhere in between breaks the pair": {
			{id: "row:1"},
			{id: "row:2", after: 50 * time.Millisecond},
			{id: "row:1", after: 50 * time.Millisecond},
		},
		"an unnamed element never doubles": {
			{id: ""},
			{id: "", after: 10 * time.Millisecond},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			clock := &fakeClock{at: time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)}
			clicks := NewClicks(clock.now)
			for i, c := range sequence {
				clock.tick(c.after)
				if got := clicks.Double(c.id); got != c.want {
					t.Errorf("click %d on %q after %s: double=%v, want %v", i+1, c.id, c.after, got, c.want)
				}
			}
		})
	}
}

// A frozen clock is what a golden test hands a view, and two clicks at the same
// instant are as close together as two clicks can be.
func TestClicks_AFrozenClockStillReportsADouble(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)
	clicks := NewClicks(func() time.Time { return at })

	if clicks.Double("row:1") {
		t.Error("the first click on an element reported a double")
	}
	if !clicks.Double("row:1") {
		t.Error("a second click at the same instant did not report a double")
	}
}

func TestClicks_ForgettingTheLastClickBreaksThePair(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)}
	clicks := NewClicks(clock.now)

	clicks.Double("row:1")
	clicks.Forget()
	clock.tick(10 * time.Millisecond)
	if clicks.Double("row:1") {
		t.Error("a click after Forget completed a pair the view had already spent")
	}
}

// A clock going backwards — a machine's time being corrected under a running
// program — must not read as two clicks in the same instant.
func TestClicks_AClockThatGoesBackwardsIsNotADouble(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{at: time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)}
	clicks := NewClicks(clock.now)

	clicks.Double("row:1")
	clock.tick(-time.Hour)
	if clicks.Double("row:1") {
		t.Error("a click before the previous one reported a double")
	}
}

func TestClicks_WithoutAClockItStillTimesSomething(t *testing.T) {
	t.Parallel()

	clicks := NewClicks(nil)
	if clicks.Double("row:1") {
		t.Error("the first click reported a double")
	}
	if !clicks.Double("row:1") {
		t.Error("two clicks in the same microsecond did not report a double")
	}
}
