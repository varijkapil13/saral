package cloud

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// failThenSucceed answers with status for the first n requests and with an
// empty JSON array after that.
func failThenSucceed(n, status int, header map[string]string) http.HandlerFunc {
	var served atomic.Int64
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if served.Add(1) <= int64(n) {
			for key, value := range header {
				w.Header().Set(key, value)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"errorMessages":["not this time"],"errors":{}}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}
}

func TestRetry_HonoursRetryAfterBeforeBackingOff(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field",
		failThenSucceed(1, http.StatusTooManyRequests, map[string]string{"Retry-After": "30"})))
	defer s.Close()

	c, clock := testClient(t, s.URL())
	if _, err := c.do(t.Context(), fieldRequest()); err != nil {
		t.Fatalf("the retried request failed: %v", err)
	}

	if got := clock.waited(); !slices.Equal(got, []time.Duration{30 * time.Second}) {
		t.Errorf("waited %v, want exactly the 30s the site asked for", got)
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the site served %d requests, want 2", served)
	}
}

func TestRetry_BacksOffExponentiallyWhenTheSiteNamesNoInterval(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field",
		failThenSucceed(3, http.StatusBadGateway, nil)))
	defer s.Close()

	c, clock := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 4, Base: 100 * time.Millisecond, Max: time.Minute}))
	if _, err := c.do(t.Context(), fieldRequest()); err != nil {
		t.Fatalf("the retried request failed: %v", err)
	}

	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	if got := clock.waited(); !slices.Equal(got, want) {
		t.Errorf("waited %v, want %v", got, want)
	}
}

func TestRetry_GivesUpAtTheAttemptLimitAndReturnsTheLastFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithStatus(http.MethodGet, "/rest/api/3/field", http.StatusServiceUnavailable, ""))
	defer s.Close()

	c, clock := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 3}))
	_, err := c.do(t.Context(), fieldRequest())

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if served := len(s.Requests()); served != 3 {
		t.Errorf("the site served %d requests, want the 3 attempts the policy allows", served)
	}
	if waits := len(clock.waited()); waits != 2 {
		t.Errorf("waited %d times, want one fewer than the attempts", waits)
	}
}

func TestRetry_ReplaysOnlyWhatIsSafeToReplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		repeatable bool
		status     int
		want       int
	}{
		{name: "a read is replayed after a server fault", method: http.MethodGet, status: http.StatusBadGateway, want: 3},
		{name: "a write is not replayed after a server fault", method: http.MethodPost, status: http.StatusBadGateway, want: 1},
		{name: "a search is a post that only reads, so it is replayed", method: http.MethodPost, repeatable: true, status: http.StatusBadGateway, want: 3},
		{name: "a write is replayed after a throttle, which refused it before it ran", method: http.MethodPost, status: http.StatusTooManyRequests, want: 3},
		{name: "a rejected write is not replayed", method: http.MethodPost, status: http.StatusBadRequest, want: 1},
		{name: "a rejected read is not replayed either", method: http.MethodGet, status: http.StatusBadRequest, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithStatus(tt.method, "/rest/api/3/issue", tt.status, ""))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 3}))
			r := request{method: tt.method, path: "/rest/api/3/issue", repeatable: tt.repeatable}
			if tt.method == http.MethodPost {
				r.body = map[string]string{"summary": "a new issue"}
			}
			if _, err := c.do(t.Context(), r); err == nil {
				t.Fatalf("HTTP %d came back as a success", tt.status)
			}
			if served := len(s.Requests()); served != tt.want {
				t.Errorf("the site served %d requests, want %d", served, tt.want)
			}
		})
	}
}

func TestRetry_AbandonsTheWaitTheMomentTheContextEnds(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithRateLimit(http.MethodGet, "/rest/api/3/field", 30*time.Second))
	defer s.Close()

	c, clock := testClient(t, s.URL())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	clock.whenWaiting(func(ctx context.Context, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})

	_, err := c.do(ctx, fieldRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
	if served := len(s.Requests()); served != 1 {
		t.Errorf("the site served %d requests: the wait was sat out rather than abandoned", served)
	}
}

