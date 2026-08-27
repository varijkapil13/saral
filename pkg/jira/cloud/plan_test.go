package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func planClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

// planReachable is the override every test that wants the answered case applies:
// the fixture server refuses this route by default, because a refusal is what an
// ordinary token gets.
func planReachable() jiratest.ServerOption {
	return jiratest.WithFixture(http.MethodGet, planPath, "plans_ok.json")
}

func planAnswering(body string) jiratest.ServerOption {
	return jiratest.WithHandler(http.MethodGet, planPath, jsonHandler(http.StatusOK, body))
}

// planPagesInTurn answers one body per request, holding the last one once the
// bodies run out, so a walk reads them the way a site hands them over.
func planPagesInTurn(bodies ...string) jiratest.ServerOption {
	var mu sync.Mutex
	var served int
	return jiratest.WithHandler(http.MethodGet, planPath, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		body := bodies[min(served, len(bodies)-1)]
		served++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

// planThenFailure answers the first request with a page that carries a cursor
// and every request after it with a failure, which is a token whose permission
// is revoked, or a site that starts refusing, part way through a walk.
func planThenFailure(t *testing.T, status int, fixture string) jiratest.ServerOption {
	t.Helper()

	failure, err := jiratest.Fixture(fixture)
	if err != nil {
		t.Fatalf("reading the body the site fails with: %v", err)
	}
	var mu sync.Mutex
	var served int
	return jiratest.WithHandler(http.MethodGet, planPath, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		first := served == 0
		served++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if first {
			_, _ = w.Write([]byte(planPageOne))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(failure)
	})
}

// planEmptyPagesThenAPlan answers with pages carrying nothing and a fresh cursor
// each time, then a page with a plan on it. The plan at the end is what turns a
// walk that follows an empty page into a wrong count rather than a hang.
func planEmptyPagesThenAPlan(empties int) jiratest.ServerOption {
	var mu sync.Mutex
	var served int
	return jiratest.WithHandler(http.MethodGet, planPath, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		page := served
		served++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if page >= empties {
			_, _ = w.Write([]byte(planPageTwo))
			return
		}
		_, _ = w.Write([]byte(`{"last": false, "nextPageCursor": "page-` + strconv.Itoa(page+1) + `", "values": []}`))
	})
}

// The plan list, spelled the way Atlassian's schema spells it: last rather than
// isLast, and a cursor to follow rather than an offset to add.
const (
	planPageOne = `{
  "cursor": "",
  "last": false,
  "nextPageCursor": "second-page",
  "size": 1,
  "total": 2,
  "values": [
    {"id": "7", "name": "EX quarterly plan", "scenarioId": "108", "status": "Active",
     "issueSources": [{"type": "Project", "value": 10000}]}
  ]
}`

	planPageTwo = `{
  "cursor": "second-page",
  "last": true,
  "nextPageCursor": "",
  "size": 1,
  "total": 2,
  "values": [
    {"id": "12", "name": "EX platform plan", "scenarioId": "117", "status": "Archived",
     "issueSources": [{"type": "Filter", "value": 10200}]}
  ]
}`

	// planEndSpeltIsLast is the committed fixture's spelling of the end flag with
	// a cursor still on the page — a shape no site has been seen to send, and the
	// only one that tells the two spellings apart.
	planEndSpeltIsLast = `{
  "maxResults": 50,
  "total": 1,
  "isLast": true,
  "nextPageCursor": "loop-back",
  "values": [
    {"id": "7", "name": "EX quarterly plan", "status": "Active", "issueSources": []}
  ]
}`

	// planLoop is a page that hands back the cursor that fetched it, which is
	// the shape a walk with no guard follows forever.
	planLoop = `{
  "last": false,
  "nextPageCursor": "same-page",
  "values": [
    {"id": "7", "name": "EX quarterly plan", "status": "Active", "issueSources": []}
  ]
}`

	// planShapesThatDisagree is one plan carrying everything about this endpoint
	// that disagrees with itself or with the port: an id sent as a number, a
	// source type the port has no constant for, and values that are ids.
	planShapesThatDisagree = `{
  "last": true,
  "values": [
    {"id": 7, "name": "EX quarterly plan", "status": "Active",
     "issueSources": [{"type": "Custom", "value": 42}, {"type": "BOARD", "value": 10}]}
  ]
}`
)

func TestPlans_ReadsEveryPlanTheSiteListsWithItsIssueSources(t *testing.T) {
	t.Parallel()

	c, s := planClient(t, planReachable())

	got, err := c.Plans(t.Context())
	if err != nil {
		t.Fatalf("reading the site's plans: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("read %d plans, want the 3 the site listed: %+v", len(got), got)
	}

	ids := make([]string, 0, len(got))
	for _, plan := range got {
		ids = append(ids, plan.ID)
	}
	if want := []string{"7", "12", "31"}; !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v in the order the site listed them", ids, want)
	}
	if got[0].Name != "EX Rollout Plan" || got[0].Status != "Active" {
		t.Errorf("the first plan is %q/%q, want the site's own name and status", got[0].Name, got[0].Status)
	}
	// A plan the site says is thrown away is still a plan, and Active, Trashed
	// and Archived are the whole enum: dropping the row would hide it.
	if got[2].Status != "Trashed" {
		t.Errorf("the retired plan's status = %q, want the site's own word for it", got[2].Status)
	}

	project := []jira.PlanSource{{Type: jira.PlanSourceProject, Value: "10000"}}
	if !slices.Equal(got[0].Sources, project) {
		t.Errorf("sources = %+v, want %+v: the value is a project id, and the type is the port's own word for it", got[0].Sources, project)
	}
	mixed := []jira.PlanSource{
		{Type: jira.PlanSourceBoard, Value: "10"},
		{Type: jira.PlanSourceBoard, Value: "11"},
		{Type: jira.PlanSourceFilter, Value: "10200"},
	}
	if !slices.Equal(got[1].Sources, mixed) {
		t.Errorf("sources = %+v, want %+v", got[1].Sources, mixed)
	}

	for _, plan := range got {
		if plan.Local {
			t.Errorf("plan %s came back Local, and a plan read from Jira is not one this client made up", plan.ID)
		}
	}

	query := planQueryOn(t, s)
	if query.Get("maxResults") != "50" {
		t.Errorf("maxResults = %q, want the endpoint's documented maximum of 50 asked for rather than left to the site",
			query.Get("maxResults"))
	}
	if query.Has("cursor") {
		t.Errorf("the first page was asked for with cursor=%q; there is no cursor to start from", query.Get("cursor"))
	}
	// Neither flag is sent. The schema documents no default for either, so what
	// a site lists is the site's own business and a status is read, not assumed.
	for _, key := range []string{"includeTrashed", "includeArchived"} {
		if query.Has(key) {
			t.Errorf("%s = %q was sent; nothing here asks a site to change what it lists", key, query.Get(key))
		}
	}
}

func TestPlans_RefusesWithTheCapabilityThePlanViewFallsBackOn(t *testing.T) {
	t.Parallel()

	// No override: the fixture server refuses this route by default, because
	// every Plans endpoint wants Administer Jira and per-plan rights in the web
	// UI do not grant it.
	c, _ := planClient(t)

	got, err := c.Plans(t.Context())
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
	if refused.Capability != jira.CapPlans {
		t.Errorf("Capability = %q, want %q so the view can tell this refusal from every other failure",
			refused.Capability, jira.CapPlans)
	}
	// The site knows whether it was the permission or the subscription; this
	// client does not, so the sentence shown beside the local plans is the
	// site's own.
	if !strings.Contains(refused.Reason, "Administer Jira") {
		t.Errorf("Reason = %q, want the site's own words about what it refused", refused.Reason)
	}
	if len(got) != 0 {
		t.Errorf("the refusal came back with %+v attached, and a view drawing it would show plans nobody can open", got)
	}
}

func TestPlans_FollowsTheCursorToTheEndOfTheList(t *testing.T) {
	t.Parallel()

	c, s := planClient(t, planPagesInTurn(planPageOne, planPageTwo))

	got, err := c.Plans(t.Context())
	if err != nil {
		t.Fatalf("walking the plan list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d plans, want both pages: %+v", len(got), got)
	}
	if got[0].ID != "7" || got[1].ID != "12" {
		t.Errorf("read %s then %s, want the pages in the order the site handed them over", got[0].ID, got[1].ID)
	}

	served := s.Requests()
	if len(served) != 2 {
		t.Fatalf("the site served %d requests, want one per page: %+v", len(served), served)
	}
	first, err := url.ParseQuery(served[0].Query)
	if err != nil {
		t.Fatalf("reading the first request's query %q: %v", served[0].Query, err)
	}
	if first.Has("cursor") {
		t.Errorf("the first page was asked for with cursor=%q", first.Get("cursor"))
	}
	second, err := url.ParseQuery(served[1].Query)
	if err != nil {
		t.Fatalf("reading the second request's query %q: %v", served[1].Query, err)
	}
	if second.Get("cursor") != "second-page" {
		t.Errorf("cursor = %q, want the one the first page handed back", second.Get("cursor"))
	}
	if second.Get("maxResults") != "50" {
		t.Errorf("maxResults = %q on the second page, want 50 on every page", second.Get("maxResults"))
	}
}

func TestPlans_StopsOnTheEndFlagUnderEitherSpelling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "last, which is what the schema calls it", body: `{"last": true, "nextPageCursor": "loop-back",
  "values": [{"id": "7", "name": "EX quarterly plan", "status": "Active", "issueSources": []}]}`},
		{name: "isLast, which is what the fixtures call it", body: planEndSpeltIsLast},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := planClient(t, planAnswering(tt.body))

			got, err := c.Plans(t.Context())
			if err != nil {
				t.Fatalf("reading a single-page plan list: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("read %d plans, want the one the page carried: %+v", len(got), got)
			}
			if len(got[0].Sources) != 0 {
				t.Errorf("sources = %+v, want none: a plan with no issue source is an empty timeline, not an error", got[0].Sources)
			}
			if served := s.Requests(); len(served) != 1 {
				t.Errorf("the site served %d requests, want one: the page said it was the last and still carried a cursor",
					len(served))
			}
		})
	}
}

