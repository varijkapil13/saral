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

// peopleColonID is the account id in the fixtures that carries a colon. Two of
// the eleven accounts on the site this was measured against did, so it is not a
// curiosity: it goes into a query parameter, and it is what a caller keys a
// cache or builds a JQL clause with.
const peopleColonID = "140022:example-app-0001"

func peopleNames(users []jira.User) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.DisplayName)
	}
	return out
}

func TestFindPeople_ReadsTheBareArrayTheEndpointAnswers(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.FindPeople(t.Context(), jira.PeopleQuery{Match: "exa"})
	if err != nil {
		t.Fatalf("searching for accounts: %v", err)
	}

	// No envelope, no total, no isLast: neither paginator in this package reads
	// this shape, which is why the search does not use one.
	if len(got) != 5 {
		t.Fatalf("got %d accounts (%v), want the five the site listed", len(got), peopleNames(got))
	}
	first := got[0]
	if first.AccountID == "" || first.DisplayName == "" || first.AvatarURL == "" {
		t.Errorf("the first account reads as %+v, want the id, name and avatar the site sent", first)
	}
	if first.TimeZone == nil {
		t.Error("the account came back with no timezone, and the site names one")
	}
}

func TestFindPeople_LabelsEachKindOfAccountWithoutHidingAny(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.FindPeople(t.Context(), jira.PeopleQuery{})
	if err != nil {
		t.Fatalf("searching for accounts: %v", err)
	}

	kinds := make(map[string]jira.AccountKind, len(got))
	for _, u := range got {
		kinds[u.DisplayName] = u.Kind
	}
	want := map[string]jira.AccountKind{
		"Example User":    jira.AccountPerson,
		"Second Example":  jira.AccountPerson,
		"Retired Example": jira.AccountPerson,
		"Nightly Runner":  jira.AccountApp,
		"Portal Visitor":  jira.AccountCustomer,
	}
	for name, kind := range want {
		if kinds[name] != kind {
			t.Errorf("%s reads as %v, want %v", name, kinds[name], kind)
		}
	}
	// An app account is assigned work and reports issues like anybody else, so
	// the kind is a label a picker sinks and badges by, never a filter here.
	if len(got) != len(want) {
		t.Errorf("got %d accounts (%v), want every one the site listed", len(got), peopleNames(got))
	}
}

func TestFindPeople_AsksTheAssignableEndpointWhenTheQueryNamesAProject(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.FindPeople(t.Context(), jira.PeopleQuery{Match: "exa", Project: "EX", Limit: 7})
	if err != nil {
		t.Fatalf("searching for accounts in EX: %v", err)
	}

	// The assignable endpoint answers only accounts that can be given work,
	// which drops the app and the customer without being asked for that.
	for _, u := range got {
		if u.Kind != jira.AccountPerson {
			t.Errorf("%s came back as %v from the assignable search", u.DisplayName, u.Kind)
		}
	}

	asked := peopleQueryOn(t, s, peopleAssignablePath)
	if asked.Get("project") != "EX" {
		t.Errorf("project = %q, want the one the query named", asked.Get("project"))
	}
	if asked.Get("query") != "exa" {
		t.Errorf("query = %q, want what the caller typed", asked.Get("query"))
	}
	if asked.Get("maxResults") != "7" {
		t.Errorf("maxResults = %q, want the caller's own ceiling", asked.Get("maxResults"))
	}
	if n := peopleCount(s, peopleSearchPath); n != 0 {
		t.Errorf("the site-wide search was called %d times as well", n)
	}
}

// query absent is a 400 and query empty matches everybody, so the parameter is
// always sent — including when the caller has typed nothing, which is the state
// a picker opens in.
func TestFindPeople_AlwaysSendsAQueryParameterEvenWhenItIsEmpty(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.FindPeople(t.Context(), jira.PeopleQuery{}); err != nil {
		t.Fatalf("searching with no needle: %v", err)
	}

	asked := peopleQueryOn(t, s, peopleSearchPath)
	if _, sent := asked["query"]; !sent {
		t.Fatalf("no query parameter was sent (%q), and its absence is a 400", asked.Encode())
	}
	if asked.Get("query") != "" {
		t.Errorf("query = %q, want the empty one", asked.Get("query"))
	}
	if asked.Get("maxResults") != "50" {
		t.Errorf("maxResults = %q, want the adapter's own ceiling for a caller that named none", asked.Get("maxResults"))
	}
}

