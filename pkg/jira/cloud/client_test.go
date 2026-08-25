package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	testEmail = "sam@example.invalid"
	testToken = "not-a-real-api-token-1234"
)

var testNow = time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)

// testClock records what the retry loop asked to wait for instead of waiting,
// which is what keeps these tests instant and their assertions exact.
type testClock struct {
	mu     sync.Mutex
	now    time.Time
	waits  []time.Duration
	onWait func(ctx context.Context, d time.Duration) error
}

func newTestClock() *testClock { return &testClock{now: testNow} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Wait(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.waits = append(c.waits, d)
	hook := c.onWait
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, d)
	}
	return ctx.Err()
}

func (c *testClock) waited() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func (c *testClock) whenWaiting(hook func(ctx context.Context, d time.Duration) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onWait = hook
}

// testClient builds a client whose waits are recorded rather than served and
// whose jitter is the identity, so a backoff is an assertable number.
func testClient(t *testing.T, site string, opts ...Option) (*Client, *testClock) {
	t.Helper()

	clock := newTestClock()
	base := []Option{
		WithClock(clock),
		WithJitter(func(d time.Duration) time.Duration { return d }),
	}
	c, err := New(site, testEmail, testToken, append(base, opts...)...)
	if err != nil {
		t.Fatalf("building a client for %s: %v", site, err)
	}
	return c, clock
}

// waitUntil blocks until cond holds, without a sleep and without a fixed number
// of spins: what it is waiting for is another goroutine reaching a state.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
	}
}

func fieldRequest() request {
	return request{method: http.MethodGet, path: "/rest/api/3/field"}
}

func TestNew_RefusesASiteOrCredentialItCannotUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		site  string
		email string
		token string
		want  string
	}{
		{name: "no site at all", site: "", email: testEmail, token: testToken, want: "the site is required"},
		{name: "a site that is not an http address", site: "ftp://example.atlassian.net", email: testEmail, token: testToken, want: "http or https"},
		{name: "a site carrying credentials", site: "https://sam:hunter2@example.atlassian.net", email: testEmail, token: testToken, want: "must not carry credentials"},
		{name: "no account email", site: "example.atlassian.net", email: "  ", token: testToken, want: "account email is required"},
		{name: "no token", site: "example.atlassian.net", email: testEmail, token: "", want: "API token is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := New(tt.site, tt.email, tt.token)
			if err == nil {
				t.Fatalf("New(%q, %q, …) built %v, want a refusal", tt.site, tt.email, c)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("New said %q, want it to mention %q", err, tt.want)
			}
			if strings.Contains(err.Error(), tt.token) && tt.token != "" {
				t.Errorf("New put the token in its error: %q", err)
			}
		})
	}
}

func TestParseSite_ReadsEveryFormAProfileMayWriteASiteIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		site string
		want string
	}{
		{name: "a bare host", site: "example.atlassian.net", want: "https://example.atlassian.net"},
		{name: "a host with the scheme already on it", site: "https://example.atlassian.net", want: "https://example.atlassian.net"},
		{name: "a trailing slash", site: "https://example.atlassian.net/", want: "https://example.atlassian.net"},
		{name: "surrounding whitespace", site: "  example.atlassian.net  ", want: "https://example.atlassian.net"},
		{name: "a loopback address with a port", site: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{name: "a host behind a path prefix", site: "https://proxy.example/jira/", want: "https://proxy.example/jira"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSite(tt.site)
			if err != nil {
				t.Fatalf("parseSite(%q): %v", tt.site, err)
			}
			if got.String() != tt.want {
				t.Errorf("parseSite(%q) = %s, want %s", tt.site, got, tt.want)
			}
		})
	}
}

func TestEndpoint_KeepsThePathPrefixAndEncodesTheQuery(t *testing.T) {
	t.Parallel()

	c, _ := testClient(t, "https://proxy.example/jira")
	got := c.endpoint(request{
		method: http.MethodGet,
		path:   "/rest/api/3/search/approximate-count",
		query:  url.Values{"jql": {"project = EX ORDER BY updated"}},
	})
	want := "https://proxy.example/jira/rest/api/3/search/approximate-count?jql=project+%3D+EX+ORDER+BY+updated"
	if got != want {
		t.Errorf("endpoint = %s, want %s", got, want)
	}
}