func TestPlans_StopsWhenTheCursorLoopsBackOnItself(t *testing.T) {
	t.Parallel()

	c, s := planClient(t, planAnswering(planLoop))

	got, err := c.Plans(t.Context())
	if err != nil {
		t.Fatalf("walking a plan list that never ends: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d plans, want the two pages read before the cursor repeated: %+v", len(got), got)
	}
	if served := s.Requests(); len(served) != 2 {
		t.Errorf("the site served %d requests, want 2: a cursor already followed is the end of the walk", len(served))
	}
}

func TestPlans_StopsOnAPageWithNothingOnItRatherThanFollowingItsCursor(t *testing.T) {
	t.Parallel()

	c, s := planClient(t, planEmptyPagesThenAPlan(3))

	got, err := c.Plans(t.Context())
	if err != nil {
		t.Fatalf("walking a plan list whose first page carried nothing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("read %+v, want none: the page the site answered with carried no plans", got)
	}
	if served := s.Requests(); len(served) != 1 {
		t.Errorf("the site served %d requests, want 1: the bound counts plans, so a site handing back empty pages and fresh cursors is a read with no end",
			len(served))
	}
}

func TestPlans_StopsAtItsOwnBoundWhenTheSiteKeepsHandingBackCursors(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var served int
	c, s := planClient(t, jiratest.WithHandler(http.MethodGet, planPath, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		page := served
		served++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(planFullPage(page)))
	}))

	got, err := c.Plans(t.Context())
	if err != nil {
		t.Fatalf("walking a plan list with no end: %v", err)
	}
	if len(got) != 500 {
		t.Errorf("read %d plans, want the bound of 500", len(got))
	}
	if len(s.Requests()) != 10 {
		t.Errorf("the site served %d requests, want 10 pages of 50: the walk stops at its bound rather than reading on",
			len(s.Requests()))
	}
}