func TestFindPeople_DropsAnEntryThatNamesNoAccount(t *testing.T) {
	t.Parallel()

	const body = `[{"accountId":"5b10a2844c20165700ede21g","displayName":"Example User","active":true},
		{"displayName":"","active":true}]`

	s := jiratest.NewServer(peopleAnswering(peopleSearchPath, body))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.FindPeople(t.Context(), jira.PeopleQuery{})
	if err != nil {
		t.Fatalf("searching for accounts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d accounts (%+v), want the one that names somebody", len(got), got)
	}
}

func TestPeople_WalksThePagesTheBulkReadDefaultsTo(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.People(t.Context(), []string{"5b10a2844c20165700ede21g", peopleColonID, "5b10ffffffffffffffffffff"})
	if err != nil {
		t.Fatalf("resolving account ids: %v", err)
	}

	// Three ids asked for, two accounts back: the third is the JSON null the
	// site answers for an id it does not know, and a blank row is worse than an
	// absence. The second page is only reached because the null was counted.
	if names := peopleNames(got); !slices.Equal(names, []string{"Example User", "Nightly Runner"}) {
		t.Fatalf("got %v, want the two accounts the site knows and no blank row", names)
	}
	for _, u := range got {
		if u.AccountID == "" || u.DisplayName == "" {
			t.Errorf("a blank row reached the caller: %+v", u)
		}
	}
	if n := peopleCount(s, peopleBulkPath); n != 2 {
		t.Errorf("the bulk endpoint was called %d times, want the two pages it answers in", n)
	}
}

func TestPeople_EscapesAnAccountIdThatCarriesAColon(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.People(t.Context(), []string{peopleColonID})
	if err != nil {
		t.Fatalf("resolving an account id with a colon in it: %v", err)
	}
	if !slices.ContainsFunc(got, func(u jira.User) bool { return u.AccountID == peopleColonID }) {
		t.Fatalf("got %+v, want the account whose id carries a colon, spelt as the site spells it", got)
	}

	raw := peopleRawQueryOn(t, s, peopleBulkPath)
	if !strings.Contains(raw, "accountId=140022%3Aexample-app-0001") {
		t.Errorf("the id went on the wire as %q, want the colon percent-encoded", raw)
	}
	// The site answers the escaped form with the raw one, so the two spellings
	// of one id must never be compared without decoding first.
	asked := peopleQueryOn(t, s, peopleBulkPath)
	if got := asked.Get("accountId"); got != peopleColonID {
		t.Errorf("the decoded id is %q, want %q", got, peopleColonID)
	}
}

func TestPeople_AsksForNothingWhenThereAreNoIdsToResolve(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	for name, ids := range map[string][]string{
		"no ids at all":          nil,
		"an empty list":          {},
		"nothing but blank ones": {"", "   "},
	} {
		got, err := c.People(t.Context(), ids)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 0 {
			t.Errorf("%s came back with %+v", name, got)
		}
	}
	// A bulk read with no accountId is a 400, so asking is worse than not.
	if n := len(s.Requests()); n != 0 {
		t.Errorf("the site was asked %d times for nobody", n)
	}
}

func TestPeople_AsksForEachIdOnceHoweverManyTimesItWasNamed(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	if _, err := c.People(t.Context(), []string{"a-1", " a-1 ", "a-2", "a-1"}); err != nil {
		t.Fatalf("resolving repeated ids: %v", err)
	}

	asked := peopleQueryOn(t, s, peopleBulkPath)
	if ids := asked["accountId"]; !slices.Equal(ids, []string{"a-1", "a-2"}) {
		t.Errorf("the request named %v, want each id once", ids)
	}
}

func TestPeople_ChunksALongListOfIdsAcrossRequests(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	ids := make([]string, peopleBulkChunk+1)
	for i := range ids {
		ids[i] = "acct-" + strconv.Itoa(i)
	}

	c, _ := testClient(t, s.URL())
	if _, err := c.People(t.Context(), ids); err != nil {
		t.Fatalf("resolving %d ids: %v", len(ids), err)
	}

	for _, req := range s.Requests() {
		asked, err := url.ParseQuery(req.Query)
		if err != nil {
			t.Fatalf("reading the bulk query: %v", err)
		}
		if n := len(asked["accountId"]); n > peopleBulkChunk {
			t.Errorf("one request carried %d ids, want at most %d", n, peopleBulkChunk)
		}
	}
	if n := peopleCount(s, peopleBulkPath); n < 2 {
		t.Errorf("%d ids went out in %d requests, want more than one chunk", len(ids), n)
	}
}

