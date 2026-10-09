package move

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// Backoff has to grow and then stop growing: a poll a minute apart reports a move
// finished long after it was.
func TestBackoff_GrowsToACeilingAndStartsAtOnce(t *testing.T) {
	t.Parallel()
	if got := Backoff(0); got != 0 {
		t.Errorf("the first question waits %s", got)
	}
	last := Backoff(1)
	for at := 2; at < 12; at++ {
		got := Backoff(at)
		switch {
		case got < last:
			t.Fatalf("question %d waits %s after %s", at, got, last)
		case got > PollCap:
			t.Fatalf("question %d waits %s, past the %s ceiling", at, got, PollCap)
		}
		last = got
	}
	if last != PollCap {
		t.Errorf("the wait settled at %s rather than the %s ceiling", last, PollCap)
	}
}

func TestSleep_GivesUpTheMomentTheContextIsDone(t *testing.T) {
	t.Parallel()
	if err := Sleep(context.Background(), 0); err != nil {
		t.Errorf("waiting for no time at all answered %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sleep(ctx, time.Hour); err == nil {
		t.Error("a cancelled follower waited out the backoff")
	}
	if err := Sleep(context.Background(), time.Microsecond); err != nil {
		t.Errorf("a pause that elapsed answered %v", err)
	}
}

type waits struct {
	mu  sync.Mutex
	got []time.Duration
}

func (w *waits) wait(ctx context.Context, d time.Duration) error {
	w.mu.Lock()
	w.got = append(w.got, d)
	w.mu.Unlock()
	return ctx.Err()
}

func submitted(t *testing.T, f *jiratest.Fake) jira.TaskRef {
	t.Helper()
	iss := seeded(t, f, "PROJ-1")
	ref, err := Submit(t.Context(), f, jira.MoveRequest{Keys: []string{"PROJ-1"}, TargetProjectKey: "OTHER", TargetIssueTypeID: iss[0].Type.ID})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestTask_IsDrainedUntilItStopsAskingLessOftenEachTime(t *testing.T) {
	t.Parallel()
	f := newFake(2)
	w := &waits{}
	task := Follow(f, submitted(t, f), w.wait)

	var states []jira.TaskState
	for range 10 {
		got, err := task.Next(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, got.Status.State)
		if got.Status.State.Done() {
			break
		}
	}
	if len(states) == 0 || states[len(states)-1] != jira.TaskComplete {
		t.Fatalf("the task went %v and never completed", states)
	}
	if w.got[0] != 0 {
		t.Errorf("the first question waited %s", w.got[0])
	}
	for i := 1; i < len(w.got); i++ {
		if w.got[i] <= w.got[i-1] {
			t.Errorf("the waits %v do not back off", w.got)
			break
		}
	}
	if _, err := task.Next(t.Context()); !errors.Is(err, ErrFollowed) {
		t.Errorf("asking after the task finished answered %v", err)
	}
}

func TestTask_ARateLimitIsAPauseOfWhatJiraAskedFor(t *testing.T) {
	t.Parallel()
	f := newFake(2)
	w := &waits{}
	task := Follow(f, submitted(t, f), w.wait)

	f.FailNext(&jira.RateLimitError{RetryAfter: 3 * time.Second})
	got, err := task.Next(t.Context())
	if err != nil {
		t.Fatalf("a rate limit ended the follow: %v", err)
	}
	if got.Paused != 3*time.Second {
		t.Errorf("the pause is %s, want the 3s Jira asked for", got.Paused)
	}
	if _, err := task.Next(t.Context()); err != nil {
		t.Fatal(err)
	}
	if w.got[1] != 3*time.Second {
		t.Errorf("the next question waited %s rather than the pause", w.got[1])
	}

	f.FailNext(&jira.RateLimitError{})
	if got, _ := task.Next(t.Context()); got.Paused <= 0 {
		t.Error("a rate limit naming no wait was not a pause")
	}
}

func TestTask_AnyOtherFailurePassesThroughUnwrapped(t *testing.T) {
	t.Parallel()
	for name, want := range failures() {
		if limited := new(*jira.RateLimitError); errors.As(want, limited) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(2)
			task := Follow(f, submitted(t, f), (&waits{}).wait)
			f.FailNext(want)
			_, err := task.Next(t.Context())
			mustBe(t, err, want)
		})
	}
}

func TestTask_GivesUpWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()
	f := newFake(2)
	task := Follow(f, submitted(t, f), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := task.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled follow answered %v", err)
	}
	if task.Ref().ID == "" {
		t.Error("the task forgot its ref")
	}
}

func TestTask_PollsWithTheWholeRef(t *testing.T) {
	t.Parallel()
	f := newFake(2)
	ref := submitted(t, f)
	spy := &watcher{TaskWatcher: f}
	task := Follow(spy, ref, (&waits{}).wait)
	if _, err := task.Next(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(spy.polled) != 1 || spy.polled[0] != ref {
		t.Errorf("the queue was asked with %+v, want %+v", spy.polled, ref)
	}
}

type watcher struct {
	jira.TaskWatcher
	polled []jira.TaskRef
}

func (w *watcher) Task(ctx context.Context, ref jira.TaskRef) (jira.TaskStatus, error) {
	w.polled = append(w.polled, ref)
	return w.TaskWatcher.Task(ctx, ref)
}
