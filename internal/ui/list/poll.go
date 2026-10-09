package list

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/internal/ui/kernel"
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
func (m *Model) pollTick() tea.Cmd {
	every, ok := m.poller.Arm(m.focused && m.lister.Live())
	if !ok {
		return nil
	}
	gen := m.gen
	// Addressed like a read and unlike a widget's tick: this one is the list's
	// own, and a tick that came due while the palette was up would otherwise be
	// eaten there and leave the poller armed for good.
	return kernel.Reply(tea.Tick(every, func(time.Time) tea.Msg { return pollMsg{gen: gen} }), m.addr)
}

// polled acts on a tick: re-read what is on screen, which patches the rows and
// leaves the cursor, the scroll and the filter alone.
//
// A poll while the user is typing into either prompt or picking a number key is
// dropped rather than run: the rows would move under a gesture that is half
// finished. The next tick is lined up anyway, so the poller does not stop
// because somebody paused over a keystroke.
func (m *Model) polled(msg pollMsg) tea.Cmd {
	switch m.poller.Due(appsearch.PollState{
		Focused: m.focused,
		Current: m.current(msg.gen),
		Busy:    m.loading || m.filtering || m.asking || m.bind != bindNone,
	}) {
	case appsearch.PollIdle:
		return nil
	case appsearch.PollWait:
		return m.pollTick()
	case appsearch.PollRun:
	}
	return m.refetch(whyBackground)
}