func TestPeople_ReportsARefusalRateLimitAndTransportFailureAsThemselves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		path   string
		call   func(context.Context, *Client) error
		opt    func(path string) jiratest.ServerOption
		assert func(*testing.T, error)
	}{
		{
			name: "a search this token may not run",
			path: peopleSearchPath,
			opt: func(path string) jiratest.ServerOption {
				return jiratest.WithStatus(http.MethodGet, path, http.StatusForbidden, "forbidden_browse_users.json")
			},
			assert: func(t *testing.T, err error) {
				t.Helper()
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
				if refused.Capability != jira.CapPeople {
					t.Errorf("Capability = %q, want %q", refused.Capability, jira.CapPeople)
				}
				// The site knows which permission scheme refused and this client
				// does not, so the sentence has to be the site's own.
				if !strings.Contains(refused.Reason, "Browse users and groups") {
					t.Errorf("Reason = %q, want the site's own words", refused.Reason)
				}
			},
		},
		{
			name: "a site that is rate limiting",
			path: peopleSearchPath,
			opt: func(path string) jiratest.ServerOption {
				return jiratest.WithRateLimit(http.MethodGet, path, 30*time.Second)
			},
			assert: func(t *testing.T, err error) {
				t.Helper()
				var limited *jira.RateLimitError
				if !errors.As(err, &limited) {
					t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
				}
				if limited.RetryAfter != 30*time.Second {
					t.Errorf("RetryAfter = %s, want the 30s the site asked for", limited.RetryAfter)
				}
			},
		},
		{
			name: "a site that broke",
			path: peopleSearchPath,
			opt: func(path string) jiratest.ServerOption {
				return jiratest.WithStatus(http.MethodGet, path, http.StatusInternalServerError, "")
			},
			assert: peopleWantTransport,
		},
		{
			name: "an answer that is not JSON",
			path: peopleSearchPath,
			opt: func(path string) jiratest.ServerOption {
				return peopleAnswering(path, "<html>a proxy answered instead</html>")
			},
			assert: peopleWantTransport,
		},
		{
			name: "an answer that is the wrong JSON",
			path: peopleSearchPath,
			opt: func(path string) jiratest.ServerOption {
				return peopleAnswering(path, `{"values":[]}`)
			},
			assert: peopleWantTransport,
		},
		{
			name: "a bulk read this token may not run",
			path: peopleBulkPath,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.People(ctx, []string{"5b10a2844c20165700ede21g"})
				return err
			},
			opt: func(path string) jiratest.ServerOption {
				return jiratest.WithStatus(http.MethodGet, path, http.StatusForbidden, "forbidden_browse_users.json")
			},
			assert: func(t *testing.T, err error) {
				t.Helper()
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
				if refused.Capability != jira.CapPeople {
					t.Errorf("Capability = %q, want %q", refused.Capability, jira.CapPeople)
				}
			},
		},
		{
			name: "a bulk read whose body is the wrong JSON",
			path: peopleBulkPath,
			call: func(ctx context.Context, c *Client) error {
				_, err := c.People(ctx, []string{"5b10a2844c20165700ede21g"})
				return err
			},
			opt: func(path string) jiratest.ServerOption {
				return peopleAnswering(path, `["not an envelope"]`)
			},
			assert: peopleWantTransport,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(tt.opt(tt.path))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			var err error
			if tt.call != nil {
				err = tt.call(t.Context(), c)
			} else {
				_, err = c.FindPeople(t.Context(), jira.PeopleQuery{})
			}
			if err == nil {
				t.Fatal("the failure came back as an answer")
			}
			tt.assert(t, err)
		})
	}
}

func TestFindPeople_HonoursACancelledContext(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	arrived := make(chan struct{})
	var once sync.Once

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, peopleSearchPath, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(arrived) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(t.Context())
	c, _ := testClient(t, s.URL())

	errs := make(chan error, 1)
	go func() {
		_, err := c.FindPeople(ctx, jira.PeopleQuery{})
		errs <- err
	}()

	<-arrived
	cancel()

	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the caller's own cancellation", err)
	}
}

func peopleWantTransport(t *testing.T, err error) {
	t.Helper()

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
}

func peopleAnswering(path, body string) jiratest.ServerOption {
	return jiratest.WithHandler(http.MethodGet, path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func peopleRawQueryOn(t *testing.T, s *jiratest.Server, path string) string {
	t.Helper()

	for _, req := range s.Requests() {
		if req.Path == path {
			return req.Query
		}
	}
	t.Fatalf("nothing was served for %s", path)
	return ""
}

func peopleQueryOn(t *testing.T, s *jiratest.Server, path string) url.Values {
	t.Helper()

	asked, err := url.ParseQuery(peopleRawQueryOn(t, s, path))
	if err != nil {
		t.Fatalf("reading the query for %s: %v", path, err)
	}
	return asked
}

func peopleCount(s *jiratest.Server, path string) int {
	n := 0
	for _, req := range s.Requests() {
		if req.Path == path {
			n++
		}
	}
	return n
}
