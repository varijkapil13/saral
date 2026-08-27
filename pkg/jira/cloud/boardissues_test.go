package cloud

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// The two routes the fixture server registers the board issue reads under. A
// default route is overridden by spelling the wildcard exactly as the default
// spells it, or both patterns are registered and the mux panics.
const (
	boardIssuesRoute  = "/rest/agile/1.0/board/{id}/issue"
	boardBacklogRoute = "/rest/agile/1.0/board/{id}/backlog"
)

// cardFields is a narrow field list, which is the only kind either read takes.
var cardFields = []string{"summary", "status", "issuetype"}

func boardIssuesReads(t *testing.T) map[string]struct {
	path string
	read func(*Client, jira.BoardQuery) (jira.Page[jira.Issue], error)
} {
	t.Helper()
	return map[string]struct {
		path string
		read func(*Client, jira.BoardQuery) (jira.Page[jira.Issue], error)
	}{
		"the board": {
			path: boardIssuesPath(boardTestID),
			read: func(c *Client, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
				return c.BoardIssues(t.Context(), boardTestID, q)
			},
		},
		"its backlog": {
			path: boardBacklogPath(boardTestID),
			read: func(c *Client, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
				return c.BoardBacklog(t.Context(), boardTestID, q)
			},
		},
	}
}

// Both reads answer the envelope board_issues.json carries: the array named
// issues rather than values, a total, and no isLast. A decoder reading only
// values turns either of them into an empty page and no error.
func TestBoardIssues_ReadsTheAgileEnvelopeThatNamesItsArrayIssues(t *testing.T) {
	t.Parallel()

	for name, tc := range boardIssuesReads(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, s := boardClient(t)
			page, err := tc.read(c, jira.BoardQuery{Fields: cardFields})
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			if len(page.Items) == 0 {
				t.Fatal("the page holds no issue, which is what reading only the values key produces")
			}
			total, counted := page.Count()
			if !counted {
				t.Error("the page reports no total, and this envelope carries one")
			}
			if total != len(page.Items) {
				t.Errorf("the page reports %d issues and carries %d", total, len(page.Items))
			}
			if page.HasMore() {
				t.Error("the walk did not end on a page holding every issue the total claims")
			}
			for _, iss := range page.Items {
				if iss.Key == "" || iss.Summary == "" {
					t.Errorf("an issue came back as %+v", iss)
				}
				if !iss.Requested.Has("summary") || iss.Requested.Wide() {
					t.Errorf("%s carries the mask %v, want the narrow list the read asked for",
						iss.Key, iss.Requested.IDs())
				}
			}
			if got := sentTo(t, s, http.MethodGet, tc.path).Path; got != tc.path {
				t.Errorf("the read went to %s, want %s", got, tc.path)
			}
		})
	}
}

// The field list goes out narrow, the page length is the one this client asked
// for, and neither wildcard is ever sent.
func TestBoardIssues_AsksForTheNarrowFieldListAndAPageLengthOfItsOwn(t *testing.T) {
	t.Parallel()

	for name, tc := range boardIssuesReads(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, s := boardClient(t)
			if _, err := tc.read(c, jira.BoardQuery{Fields: append(slices.Clone(cardFields), " status ", "")}); err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			query := boardQueryOn(t, s, tc.path)
			if got := query.Get("fields"); got != strings.Join(cardFields, ",") {
				t.Errorf("fields = %q, want the trimmed list with no repeat, %q", got, strings.Join(cardFields, ","))
			}
			if got := query.Get("maxResults"); got != strconv.Itoa(boardIssuePageSize) {
				t.Errorf("maxResults = %q, want %d; a length the site chooses moves when the site does",
					got, boardIssuePageSize)
			}
			if query.Has("jql") {
				t.Errorf("jql = %q was sent for a read carrying no sub-query", query.Get("jql"))
			}
		})
	}
}

// A caller's own page length is honoured, because the Agile API echoes the
// number sent rather than capping it silently.
func TestBoardIssues_SendsThePageLengthTheCallerAskedFor(t *testing.T) {
	t.Parallel()

	c, s := boardClient(t)
	if _, err := c.BoardIssues(t.Context(), boardTestID, jira.BoardQuery{Fields: cardFields, MaxResults: 7}); err != nil {
		t.Fatalf("reading the board: %v", err)
	}
	if got := boardQueryOn(t, s, boardIssuesPath(boardTestID)).Get("maxResults"); got != "7" {
		t.Errorf("maxResults = %q, want 7", got)
	}
}

// The board's sub-query is the one part of a board the endpoint does not apply,
// so it is sent as the endpoint's own jql parameter — bracketed, because the
// site ANDs the parameter onto the board's filter and a sub-query with an OR at
// the top of it would otherwise widen the board rather than narrow it.
func TestBoardIssues_SendsTheBoardsSubQueryAsABracketedClause(t *testing.T) {
	t.Parallel()

	const sub = "resolved >= -14d OR resolved is EMPTY"

	for name, tc := range boardIssuesReads(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, s := boardClient(t)
			if _, err := tc.read(c, jira.BoardQuery{Fields: cardFields, SubQuery: "  " + sub + "  "}); err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			got := boardQueryOn(t, s, tc.path).Get("jql")
			if got != "("+sub+")" {
				t.Errorf("jql = %q, want the sub-query in brackets: %q", got, "("+sub+")")
			}
		})
	}
}

