package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	testSprintsRoute      = "/rest/agile/1.0/board/{id}/sprint"
	testSprintRoute       = "/rest/agile/1.0/sprint/{id}"
	testSprintNewRoute    = "/rest/agile/1.0/sprint"
	testSprintIssuesRoute = "/rest/agile/1.0/sprint/{id}/issue"
	testBacklogRoute      = "/rest/agile/1.0/backlog/issue"

	testBoard = int64(10)
	// One sprint per state a write can find, so a test names the state it means
	// rather than a number: 41 is closed, 42 active, 43 a future sprint nobody
	// has dated yet, and 44 a future sprint with both dates on it.
	testClosedSprint = int64(41)
	testActiveSprint = int64(42)
	testBlankSprint  = int64(43)
	testDatedSprint  = int64(44)
)

// sprintMemberRoute answers the member route with the sprint the id asks for,
// because the state machine checks mean the same call behaves differently on a
// closed sprint and a future one.
func sprintMemberRoute(t *testing.T) jiratest.ServerOption {
	t.Helper()

	bodies := map[string]string{
		strconv.FormatInt(testClosedSprint, 10): string(fixture(t, "sprint_one.json")),
		strconv.FormatInt(testActiveSprint, 10): string(fixture(t, "sprint_updated.json")),
		// No committed fixture answers an undated sprint on the member route,
		// and the entry the sprint page carries for one is the same object.
		strconv.FormatInt(testBlankSprint, 10): sprintFromPage(t, testBlankSprint),
		strconv.FormatInt(testDatedSprint, 10): string(fixture(t, "sprint_created.json")),
	}
	return jiratest.WithHandler(http.MethodGet, testSprintRoute, func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.PathValue("id")]
		if !ok {
			jsonHandler(http.StatusNotFound, `{"errorMessages":[],"errors":{"sprintId":"Sprint does not exist or you do not have permission to view it"}}`)(w, r)
			return
		}
		jsonHandler(http.StatusOK, body)(w, r)
	})
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	b, err := jiratest.Fixture(name)
	if err != nil {
		t.Fatalf("reading the %s fixture: %v", name, err)
	}
	return b
}

// sprintFromPage is one entry of sprint_page.json as the member route would
// answer it, so a case the committed fixtures cover only inside a page is still
// read from the committed bytes.
func sprintFromPage(t *testing.T, id int64) string {
	t.Helper()

	var page struct {
		Values []json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(fixture(t, "sprint_page.json"), &page); err != nil {
		t.Fatalf("reading sprint_page.json: %v", err)
	}
	for _, raw := range page.Values {
		var entry struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatalf("reading an entry of sprint_page.json: %v", err)
		}
		if entry.ID == id {
			return string(raw)
		}
	}
	t.Fatalf("sprint_page.json holds no sprint %d", id)
	return ""
}

func sprintClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(append([]jiratest.ServerOption{sprintMemberRoute(t)}, opts...)...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

func TestSprints_ReadsABoardsSprintsAndAsksTheSiteToNarrowThem(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t)

	page, err := c.Sprints(t.Context(), testBoard, jira.SprintActive, jira.SprintFuture)
	if err != nil {
		t.Fatalf("listing a board's sprints: %v", err)
	}
	got, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the sprints: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sprints, want the 3 the page holds", len(got))
	}
	for _, sp := range got {
		if sp.BoardID != testBoard {
			t.Errorf("sprint %d says it is on board %d rather than %d", sp.ID, sp.BoardID, testBoard)
		}
	}
	if got[0].State != jira.SprintClosed || got[1].State != jira.SprintActive || got[2].State != jira.SprintFuture {
		t.Errorf("got the states %s/%s/%s, want closed/active/future", got[0].State, got[1].State, got[2].State)
	}
	if got[2].Start != nil || got[2].End != nil {
		t.Errorf("the future sprint arrived with dates %v/%v, and it has none set", got[2].Start, got[2].End)
	}
	if got[0].Complete == nil {
		t.Error("the closed sprint arrived without the completion date the site sent")
	}

	query, err := url.ParseQuery(sentTo(t, s, http.MethodGet, "/rest/agile/1.0/board/10/sprint").Query)
	if err != nil {
		t.Fatalf("reading the query: %v", err)
	}
	if state := query.Get("state"); state != "active,future" {
		t.Errorf("the request asked for state=%q; narrowing is the endpoint's job and a client that walks every closed sprint pays for all of them", state)
	}
	if size := query.Get("maxResults"); size != strconv.Itoa(sprintPageSize) {
		t.Errorf("the request asked for maxResults=%q, want an explicit %d", size, sprintPageSize)
	}
}

func TestSprints_AsksForEveryStateWhenTheCallerNamesNone(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t)

	if _, err := c.Sprints(t.Context(), testBoard); err != nil {
		t.Fatalf("listing every sprint: %v", err)
	}
	query, err := url.ParseQuery(sentTo(t, s, http.MethodGet, "/rest/agile/1.0/board/10/sprint").Query)
	if err != nil {
		t.Fatalf("reading the query: %v", err)
	}
	if _, named := query["state"]; named {
		t.Errorf("the request carried state=%q for a caller that named none", query.Get("state"))
	}
}

