package cloud

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const boardProjectRoute = "/rest/agile/1.0/board/{id}/project"

func projectPageServing(total int, withTotal bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		size, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
		end := min(startAt+size, total)
		rows := make([]string, 0, size)
		for i := startAt; i < end; i++ {
			rows = append(rows, fmt.Sprintf(`{"id":"%d","key":"P%d","name":"Project %d"}`, 10000+i, i, i))
		}
		totalField := ""
		if withTotal {
			totalField = fmt.Sprintf(`"total":%d,`, total)
		}
		jsonHandler(http.StatusOK, fmt.Sprintf(`{"startAt":%d,"maxResults":%d,%s"isLast":%t,"values":[%s]}`,
			startAt, size, totalField, end == total, strings.Join(rows, ",")))(w, r)
	}
}

func TestBoardProjects_ReadsIdKeyAndNameOfEveryProject(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithFixture(http.MethodGet, boardProjectRoute, "board_projects.json"))
	got, err := c.BoardProjects(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading the projects behind the board: %v", err)
	}
	want := []jira.ProjectRef{
		{ID: "10000", Key: "EX", Name: "Example"},
		{ID: "10001", Key: "OPS", Name: "Operations"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("project %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBoardProjects_WalksEveryPage(t *testing.T) {
	t.Parallel()

	const total = 120
	c, s := boardClient(t, jiratest.WithHandler(http.MethodGet, boardProjectRoute, projectPageServing(total, true)))
	got, err := c.BoardProjects(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading the projects behind the board: %v", err)
	}
	if len(got) != total {
		t.Fatalf("got %d projects, want %d", len(got), total)
	}
	if served := len(s.Requests()); served < 2 {
		t.Errorf("%d request(s) served, want a walk over more than one page", served)
	}
}

func TestBoardProjects_ARunPastTheBoundIsRefusedRatherThanCutShort(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithHandler(http.MethodGet, boardProjectRoute, projectPageServing(boardProjectBound+30, false)))
	_, err := c.BoardProjects(t.Context(), boardTestID)
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
}

func TestBoardProjects_AnEmptyAnswerIsNotAnError(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithHandler(http.MethodGet, boardProjectRoute,
		jsonHandler(http.StatusOK, `{"startAt":0,"maxResults":50,"total":0,"isLast":true,"values":[]}`)))
	got, err := c.BoardProjects(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading the projects behind the board: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestBoardProjects_ANumericIDIsReadLeniently(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithHandler(http.MethodGet, boardProjectRoute,
		jsonHandler(http.StatusOK, `{"startAt":0,"maxResults":50,"total":2,"isLast":true,"values":[{"id":10000,"key":"EX","name":"Example"},{"name":"No identity"}]}`)))
	got, err := c.BoardProjects(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading the projects behind the board: %v", err)
	}
	if len(got) != 1 || got[0].ID != "10000" || got[0].Key != "EX" {
		t.Errorf("got %+v, want the one project with an identity, id 10000", got)
	}
}

func TestBoardProjects_RefusesABoardIDThatIsNotPositive(t *testing.T) {
	t.Parallel()

	c, s := boardClient(t)
	for _, id := range []int64{0, -1} {
		_, err := c.BoardProjects(t.Context(), id)
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("board id %d: got %T (%v), want a *jira.ValidationError", id, err, err)
		}
	}
	if served := s.Requests(); len(served) != 0 {
		t.Errorf("%d request(s) sent for a board id that cannot exist", len(served))
	}
}

func TestBoardProjects_A404NamesTheBoard(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithStatus(http.MethodGet, boardProjectRoute, http.StatusNotFound, "not_found_board.json"))
	_, err := c.BoardProjects(t.Context(), 99999)
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "board" || missing.ID != "99999" {
		t.Errorf("the 404 names %s %s, want board 99999", missing.Kind, missing.ID)
	}
	if want := agileBoardReason(t); missing.Detail != want {
		t.Errorf("Detail = %q, want the site's own sentence %q", missing.Detail, want)
	}
}
