package widget

import "time"

// DoubleClick is how long after a click a second one on the same element is
// still the same gesture. Long enough for a deliberate double-click on a
// trackpad, short enough that two decisions a second apart stay two clicks.
const DoubleClick = 400 * time.Millisecond

// Clicks turns a stream of clicks into single and double ones.
//
// Bubble Tea v2 reports a click as position, button and modifier and nothing
// else — no click count, and no instant to compare with the last one — so a view
// that wants a double-click has to time it. The alternative every view here
// reached for first was "a second click on the row that is already selected",
// which cannot tell a double-click from two deliberate clicks minutes apart, and
// so opens an issue under a user who was only looking at it.
type Clicks struct {
	window time.Duration
	now    func() time.Time

	id string
	at time.Time
}

// NewClicks times double-clicks against the session's clock, which a test
// injects and winds forward rather than sleeping on.
func NewClicks(now func() time.Time) *Clicks {
	if now == nil {
		now = time.Now
	}
	return &Clicks{window: DoubleClick, now: now}
}

// Double records a click on id and reports whether it completes a double-click
// on the same id. A double-click consumes both, so three clicks are a single, a
// double and a single rather than two overlapping doubles.
func (c *Clicks) Double(id string) bool {
	at := c.now()
	if id != "" && id == c.id {
		if since := at.Sub(c.at); since >= 0 && since < c.window {
			c.id, c.at = "", time.Time{}
			return true
		}
	}
	c.id, c.at = id, at
	return false
}

// Forget drops what was clicked last, so that the next click on the same
// element is a single one. A view calls it when the click it just took changed
// what is on screen, and the element under the pointer is no longer the one that
// was clicked.
func (c *Clicks) Forget() { c.id, c.at = "", time.Time{} }
