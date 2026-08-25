package list

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

// pollEvery is how often a focused list re-reads itself, or zero for never,
// which is where it starts.
//
// It is a package variable set by the composition root rather than a field on
// kernel.Deps, for the same reason onboarding takes its connector that way: this
// is a preference of the run, the kernel is closed to another field for it, and a
// view may not read the config file itself.
var pollEvery atomic.Int64

// SetPollInterval turns the optional poller on for this process. Zero or less
// turns it off, which is the default: a client that polls whether or not anybody
// asked spends every user's rate limit on a screen nobody is looking at.
func SetPollInterval(d time.Duration) { pollEvery.Store(int64(d)) }

// PollInterval reports what SetPollInterval was last given, so that the
// composition root can check its own flag arrived.
func PollInterval() time.Duration { return time.Duration(pollEvery.Load()) }

// pollMsg is a poll coming due. It carries the generation it was scheduled for,
// so a tick left over from a search the user has already changed is not acted on
// as though it were about the one on screen.
type pollMsg struct{ gen int }

// pollTick schedules the next poll, or nothing at all.
//
// One tick is outstanding at a time, it is only scheduled for the view that has
// the keyboard, and it stops for good the first time Jira says it is being asked
// too often (docs/UX.md — a rate limit pauses any poller).
func (m *Model) pollTick() tea.Cmd {
	if m.poll <= 0 || m.pollArmed || m.pollPaused || !m.focused || m.search == nil {
		return nil
	}
	m.pollArmed = true
	gen := m.gen
	return tea.Tick(m.poll, func(time.Time) tea.Msg { return pollMsg{gen: gen} })
}

// polled acts on a tick: re-read what is on screen, which patches the rows and
// leaves the cursor, the scroll and the filter alone.
//
// A poll while the user is typing into the filter or picking a number key is
// dropped rather than run: the rows would move under a gesture that is half
// finished. The next tick is lined up anyway, so the poller does not stop
// because somebody paused over a keystroke.
func (m *Model) polled(msg pollMsg) tea.Cmd {
	m.pollArmed = false
	switch {
	case m.pollPaused || !m.focused:
		return nil
	case !m.current(msg.gen) || m.loading || m.filtering || m.bind != bindNone:
		return m.pollTick()
	}
	return m.refresh(false)
}