func TestDo_SendsBasicAuthAndAsksForJSON(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithUserAgent("saral/test"))
	var me struct {
		AccountID string `json:"accountId"`
	}
	if err := c.doJSON(t.Context(), request{method: http.MethodGet, path: "/rest/api/3/myself"}, &me); err != nil {
		t.Fatalf("reading /myself: %v", err)
	}
	if me.AccountID == "" {
		t.Error("the response decoded into an empty account")
	}

	served := s.Requests()
	if len(served) != 1 {
		t.Fatalf("the site served %d requests, want 1", len(served))
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testEmail+":"+testToken))
	if got := served[0].Header.Get("Authorization"); got != want {
		t.Errorf("Authorization = %q, want the basic auth pair", got)
	}
	if got := served[0].Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := served[0].Header.Get("User-Agent"); got != "saral/test" {
		t.Errorf("User-Agent = %q, want saral/test", got)
	}
}

func TestDo_MapsEveryRefusedStatusToItsTypedError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		fixture string
		assert  func(t *testing.T, err error)
	}{
		{
			name:    "a rejected request is a validation failure",
			status:  http.StatusBadRequest,
			fixture: "validation_error.json",
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.ValidationError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
			},
		},
		{
			name:   "a rejected credential is an auth failure",
			status: http.StatusUnauthorized,
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.AuthError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.AuthError", err, err)
				}
			},
		},
		{
			name:    "a refusal is a capability answer",
			status:  http.StatusForbidden,
			fixture: "plans_403.json",
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.CapabilityError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
				if !strings.Contains(target.Reason, "Administer Jira") {
					t.Errorf("Reason = %q, want Jira's own wording out of the body", target.Reason)
				}
			},
		},
		{
			name:   "a missing resource is a not-found",
			status: http.StatusNotFound,
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.NotFoundError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
				if target.Kind != "field catalogue" || target.ID != "EX" {
					t.Errorf("got %s %s, want the request's own target", target.Kind, target.ID)
				}
			},
		},
		{
			name:   "a resource that is gone is also a not-found",
			status: http.StatusGone,
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.NotFoundError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
			},
		},
		{
			name:   "a concurrent edit is a conflict",
			status: http.StatusConflict,
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.ConflictError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.ConflictError", err, err)
				}
			},
		},
		{
			name:    "a throttled request is a rate limit",
			status:  http.StatusTooManyRequests,
			fixture: "rate_limited.json",
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.RateLimitError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
				if target.Endpoint != "/rest/api/3/field" {
					t.Errorf("Endpoint = %q, want the path that was throttled", target.Endpoint)
				}
			},
		},
		{
			name:   "a server fault is a transport failure",
			status: http.StatusInternalServerError,
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.TransportError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
				}
				if target.Status != http.StatusInternalServerError {
					t.Errorf("Status = %d, want 500", target.Status)
				}
				if target.Op != "GET /rest/api/3/field" {
					t.Errorf("Op = %q, want the method and path", target.Op)
				}
			},
		},
		{
			name:   "a status outside the taxonomy is a transport failure",
			status: http.StatusNotImplemented,
			assert: func(t *testing.T, err error) {
				t.Helper()
				var target *jira.TransportError
				if !errors.As(err, &target) {
					t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithStatus(http.MethodGet, "/rest/api/3/field", tt.status, tt.fixture))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			r := fieldRequest()
			r.kind, r.id = "field catalogue", "EX"
			_, err := c.do(t.Context(), r)
			if err == nil {
				t.Fatalf("HTTP %d came back as a success", tt.status)
			}
			tt.assert(t, err)
		})
	}
}

func TestDo_MapsAValidationFailureFieldByFieldInTheOrderJiraWroteThem(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithStatus(http.MethodPost, "/rest/api/3/issue", http.StatusBadRequest, "validation_error.json"))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.do(t.Context(), request{method: http.MethodPost, path: "/rest/api/3/issue", body: map[string]string{"summary": ""}})

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	wantOrder := []string{"summary", "duedate", "customfield_10032"}
	if len(invalid.Fields) != len(wantOrder) {
		t.Fatalf("got %d field messages, want %d: %v", len(invalid.Fields), len(wantOrder), invalid.Fields)
	}
	for i, field := range wantOrder {
		if invalid.Fields[i].Field != field {
			t.Errorf("field %d is %q, want %q — the order Jira wrote them in is the order a form focuses them in",
				i, invalid.Fields[i].Field, field)
		}
	}
	if msg, ok := invalid.For("summary"); !ok || !strings.Contains(msg, "summary") {
		t.Errorf("the summary message is %q, want Jira's own", msg)
	}
	if len(invalid.Messages) != 1 || !strings.Contains(invalid.Messages[0], "customfield_10019") {
		t.Errorf("Messages = %v, want the one loose errorMessages entry", invalid.Messages)
	}
}

