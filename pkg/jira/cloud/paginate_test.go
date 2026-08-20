package cloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// wireIssue is as much of a search result as a paging test needs.
type wireIssue struct {
	Key string `json:"key"`
}

// wireSprint stands in for anything the Agile API pages by offset.
type wireSprint struct {
	ID int `json:"id"`
}

const searchPath = "/rest/api/3/search/jql"

func searchPages() func(token string) request {
	return func(token string) request {
		body := map[string]any{
			"jql":        "project = EX ORDER BY updated DESC",
			"fields":     []string{"summary", "status"},
			"maxResults": 50,
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		return request{method: http.MethodPost, path: searchPath, body: body, repeatable: true}
	}
}

func decodeIssues(resp *response) ([]wireIssue, string, error) {
	return decodeTokenPage[wireIssue](resp, http.MethodPost+" "+searchPath)
}

func keysOf(issues []wireIssue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Key)
	}
	return out
}

func TestCursorPages_WalksTheSearchFixturesToExhaustion(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	first, err := cursorPages(t.Context(), c, searchPages(), decodeIssues)
	if err != nil {
		t.Fatalf("fetching the first page: %v", err)
	}
	if !first.HasMore() {
		t.Fatal("the first page reports no more, but the fixture hands out a token")
	}
	if _, ok := first.Count(); ok {
		t.Error("a cursor page reported a total; /search/jql does not send one")
	}

	all, err := jira.Collect(t.Context(), first, 0)
	if err != nil {
		t.Fatalf("walking the pages: %v", err)
	}
	want := []string{"EX-1", "EX-2", "EX-3"}
	if got := keysOf(all); !slices.Equal(got, want) {
		t.Errorf("collected %v, want %v", got, want)
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the site served %d requests, want one per page", served)
	}

	second, err := first.Next(t.Context())
	if err != nil {
		t.Fatalf("re-walking to the second page: %v", err)
	}
	if second.HasMore() {
		t.Error("the last page reports more; isLast said otherwise")
	}
}

func TestCursorPages_EchoesTheTokenBackInTheRequestBody(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	first, err := cursorPages(t.Context(), c, searchPages(), decodeIssues)
	if err != nil {
		t.Fatalf("fetching the first page: %v", err)
	}
	if _, err = first.Next(t.Context()); err != nil {
		t.Fatalf("fetching the second page: %v", err)
	}

	served := s.Requests()
	if len(served) != 2 {
		t.Fatalf("the site served %d requests, want 2", len(served))
	}
	var sent struct {
		NextPageToken string `json:"nextPageToken"`
	}
	if err = json.Unmarshal([]byte(served[1].Body), &sent); err != nil {
		t.Fatalf("reading the second request body: %v", err)
	}
	if sent.NextPageToken == "" {
		t.Error("the second request carried no nextPageToken, so it asked for page one again")
	}
}

