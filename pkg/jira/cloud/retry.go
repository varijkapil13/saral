package cloud

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Clock is the time source the retry loop waits on. It is injected because a
// test of a backoff that actually slept would be both slow and flaky.
type Clock interface {
	// Now is the current instant, which is what reads a Retry-After given as a
	// date rather than as a number of seconds.
	Now() time.Time
	// Wait blocks for d and returns nil, or returns the context's error the
	// moment the context ends — a view that closed is not waiting out a
	// thirty-second backoff.
	Wait(ctx context.Context, d time.Duration) error
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) Wait(ctx context.Context, d time.Duration) error {
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

// Jitter spreads a backoff wait, so that two clients throttled at the same
// moment do not come back at the same moment. It is given the computed delay
// and returns the delay to wait.
type Jitter func(d time.Duration) time.Duration

// fullJitter picks uniformly between half the computed delay and all of it,
// which keeps the growth of the backoff while breaking up the synchrony.
func fullJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(d-half)+1))
}

// RetryPolicy bounds how hard a client tries. Attempts counts the first try, so
// an Attempts of 1 never retries.
type RetryPolicy struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

// DefaultRetry is the policy a client gets when the caller does not give one.
func DefaultRetry() RetryPolicy {
	return RetryPolicy{Attempts: 4, Base: 250 * time.Millisecond, Max: 10 * time.Second}
}

// normalise fills in whatever the caller left zero, so that setting one field
// does not silently disable the others.
func (p RetryPolicy) normalise() RetryPolicy {
	defaults := DefaultRetry()
	if p.Attempts < 1 {
		p.Attempts = defaults.Attempts
	}
	if p.Base <= 0 {
		p.Base = defaults.Base
	}
	if p.Max <= 0 {
		p.Max = defaults.Max
	}
	if p.Max < p.Base {
		p.Max = p.Base
	}
	return p
}

// backoff doubles from Base for each attempt already spent, up to Max. The
// doubling is a loop rather than a shift because a large Max and a large
// attempt count overflow one.
func (p RetryPolicy) backoff(attempt int) time.Duration {
	d := p.Base
	for range attempt - 1 {
		d *= 2
		if d <= 0 || d >= p.Max {
			return p.Max
		}
	}
	return d
}

// retryable says whether a failed attempt is worth another one.
//
// A 429 always is: the request was refused before it ran, so replaying it
// cannot duplicate a write. A 5xx or a broken connection may have got far
// enough to change something, so those are replayed only when the request says
// replaying it is safe.
func retryable(r request, resp *response, err error) bool {
	if err != nil {
		return r.canRepeat()
	}
	if resp.status == http.StatusTooManyRequests {
		return true
	}
	return resp.status >= http.StatusInternalServerError && r.canRepeat()
}

// waitFor is how long to hold off before the next attempt. A Retry-After is
// honoured exactly: the site has said when it will answer again, and guessing
// anything shorter is how a client gets itself throttled harder.
func (c *Client) waitFor(failure error, attempt int) time.Duration {
	if after, ok := jira.RetryAfter(failure); ok && after > 0 {
		return after
	}
	return c.jitter(c.retry.backoff(attempt))
}

// acquire takes one of the client's concurrency slots, or gives up when the
// context ends while every slot is busy.
func (c *Client) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) release() { <-c.gate }

// retryAfter reads how long the site asked to be left alone for. Jira sends
// Retry-After as a number of seconds; HTTP also allows a date, and Atlassian
// sends X-RateLimit-Reset as an absolute time when it sends no Retry-After.
func retryAfter(h http.Header, now time.Time) time.Duration {
	if d, ok := parseRetryAfter(h.Get("Retry-After"), now); ok {
		return d
	}
	if reset := strings.TrimSpace(h.Get("X-RateLimit-Reset")); reset != "" {
		if at, err := time.Parse(time.RFC3339, reset); err == nil {
			return max(0, at.Sub(now))
		}
	}
	return 0
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(trimmed); err == nil {
		return max(0, time.Duration(seconds)*time.Second), true
	}
	if at, err := http.ParseTime(trimmed); err == nil {
		return max(0, at.Sub(now)), true
	}
	return 0, false
}