func TestDo_ReadsRetryAfterOffTheRateLimitWithAndWithoutTheHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opt  jiratest.ServerOption
		want time.Duration
	}{
		{
			name: "the site said when to come back",
			opt:  jiratest.WithRateLimit(http.MethodGet, "/rest/api/3/field", 30*time.Second),
			want: 30 * time.Second,
		},
		{
			name: "the site sent no Retry-After",
			opt:  jiratest.WithStatus(http.MethodGet, "/rest/api/3/field", http.StatusTooManyRequests, "rate_limited.json"),
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(tt.opt)
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			_, err := c.do(t.Context(), fieldRequest())

			var limited *jira.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
			}
			if limited.RetryAfter != tt.want {
				t.Errorf("RetryAfter = %s, want %s", limited.RetryAfter, tt.want)
			}
		})
	}
}

func TestDo_TreatsABodyThatWillNotDecodeAsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("<html>your proxy has opinions</html>"))
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	var fields []struct{}
	err := c.doJSON(t.Context(), fieldRequest(), &fields)

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if broken.Status != http.StatusOK {
		t.Errorf("Status = %d, want the 200 the body arrived with", broken.Status)
	}
}

func TestDo_TreatsADialFailureAsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	site := s.URL()
	s.Close()

	c, _ := testClient(t, site, WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.do(t.Context(), fieldRequest())

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if broken.Status != 0 {
		t.Errorf("Status = %d, want 0: the request never reached a server", broken.Status)
	}
}

func TestDo_ReturnsTheContextErrorWhenTheCallerCancelsMidFlight(t *testing.T) {
	t.Parallel()

	arrived := make(chan struct{}, 1)
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field", func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	go func() {
		_, err := c.do(ctx, fieldRequest())
		failed <- err
	}()

	<-arrived
	cancel()
	err := <-failed
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
	var broken *jira.TransportError
	if errors.As(err, &broken) {
		t.Error("a caller cancelling its own work is not a transport failure and must not show a stale badge")
	}
}

func TestDo_CoalescesIdenticalRequestsThatAreInFlightAtOnce(t *testing.T) {
	t.Parallel()

	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
		arrived <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"summary"}]`))
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	r := fieldRequest()

	const callers = 5
	results := make(chan error, callers)
	for range callers {
		go func() {
			_, err := c.do(t.Context(), r)
			results <- err
		}()
	}

	<-arrived
	// Give the four followers time to reach the coalescer; if they had not, the
	// site would see more than one request and the assertion below would say so.
	waitUntil(t, "every caller to be waiting on the one call in flight", func() bool {
		return runtime.NumGoroutine() > 0 && len(s.Requests()) == 1
	})
	close(release)
	for range callers {
		if err := <-results; err != nil {
			t.Fatalf("a coalesced caller failed: %v", err)
		}
	}

	if served := len(s.Requests()); served != 1 {
		t.Errorf("the site served %d requests, want 1: %d callers asked for the same page", served, callers)
	}

	// And a later caller starts a fresh call rather than attaching to a finished
	// one, which is the half a coalescer gets wrong by caching.
	if _, err := c.do(t.Context(), r); err != nil {
		t.Fatalf("a caller after the flight: %v", err)
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the site served %d requests, want 2: the second ask is a new call", served)
	}
}

func TestDo_NeverCoalescesAWrite(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, "/rest/api/3/issue", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"EX-1"}`))
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	r := request{method: http.MethodPost, path: "/rest/api/3/issue", body: map[string]string{"summary": "the same summary"}}

	const callers = 4
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.do(t.Context(), r); err != nil {
				t.Errorf("creating an issue: %v", err)
			}
		}()
	}
	wg.Wait()

	if served := len(s.Requests()); served != callers {
		t.Errorf("the site served %d requests, want %d: two creates are two issues, however alike they look", served, callers)
	}
}