func TestSprints_DropsAStateThatWouldCostTheCallerEverySprintOnTheBoard(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t)

	if _, err := c.Sprints(t.Context(), testBoard, jira.SprintActive, "", "  "); err != nil {
		t.Fatalf("listing sprints: %v", err)
	}
	query, err := url.ParseQuery(sentTo(t, s, http.MethodGet, "/rest/agile/1.0/board/10/sprint").Query)
	if err != nil {
		t.Fatalf("reading the query: %v", err)
	}
	if state := query.Get("state"); state != "active" {
		t.Errorf("the request asked for state=%q; a blank entry sent as one refuses the whole request", state)
	}
}

func TestSprints_WalksEveryPageAndEndsWhereTheSiteSaysItDoes(t *testing.T) {
	t.Parallel()

	pages := []string{
		`{"startAt":0,"maxResults":2,"total":3,"isLast":false,"values":[
			{"id":41,"state":"closed","name":"one","originBoardId":10},
			{"id":42,"state":"active","name":"two","originBoardId":10}]}`,
		`{"startAt":2,"maxResults":2,"total":3,"isLast":true,"values":[
			{"id":43,"state":"future","name":"three","originBoardId":10}]}`,
	}
	c, s := sprintClient(t, jiratest.WithHandler(http.MethodGet, testSprintsRoute, func(w http.ResponseWriter, r *http.Request) {
		startAt := r.URL.Query().Get("startAt")
		if startAt == "" || startAt == "0" {
			jsonHandler(http.StatusOK, pages[0])(w, r)
			return
		}
		jsonHandler(http.StatusOK, pages[1])(w, r)
	}))

	first, err := c.Sprints(t.Context(), testBoard)
	if err != nil {
		t.Fatalf("listing sprints: %v", err)
	}
	got, err := jira.Collect(t.Context(), first, 0)
	if err != nil {
		t.Fatalf("walking the sprints: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sprints over two pages, want 3", len(got))
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the walk cost %d requests, want the 2 the pages need", served)
	}
	if total, known := first.Count(); !known || total != 3 {
		t.Errorf("the first page reports total %d (known %v), want the 3 the envelope claims", total, known)
	}
}

func TestSprints_EndsOnAnEmptyPageWhateverTheTotalClaimed(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t, jiratest.WithHandler(http.MethodGet, testSprintsRoute, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("startAt") == "" || r.URL.Query().Get("startAt") == "0" {
			jsonHandler(http.StatusOK, `{"startAt":0,"maxResults":50,"total":900,"values":[{"id":41,"state":"closed","name":"one"}]}`)(w, r)
			return
		}
		jsonHandler(http.StatusOK, `{"startAt":1,"maxResults":50,"total":900,"values":[]}`)(w, r)
	}))

	page, err := c.Sprints(t.Context(), testBoard)
	if err != nil {
		t.Fatalf("listing sprints: %v", err)
	}
	got, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the sprints: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sprints, want the 1 the site actually had", len(got))
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the walk cost %d requests; a total of 900 is not a reason to keep asking past an empty page", served)
	}
}

func TestSprints_ASprintTheSiteNamedNoBoardForIsOnTheBoardItWasReadThrough(t *testing.T) {
	t.Parallel()

	c, _ := sprintClient(t, jiratest.WithHandler(http.MethodGet, testSprintsRoute,
		jsonHandler(http.StatusOK, `{"startAt":0,"maxResults":50,"total":1,"isLast":true,"values":[
			{"id":41,"state":"active","name":"EX Sprint 8"}]}`)))

	page, err := c.Sprints(t.Context(), testBoard)
	if err != nil {
		t.Fatalf("listing sprints: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("got %d sprints, want 1", len(page.Items))
	}
	if got := page.Items[0].BoardID; got != testBoard {
		t.Errorf("got board %d, want the %d it was read through", got, testBoard)
	}
}

func TestSprint_ReadsEitherSpellingOfTheDatesASiteSends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		id    int64
		start time.Time
		end   time.Time
	}{
		{
			// sprint_one.json writes an offset with a colon in it.
			name:  "an offset written out in full",
			id:    testClosedSprint,
			start: time.Date(2026, 1, 5, 8, 0, 0, 0, time.UTC),
			end:   time.Date(2026, 1, 19, 8, 0, 0, 0, time.UTC),
		},
		{
			// sprint_created.json writes the same instant normalised to UTC,
			// which is the spelling a real site answers a sprint boundary in.
			name:  "an instant already normalised to Z",
			id:    testDatedSprint,
			start: time.Date(2026, 2, 2, 8, 0, 0, 0, time.UTC),
			end:   time.Date(2026, 2, 16, 8, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := sprintClient(t)

			got, err := c.Sprint(t.Context(), tt.id)
			if err != nil {
				t.Fatalf("reading sprint %d: %v", tt.id, err)
			}
			if got.Start == nil || !got.Start.Equal(tt.start) {
				t.Errorf("got a start of %v, want %v", got.Start, tt.start)
			}
			if got.End == nil || !got.End.Equal(tt.end) {
				t.Errorf("got an end of %v, want %v", got.End, tt.end)
			}
		})
	}
}