func TestCursorPages_TreatsARepeatedTokenAsExhaustion(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, searchPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issues":[{"key":"EX-1"}],"nextPageToken":"the-same-token-forever"}`))
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	first, err := cursorPages(t.Context(), c, searchPages(), decodeIssues)
	if err != nil {
		t.Fatalf("fetching the first page: %v", err)
	}
	all, err := jira.Collect(t.Context(), first, 0)
	if err != nil {
		t.Fatalf("walking the pages: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("collected %d issues, want 2: the second page hands back the token that fetched it", len(all))
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the site served %d requests, want 2 — a looping token must not loop the client", served)
	}
}

func TestCursorPages_SurfacesAFailureOnAPageAfterTheFirst(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, searchPath, func(w http.ResponseWriter, r *http.Request) {
		var sent struct {
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if sent.NextPageToken == "" {
			_, _ = w.Write([]byte(`{"issues":[{"key":"EX-1"}],"nextPageToken":"page-two"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errorMessages":["This query is no longer yours to run."],"errors":{}}`))
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	first, err := cursorPages(t.Context(), c, searchPages(), decodeIssues)
	if err != nil {
		t.Fatalf("fetching the first page: %v", err)
	}
	_, err = first.Next(t.Context())

	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
}

// agilePages answers an offset-paged endpoint out of a fixed number of items,
// reporting whatever total and isLast the case under test asks it to.
func agilePages(items, pageSize int, reportTotal, reportIsLast bool, truncateAt int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startAt, err := strconv.Atoi(r.URL.Query().Get("startAt"))
		if err != nil {
			startAt = 0
		}
		end := min(startAt+pageSize, items)
		if truncateAt >= 0 && startAt >= truncateAt {
			end = startAt
		}
		values := make([]wireSprint, 0, max(0, end-startAt))
		for id := startAt; id < end; id++ {
			values = append(values, wireSprint{ID: id})
		}
		page := struct {
			StartAt    int          `json:"startAt"`
			MaxResults int          `json:"maxResults"`
			Total      *int         `json:"total,omitempty"`
			IsLast     *bool        `json:"isLast,omitempty"`
			Values     []wireSprint `json:"values"`
		}{StartAt: startAt, MaxResults: pageSize, Values: values}
		if reportTotal {
			page.Total = &items
		}
		if reportIsLast {
			last := end >= items
			page.IsLast = &last
		}
		body, err := json.Marshal(page)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func TestOffsetPages_WalksAnAgileEndpointToExhaustion(t *testing.T) {
	t.Parallel()

	const sprintPath = "/rest/agile/1.0/board/10/sprint"

	tests := []struct {
		name        string
		items       int
		pageSize    int
		total       bool
		isLast      bool
		truncateAt  int
		wantItems   int
		wantPages   int
		wantCounted bool
	}{
		{
			name: "a total and an isLast", items: 7, pageSize: 3, total: true, isLast: true,
			truncateAt: -1, wantItems: 7, wantPages: 3, wantCounted: true,
		},
		{
			name: "a total and no isLast", items: 7, pageSize: 3, total: true,
			truncateAt: -1, wantItems: 7, wantPages: 3, wantCounted: true,
		},
		{
			name: "neither, so only an empty page ends the walk", items: 5, pageSize: 2,
			truncateAt: -1, wantItems: 5, wantPages: 4,
		},
		{
			name: "a total the endpoint then silently truncates against", items: 20, pageSize: 3, total: true,
			truncateAt: 6, wantItems: 6, wantPages: 3, wantCounted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, sprintPath,
				agilePages(tt.items, tt.pageSize, tt.total, tt.isLast, tt.truncateAt)))
			defer s.Close()

			c, _ := testClient(t, s.URL())
			build := func(startAt int) request {
				return request{
					method: http.MethodGet,
					path:   sprintPath,
					query:  pagedQuery(url.Values{"state": {"active,future"}}, startAt, tt.pageSize),
				}
			}
			first, err := offsetPages(t.Context(), c, build, func(resp *response) ([]wireSprint, int, bool, error) {
				return decodeAgilePage[wireSprint](resp, http.MethodGet+" "+sprintPath)
			})
			if err != nil {
				t.Fatalf("fetching the first page: %v", err)
			}

			count, counted := first.Count()
			if counted != tt.wantCounted {
				t.Errorf("Count() reported %t, want %t", counted, tt.wantCounted)
			}
			if counted && count != tt.items {
				t.Errorf("Count() = %d, want the %d the endpoint claims", count, tt.items)
			}

			all, err := jira.Collect(t.Context(), first, 0)
			if err != nil {
				t.Fatalf("walking the pages: %v", err)
			}
			if len(all) != tt.wantItems {
				t.Errorf("collected %d sprints, want %d", len(all), tt.wantItems)
			}
			for i, sprint := range all {
				if sprint.ID != i {
					t.Fatalf("sprint %d is %d: a page was fetched from the wrong offset", i, sprint.ID)
				}
			}
			if served := len(s.Requests()); served != tt.wantPages {
				t.Errorf("the site served %d requests, want %d", served, tt.wantPages)
			}
		})
	}
}

func TestOffsetPages_ReadsTheSprintFixtureAsOneCountedPage(t *testing.T) {
	t.Parallel()

	const sprintPath = "/rest/agile/1.0/board/10/sprint"

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	page, err := offsetPages(t.Context(), c,
		func(startAt int) request {
			return request{method: http.MethodGet, path: sprintPath, query: pagedQuery(nil, startAt, 50)}
		},
		func(resp *response) ([]wireSprint, int, bool, error) {
			return decodeAgilePage[wireSprint](resp, http.MethodGet+" "+sprintPath)
		})
	if err != nil {
		t.Fatalf("reading the sprint fixture: %v", err)
	}
	if len(page.Items) != 3 {
		t.Errorf("read %d sprints, want the 3 in the fixture", len(page.Items))
	}
	if count, ok := page.Count(); !ok || count != 3 {
		t.Errorf("Count() = %d, %t, want 3, true", count, ok)
	}
	if page.HasMore() {
		t.Error("the fixture says isLast, but the page reports more")
	}
}

func TestDecodeAgilePage_ReportsNoTotalWhenTheEndpointSendsNone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantTotal int
		wantLast  bool
		wantItems int
	}{
		{name: "a total and an isLast", body: `{"total":9,"isLast":true,"values":[{"id":1}]}`, wantTotal: 9, wantLast: true, wantItems: 1},
		{name: "no total", body: `{"isLast":false,"values":[{"id":1}]}`, wantTotal: -1, wantItems: 1},
		{name: "a total of zero, which is not a missing total", body: `{"total":0,"values":[]}`, wantTotal: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			items, total, last, err := decodeAgilePage[wireSprint](&response{status: http.StatusOK, body: []byte(tt.body)}, "GET /x")
			if err != nil {
				t.Fatalf("decoding %s: %v", tt.body, err)
			}
			if total != tt.wantTotal || last != tt.wantLast || len(items) != tt.wantItems {
				t.Errorf("decoded %d items, total %d, isLast %t; want %d, %d, %t",
					len(items), total, last, tt.wantItems, tt.wantTotal, tt.wantLast)
			}
		})
	}
}