func TestPlans_ReadsTheShapesThisEndpointDisagreesWithItselfAbout(t *testing.T) {
	t.Parallel()

	c, _ := planClient(t, planAnswering(planShapesThatDisagree))

	got, err := c.Plans(t.Context())
	if err != nil {
		t.Fatalf("reading a plan whose id arrived as a number: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read %d plans, want one: %+v", len(got), got)
	}
	if got[0].ID != "7" {
		t.Errorf("ID = %q, want %q: the list sends a string and the single-plan read sends a number", got[0].ID, "7")
	}
	want := []jira.PlanSource{
		{Type: jira.PlanSourceType("custom"), Value: "42"},
		{Type: jira.PlanSourceBoard, Value: "10"},
	}
	if !slices.Equal(got[0].Sources, want) {
		t.Errorf("sources = %+v, want %+v: a type this port has no constant for keeps the site's own word rather than costing the source",
			got[0].Sources, want)
	}
}

func TestPlans_ReportsEveryOtherWayTheCallFailsAsItself(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opt    jiratest.ServerOption
		assert func(*testing.T, error)
	}{
		{
			name: "a request the site would not accept",
			opt:  jiratest.WithStatus(http.MethodGet, planPath, http.StatusBadRequest, "validation_error.json"),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
				}
			},
		},
		{
			name: "credentials the site does not recognise",
			opt:  jiratest.WithStatus(http.MethodGet, planPath, http.StatusUnauthorized, ""),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var unauthenticated *jira.AuthError
				if !errors.As(err, &unauthenticated) {
					t.Fatalf("got %T (%v), want a *jira.AuthError", err, err)
				}
			},
		},
		{
			name: "a site with no Plans API at all",
			opt:  jiratest.WithStatus(http.MethodGet, planPath, http.StatusNotFound, "problem_no_endpoint.json"),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError: an absent Plans API is not a refused one", err, err)
				}
				if missing.Kind != "the site's plans" || missing.ID != planPath {
					t.Errorf("the absence names %q %q, want the plan list it asked for", missing.Kind, missing.ID)
				}
			},
		},
		{
			name: "a site that is rate limiting",
			opt:  jiratest.WithRateLimit(http.MethodGet, planPath, 30*time.Second),
			assert: func(t *testing.T, err error) {
				t.Helper()
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
				if limited.RetryAfter != 30*time.Second {
					t.Errorf("RetryAfter = %s, want the 30s the site asked for", limited.RetryAfter)
				}
				if limited.Endpoint != planPath {
					t.Errorf("Endpoint = %q, want %q: a budget is per endpoint", limited.Endpoint, planPath)
				}
			},
		},
		{
			name: "a site that broke",
			opt:  jiratest.WithStatus(http.MethodGet, planPath, http.StatusInternalServerError, ""),
			assert: func(t *testing.T, err error) {
				t.Helper()
				planWantTransport(t, err, http.StatusInternalServerError)
			},
		},
		{
			name: "an answer that is not JSON",
			opt:  planAnswering("<html>a proxy answered instead</html>"),
			assert: func(t *testing.T, err error) {
				t.Helper()
				planWantTransport(t, err, http.StatusOK)
			},
		},
		{
			name: "an answer that is the wrong JSON",
			opt:  planAnswering(`["not an envelope"]`),
			assert: func(t *testing.T, err error) {
				t.Helper()
				planWantTransport(t, err, http.StatusOK)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := planClient(t, tt.opt)

			got, err := c.Plans(t.Context())
			if len(got) != 0 {
				t.Errorf("the failure came back with %+v attached", got)
			}
			tt.assert(t, err)

			// Only a 403 is a capability answer. Everything else has to stay
			// what it was, or the view draws local plans and blames a
			// permission for a broken proxy.
			var refused *jira.CapabilityError
			if errors.As(err, &refused) {
				t.Errorf("got a *jira.CapabilityError (%v) for something that was not a refusal", refused)
			}
		})
	}
}