func TestSprint_AnswersWithNoDatesUntilTheyAreSet(t *testing.T) {
	t.Parallel()

	c, _ := sprintClient(t)

	got, err := c.Sprint(t.Context(), testBlankSprint)
	if err != nil {
		t.Fatalf("reading an undated sprint: %v", err)
	}
	if got.State != jira.SprintFuture {
		t.Errorf("got state %q, want future", got.State)
	}
	if got.Start != nil || got.End != nil || got.Complete != nil {
		t.Errorf("got %v/%v/%v, want no dates at all: a sprint answers without them until they are set", got.Start, got.End, got.Complete)
	}
	if got.BoardID != testBoard {
		t.Errorf("got board %d, want the origin board %d", got.BoardID, testBoard)
	}
}

func TestSprint_ReadsAStateWhateverCaseTheSiteSpeltIt(t *testing.T) {
	t.Parallel()

	// The member route spells the three states in lower case and the sprint
	// field an issue carries spells them in upper; every write below refuses on
	// a state it cannot read.
	const shouty = `{"id":42,"state":"  ACTIVE  ","name":"EX Sprint 8","originBoardId":10,
		"startDate":"2026-01-19T08:00:00.000Z","endDate":"2026-02-02T08:00:00.000Z"}`

	c, s := sprintClient(t,
		jiratest.WithHandler(http.MethodGet, testSprintRoute, jsonHandler(http.StatusOK, shouty)),
		jiratest.WithFixture(http.MethodPost, testSprintRoute, "sprint_one.json"))

	got, err := c.Sprint(t.Context(), testActiveSprint)
	if err != nil {
		t.Fatalf("reading a sprint: %v", err)
	}
	if got.State != jira.SprintActive {
		t.Errorf("got state %q, want active", got.State)
	}
	if _, err := c.CompleteSprint(t.Context(), testActiveSprint); err != nil {
		t.Fatalf("completing a sprint the site called ACTIVE: %v", err)
	}
	body := sentBody(t, sentTo(t, s, http.MethodPost, "/rest/agile/1.0/sprint/42"))
	if state, _ := body["state"].(string); state != string(jira.SprintClosed) {
		t.Errorf("completing sent state %q", state)
	}
}

func TestSprint_AMissingSprintNamesTheSprintRatherThanAURL(t *testing.T) {
	t.Parallel()

	c, _ := sprintClient(t)

	_, err := c.Sprint(t.Context(), 999)
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "sprint" || missing.ID != "999" {
		t.Errorf("the failure names %s %s rather than the sprint", missing.Kind, missing.ID)
	}
}

func TestCreateSprint_SendsTheBoardTheNameAndTheDatesAndNothingElse(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t)
	start := time.Date(2026, 2, 2, 8, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 14)

	got, err := c.CreateSprint(t.Context(), jira.SprintInput{
		BoardID: testBoard,
		Name:    "  EX Sprint 10  ",
		Goal:    "Stream the attachment preview instead of buffering it.",
		Start:   &start,
		End:     &end,
	})
	if err != nil {
		t.Fatalf("creating a sprint: %v", err)
	}
	if got.ID != testDatedSprint {
		t.Errorf("got sprint %d back, want the %d the site created", got.ID, testDatedSprint)
	}
	if got.State != jira.SprintFuture {
		t.Errorf("a created sprint came back %q, want future", got.State)
	}

	body := sentBody(t, sentTo(t, s, http.MethodPost, testSprintNewRoute))
	if keys := sortedKeys(body); !slices.Equal(keys, []string{"endDate", "goal", "name", "originBoardId", "startDate"}) {
		t.Errorf("the create sent %v", keys)
	}
	if name, _ := body["name"].(string); name != "EX Sprint 10" {
		t.Errorf("the create sent the name %q untrimmed", name)
	}
	if board, _ := body["originBoardId"].(float64); int64(board) != testBoard {
		t.Errorf("the create sent originBoardId %v, want %d", body["originBoardId"], testBoard)
	}
	if sent, _ := body["startDate"].(string); sent != "2026-02-02T08:00:00.000+00:00" {
		t.Errorf("the create sent startDate %q; a date in another layout is a localised 400 nothing may branch on", sent)
	}
	if sent, _ := body["endDate"].(string); sent != "2026-02-16T08:00:00.000+00:00" {
		t.Errorf("the create sent endDate %q", sent)
	}
}

func TestSprintWrites_SendADateInTheLayoutTheAgileEndpointTakes(t *testing.T) {
	t.Parallel()

	// A zone two hours east, so the offset in the body is a real one rather
	// than a Z that three wrong layouts would all still look right in.
	at := time.Date(2026, 2, 2, 9, 0, 0, 0, time.FixedZone("", 2*60*60))
	const want = "2026-02-02T09:00:00.000+02:00"

	tests := []struct {
		name string
		path string
		run  func(ctx context.Context, c *Client) error
	}{
		{name: "a create", path: testSprintNewRoute, run: func(ctx context.Context, c *Client) error {
			_, err := c.CreateSprint(ctx, jira.SprintInput{BoardID: testBoard, Name: "EX Sprint 10", Start: &at})
			return err
		}},
		{name: "an update", path: "/rest/agile/1.0/sprint/42", run: func(ctx context.Context, c *Client) error {
			_, err := c.UpdateSprint(ctx, testActiveSprint, jira.SprintPatch{Start: &at})
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			if err := tt.run(t.Context(), c); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			body := sentBody(t, sentTo(t, s, http.MethodPost, tt.path))
			if sent, _ := body["startDate"].(string); sent != want {
				t.Errorf("%s sent startDate %q, want %q: the offset carries a colon and the millis are not optional", tt.name, sent, want)
			}
		})
	}
}