func TestDecodeTokenPage_ReadsEitherItemsKeyAndHonoursIsLast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantItems int
		wantNext  string
	}{
		{name: "the search endpoint's issues", body: `{"issues":[{"key":"EX-1"}],"nextPageToken":"more"}`, wantItems: 1, wantNext: "more"},
		{name: "the other endpoints' values", body: `{"values":[{"key":"EX-1"},{"key":"EX-2"}],"nextPageToken":"more"}`, wantItems: 2, wantNext: "more"},
		{name: "an absent token is the end", body: `{"issues":[{"key":"EX-1"}]}`, wantItems: 1},
		{name: "isLast beats a token that came with it", body: `{"issues":[],"nextPageToken":"more","isLast":true}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			items, next, err := decodeTokenPage[wireIssue](&response{status: http.StatusOK, body: []byte(tt.body)}, "POST /x")
			if err != nil {
				t.Fatalf("decoding %s: %v", tt.body, err)
			}
			if len(items) != tt.wantItems || next != tt.wantNext {
				t.Errorf("decoded %d items and %q; want %d and %q", len(items), next, tt.wantItems, tt.wantNext)
			}
		})
	}
}

func TestDecodeTokenPage_TreatsAnUndecodableEnvelopeAsATransportFailure(t *testing.T) {
	t.Parallel()

	_, _, err := decodeTokenPage[wireIssue](&response{status: http.StatusOK, body: []byte(`{"issues": "not an array"}`)}, "POST /x")

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
}

func TestPagedQuery_KeepsTheCallersQueryAndLeavesItAlone(t *testing.T) {
	t.Parallel()

	base := url.Values{"state": {"active"}}
	got := pagedQuery(base, 50, 25)
	if got.Get("state") != "active" || got.Get("startAt") != "50" || got.Get("maxResults") != "25" {
		t.Errorf("pagedQuery = %v, want the caller's query with the offset on it", got)
	}
	if base.Has("startAt") {
		t.Error("pagedQuery wrote into the query it was given")
	}
	if first := pagedQuery(nil, 0, 50); first.Has("startAt") {
		t.Errorf("the first page asked for startAt=%s, want the parameter left off", first.Get("startAt"))
	}
}