func TestRetryAfter_ReadsSecondsADateAndTheResetHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header map[string]string
		want   time.Duration
	}{
		{name: "a number of seconds", header: map[string]string{"Retry-After": "45"}, want: 45 * time.Second},
		{name: "seconds with whitespace around them", header: map[string]string{"Retry-After": " 45 "}, want: 45 * time.Second},
		{name: "an HTTP date in the future", header: map[string]string{"Retry-After": testNow.Add(90 * time.Second).UTC().Format(http.TimeFormat)}, want: 90 * time.Second},
		{name: "an HTTP date already past", header: map[string]string{"Retry-After": testNow.Add(-time.Hour).UTC().Format(http.TimeFormat)}, want: 0},
		{name: "a negative number of seconds", header: map[string]string{"Retry-After": "-5"}, want: 0},
		{name: "nonsense", header: map[string]string{"Retry-After": "soon"}, want: 0},
		{name: "no header at all", header: nil, want: 0},
		{name: "Atlassian's own reset header", header: map[string]string{"X-RateLimit-Reset": testNow.Add(12 * time.Second).Format(time.RFC3339)}, want: 12 * time.Second},
		{name: "Retry-After wins over the reset header", header: map[string]string{
			"Retry-After":       "5",
			"X-RateLimit-Reset": testNow.Add(time.Hour).Format(time.RFC3339),
		}, want: 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := http.Header{}
			for key, value := range tt.header {
				h.Set(key, value)
			}
			if got := retryAfter(h, testNow); got != tt.want {
				t.Errorf("retryAfter(%v) = %s, want %s", tt.header, got, tt.want)
			}
		})
	}
}

func TestBackoff_DoublesFromTheBaseUpToTheCap(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{Attempts: 10, Base: time.Second, Max: 5 * time.Second}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i, expected := range want {
		if got := policy.backoff(i + 1); got != expected {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, expected)
		}
	}
	if got := (RetryPolicy{Base: time.Second, Max: time.Hour}).backoff(60); got != time.Hour {
		t.Errorf("backoff(60) = %s, want the cap rather than an overflow", got)
	}
}