func TestCreateSprint_SendsNoDateTheCallerDidNotSet(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t)

	if _, err := c.CreateSprint(t.Context(), jira.SprintInput{BoardID: testBoard, Name: "EX Sprint 10"}); err != nil {
		t.Fatalf("creating an undated sprint: %v", err)
	}
	body := sentBody(t, sentTo(t, s, http.MethodPost, testSprintNewRoute))
	if keys := sortedKeys(body); !slices.Equal(keys, []string{"name", "originBoardId"}) {
		t.Errorf("the create sent %v; a date nobody set is not a date to send", keys)
	}
}

func TestCreateSprint_RefusesBeforeTheWireWhatTheSiteWouldOnlyRefuseAfterIt(t *testing.T) {
	t.Parallel()

	early := time.Date(2026, 2, 2, 8, 0, 0, 0, time.UTC)
	late := early.AddDate(0, 0, 14)

	tests := []struct {
		name  string
		in    jira.SprintInput
		field string
	}{
		{
			name:  "no board to put it on",
			in:    jira.SprintInput{Name: "EX Sprint 10"},
			field: "originBoardId",
		},
		{
			name:  "a name that is only spaces",
			in:    jira.SprintInput{BoardID: testBoard, Name: "   "},
			field: "name",
		},
		{
			name:  "an end before the start",
			in:    jira.SprintInput{BoardID: testBoard, Name: "EX Sprint 10", Start: &late, End: &early},
			field: "endDate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			_, err := c.CreateSprint(t.Context(), tt.in)
			var rejected *jira.ValidationError
			if !errors.As(err, &rejected) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, named := rejected.For(tt.field); !named {
				t.Errorf("the refusal says %q and never names %s, so a form has nothing to annotate", rejected.Error(), tt.field)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site was asked %d times for a call that had nothing to send", served)
			}
		})
	}
}

func TestUpdateSprint_SendsOnlyTheFieldsThePatchNames(t *testing.T) {
	t.Parallel()

	name := "EX Sprint 8"
	goal := "Ship the field cache."
	blank := ""
	end := time.Date(2026, 2, 9, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		patch  jira.SprintPatch
		keys   []string
		values map[string]string
	}{
		{name: "a goal on its own", patch: jira.SprintPatch{Goal: &goal}, keys: []string{"goal"},
			values: map[string]string{"goal": goal}},
		{name: "a rename on its own", patch: jira.SprintPatch{Name: &name}, keys: []string{"name"},
			values: map[string]string{"name": name}},
		{name: "a goal emptied on purpose", patch: jira.SprintPatch{Goal: &blank}, keys: []string{"goal"},
			values: map[string]string{"goal": ""}},
		{name: "a rename and a new end date", patch: jira.SprintPatch{Name: &name, End: &end}, keys: []string{"endDate", "name"},
			values: map[string]string{"name": name, "endDate": "2026-02-09T08:00:00.000+00:00"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			got, err := c.UpdateSprint(t.Context(), testActiveSprint, tt.patch)
			if err != nil {
				t.Fatalf("updating a sprint: %v", err)
			}
			if got.Name == "" {
				t.Error("the answer carries no name; a patch that names one field must not blank the others")
			}
			body := sentBody(t, sentTo(t, s, http.MethodPost, "/rest/agile/1.0/sprint/42"))
			if keys := sortedKeys(body); !slices.Equal(keys, tt.keys) {
				t.Errorf("the update sent %v, want %v: every extra key is a field the site would overwrite", keys, tt.keys)
			}
			for key, want := range tt.values {
				if sent, _ := body[key].(string); sent != want {
					t.Errorf("the update sent %s=%q, want %q", key, sent, want)
				}
			}
		})
	}
}

func TestUpdateSprint_RefusesToMoveTheDatesOfASprintThatIsOver(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		patch  jira.SprintPatch
		fields []string
	}{
		{name: "a new start", patch: jira.SprintPatch{Start: &when}, fields: []string{"startDate"}},
		{name: "a new end", patch: jira.SprintPatch{End: &when}, fields: []string{"endDate"}},
		{name: "both of them", patch: jira.SprintPatch{Start: &when, End: &when}, fields: []string{"startDate", "endDate"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			_, err := c.UpdateSprint(t.Context(), testClosedSprint, tt.patch)
			var rejected *jira.ValidationError
			if !errors.As(err, &rejected) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			for _, field := range tt.fields {
				if _, named := rejected.For(field); !named {
					t.Errorf("the refusal says %q and never names %s", rejected.Error(), field)
				}
			}
			for _, sent := range s.Requests() {
				if sent.Method == http.MethodPost {
					t.Errorf("the refusal still wrote to %s", sent.Path)
				}
			}
		})
	}
}

