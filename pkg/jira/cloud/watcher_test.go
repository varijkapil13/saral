package cloud

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	watchersRoute    = "/rest/api/3/issue/{key}/watchers"
	testWatchersPath = "/rest/api/3/issue/" + testIssueKey + "/watchers"
)

func watcherClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

// watcherCall is one of the three methods this file covers, so the
// failure-path tables can drive all three with one loop.
type watcherCall struct {
	name   string
	method string
	run    func(ctx context.Context, c *Client) error
}

func watcherCalls() []watcherCall {
	return []watcherCall{
		{
			name: "Watchers", method: http.MethodGet,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Watchers(ctx, testIssueKey)
				return err
			},
		},
		{
			name: "Watch", method: http.MethodPost,
			run: func(ctx context.Context, c *Client) error {
				return c.Watch(ctx, testIssueKey, "5b10a2844c20165700ede21g")
			},
		},
		{
			name: "Unwatch", method: http.MethodDelete,
			run: func(ctx context.Context, c *Client) error {
				return c.Unwatch(ctx, testIssueKey, "5b10a2844c20165700ede21g")
			},
		},
	}
}

func TestWatchers_DecodesCountWatchingAndPeople(t *testing.T) {
	t.Parallel()

	c, _ := watcherClient(t)
	got, err := c.Watchers(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("reading who watches %s: %v", testIssueKey, err)
	}
	if got.Count != 2 {
		t.Errorf("got a count of %d, want the 2 the fixture holds", got.Count)
	}
	if !got.Watching {
		t.Error("the fixture says the authenticated account is watching, and it came back false")
	}
	if len(got.People) != 2 {
		t.Fatalf("got %d named watchers, want 2: %+v", len(got.People), got.People)
	}
	if got.People[0].AccountID != "5b10a2844c20165700ede21g" || got.People[0].DisplayName != "Example User" {
		t.Errorf("the first watcher is %+v", got.People[0])
	}
	if got.People[1].AccountID != "5b10ac8d82e05b22cc7d4ef5" {
		t.Errorf("the second watcher is %+v", got.People[1])
	}
}

func TestWatchers_AHiddenListIsCountWithNoPeople(t *testing.T) {
	t.Parallel()

	c, _ := watcherClient(t, jiratest.WithFixture(http.MethodGet, watchersRoute, "watchers_hidden.json"))
	got, err := c.Watchers(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("reading a hidden watcher list: %v", err)
	}
	if got.Count != 3 {
		t.Errorf("got a count of %d, want the 3 the fixture claims", got.Count)
	}
	if got.Watching {
		t.Error("the fixture says the authenticated account is not watching, and it came back true")
	}
	if len(got.People) != 0 {
		t.Errorf("a token without the permission to view watchers named %+v", got.People)
	}
}

func TestWatch_SendsTheAccountIDAsABareJSONString(t *testing.T) {
	t.Parallel()

	c, s := watcherClient(t)
	if err := c.Watch(t.Context(), testIssueKey, "5b10a2844c20165700ede21g"); err != nil {
		t.Fatalf("watching as another account: %v", err)
	}

	sent := sentTo(t, s, http.MethodPost, testWatchersPath)
	if got := strings.TrimSpace(sent.Body); got != `"5b10a2844c20165700ede21g"` {
		t.Errorf("the body sent is %q, want the account id as a bare JSON string", got)
	}
	if ct := sent.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type is %q, want application/json", ct)
	}
}

func TestWatch_WatchingSelfSendsNoBodyAtAll(t *testing.T) {
	t.Parallel()

	c, s := watcherClient(t)
	if err := c.Watch(t.Context(), testIssueKey, ""); err != nil {
		t.Fatalf("watching as self: %v", err)
	}

	sent := sentTo(t, s, http.MethodPost, testWatchersPath)
	if sent.Body != "" {
		t.Errorf("watching self sent a body of %q, want none at all", sent.Body)
	}
	if ct := sent.Header.Get("Content-Type"); ct != "" {
		t.Errorf("watching self sent Content-Type %q for a request with no body", ct)
	}
}