func TestDo_TakesOverACallTheStartingCallerAbandoned(t *testing.T) {
	t.Parallel()

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field", func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"summary"}]`))
		case <-r.Context().Done():
		}
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	r := fieldRequest()

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := c.do(leaderCtx, r)
		leaderDone <- err
	}()
	<-arrived

	followerDone := make(chan error, 1)
	go func() {
		_, err := c.do(t.Context(), r)
		followerDone <- err
	}()
	waitUntil(t, "the follower to reach the call in flight", func() bool {
		return len(s.Requests()) == 1
	})

	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("the abandoning caller got %v, want its own context error", err)
	}
	close(release)
	if err := <-followerDone; err != nil {
		t.Fatalf("the caller still waiting got %v, want the page it asked for", err)
	}
	// The site is asked twice here, and that is the point: the first ask went
	// with the caller who cancelled it, so the one still waiting starts a fresh
	// one rather than being handed a cancellation it never asked for.
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the site served %d requests, want 2", served)
	}
}

// A stalled site produces a timeout that unwraps to context.DeadlineExceeded,
// which is exactly what a caller's own expired deadline produces. Reading
// abandonment from the error value therefore treats an unwell site as a caller
// walking away, and hands the work to each waiter in turn — multiplying load on
// the site precisely when it is least able to take it.
func TestCoalesce_DoesNotRestartACallThatFailedOnItsOwnMerits(t *testing.T) {
	t.Parallel()

	c, _ := testClient(t, "example.atlassian.net")
	var calls atomic.Int64
	_, err := c.coalesce(t.Context(), "GET /rest/api/3/field", func(context.Context) (*response, error) {
		calls.Add(1)
		// What a stalled site really produces: net/http's header timeout unwraps
		// to context.DeadlineExceeded, the same sentinel a caller's own expired
		// deadline yields.
		return nil, &jira.TransportError{Op: "GET /rest/api/3/field", Err: context.DeadlineExceeded}
	})

	if err == nil {
		t.Fatal("a stalled site answered successfully")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("the call ran %d times, want 1: the site failed, nobody abandoned anything", got)
	}
}

// The other half: a caller that really does leave hands its call to whoever is
// still waiting, rather than passing on a cancellation they never asked for.
func TestCoalesce_RestartsACallItsOwnCallerAbandoned(t *testing.T) {
	t.Parallel()

	c, _ := testClient(t, "example.atlassian.net")
	ctx, cancel := context.WithCancel(t.Context())
	var calls atomic.Int64
	_, err := c.coalesce(ctx, "GET /rest/api/3/field", func(inner context.Context) (*response, error) {
		if calls.Add(1) == 1 {
			cancel()
			return nil, inner.Err()
		}
		return nil, errors.New("second attempt")
	})

	if got := calls.Load(); got != 1 {
		t.Errorf("the call ran %d times; this caller cancelled itself and should get its own error back", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want the caller's own context error", err)
	}
}

// stubDoer answers every request the same way, without a server.
type stubDoer struct {
	err   error
	calls atomic.Int64
}

func (d *stubDoer) Do(*http.Request) (*http.Response, error) {
	d.calls.Add(1)
	return nil, d.err
}

func TestWithHTTPClient_SendsThroughTheCallersClientAndMapsItsTimeout(t *testing.T) {
	t.Parallel()

	doer := &stubDoer{err: &url.Error{
		Op:  "Get",
		URL: "https://example.atlassian.net/rest/api/3/field",
		Err: os.ErrDeadlineExceeded,
	}}
	c, _ := testClient(t, "example.atlassian.net", WithHTTPClient(doer), WithRetry(RetryPolicy{Attempts: 2}))
	_, err := c.do(t.Context(), fieldRequest())

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError: a timeout of the client's own is not the caller cancelling", err, err)
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Errorf("the cause did not survive being wrapped: %v", err)
	}
	if got := doer.calls.Load(); got != 2 {
		t.Errorf("the client sent %d requests, want the 2 attempts the policy allows", got)
	}
}