func TestUpdateSprint_RenamesWithoutReadingTheSprintFirst(t *testing.T) {
	t.Parallel()

	name := "EX Sprint 8"
	c, s := sprintClient(t)

	if _, err := c.UpdateSprint(t.Context(), testClosedSprint, jira.SprintPatch{Name: &name}); err != nil {
		t.Fatalf("renaming a closed sprint: %v", err)
	}
	if served := len(s.Requests()); served != 1 {
		t.Errorf("a rename cost %d requests; only a patch that moves a date has a state to check", served)
	}
}

func TestUpdateSprint_RefusesAPatchThatNamesNothingRatherThanSendingAnEmptyOne(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t)

	_, err := c.UpdateSprint(t.Context(), testActiveSprint, jira.SprintPatch{})
	var rejected *jira.ValidationError
	if !errors.As(err, &rejected) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site was asked %d times to change nothing", served)
	}
}

func TestSprintWrites_NeverReachThePutThatNullsEveryOmittedField(t *testing.T) {
	t.Parallel()

	name := "EX Sprint 8"
	c, s := sprintClient(t,
		jiratest.WithHandler(http.MethodPut, testSprintRoute, jsonHandler(http.StatusOK, `{"id":42}`)),
		jiratest.WithHandler(http.MethodPut, testSprintNewRoute, jsonHandler(http.StatusOK, `{"id":42}`)),
	)

	writes := []struct {
		name string
		run  func() error
	}{
		{name: "create", run: func() error {
			_, err := c.CreateSprint(t.Context(), jira.SprintInput{BoardID: testBoard, Name: name})
			return err
		}},
		{name: "update", run: func() error {
			_, err := c.UpdateSprint(t.Context(), testActiveSprint, jira.SprintPatch{Name: &name})
			return err
		}},
		{name: "start", run: func() error {
			_, err := c.StartSprint(t.Context(), testDatedSprint)
			return err
		}},
		{name: "complete", run: func() error {
			_, err := c.CompleteSprint(t.Context(), testActiveSprint)
			return err
		}},
		{name: "move into a sprint", run: func() error {
			return c.MoveToSprint(t.Context(), testActiveSprint, []string{testIssueKey})
		}},
		{name: "move to the backlog", run: func() error {
			return c.MoveToBacklog(t.Context(), []string{testIssueKey})
		}},
	}
	for _, write := range writes {
		if err := write.run(); err != nil {
			t.Fatalf("the %s write: %v", write.name, err)
		}
	}

	for _, sent := range s.Requests() {
		if sent.Method == http.MethodPut {
			t.Errorf("a write reached PUT %s, which nulls every field the body leaves out", sent.Path)
		}
	}
}

func TestStartSprint_RefusesEveryMoveTheStateMachineDoesNotHave(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		id     int64
		fields []string
	}{
		{name: "a sprint that is already closed", id: testClosedSprint, fields: []string{"state"}},
		{name: "a sprint that is already active", id: testActiveSprint, fields: []string{"state"}},
		{name: "a future sprint nobody has dated", id: testBlankSprint, fields: []string{"startDate", "endDate"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			_, err := c.StartSprint(t.Context(), tt.id)
			var rejected *jira.ValidationError
			if !errors.As(err, &rejected) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			for _, field := range tt.fields {
				if _, named := rejected.For(field); !named {
					t.Errorf("the refusal says %q and never names %s", rejected.Error(), field)
				}
			}
			for _, sent := range s.Requests() {
				if sent.Method == http.MethodPost {
					t.Errorf("the refusal still wrote to %s; the state machine is checked here so that it does not cost a round trip", sent.Path)
				}
			}
		})
	}
}

func TestStartSprint_MovesADatedFutureSprintAndSendsNothingButTheState(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t, jiratest.WithFixture(http.MethodPost, testSprintRoute, "sprint_updated.json"))

	got, err := c.StartSprint(t.Context(), testDatedSprint)
	if err != nil {
		t.Fatalf("starting a dated future sprint: %v", err)
	}
	if got.State != jira.SprintActive {
		t.Errorf("got state %q back, want the active one the site reported", got.State)
	}
	body := sentBody(t, sentTo(t, s, http.MethodPost, "/rest/agile/1.0/sprint/44"))
	if keys := sortedKeys(body); !slices.Equal(keys, []string{"state"}) {
		t.Errorf("starting a sprint sent %v, want state alone", keys)
	}
	if state, _ := body["state"].(string); state != string(jira.SprintActive) {
		t.Errorf("starting a sprint sent state %q", state)
	}
}

func TestCompleteSprint_ClosesAnActiveSprintAndNeverWritesTheCompletionDate(t *testing.T) {
	t.Parallel()

	c, s := sprintClient(t, jiratest.WithFixture(http.MethodPost, testSprintRoute, "sprint_one.json"))

	got, err := c.CompleteSprint(t.Context(), testActiveSprint)
	if err != nil {
		t.Fatalf("completing an active sprint: %v", err)
	}
	if got.State != jira.SprintClosed || got.Complete == nil {
		t.Errorf("got %q with a completion date of %v, want a closed sprint with one", got.State, got.Complete)
	}
	body := sentBody(t, sentTo(t, s, http.MethodPost, "/rest/agile/1.0/sprint/42"))
	if keys := sortedKeys(body); !slices.Equal(keys, []string{"state"}) {
		t.Errorf("completing a sprint sent %v; completeDate is not writable and a request carrying one is refused whole", keys)
	}
	if state, _ := body["state"].(string); state != string(jira.SprintClosed) {
		t.Errorf("completing a sprint sent state %q", state)
	}
}

