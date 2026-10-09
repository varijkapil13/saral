package move

import (
	"context"
	"errors"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// PollCap is as long as a follower ever waits between two questions about a
// task. A bulk move over a thousand issues takes minutes, and a poll a minute
// apart would report it finished long after it was.
const PollCap = 4 * time.Second

// ErrFollowed is Next asked again after the task it follows has finished.
var ErrFollowed = errors.New("the task has already finished")

// Waiter is how long to wait before asking the queue again. It is injected so
// that a test can hold the backoff to account without spending it.
type Waiter func(ctx context.Context, d time.Duration) error

// Sleep waits, and gives up the moment ctx is done.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Backoff is how long to wait before the next question about a task. The first
// is asked at once, because a small move is finished by the time a fixed delay
// would have elapsed, and the wait then doubles to a ceiling.
func Backoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	d := 250 * time.Millisecond
	for range attempt - 1 {
		d *= 2
		if d >= PollCap {
			return PollCap
		}
	}
	return d
}

// held is how long a rate limit asks for, which is a pause rather than a
// failure: the queue is still working and the follower is only being told to
// stop asking so often.
func held(err error, attempt int) (time.Duration, bool) {
	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		return 0, false
	}
	if limited.RetryAfter > 0 {
		return limited.RetryAfter, true
	}
	return Backoff(attempt + 1), true
}

// Progress is one answer about a task. Paused is non-zero when the queue
// answered with a rate limit instead, and is the wait before the next question;
// Status is then empty.
type Progress struct {
	Status jira.TaskStatus
	Paused time.Duration
}

// Task follows one task on its queue until it stops. The ref is passed back to
// the port untouched: a bulk move is followed on its own queue, and an id put
// into the generic task path answers a body that does not decode as this one.
type Task struct {
	watcher jira.TaskWatcher
	ref     jira.TaskRef
	wait    Waiter
	attempt int
	after   time.Duration
	done    bool
}

// Follow starts following ref. A nil wait is Sleep.
func Follow(watcher jira.TaskWatcher, ref jira.TaskRef, wait Waiter) *Task {
	if wait == nil {
		wait = Sleep
	}
	return &Task{watcher: watcher, ref: ref, wait: wait}
}

// Ref is the task being followed.
func (t *Task) Ref() jira.TaskRef { return t.ref }

// Next waits out the backoff and asks the queue once. A rate limit is answered
// as a pause and not an error. Whether the task has stopped is State.Done and
// never a switch written here: CANCEL_REQUESTED is a task still running. Next is
// not safe for concurrent use; a caller drains it one call at a time.
func (t *Task) Next(ctx context.Context) (Progress, error) {
	if t.done {
		return Progress{}, ErrFollowed
	}
	if err := t.wait(ctx, t.after); err != nil {
		return Progress{}, err
	}
	status, err := t.watcher.Task(ctx, t.ref)
	if err != nil {
		if pause, limited := held(err, t.attempt); limited {
			t.attempt++
			t.after = pause
			return Progress{Paused: pause}, nil
		}
		return Progress{}, err
	}
	if status.State.Done() {
		t.done = true
		return Progress{Status: status}, nil
	}
	t.attempt++
	t.after = Backoff(t.attempt)
	return Progress{Status: status}, nil
}
