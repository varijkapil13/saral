package search

import (
	"errors"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// PollAction is what a poll coming due should do.
type PollAction uint8

const (
	// PollIdle does nothing and lines nothing up: the poller is paused or
	// nobody is looking.
	PollIdle PollAction = iota
	// PollWait skips this poll and lines up the next.
	PollWait
	// PollRun re-reads what is on screen.
	PollRun
)

// PollState is what the view knows when a poll comes due.
type PollState struct {
	Focused bool
	// Current is false for a tick left over from a search the user has since
	// changed.
	Current bool
	// Busy is a fetch in flight or a gesture half finished: the rows would move
	// under it.
	Busy bool
}

// Poller decides when a list re-reads itself. One tick is outstanding at a
// time, and it stops for good the first time Jira says it is being asked too
// often (docs/UX.md — a rate limit pauses any poller). The caller owns the
// timer.
type Poller struct {
	every  time.Duration
	armed  bool
	paused bool
}

// NewPoller polls every interval; zero or less never polls.
func NewPoller(every time.Duration) Poller { return Poller{every: every} }

// Every is the interval the poller was built with.
func (p *Poller) Every() time.Duration { return p.every }

// Armed reports whether a tick is outstanding.
func (p *Poller) Armed() bool { return p.armed }

// Paused reports whether a rate limit stopped the poller for good.
func (p *Poller) Paused() bool { return p.paused }

// Arm lines up the next tick and says how long to wait for it, or false when
// none is due: polling is off, a tick is already outstanding, the poller is
// paused, or ready is false.
func (p *Poller) Arm(ready bool) (time.Duration, bool) {
	if p.every <= 0 || p.armed || p.paused || !ready {
		return 0, false
	}
	p.armed = true
	return p.every, true
}

// Due takes the outstanding tick and decides what it does.
func (p *Poller) Due(s PollState) PollAction {
	p.armed = false
	switch {
	case p.paused || !s.Focused:
		return PollIdle
	case !s.Current || s.Busy:
		return PollWait
	}
	return PollRun
}

// Fail pauses the poller for good when err is a rate limit.
func (p *Poller) Fail(err error) {
	var limit *jira.RateLimitError
	if errors.As(err, &limit) {
		p.paused = true
	}
}