func TestCompleteSprint_RefusesASprintThatIsNotRunning(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		id   int64
	}{
		{name: "one that is already closed", id: testClosedSprint},
		{name: "one that has not started", id: testBlankSprint},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			_, err := c.CompleteSprint(t.Context(), tt.id)
			var rejected *jira.ValidationError
			if !errors.As(err, &rejected) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, named := rejected.For("state"); !named {
				t.Errorf("the refusal says %q and never names state", rejected.Error())
			}
			for _, sent := range s.Requests() {
				if sent.Method == http.MethodPost {
					t.Errorf("the refusal still wrote to %s", sent.Path)
				}
			}
		})
	}
}

// sprintMoveCall is one of the two calls that chunk, so every chunking rule is
// asserted about both of them.
type sprintMoveCall struct {
	name  string
	route string
	path  string
	run   func(ctx context.Context, c *Client, keys []string) error
}

func sprintMoveCalls() []sprintMoveCall {
	return []sprintMoveCall{
		{
			name: "moving issues into a sprint", route: testSprintIssuesRoute, path: "/rest/agile/1.0/sprint/42/issue",
			run: func(ctx context.Context, c *Client, keys []string) error {
				return c.MoveToSprint(ctx, testActiveSprint, keys)
			},
		},
		{
			name: "moving issues to the backlog", route: testBacklogRoute, path: testBacklogRoute,
			run: func(ctx context.Context, c *Client, keys []string) error {
				return c.MoveToBacklog(ctx, keys)
			},
		},
	}
}

// issueKeys is n keys, which is how a test asks for more than one call's worth.
func issueKeys(n int) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, "EX-"+strconv.Itoa(i))
	}
	return out
}

func movedIn(t *testing.T, s *jiratest.Server, path string) [][]string {
	t.Helper()

	var out [][]string
	for _, sent := range s.Requests() {
		if sent.Path != path {
			continue
		}
		body := sentBody(t, sent)
		raw, ok := body["issues"].([]any)
		if !ok {
			t.Fatalf("the move to %s sent %v rather than a list of issues", path, body)
		}
		chunk := make([]string, 0, len(raw))
		for _, key := range raw {
			text, isText := key.(string)
			if !isText {
				t.Fatalf("the move to %s named an issue as %T", path, key)
			}
			chunk = append(chunk, text)
		}
		out = append(out, chunk)
	}
	return out
}

func TestMoveIssues_ChunksAtTheCapTheEndpointsShare(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintMoveCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)
			keys := issueKeys(120)

			if err := tc.run(t.Context(), c, keys); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			chunks := movedIn(t, s, tc.path)
			if len(chunks) != 3 {
				t.Fatalf("120 issues went in %d calls, want 3 of at most %d", len(chunks), sprintMoveChunk)
			}
			var sent []string
			for i, chunk := range chunks {
				if len(chunk) > sprintMoveChunk {
					t.Errorf("call %d carried %d issues; over the cap the endpoint refuses the call whole", i+1, len(chunk))
				}
				sent = append(sent, chunk...)
			}
			if !slices.Equal(sent, keys) {
				t.Errorf("the chunks between them moved %d issues, want the %d asked for", len(sent), len(keys))
			}
		})
	}
}

func TestMoveIssues_SaysWhichIssuesMovedWhenAChunkPartWayThroughIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintMoveCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			c, s := sprintClient(t, jiratest.WithHandler(http.MethodPost, tc.route, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls < 3 {
					jsonHandler(http.StatusNoContent, "")(w, r)
					return
				}
				jsonHandler(http.StatusForbidden, `{"errorMessages":["You do not have permission to edit these issues."],"errors":{}}`)(w, r)
			}))
			// Four chunks with the third refused, so that stopping and
			// carrying on are two different numbers of calls.
			keys := issueKeys(200)

			err := tc.run(t.Context(), c, keys)
			var partial *jira.PartialMoveError
			if !errors.As(err, &partial) {
				t.Fatalf("got %T (%v), want a *jira.PartialMoveError: a half-moved backlog is a real state", err, err)
			}
			if !slices.Equal(partial.Moved, keys[:100]) {
				t.Errorf("it reports %d issues moved, want the 100 the first two calls took", len(partial.Moved))
			}
			if !slices.Equal(partial.Pending, keys[100:]) {
				t.Errorf("it reports %d issues left, want the 100 the refusal stopped", len(partial.Pending))
			}
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("the partial move hides the refusal underneath it: %v", err)
			}
			if !strings.Contains(refused.Error(), "permission to edit these issues") {
				t.Errorf("the reason lost the site's own wording: %q", refused.Error())
			}
			if chunks := len(movedIn(t, s, tc.path)); chunks != 3 {
				t.Errorf("the walk made %d calls; it stops at the first refusal rather than sending the rest into the same one", chunks)
			}
		})
	}
}