// A status line is one line wide, and the sentence a site that is not listening
// produces has to have said why by the end of it.
func TestFailure_NamesTheReasonWithoutRepeatingTheURLTheOpAlreadyCarries(t *testing.T) {
	t.Parallel()

	const why = "dial tcp 127.0.0.1:62630: connect: connection refused"
	doer := &stubDoer{err: &url.Error{
		Op:  "Post",
		URL: "http://127.0.0.1:62630/rest/api/3/search/jql?fields=summary%2Cstatus",
		Err: errors.New(why),
	}}
	c, _ := testClient(t, "127.0.0.1:62630", WithHTTPClient(doer), WithRetry(RetryPolicy{Attempts: 1}))

	_, err := c.do(t.Context(), request{method: http.MethodPost, path: "/rest/api/3/search/jql", repeatable: true})
	if err == nil {
		t.Fatal("a site that is not listening produced no error")
	}
	said := err.Error()

	if !strings.Contains(said, why) {
		t.Errorf("the sentence does not say why: %q", said)
	}
	if !strings.Contains(said, "POST /rest/api/3/search/jql") {
		t.Errorf("the sentence no longer names the endpoint, which a bug report needs: %q", said)
	}
	if strings.Contains(said, "http://127.0.0.1:62630/rest") {
		t.Errorf("the sentence repeats the URL the endpoint already names: %q", said)
	}
	// 100 columns is a narrow terminal, not a small one, and the reason has to
	// have arrived by then.
	if cut := 100; len(said) > cut || !strings.Contains(said[:min(len(said), cut)], "connection refused") {
		t.Errorf("the reason is %d characters in, past where a status line ends: %q", strings.Index(said, why), said)
	}
}

func TestDecode_LeavesTheTargetAloneWhenTheAnswerHasNoBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp response
	}{
		{name: "a no-content answer", resp: response{status: http.StatusNoContent, body: []byte(`{"key":"EX-1"}`)}},
		{name: "an empty body", resp: response{status: http.StatusOK, body: []byte("  \n")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := struct {
				Key string `json:"key"`
			}{}
			if err := tt.resp.decode("DELETE /rest/api/3/issue/EX-1", &out); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if out.Key != "" {
				t.Errorf("decoded %q out of a body-less answer", out.Key)
			}
		})
	}
}

func TestEncodeBody_MarshalsOnceAndPassesRawBytesThrough(t *testing.T) {
	t.Parallel()

	encoded, contentType, err := encodeBody(request{method: http.MethodPost, path: "/x", body: map[string]int{"maxResults": 50}})
	if err != nil {
		t.Fatalf("encoding a value: %v", err)
	}
	if string(encoded) != `{"maxResults":50}` || contentType != "application/json" {
		t.Errorf("got %s as %q, want the JSON encoding", encoded, contentType)
	}

	raw := []byte("--boundary\r\n")
	encoded, contentType, err = encodeBody(request{method: http.MethodPost, path: "/x", body: raw})
	if err != nil {
		t.Fatalf("encoding raw bytes: %v", err)
	}
	if !bytes.Equal(encoded, raw) || contentType != "" {
		t.Errorf("got %s as %q, want the bytes as they stand and no content type of ours", encoded, contentType)
	}

	if _, _, err = encodeBody(request{method: http.MethodPost, path: "/x", body: make(chan int)}); err == nil {
		t.Error("a body that cannot be marshalled encoded without complaint")
	}
}

func TestParseFieldErrors_SurvivesAnErrorsKeyThatIsNotAnObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "the Agile API's empty array", body: `{"errorMessages":["nope"],"errors":[]}`, want: 0},
		{name: "no errors key at all", body: `{"errorMessages":["nope"]}`, want: 0},
		{name: "a body that is not JSON", body: `<html>`, want: 0},
		{name: "an entry that is not a string", body: `{"errors":{"labels":["too many"]}}`, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := len(parseErrorBody([]byte(tt.body)).fields); got != tt.want {
				t.Errorf("got %d field messages out of %s, want %d", got, tt.body, tt.want)
			}
		})
	}
}