func TestRetryPolicy_NormaliseFillsInOnlyWhatWasLeftZero(t *testing.T) {
	t.Parallel()

	defaults := DefaultRetry()
	tests := []struct {
		name string
		in   RetryPolicy
		want RetryPolicy
	}{
		{name: "an untouched policy is the default", in: RetryPolicy{}, want: defaults},
		{
			name: "turning retrying off leaves the rest alone",
			in:   RetryPolicy{Attempts: 1},
			want: RetryPolicy{Attempts: 1, Base: defaults.Base, Max: defaults.Max},
		},
		{
			name: "a cap below the base is raised to it",
			in:   RetryPolicy{Attempts: 2, Base: time.Minute, Max: time.Second},
			want: RetryPolicy{Attempts: 2, Base: time.Minute, Max: time.Minute},
		},
		{
			name: "a negative attempt count is not an infinite loop",
			in:   RetryPolicy{Attempts: -3},
			want: defaults,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.in.normalise(); got != tt.want {
				t.Errorf("normalise() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFullJitter_StaysBetweenHalfTheDelayAndAllOfIt(t *testing.T) {
	t.Parallel()

	const delay = 800 * time.Millisecond
	for range 200 {
		got := fullJitter(delay)
		if got < delay/2 || got > delay {
			t.Fatalf("fullJitter(%s) = %s, want it inside [%s, %s]", delay, got, delay/2, delay)
		}
	}
	if got := fullJitter(0); got != 0 {
		t.Errorf("fullJitter(0) = %s, want 0", got)
	}
}

func TestSystemClock_ReturnsAtOnceWhenThereIsNothingToWaitFor(t *testing.T) {
	t.Parallel()

	clock := systemClock{}
	if err := clock.Wait(t.Context(), 0); err != nil {
		t.Errorf("waiting for no time at all: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := clock.Wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("waiting on a cancelled context gave %v, want the context's error", err)
	}
	if clock.Now().IsZero() {
		t.Error("the system clock reports the zero time")
	}
}

func TestAcquire_GivesUpWhenEverySlotIsBusyAndTheContextEnds(t *testing.T) {
	t.Parallel()

	c, _ := testClient(t, "example.atlassian.net", WithMaxConcurrent(1))
	if err := c.acquire(t.Context()); err != nil {
		t.Fatalf("taking the only slot: %v", err)
	}
	defer c.release()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("acquire gave %v, want the context's error", err)
	}
}

func TestRetryable_KnowsWhichFailuresAreWorthRepeating(t *testing.T) {
	t.Parallel()

	read := request{method: http.MethodGet, path: "/rest/api/3/field"}
	write := request{method: http.MethodPost, path: "/rest/api/3/issue"}

	tests := []struct {
		name string
		r    request
		resp *response
		err  error
		want bool
	}{
		{name: "a broken connection on a read", r: read, err: errors.New("connection reset"), want: true},
		{name: "a broken connection on a write", r: write, err: errors.New("connection reset"), want: false},
		{name: "a throttled write", r: write, resp: &response{status: http.StatusTooManyRequests}, want: true},
		{name: "a server fault on a read", r: read, resp: &response{status: http.StatusInternalServerError}, want: true},
		{name: "a server fault on a write", r: write, resp: &response{status: http.StatusInternalServerError}, want: false},
		{name: "a rejected read", r: read, resp: &response{status: http.StatusBadRequest}, want: false},
		{name: "a refused read", r: read, resp: &response{status: http.StatusForbidden}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := retryable(tt.r, tt.resp, tt.err); got != tt.want {
				t.Errorf("retryable = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestWaitFor_PrefersTheSitesOwnIntervalOverAGuess(t *testing.T) {
	t.Parallel()

	c, _ := testClient(t, "example.atlassian.net", WithRetry(RetryPolicy{Attempts: 4, Base: time.Second, Max: time.Minute}))

	limited := &jira.RateLimitError{RetryAfter: 17 * time.Second}
	if got := c.waitFor(limited, 3); got != 17*time.Second {
		t.Errorf("waitFor(rate limit) = %s, want the 17s the site named", got)
	}
	silent := &jira.RateLimitError{}
	if got := c.waitFor(silent, 3); got != 4*time.Second {
		t.Errorf("waitFor(rate limit with no interval) = %s, want the backoff for attempt 3", got)
	}
	if got := c.waitFor(&jira.TransportError{Op: "GET /x"}, 1); got != time.Second {
		t.Errorf("waitFor(transport failure) = %s, want the base backoff", got)
	}
}

func TestRetry_ResendsTheSameBodyOnEveryAttempt(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, "/rest/api/3/search/jql",
		failThenSucceed(2, http.StatusTooManyRequests, map[string]string{"Retry-After": "1"})))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	r := request{
		method:     http.MethodPost,
		path:       "/rest/api/3/search/jql",
		body:       map[string]any{"jql": "project = EX", "maxResults": 50},
		repeatable: true,
	}
	if _, err := c.do(t.Context(), r); err != nil {
		t.Fatalf("the retried search failed: %v", err)
	}

	served := s.Requests()
	if len(served) != 3 {
		t.Fatalf("the site served %d requests, want 3", len(served))
	}
	for i, req := range served {
		if req.Body != served[0].Body {
			t.Errorf("attempt %d sent %s, want the same bytes as the first attempt %s", i+1, req.Body, served[0].Body)
		}
		if got := req.Header.Get("Content-Length"); got != strconv.Itoa(len(req.Body)) {
			t.Errorf("attempt %d declared Content-Length %q for a %d byte body", i+1, got, len(req.Body))
		}
	}
}