func TestMoveIssues_AFirstChunkRefusedIsTheRefusalAndNothingMore(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintMoveCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := sprintClient(t, jiratest.WithStatus(http.MethodPost, tc.route, http.StatusForbidden, "plans_403.json"))

			err := tc.run(t.Context(), c, issueKeys(60))
			var partial *jira.PartialMoveError
			if errors.As(err, &partial) {
				t.Errorf("nothing moved, and it reports a partial move of %d issues", len(partial.Moved))
			}
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
		})
	}
}

func TestMoveIssues_MovingNothingAsksTheSiteForNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintMoveCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			if err := tc.run(t.Context(), c, nil); err != nil {
				t.Fatalf("moving no issues: %v", err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("moving nothing cost %d requests", served)
			}
		})
	}
}

func TestMoveIssues_RefusesAListHoldingSomethingThatIsNotAKey(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintMoveCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			err := tc.run(t.Context(), c, []string{"EX-1", "  ", "EX-3"})
			var rejected *jira.ValidationError
			if !errors.As(err, &rejected) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("a list with a blank key still moved %d chunks", served)
			}
		})
	}
}

// sprintCall is one adapter method under test, with the route a failure is
// planted on. The two that read before they write appear twice: once with the
// failure on the read they make first, and once with it on the write, where the
// read answers and only the second leg fails.
type sprintCall struct {
	name    string
	method  string
	route   string
	decodes bool
	run     func(ctx context.Context, c *Client) error
}

func sprintCalls() []sprintCall {
	name := "EX Sprint 8"
	return []sprintCall{
		{
			name: "listing a board's sprints", method: http.MethodGet, route: testSprintsRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				page, err := c.Sprints(ctx, testBoard)
				if err != nil {
					return err
				}
				_, err = jira.Collect(ctx, page, 0)
				return err
			},
		},
		{
			name: "reading one sprint", method: http.MethodGet, route: testSprintRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Sprint(ctx, testDatedSprint)
				return err
			},
		},
		{
			name: "creating a sprint", method: http.MethodPost, route: testSprintNewRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.CreateSprint(ctx, jira.SprintInput{BoardID: testBoard, Name: name})
				return err
			},
		},
		{
			name: "updating a sprint", method: http.MethodPost, route: testSprintRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.UpdateSprint(ctx, testActiveSprint, jira.SprintPatch{Name: &name})
				return err
			},
		},
		{
			name: "starting a sprint", method: http.MethodGet, route: testSprintRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.StartSprint(ctx, testDatedSprint)
				return err
			},
		},
		{
			name: "completing a sprint", method: http.MethodGet, route: testSprintRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.CompleteSprint(ctx, testActiveSprint)
				return err
			},
		},
		{
			name: "starting a sprint, with the read healthy and the write refused", method: http.MethodPost, route: testSprintRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.StartSprint(ctx, testDatedSprint)
				return err
			},
		},
		{
			name: "completing a sprint, with the read healthy and the write refused", method: http.MethodPost, route: testSprintRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.CompleteSprint(ctx, testActiveSprint)
				return err
			},
		},
		{
			name: "moving issues into a sprint", method: http.MethodPost, route: testSprintIssuesRoute,
			run: func(ctx context.Context, c *Client) error {
				return c.MoveToSprint(ctx, testActiveSprint, []string{testIssueKey})
			},
		},
		{
			name: "moving issues to the backlog", method: http.MethodPost, route: testBacklogRoute,
			run: func(ctx context.Context, c *Client) error {
				return c.MoveToBacklog(ctx, []string{testIssueKey})
			},
		},
	}
}

func TestSprints_ARefusalBecomesTheSentenceTheUserReads(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := `{"errorMessages":[],"errors":{"rapidViewId":"You do not have permission to manage sprints on this board."}}`
			c, _ := sprintClient(t, jiratest.WithHandler(tc.method, tc.route, jsonHandler(http.StatusForbidden, body)))

			err := tc.run(t.Context(), c)
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			if !strings.Contains(refused.Error(), "permission to manage sprints") {
				t.Errorf("the reason lost the sentence the Agile API hid under errors: %q", refused.Error())
			}
		})
	}
}

func TestSprints_ARateLimitCarriesTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := sprintClient(t, jiratest.WithRateLimit(tc.method, tc.route, 30*time.Second))

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

func TestSprints_ARejectedRequestNamesTheFieldTheSiteNamed(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := `{"errorMessages":[],"errors":{"startDate":"Datum liegt in der Vergangenheit"}}`
			c, _ := sprintClient(t, jiratest.WithHandler(tc.method, tc.route, jsonHandler(http.StatusBadRequest, body)))

			err := tc.run(t.Context(), c)
			var rejected *jira.ValidationError
			if !errors.As(err, &rejected) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if message, named := rejected.For("startDate"); !named || message == "" {
				t.Errorf("the refusal says %q; the site's own words are the only thing that separates the reasons", rejected.Error())
			}
		})
	}
}