func TestParseTime_ReadsBothOfJirasLayoutsAndNeitherMistakesTheOther(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		want     time.Time
		platform bool
		agile    bool
	}{
		{
			name:     "the platform API's offset with no colon",
			in:       "2021-01-17T12:34:00.000+0000",
			want:     time.Date(2021, time.January, 17, 12, 34, 0, 0, time.UTC),
			platform: true,
		},
		{
			name:  "the Agile API's offset with a colon",
			in:    "2015-04-11T15:22:00.000+10:00",
			want:  time.Date(2015, time.April, 11, 15, 22, 0, 0, time.FixedZone("", 10*60*60)),
			agile: true,
		},
		{
			name:     "a platform date-time east of Greenwich",
			in:       "2026-02-11T09:41:22.104+0100",
			want:     time.Date(2026, time.February, 11, 9, 41, 22, 104_000_000, time.FixedZone("", 60*60)),
			platform: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseTime(tt.in)
			if err != nil {
				t.Fatalf("parseTime(%q): %v", tt.in, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("parseTime(%q) = %s, want %s", tt.in, got, tt.want)
			}

			gotPlatform, errPlatform := parsePlatformTime(tt.in)
			if tt.platform {
				if errPlatform != nil || !gotPlatform.Equal(tt.want) {
					t.Errorf("parsePlatformTime(%q) = %s, %v", tt.in, gotPlatform, errPlatform)
				}
			} else if errPlatform == nil {
				t.Errorf("parsePlatformTime read %q, which is the other API's layout", tt.in)
			}

			gotAgile, errAgile := parseAgileTime(tt.in)
			if tt.agile {
				if errAgile != nil || !gotAgile.Equal(tt.want) {
					t.Errorf("parseAgileTime(%q) = %s, %v", tt.in, gotAgile, errAgile)
				}
			} else if errAgile == nil {
				t.Errorf("parseAgileTime read %q, which is the other API's layout", tt.in)
			}
		})
	}
}

func TestTimestamp_ReadsAnUnsetDateAsUnsetAndRefusesNonsense(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		want    time.Time
		wantErr bool
	}{
		{name: "a platform date-time", json: `"2021-01-17T12:34:00.000+0000"`, want: time.Date(2021, time.January, 17, 12, 34, 0, 0, time.UTC)},
		{name: "an Agile date-time", json: `"2015-04-11T15:22:00.000+10:00"`, want: time.Date(2015, time.April, 11, 15, 22, 0, 0, time.FixedZone("", 10*60*60))},
		{name: "an RFC 3339 instant", json: `"2026-03-02T09:00:00Z"`, want: testNow},
		{name: "an absent date", json: `null`},
		{name: "an empty string", json: `""`},
		{name: "a date-time in nobody's layout", json: `"17/01/2021"`, wantErr: true},
		{name: "a number where a date-time belongs", json: `1737117240000`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got timestamp
			err := got.UnmarshalJSON([]byte(tt.json))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("decoding %s came back with %s and no complaint", tt.json, got.Time)
				}
				return
			}
			if err != nil {
				t.Fatalf("decoding %s: %v", tt.json, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("decoded %s as %s, want %s", tt.json, got.Time, tt.want)
			}
			if tt.want.IsZero() && got.ptr() != nil {
				t.Errorf("an unset date came back as a pointer to %s", got.Time)
			}
			if !tt.want.IsZero() && (got.ptr() == nil || !got.ptr().Equal(tt.want)) {
				t.Errorf("ptr() lost the instant %s", tt.want)
			}
		})
	}
}

func TestEpochMillis_ReadsTheTaskEndpointsOwnTimeFormat(t *testing.T) {
	t.Parallel()

	var got epochMillis
	if err := got.UnmarshalJSON([]byte("1772442000000")); err != nil {
		t.Fatalf("decoding an epoch time: %v", err)
	}
	if !got.Equal(testNow) {
		t.Errorf("decoded %s, want %s", got.Time, testNow)
	}

	var absent epochMillis
	if err := absent.UnmarshalJSON([]byte("null")); err != nil {
		t.Fatalf("decoding an absent epoch time: %v", err)
	}
	if !absent.IsZero() {
		t.Errorf("an absent epoch time decoded as %s", absent.Time)
	}
	if err := absent.UnmarshalJSON([]byte(`"yesterday"`)); err == nil {
		t.Error("a string decoded as an epoch time without complaint")
	}
}

func TestCoalesce_DoesNotPutTheRequestSignatureInTheErrorItReturns(t *testing.T) {
	t.Parallel()

	// The signature is method, path, query and body — for a search that is the
	// JQL and the page token, and a page token embeds the query it came from.
	// An error is the one value certain to be logged.
	const secret = `{"jql":"project = EX","nextPageToken":"CAEaAggDIhBhZ2VudC1wYWdl"}`
	c, _ := testClient(t, "example.atlassian.net")

	ctx, cancel := context.WithCancel(t.Context())
	_, err := c.coalesce(ctx, "POST /rest/api/3/search/jql\x00"+secret, func(inner context.Context) (*response, error) {
		cancel()
		return nil, inner.Err()
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "nextPageToken") || strings.Contains(err.Error(), "project = EX") {
		t.Errorf("the error quotes the request signature: %v", err)
	}
}