// A read that names no field is refused before the site is asked: without a
// field list the endpoint answers with every navigable and Agile field the site
// has, on every issue.
func TestBoardIssues_RefuseAReadThatNamesNoFieldBeforeAskingTheSite(t *testing.T) {
	t.Parallel()

	for _, fields := range [][]string{nil, {}, {"  ", ""}} {
		c, s := boardClient(t)
		for name, tc := range boardIssuesReads(t) {
			_, err := tc.read(c, jira.BoardQuery{Fields: fields})
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("%s with fields %v: got %T (%v), want a *jira.ValidationError", name, fields, err, err)
			}
			if _, named := invalid.For("fields"); !named {
				t.Errorf("the refusal says %v and does not name fields", invalid.Fields)
			}
		}
		if served := s.Requests(); len(served) != 0 {
			t.Errorf("the site was sent %v, and there was nothing to ask it", served)
		}
	}
}

func TestBoardIssues_RefuseABoardIDThatNamesNoBoard(t *testing.T) {
	t.Parallel()

	c, s := boardClient(t)
	for _, id := range []int64{0, -1} {
		if _, err := c.BoardIssues(t.Context(), id, jira.BoardQuery{Fields: cardFields}); err == nil {
			t.Errorf("board id %d was accepted", id)
		}
		if _, err := c.BoardBacklog(t.Context(), id, jira.BoardQuery{Fields: cardFields}); err == nil {
			t.Errorf("board id %d was accepted on the backlog read", id)
		}
	}
	if served := s.Requests(); len(served) != 0 {
		t.Errorf("the site was sent %v, and there was nothing to ask it", served)
	}
}

// This envelope sends no isLast, so a walk over one that also sends no total has
// only the page length to go on: a page shorter than the maxResults the response
// itself echoes is the last one.
func TestBoardIssues_EndTheWalkOnAPageShorterThanTheOneAskedFor(t *testing.T) {
	t.Parallel()

	c, s := boardClient(t, jiratest.WithHandler(http.MethodGet, boardIssuesRoute,
		func(w http.ResponseWriter, r *http.Request) {
			at, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
			rows := make([]string, 0, 2)
			for i := at; i < min(at+2, 3); i++ {
				rows = append(rows, `{"id":"`+strconv.Itoa(20000+i)+`","key":"EX-`+strconv.Itoa(i+1)+
					`","fields":{"summary":"Row `+strconv.Itoa(i+1)+`"}}`)
			}
			jsonHandler(http.StatusOK, `{"startAt":`+strconv.Itoa(at)+
				`,"maxResults":2,"issues":[`+strings.Join(rows, ",")+`]}`)(w, r)
		}))

	page, err := c.BoardIssues(t.Context(), boardTestID, jira.BoardQuery{Fields: cardFields, MaxResults: 2})
	if err != nil {
		t.Fatalf("reading the board: %v", err)
	}
	if _, counted := page.Count(); counted {
		t.Error("the page reports a total, and this envelope carries none")
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the pages: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("the walk gathered %d issues, want 3", len(all))
	}
	offsets := make([]string, 0, 2)
	for _, sent := range s.Requests() {
		query, err := url.ParseQuery(sent.Query)
		if err != nil {
			t.Fatalf("reading a recorded query: %v", err)
		}
		offsets = append(offsets, query.Get("startAt"))
	}
	if !slices.Equal(offsets, []string{"", "2"}) {
		t.Errorf("the walk asked for offsets %v, want the short second page to end it", offsets)
	}
}

// A refusal names CapBoards on every page of the walk and not only the first, so
// a board that goes unreadable part way through reads the way one refused up
// front does.
func TestBoardIssues_NameCapBoardsOnAPageOtherThanTheFirst(t *testing.T) {
	t.Parallel()

	c, s := boardClient(t, jiratest.WithHandler(http.MethodGet, boardIssuesRoute,
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("startAt") != "" {
				jsonHandler(http.StatusForbidden, boardForbidden)(w, r)
				return
			}
			jsonHandler(http.StatusOK, `{"startAt":0,"maxResults":1,"issues":[`+
				`{"id":"20001","key":"EX-1","fields":{"summary":"Row 1"}}]}`)(w, r)
		}))

	page, err := c.BoardIssues(t.Context(), boardTestID, jira.BoardQuery{Fields: cardFields, MaxResults: 1})
	if err != nil {
		t.Fatalf("reading the first page: %v", err)
	}
	if !page.HasMore() {
		t.Fatal("a full page with neither a total nor an isLast has to be followed")
	}
	_, err = page.Next(t.Context())
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
	if refused.Capability != jira.CapBoards {
		t.Errorf("the refusal names %q, want %q", refused.Capability, jira.CapBoards)
	}
	if len(s.Requests()) != 2 {
		t.Errorf("the site served %d requests, want the first page and the refused second", len(s.Requests()))
	}
}