func TestSprints_ATransportFailureIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := sprintClient(t, jiratest.WithHandler(tc.method, tc.route,
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

func TestSprints_ABodyThisClientCannotReadIsATransportFailure(t *testing.T) {
	t.Parallel()

	bodies := []struct {
		name string
		body string
	}{
		{name: "JSON that stops half way", body: `{"startAt":0,"values":[`},
		{name: "a page that is a bare array", body: `["not an envelope"]`},
		// One body a list read and a member read both reach the date through.
		{name: "a date in a layout no Jira API writes", body: `{"id":44,"state":"future","startDate":"02/02/2026",
			"values":[{"id":44,"state":"future","startDate":"02/02/2026"}]}`},
	}

	for _, tc := range sprintCalls() {
		if !tc.decodes {
			continue
		}
		for _, body := range bodies {
			t.Run(tc.name+"/"+body.name, func(t *testing.T) {
				t.Parallel()

				c, _ := sprintClient(t, jiratest.WithHandler(tc.method, tc.route, jsonHandler(http.StatusOK, body.body)))

				err := tc.run(t.Context(), c)
				var down *jira.TransportError
				if !errors.As(err, &down) {
					t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
				}
			})
		}
	}
}

func TestSprints_TheSiteThatNeverAnsweredIsATransportFailureWithNoStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer()
			dead := s.URL()
			s.Close()
			c, _ := testClient(t, dead, WithRetry(RetryPolicy{Attempts: 1}))

			err := tc.run(t.Context(), c)
			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if down.Status != 0 {
				t.Errorf("a host that never answered reports HTTP %d", down.Status)
			}
		})
	}
}

func TestSprints_ReturnTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	for _, tc := range sprintCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := tc.run(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("a cancelled caller still reached the site %d times", served)
			}
		})
	}
}

func TestSprints_ACancellationMidFlightComesBackAsTheCallersOwnError(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, testSprintsRoute, func(_ http.ResponseWriter, r *http.Request) {
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
		_, err := c.Sprints(ctx, testBoard)
		failed <- err
	}()

	receive(t, "the request to reach the site", arrived)
	cancel()
	if err := receive(t, "the cancelled call to come back", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
}

func TestCreateSprint_IsNeverReplayedAfterAServerFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithStatus(http.MethodPost, testSprintNewRoute, http.StatusBadGateway, ""))
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 4, Base: time.Millisecond, Max: time.Millisecond}))

	if _, err := c.CreateSprint(t.Context(), jira.SprintInput{BoardID: testBoard, Name: "EX Sprint 10"}); err == nil {
		t.Fatal("a 502 on a create came back as a success")
	}
	if served := len(s.Requests()); served != 1 {
		t.Errorf("the create was sent %d times; a replayed 502 is a second sprint on the board", served)
	}
}

func TestMoveToSprint_IsNeverReplayedAfterAServerFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithStatus(http.MethodPost, testSprintIssuesRoute, http.StatusBadGateway, ""))
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 4, Base: time.Millisecond, Max: time.Millisecond}))

	if err := c.MoveToSprint(t.Context(), testActiveSprint, []string{testIssueKey}); err == nil {
		t.Fatal("a 502 on a move came back as a success")
	}
	if served := len(s.Requests()); served != 1 {
		t.Errorf("the move was sent %d times", served)
	}
}

func TestSprintCalls_RefuseAnIdThatIdentifiesNothingWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(ctx context.Context, c *Client) error
	}{
		{name: "listing the sprints of board zero", run: func(ctx context.Context, c *Client) error {
			_, err := c.Sprints(ctx, 0)
			return err
		}},
		{name: "reading sprint zero", run: func(ctx context.Context, c *Client) error {
			_, err := c.Sprint(ctx, 0)
			return err
		}},
		{name: "updating sprint zero", run: func(ctx context.Context, c *Client) error {
			name := "EX Sprint 8"
			_, err := c.UpdateSprint(ctx, 0, jira.SprintPatch{Name: &name})
			return err
		}},
		{name: "starting sprint zero", run: func(ctx context.Context, c *Client) error {
			_, err := c.StartSprint(ctx, 0)
			return err
		}},
		{name: "completing sprint zero", run: func(ctx context.Context, c *Client) error {
			_, err := c.CompleteSprint(ctx, 0)
			return err
		}},
		{name: "moving issues into sprint zero", run: func(ctx context.Context, c *Client) error {
			return c.MoveToSprint(ctx, 0, []string{testIssueKey})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := sprintClient(t)

			err := tt.run(t.Context(), c)
			var rejected *jira.ValidationError
			if !errors.As(err, &rejected) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site was asked %d times about a sprint nothing can name", served)
			}
		})
	}
}

func TestPartialMoveError_SaysHowFarItGot(t *testing.T) {
	t.Parallel()

	err := &jira.PartialMoveError{
		Op:      "POST " + backlogIssuesPath,
		Moved:   issueKeys(50),
		Pending: issueKeys(3),
		Err:     fmt.Errorf("refused"),
	}
	if got := err.Error(); !strings.Contains(got, "50 of 53") {
		t.Errorf("the message reads %q and never says how far it got", got)
	}
	if !errors.Is(err, err.Err) {
		t.Error("the failure underneath is unreachable, so nothing can classify it")
	}
}

func sortedKeys(body map[string]any) []string {
	out := make([]string, 0, len(body))
	for key := range body {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}
