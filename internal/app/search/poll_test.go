package search

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

func TestPoller_IsOffUnlessAnIntervalWasGiven(t *testing.T) {
	t.Parallel()

	for _, every := range []time.Duration{0, -time.Second} {
		p := NewPoller(every)
		if _, ok := p.Arm(true); ok || p.Armed() {
			t.Errorf("a poller every %s armed itself", every)
		}
	}
}

func TestPoller_ArmsOneTickAtATimeAndOnlyWhenReady(t *testing.T) {
	t.Parallel()

	p := NewPoller(time.Minute)
	if _, ok := p.Arm(false); ok {
		t.Fatal("a poller that is not ready armed itself")
	}
	every, ok := p.Arm(true)
	if !ok || every != time.Minute || !p.Armed() {
		t.Fatalf("Arm = %s, %v, want a minute", every, ok)
	}
	if _, ok := p.Arm(true); ok {
		t.Error("a second tick was armed on top of the first")
	}
}

func TestPoller_DecidesWhatADueTickDoes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state PollState
		want  PollAction
	}{
		{"focused, current and idle runs", PollState{Focused: true, Current: true}, PollRun},
		{"nobody looking does nothing", PollState{Current: true}, PollIdle},
		{"a stale tick waits for the next", PollState{Focused: true}, PollWait},
		{"a gesture under way waits for the next", PollState{Focused: true, Current: true, Busy: true}, PollWait},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPoller(time.Minute)
			p.Arm(true)
			if got := p.Due(tc.state); got != tc.want {
				t.Errorf("Due(%+v) = %d, want %d", tc.state, got, tc.want)
			}
			if p.Armed() {
				t.Error("a tick that came due left the poller armed")
			}
		})
	}
}

func TestPoller_StopsForGoodOnARateLimitAndOnNothingElse(t *testing.T) {
	t.Parallel()

	p := NewPoller(time.Minute)
	p.Fail(&jira.TransportError{Op: "search", Status: 503})
	p.Fail(&jira.CapabilityError{Reason: "no"})
	if p.Paused() {
		t.Fatal("a failure that is not a rate limit paused the poller")
	}

	p.Arm(true)
	p.Fail(fmt.Errorf("searching: %w", &jira.RateLimitError{RetryAfter: time.Second}))
	if !p.Paused() {
		t.Fatal("a wrapped rate limit left the poller running")
	}
	if got := p.Due(PollState{Focused: true, Current: true}); got != PollIdle {
		t.Errorf("a tick outstanding when the limit arrived came due as %d", got)
	}
	if _, ok := p.Arm(true); ok {
		t.Error("a paused poller armed again")
	}
	p.Fail(errors.New("anything"))
	if !p.Paused() {
		t.Error("the pause did not hold")
	}
}