func TestPlans_ReportsAFailureThatArrivesPartWayThroughTheWalk(t *testing.T) {
	t.Parallel()

	t.Run("a refusal on the second page is still the refusal the view falls back on", func(t *testing.T) {
		t.Parallel()

		c, s := planClient(t, planThenFailure(t, http.StatusForbidden, "plans_403.json"))

		got, err := c.Plans(t.Context())
		var refused *jira.CapabilityError
		if !errors.As(err, &refused) {
			t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
		}
		if refused.Capability != jira.CapPlans {
			t.Errorf("Capability = %q, want %q: a permission revoked mid-walk is the same refusal as one on the first page",
				refused.Capability, jira.CapPlans)
		}
		if !strings.Contains(refused.Reason, "Administer Jira") {
			t.Errorf("Reason = %q, want the site's own words about what it refused", refused.Reason)
		}
		if len(got) != 0 {
			t.Errorf("the refusal came back with %+v attached, and half a list drawn beside the local plans is two answers to one question", got)
		}
		if served := s.Requests(); len(served) != 2 {
			t.Errorf("the site served %d requests, want 2: the first page, then the one it refused", len(served))
		}
	})

	t.Run("a site that breaks on the second page stays broken rather than refused", func(t *testing.T) {
		t.Parallel()

		c, _ := planClient(t, planThenFailure(t, http.StatusInternalServerError, "problem_no_endpoint.json"))

		got, err := c.Plans(t.Context())
		planWantTransport(t, err, http.StatusInternalServerError)
		if len(got) != 0 {
			t.Errorf("the failure came back with %+v attached", got)
		}
		var refused *jira.CapabilityError
		if errors.As(err, &refused) {
			t.Errorf("got a *jira.CapabilityError (%v) for a site that broke half way through a walk", refused)
		}
	})
}

