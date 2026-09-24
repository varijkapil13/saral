package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const sprintIssuesRoute = "/rest/agile/1.0/board/{id}/sprint/{sprintId}/issue"

func sprintIssuesClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

func TestSprintIssues_ReadsTheBoardScopedSprintRoute(t *testing.T) {
	t.Parallel()

	c, s := sprintIssuesClient(t)
	page, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}})
	if err != nil {
		t.Fatalf("reading sprint issues: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("the fixture answers with no issues")
	}
	sentTo(t, s, http.MethodGet, boardSprintIssuesPath(boardTestID, testActiveSprint))
}

func TestSprintIssues_RefusesWithNoFieldsWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	c, s := sprintIssuesClient(t)
	_, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{})

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, named := invalid.For("fields"); !named {
		t.Errorf("the refusal says %v and does not name fields", invalid.Fields)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site was sent %d requests for a call with no fields to ask for", served)
	}
}

func TestSprintIssues_RefusesASprintIdThatIsNotPositiveWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	c, s := sprintIssuesClient(t)
	for _, id := range []int64{0, -1} {
		_, err := c.SprintIssues(t.Context(), boardTestID, id, jira.BoardQuery{Fields: []string{"summary"}})
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("sprint id %d: got %T (%v), want a *jira.ValidationError", id, err, err)
		}
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site was sent %d requests for a sprint id that identifies nothing", served)
	}
}

func TestSprintIssues_RefusesABoardIdThatIsNotPositiveWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	c, s := sprintIssuesClient(t)
	for _, id := range []int64{0, -1} {
		_, err := c.SprintIssues(t.Context(), id, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}})
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("board id %d: got %T (%v), want a *jira.ValidationError", id, err, err)
		}
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site was sent %d requests for a board id that identifies nothing", served)
	}
}

func TestSprintIssues_SendsTheSubQueryAndQuickFiltersAsOneJQLParameter(t *testing.T) {
	t.Parallel()

	c, s := sprintIssuesClient(t)
	_, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{
		Fields:       []string{"summary"},
		SubQuery:     "resolved is EMPTY",
		QuickFilters: []string{"assignee = currentUser()"},
	})
	if err != nil {
		t.Fatalf("reading sprint issues: %v", err)
	}
	got := boardQueryOn(t, s, boardSprintIssuesPath(boardTestID, testActiveSprint))
	want := "(resolved is EMPTY) AND (assignee = currentUser())"
	if got.Get("jql") != want {
		t.Errorf("jql = %q, want %q", got.Get("jql"), want)
	}
}

func TestSprintIssues_WalksBothPagesTheSiteAnswersWith(t *testing.T) {
	t.Parallel()

	type wireIssue struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
		} `json:"fields"`
	}
	page := func(startAt, total int) string {
		end := min(startAt+1, total)
		values := make([]wireIssue, 0, max(0, end-startAt))
		for i := startAt; i < end; i++ {
			values = append(values, wireIssue{ID: strconv.Itoa(i + 1), Key: "EX-" + strconv.Itoa(i+1)})
		}
		body := struct {
			StartAt    int         `json:"startAt"`
			MaxResults int         `json:"maxResults"`
			Total      int         `json:"total"`
			Issues     []wireIssue `json:"issues"`
		}{StartAt: startAt, MaxResults: 1, Total: total, Issues: values}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshalling a page: %v", err)
		}
		return string(raw)
	}

	c, s := sprintIssuesClient(t, jiratest.WithHandler(http.MethodGet, sprintIssuesRoute, func(w http.ResponseWriter, r *http.Request) {
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		jsonHandler(http.StatusOK, page(startAt, 2))(w, r)
	}))

	got, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}, MaxResults: 1})
	if err != nil {
		t.Fatalf("reading sprint issues: %v", err)
	}
	all, err := jira.Collect(t.Context(), got, 0)
	if err != nil {
		t.Fatalf("walking the pages: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("collected %d issues over two pages, want 2", len(all))
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the walk cost %d requests, want one per page", served)
	}
	offsets := make([]string, 0, len(s.Requests()))
	for _, sent := range s.Requests() {
		q, err := url.ParseQuery(sent.Query)
		if err != nil {
			t.Fatalf("reading a recorded query: %v", err)
		}
		offsets = append(offsets, q.Get("startAt"))
	}
	if offsets[0] != "" && offsets[0] != "0" {
		t.Errorf("the first request asked for startAt=%q, want the first page", offsets[0])
	}
	if offsets[1] != "1" {
		t.Errorf("the second request asked for startAt=%q, want 1", offsets[1])
	}
}

func TestSprintIssues_A403NamesCapBoards(t *testing.T) {
	t.Parallel()

	c, _ := sprintIssuesClient(t, jiratest.WithStatus(http.MethodGet, sprintIssuesRoute, http.StatusForbidden, "plans_403.json"))
	_, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}})

	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
	if refused.Capability != jira.CapBoards {
		t.Errorf("the refusal names %q, want %q", refused.Capability, jira.CapBoards)
	}
}

func TestSprintIssues_A404NamesTheBoard(t *testing.T) {
	t.Parallel()

	c, _ := sprintIssuesClient(t, jiratest.WithStatus(http.MethodGet, sprintIssuesRoute, http.StatusNotFound, "not_found_board.json"))
	_, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}})

	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
}

func TestSprintIssues_A429IsARateLimitError(t *testing.T) {
	t.Parallel()

	c, _ := sprintIssuesClient(t, jiratest.WithRateLimit(http.MethodGet, sprintIssuesRoute, 30*time.Second))
	_, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}})

	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
	}
	if limited.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %s, want 30s", limited.RetryAfter)
	}
}

func TestSprintIssues_A502IsATransportFailure(t *testing.T) {
	t.Parallel()

	c, _ := sprintIssuesClient(t, jiratest.WithStatus(http.MethodGet, sprintIssuesRoute, http.StatusBadGateway, ""))
	_, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}})

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if broken.Status != http.StatusBadGateway {
		t.Errorf("Status = %d, want 502", broken.Status)
	}
}

func TestSprintIssues_AMalformedBodyIsATransportFailure(t *testing.T) {
	t.Parallel()

	c, _ := sprintIssuesClient(t, jiratest.WithHandler(http.MethodGet, sprintIssuesRoute,
		jsonHandler(http.StatusOK, `{"issues":[`)))
	_, err := c.SprintIssues(t.Context(), boardTestID, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}})

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
}

func TestSprintIssues_ComesBackWithTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	c, s := sprintIssuesClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := c.SprintIssues(ctx, boardTestID, testActiveSprint, jira.BoardQuery{Fields: []string{"summary"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled unwrapped", err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site was sent %d requests after the caller had already gone", served)
	}
}