func TestUnwatch_SendsTheAccountInTheQuery(t *testing.T) {
	t.Parallel()

	c, s := watcherClient(t)
	if err := c.Unwatch(t.Context(), testIssueKey, "5b10ac8d82e05b22cc7d4ef5"); err != nil {
		t.Fatalf("removing a watcher: %v", err)
	}

	sent := sentTo(t, s, http.MethodDelete, testWatchersPath)
	if sent.Query != "accountId=5b10ac8d82e05b22cc7d4ef5" {
		t.Errorf("the query sent is %q, want accountId in it", sent.Query)
	}
}

func TestUnwatch_RefusesAnEmptyAccountWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	c, s := watcherClient(t)
	err := c.Unwatch(t.Context(), testIssueKey, "  ")
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, ok := invalid.For("accountId"); !ok {
		t.Errorf("the failure does not name accountId: %v", invalid)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests for an unwatch with no account", served)
	}
}

func TestWatcher_RefusalBecomesTheSentenceTheUserReads(t *testing.T) {
	t.Parallel()

	for _, tc := range watcherCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := `{"errorMessages":["You do not have permission to manage watchers."],"errors":{}}`
			c, _ := watcherClient(t, jiratest.WithHandler(tc.method, watchersRoute, jsonHandler(http.StatusForbidden, body)))

			err := tc.run(t.Context(), c)
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			if !strings.Contains(refused.Error(), "permission to manage watchers") {
				t.Errorf("the reason lost the site's own wording: %q", refused.Error())
			}
		})
	}
}

func TestWatcher_ANotFoundIssueIsA404(t *testing.T) {
	t.Parallel()

	for _, tc := range watcherCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := watcherClient(t, jiratest.WithStatus(tc.method, watchersRoute, http.StatusNotFound, ""))

			err := tc.run(t.Context(), c)
			var missing *jira.NotFoundError
			if !errors.As(err, &missing) {
				t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
			}
		})
	}
}

func TestWatcher_RateLimitCarriesTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	for _, tc := range watcherCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := watcherClient(t, jiratest.WithRateLimit(tc.method, watchersRoute, 30*time.Second))

			err := tc.run(t.Context(), c)
			var limited *jira.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
			}
			if limited.RetryAfter != 30*time.Second {
				t.Errorf("got a wait of %s, want the 30s the header asked for", limited.RetryAfter)
			}
		})
	}
}

func TestWatcher_TransportFailureIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range watcherCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := watcherClient(t, jiratest.WithHandler(tc.method, watchersRoute,
				jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))

			err := tc.run(t.Context(), c)
			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if down.Status != http.StatusBadGateway {
				t.Errorf("the failure reports HTTP %d", down.Status)
			}
		})
	}
}

func TestWatcher_AHostThatNeverAnswersIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range watcherCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dead := jiratest.NewServer()
			site := dead.URL()
			dead.Close()
			c, _ := testClient(t, site, WithRetry(RetryPolicy{Attempts: 1}))

			err := tc.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != 0 {
				t.Errorf("a host that never answered reports HTTP %d", broken.Status)
			}
		})
	}
}

func TestWatchers_ABodyThisClientCannotReadIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "JSON that stops half way", body: `{"watchCount":2,"watchers":[`},
		{name: "an envelope that is an array", body: `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := watcherClient(t, jiratest.WithHandler(http.MethodGet, watchersRoute, jsonHandler(http.StatusOK, tc.body)))

			_, err := c.Watchers(t.Context(), testIssueKey)
			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
		})
	}
}

func TestWatcher_ReturnsTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	for _, tc := range watcherCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := watcherClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := tc.run(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests after the caller had already gone", served)
			}
		})
	}
}