func TestPlans_ReportsAHostThatNeverAnsweredAsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	dead := s.URL()
	s.Close()
	c, _ := testClient(t, dead, WithRetry(RetryPolicy{Attempts: 1}))

	got, err := c.Plans(t.Context())
	if len(got) != 0 {
		t.Errorf("a dead host came back with %+v attached", got)
	}
	planWantTransport(t, err, 0)
}

func TestPlans_NeverReachesTheSiteWhenTheCallerHasAlreadyGone(t *testing.T) {
	t.Parallel()

	c, s := planClient(t, planReachable())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := c.Plans(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled unwrapped", err)
	}
	if served := s.Requests(); len(served) != 0 {
		t.Errorf("the site was sent %d requests by a caller that had already gone: %+v", len(served), served)
	}
}

func TestPlans_ComesBackWhenTheCallerGoesMidFlight(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, planPath,
		func(_ http.ResponseWriter, r *http.Request) {
			announce()
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
	defer closeServer(t, s)
	defer letGo()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	go func() {
		_, err := c.Plans(ctx)
		failed <- err
	}()

	receive(t, "the plan read to reach the site", arrived)
	cancel()
	if err := receive(t, "the cancelled plan read to come back", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled unwrapped", err)
	}
}

// planFullPage is a page of the endpoint's own maximum with a fresh cursor after
// it, which is a site that never says it has finished.
func planFullPage(page int) string {
	var body strings.Builder
	body.WriteString(`{"last": false, "nextPageCursor": "page-`)
	body.WriteString(strconv.Itoa(page + 1))
	body.WriteString(`", "values": [`)
	for i := range planPageSize {
		if i > 0 {
			body.WriteString(",")
		}
		id := strconv.Itoa(page*planPageSize + i + 1)
		body.WriteString(`{"id": "` + id + `", "name": "EX plan ` + id + `", "status": "Active", "issueSources": []}`)
	}
	body.WriteString(`]}`)
	return body.String()
}

func planQueryOn(t *testing.T, s *jiratest.Server) url.Values {
	t.Helper()

	req := sentTo(t, s, http.MethodGet, planPath)
	query, err := url.ParseQuery(req.Query)
	if err != nil {
		t.Fatalf("reading the query %q: %v", req.Query, err)
	}
	return query
}

func planWantTransport(t *testing.T, err error, status int) {
	t.Helper()

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if broken.Status != status {
		t.Errorf("Status = %d, want %d", broken.Status, status)
	}
}
