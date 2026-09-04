package list

import (
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestPoller_IsOffUnlessTheRunAsksForIt(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(10)), 120, 20)
	if dr.m.poll != 0 {
		t.Fatalf("a list built with nobody asking polls every %s", dr.m.poll)
	}
	if cmd := dr.m.pollTick(); cmd != nil {
		t.Error("a poller nobody asked for scheduled itself")
	}
	if dr.m.pollArmed {
		t.Error("a poller nobody asked for is armed")
	}
}

// TestSetPollInterval_ReachesTheNextListBuilt is not parallel: it moves the
// process-wide interval the composition root sets, and puts it back.
func TestSetPollInterval_ReachesTheNextListBuilt(t *testing.T) {
	SetPollInterval(45 * time.Second)
	t.Cleanup(func() { SetPollInterval(0) })

	m, ok := New(testDeps(newFake(1))).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if m.poll != 45*time.Second {
		t.Errorf("the list polls every %s, want the 45s the run asked for", m.poll)
	}
}

func TestPoller_OnlyRunsForTheViewWithTheKeyboard(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(10)), 120, 20)
	dr.m.poll = time.Minute

	if cmd := dr.m.pollTick(); cmd == nil {
		t.Fatal("a focused list with polling on scheduled nothing")
	}
	dr.m.pollArmed = false
	dr.m.focused = false
	if cmd := dr.m.pollTick(); cmd != nil {
		t.Error("a list nobody is looking at scheduled a poll")
	}
}

// A tick is the poller's own answer and not a widget's: pollArmed is cleared by
// the tick arriving and by nothing else, so one delivered to whatever the
// palette put on top would stop the poller for the rest of the session.
func TestPoller_ATickIsAddressedToTheListThatArmedIt(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(10)), 120, 20)
	dr.m.poll = time.Millisecond

	cmd := dr.m.pollTick()
	if cmd == nil {
		t.Fatal("a focused list with polling on scheduled nothing")
	}
	reply, addressed := cmd().(kernel.ReplyMsg)
	if !addressed {
		t.Fatalf("the tick came back as %T, want it addressed to the list that armed it", cmd())
	}
	if !slices.Contains(reply.To, dr.m.Addr()) {
		t.Errorf("the tick is addressed to %v, which does not include the list at %v", reply.To, dr.m.Addr())
	}
	if _, due := reply.Msg.(pollMsg); !due {
		t.Errorf("the tick carries a %T, want the poll coming due", reply.Msg)
	}
}

func TestPoller_SchedulesOneTickAtATime(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(10)), 120, 20)
	dr.m.poll = time.Minute

	if cmd := dr.m.pollTick(); cmd == nil {
		t.Fatal("the first tick was not scheduled")
	}
	if cmd := dr.m.pollTick(); cmd != nil {
		t.Error("a second tick was scheduled on top of the first, which doubles the poll rate every time")
	}
}

func TestPoller_ReReadsTheRowsAndLeavesThePlaceAlone(t *testing.T) {
	t.Parallel()

	f := newFake(60, jiratest.WithPageSize(20))
	dr := openAll(t, testDeps(f), 120, 30)
	for range 32 {
		dr.key("j")
	}
	under, cursor, top := dr.m.selectedKey(), dr.m.cursor, dr.m.top
	if top == 0 {
		t.Fatal("the list never scrolled, so this proves nothing about the offset")
	}

	m := dr.m
	m.poll, m.pollArmed = time.Minute, true
	msg := firstMsg(t, m.polled(pollMsg{gen: m.gen}))
	patched, ok := msg.(patchedMsg)
	if !ok {
		t.Fatalf("a poll produced a %T, want the patchedMsg that keeps the cursor", msg)
	}

	next, _ := m.Update(patched)
	m, _ = next.(*Model)
	if m.selectedKey() != under || m.cursor != cursor || m.top != top {
		t.Errorf("a poll moved to %s at %d/%d, want %s at %d/%d",
			m.selectedKey(), m.cursor, m.top, under, cursor, top)
	}
	if !m.pollArmed {
		t.Error("the poll that just landed did not line up the next one")
	}
}

func TestPoller_StopsForGoodOnceJiraSaysItIsBeingAskedTooOften(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := openAll(t, testDeps(f), 120, 20)
	dr.m.poll = time.Minute
	before := countCalls(f, "Search")

	dr.send(failedMsg{gen: dr.m.gen, err: &jira.RateLimitError{RetryAfter: 30 * time.Second}})

	if !dr.m.pollPaused {
		t.Fatal("a rate limit left the poller running, which is what spends the rest of the budget")
	}
	if cmd := dr.m.pollTick(); cmd != nil {
		t.Error("a paused poller scheduled another tick")
	}
	if cmd := dr.m.polled(pollMsg{gen: dr.m.gen}); cmd != nil {
		t.Error("a tick outstanding when the limit arrived was acted on anyway")
	}
	if got := countCalls(f, "Search"); got != before {
		t.Errorf("the site was asked %d more times after saying to stop", got-before)
	}
}

func TestPoller_WaitsRatherThanMovingRowsUnderAHalfFinishedGesture(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := openAll(t, testDeps(f), 120, 20)
	dr.m.poll = time.Minute

	for _, tc := range []struct {
		name  string
		start func()
	}{
		{name: "a filter being typed into", start: func() { dr.key("/") }},
		{name: "a number key being picked", start: func() { dr.key("S") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dr.m.pollArmed = false
			tc.start()
			before := countCalls(f, "Search")

			if cmd := dr.m.polled(pollMsg{gen: dr.m.gen}); cmd == nil {
				t.Error("the poller gave up rather than waiting for the gesture to finish")
			}
			if got := countCalls(f, "Search"); got != before {
				t.Errorf("the rows were re-read under the gesture: %d more searches", got-before)
			}
			if !dr.m.pollArmed {
				t.Error("the next tick was not lined up")
			}
			dr.key("esc")
		})
	}
}

func TestPoller_DropsATickLeftOverFromASearchTheUserHasChanged(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := openAll(t, testDeps(f), 120, 20)
	dr.m.poll, dr.m.pollArmed = time.Minute, false
	before := countCalls(f, "Search")

	if cmd := dr.m.polled(pollMsg{gen: dr.m.gen - 1}); cmd == nil {
		t.Error("a stale tick stopped the poller instead of being dropped")
	}
	if got := countCalls(f, "Search"); got != before {
		t.Errorf("a tick for an older search was acted on: %d more searches", got-before)
	}
}
