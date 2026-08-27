package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// boardConfigRoute is the mux pattern the fixture server registers the
// configuration endpoint under. Overriding a default route needs the wildcard
// spelled exactly as the default spells it, or both patterns are registered and
// the mux panics on the ambiguity.
const boardConfigRoute = "/rest/agile/1.0/board/{id}/configuration"

// boardTestID is the board the estimation fixture describes.
const boardTestID = 10

func boardClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

// boardConfigAnswering serves a configuration body of the test's own writing, for
// the shapes no committed fixture carries.
func boardConfigAnswering(body string) jiratest.ServerOption {
	return jiratest.WithHandler(http.MethodGet, boardConfigRoute, jsonHandler(http.StatusOK, body))
}

// boardConfigWith wraps the parts of a configuration a case cares about in the
// keys the endpoint always sends, so a case reads as the one thing it is about.
func boardConfigWith(parts string) string { return boardConfigOfType("scrum", parts) }

func boardConfigOfType(boardType, parts string) string {
	body := `"id":10,"name":"EX board","type":"` + boardType + `","filter":{"id":"10001"}`
	if parts != "" {
		body += "," + parts
	}
	return "{" + body + "}"
}

// boardForbidden is what a site refusing the board endpoints answers. No
// committed fixture carries a board 403.
const boardForbidden = `{"errorMessages":["This account may not view the board."],"errors":{}}`

// boardFixtureRankID is the rank field the committed configuration fixture names,
// spelled the way the rest of Jira spells a field and read from the fixture so
// the assertion cannot drift from it.
func boardFixtureRankID(t *testing.T) string {
	t.Helper()

	var body struct {
		Ranking struct {
			RankCustomFieldID int64 `json:"rankCustomFieldId"`
		} `json:"ranking"`
	}
	if err := json.Unmarshal(fixtureBytes(t, "board_config_estimation.json"), &body); err != nil {
		t.Fatalf("decoding board_config_estimation.json: %v", err)
	}
	if body.Ranking.RankCustomFieldID <= 0 {
		t.Fatal("board_config_estimation.json names no rank field, which is the shape being read here")
	}
	return "customfield_" + strconv.FormatInt(body.Ranking.RankCustomFieldID, 10)
}

func boardQueryOn(t *testing.T, s *jiratest.Server, path string) url.Values {
	t.Helper()

	sent := sentTo(t, s, http.MethodGet, path)
	query, err := url.ParseQuery(sent.Query)
	if err != nil {
		t.Fatalf("reading the query of %s: %v", path, err)
	}
	return query
}

func TestBoards_ReadsEveryBoardDrawingOnAProjectWithTheTypeTheSiteReported(t *testing.T) {
	t.Parallel()

	c, s := boardClient(t)
	got, err := c.Boards(t.Context(), "EX")
	if err != nil {
		t.Fatalf("listing the boards on EX: %v", err)
	}

	want := []jira.Board{
		{ID: 10, Name: "EX Scrum board", Type: jira.BoardScrum, ProjectKey: "EX"},
		{ID: 11, Name: "EX Kanban board", Type: jira.BoardKanban, ProjectKey: "EX"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("the boards read as %+v, want %+v", got, want)
	}

	query := boardQueryOn(t, s, boardPath)
	if query.Get("projectKeyOrId") != "EX" {
		t.Errorf("the request narrowed by %q, want the project it was asked about", query.Get("projectKeyOrId"))
	}
	if query.Get("maxResults") != strconv.Itoa(boardPageSize) {
		t.Errorf("maxResults = %q, want the page size this walk asks for; a length the site chooses moves when the site does",
			query.Get("maxResults"))
	}
}

// A board built from a saved filter carries no location key at all, and a
// team-managed one reports a third type. Neither may cost the board.
func TestBoards_KeepsABoardWithNoProjectAndATypeNobodyExpected(t *testing.T) {
	t.Parallel()

	const body = `{"startAt":0,"maxResults":50,"total":3,"isLast":true,"values":[
		{"id":12,"name":"Delivery across teams","type":"kanban"},
		{"id":13,"name":"Team board","type":"simple","location":{"projectKey":"EX","projectId":10000}},
		{"id":14,"name":"Something new","type":"squad"}
	]}`

	c, _ := boardClient(t, jiratest.WithHandler(http.MethodGet, boardPath, jsonHandler(http.StatusOK, body)))
	got, err := c.Boards(t.Context(), "EX")
	if err != nil {
		t.Fatalf("listing the boards: %v", err)
	}

	want := []jira.Board{
		{ID: 12, Name: "Delivery across teams", Type: jira.BoardKanban},
		{ID: 13, Name: "Team board", Type: jira.BoardSimple, ProjectKey: "EX"},
		{ID: 14, Name: "Something new", Type: jira.BoardType("squad")},
	}
	if !slices.Equal(got, want) {
		t.Errorf("the boards read as %+v, want %+v", got, want)
	}
}

func TestBoards_RefuseWithoutAProjectRatherThanAskingTheSiteForEveryBoard(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"", "   "} {
		t.Run("a project key of "+strconv.Quote(key), func(t *testing.T) {
			t.Parallel()

			c, s := boardClient(t)
			got, err := c.Boards(t.Context(), key)
			if got != nil {
				t.Errorf("the refusal came back with %+v attached, want no boards at all", got)
			}
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, named := invalid.For("projectKey"); !named {
				t.Errorf("the refusal says %v and does not name projectKey, which is the field to focus", invalid.Fields)
			}
			if served := s.Requests(); len(served) != 0 {
				t.Errorf("the site was sent %v, and there was nothing to ask it", served)
			}
		})
	}
}

func TestBoards_WalkEveryPageOfTheThreeShapesTheAgileAPIAnswersIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		total   bool
		isLast  bool
		offsets []string
	}{
		{
			name:  "a total and an isLast, the shape the board collection sends",
			total: true, isLast: true, offsets: []string{"", "2", "4"},
		},
		{
			name:  "a total and no isLast, so the count ends the walk",
			total: true, offsets: []string{"", "2", "4"},
		},
		{
			name:   "an isLast and no total, so the flag ends it",
			isLast: true, offsets: []string{"", "2", "4"},
		},
		{
			name:    "neither, so the short last page ends it",
			offsets: []string{"", "2", "4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := boardClient(t, jiratest.WithHandler(http.MethodGet, boardPath,
				boardPages(5, 2, tt.total, tt.isLast)))
			got, err := c.Boards(t.Context(), "EX")
			if err != nil {
				t.Fatalf("walking the boards: %v", err)
			}
			if len(got) != 5 {
				t.Fatalf("the walk gathered %d boards, want all 5: %+v", len(got), got)
			}
			for i, board := range got {
				if board.ID != int64(i) {
					t.Errorf("board %d is id %d, want the pages in order", i, board.ID)
				}
			}
			offsets := make([]string, 0, len(tt.offsets))
			for _, sent := range s.Requests() {
				query, err := url.ParseQuery(sent.Query)
				if err != nil {
					t.Fatalf("reading a recorded query: %v", err)
				}
				offsets = append(offsets, query.Get("startAt"))
			}
			if !slices.Equal(offsets, tt.offsets) {
				t.Errorf("the walk asked for offsets %v, want %v", offsets, tt.offsets)
			}
		})
	}
}

func TestBoards_StopAtTheBoundRatherThanWalkingASiteThatNeverEnds(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithHandler(http.MethodGet, boardPath,
		boardPages(boardBound+50, 50, false, false)))
	got, err := c.Boards(t.Context(), "EX")
	if err != nil {
		t.Fatalf("walking the boards: %v", err)
	}
	if len(got) != boardBound {
		t.Errorf("the walk gathered %d boards, want it to stop at the bound of %d", len(got), boardBound)
	}
}

// A caller handed half a walk with an error beside it cannot tell it from a
// project with half as many boards, so a refusal on page two keeps nothing.
func TestBoards_KeepNoBoardsFromAWalkThatFailedPartWayThrough(t *testing.T) {
	t.Parallel()

	pages := boardPages(4, 2, false, false)
	c, s := boardClient(t, jiratest.WithHandler(http.MethodGet, boardPath,
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("startAt") == "" {
				pages(w, r)
				return
			}
			jsonHandler(http.StatusForbidden, boardForbidden)(w, r)
		}))
	got, err := c.Boards(t.Context(), "EX")

	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
	if refused.Capability != jira.CapBoards {
		t.Errorf("the refusal names %q, want %q", refused.Capability, jira.CapBoards)
	}
	if got != nil {
		t.Errorf("the refusal came back with %+v attached, want no boards at all", got)
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the walk made %d requests, want 2: one page, then the refusal", served)
	}
}

func TestBoardConfig_ReadsColumnsEstimationAndRankFromTheBoardsOwnConfiguration(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t)
	got, err := c.BoardConfig(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading the configuration of board %d: %v", boardTestID, err)
	}

	if got.BoardID != boardTestID {
		t.Errorf("BoardID = %d, want %d", got.BoardID, boardTestID)
	}
	if got.Name != "EX Scrum board" || got.Type != jira.BoardScrum {
		t.Errorf("the board reads as %q / %q", got.Name, got.Type)
	}
	if got.FilterID != "10001" {
		t.Errorf("FilterID = %q, want the saved filter behind the board", got.FilterID)
	}
	if got.SubQuery != "" {
		t.Errorf("SubQuery = %q on a board that sent none", got.SubQuery)
	}

	wantColumns := []jira.Column{
		{Name: "Backlog", StatusIDs: []string{"10000"}},
		{Name: "In Review", StatusIDs: []string{"10001"}, Min: ptrTo(1), Max: ptrTo(4)},
		{Name: "Released", StatusIDs: []string{"10002"}},
	}
	assertColumns(t, got.Columns, wantColumns)

	if got.Estimation == nil {
		t.Fatal("Estimation is nil on a board that named the field it estimates in")
	}
	if got.Estimation.Type != jira.EstimationField {
		t.Errorf("Estimation.Type = %q, want %q", got.Estimation.Type, jira.EstimationField)
	}
	if got.Estimation.Field.ID != "customfield_10032" {
		t.Errorf("the estimation field is %q, want the id the site put beside its display name", got.Estimation.Field.ID)
	}
	if got.Estimation.Field.Name != "Story Points" {
		t.Errorf("the estimation field's name is %q, want the site's own wording for it", got.Estimation.Field.Name)
	}
	if !got.Estimates() {
		t.Error("Estimates() is false on a board estimating in a named field")
	}

	if want := boardFixtureRankID(t); got.RankFieldID != want {
		t.Errorf("RankFieldID = %q, want %q, the field id the rest of Jira spells the rank field with",
			got.RankFieldID, want)
	}
	if got.Ordering() != jira.OrderRank {
		t.Errorf("Ordering() = %v, want OrderRank on a board with a rank field", got.Ordering())
	}
}

func TestBoardConfig_ReadsAKanbanBoardThatNeitherEstimatesNorRanks(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithFixture(http.MethodGet, boardConfigRoute, "board_config_no_estimation.json"))
	got, err := c.BoardConfig(t.Context(), 11)
	if err != nil {
		t.Fatalf("reading the configuration of a Kanban board: %v", err)
	}

	if got.Type != jira.BoardKanban {
		t.Errorf("Type = %q, want %q", got.Type, jira.BoardKanban)
	}
	if got.Estimation != nil {
		t.Errorf("Estimation = %+v on a board that sent no estimation object at all", *got.Estimation)
	}
	if got.Estimates() {
		t.Error("Estimates() is true on a board that does not estimate")
	}
	if got.RankFieldID != "" {
		t.Errorf("RankFieldID = %q on a board whose ranking object was empty", got.RankFieldID)
	}
	if got.Ordering() != jira.OrderFilter {
		t.Errorf("Ordering() = %v, want OrderFilter, which is what disables drag-to-reorder", got.Ordering())
	}
	if got.SubQuery != "fixVersion in unreleasedVersions() OR fixVersion is EMPTY" {
		t.Errorf("SubQuery = %q, want the condition deciding which resolved issues the board still shows", got.SubQuery)
	}
	if got.FilterID != "10002" {
		t.Errorf("FilterID = %q, want the saved filter behind the board", got.FilterID)
	}

	assertColumns(t, got.Columns, []jira.Column{
		{Name: "Backlog", StatusIDs: []string{"10000"}},
		{Name: "In Review", StatusIDs: []string{"10001"}, Max: ptrTo(4)},
		{Name: "Released", StatusIDs: []string{"10002"}},
	})
}

// Three states, not two: no estimation object, an object saying "none", and an
// object naming a field. A caller that reads the union without checking cannot
// tell the first from the second, and they mean different things.
func TestBoardConfig_TellsABoardThatDoesNotEstimateApartFromOneThatTurnedItOff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		wantSet   bool
		wantType  jira.EstimationType
		wantField string
		wantMeasr bool
	}{
		{
			name: "no estimation object at all, which is what a Kanban board sends",
			body: `"ranking":{}`,
		},
		{
			name:     "an object saying none, which is a Scrum board that turned it off",
			body:     `"estimation":{"type":"none"},"ranking":{}`,
			wantSet:  true,
			wantType: jira.EstimationNone,
		},
		{
			name:      "a system field, which carries no customfield_ prefix",
			body:      `"estimation":{"type":"field","field":{"fieldId":"timeoriginalestimate","displayName":"Original estimate"}},"ranking":{}`,
			wantSet:   true,
			wantType:  jira.EstimationField,
			wantField: "timeoriginalestimate",
			wantMeasr: true,
		},
		{
			name:     "issue count, which measures without naming a field",
			body:     `"estimation":{"type":"issueCount"},"ranking":{}`,
			wantSet:  true,
			wantType: jira.EstimationIssueCount,
		},
		{
			name:     "a type of field with no field beside it, which measures nothing",
			body:     `"estimation":{"type":"field"},"ranking":{}`,
			wantSet:  true,
			wantType: jira.EstimationField,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := boardClient(t, boardConfigAnswering(boardConfigWith(tt.body)))
			got, err := c.BoardConfig(t.Context(), boardTestID)
			if err != nil {
				t.Fatalf("reading the configuration: %v", err)
			}
			if (got.Estimation != nil) != tt.wantSet {
				t.Fatalf("Estimation = %v, want set to be %v", got.Estimation, tt.wantSet)
			}
			if tt.wantSet {
				if got.Estimation.Type != tt.wantType {
					t.Errorf("Estimation.Type = %q, want %q", got.Estimation.Type, tt.wantType)
				}
				if got.Estimation.Field.ID != tt.wantField {
					t.Errorf("the estimation field is %q, want %q verbatim", got.Estimation.Field.ID, tt.wantField)
				}
			}
			if got.Estimates() != tt.wantMeasr {
				t.Errorf("Estimates() = %v, want %v", got.Estimates(), tt.wantMeasr)
			}
		})
	}
}

// A team-managed board reports a third type, and every optional part of its
// configuration arrives under it. Gating subQuery on kanban, or estimation and
// ranking on scrum, drops the whole answer of the board style neither name
// covers.
func TestBoardConfig_ReadsEveryOptionalPartUnderATypeNeitherNameCovers(t *testing.T) {
	t.Parallel()

	const parts = `"subQuery":{"query":"fixVersion is EMPTY"},
		"columnConfig":{"constraintType":"none","columns":[{"name":"Doing","statuses":[{"id":"10001"}]}]},
		"estimation":{"type":"field","field":{"fieldId":"customfield_10032","displayName":"Story Points"}},
		"ranking":{"rankCustomFieldId":10019}`

	c, _ := boardClient(t, boardConfigAnswering(boardConfigOfType("simple", parts)))
	got, err := c.BoardConfig(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading the configuration of a team-managed board: %v", err)
	}

	if got.Type != jira.BoardSimple {
		t.Errorf("Type = %q, want %q", got.Type, jira.BoardSimple)
	}
	if got.SubQuery != "fixVersion is EMPTY" {
		t.Errorf("SubQuery = %q, want the condition the board sent", got.SubQuery)
	}
	if got.Estimation == nil {
		t.Error("Estimation is nil on a board that named the field it estimates in")
	} else if got.Estimation.Field.ID != "customfield_10032" {
		t.Errorf("the estimation field is %q, want the one the board named", got.Estimation.Field.ID)
	}
	if got.RankFieldID != "customfield_10019" {
		t.Errorf("RankFieldID = %q, want the rank field the board named", got.RankFieldID)
	}
	assertColumns(t, got.Columns, []jira.Column{{Name: "Doing", StatusIDs: []string{"10001"}}})
}

// Rank is detected on the field id, never on the ranking object: a board ordered
// by priority sends the object empty rather than leaving it out.
func TestBoardConfig_DetectsRankOnTheFieldIdAndNotOnTheRankingObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "a rank field, reported as the bare custom field number", body: `"ranking":{"rankCustomFieldId":10019}`, want: "customfield_10019"},
		{name: "an empty ranking object, which is a board ordered by its filter", body: `"ranking":{}`},
		{name: "no ranking object at all", body: ""},
		{name: "a ranking object whose number is zero, which names no field", body: `"ranking":{"rankCustomFieldId":0}`},
		{name: "a ranking object whose number is null", body: `"ranking":{"rankCustomFieldId":null}`},
		{name: "a rank id spelled as a string, which is the other way the Agile API writes an id", body: `"ranking":{"rankCustomFieldId":"10019"}`, want: "customfield_10019"},
		{name: "a rank id that is no number at all, which names no field rather than failing the read", body: `"ranking":{"rankCustomFieldId":"lexo"}`},
		{name: "a negative rank id", body: `"ranking":{"rankCustomFieldId":-1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := boardClient(t, boardConfigAnswering(boardConfigWith(tt.body)))
			got, err := c.BoardConfig(t.Context(), boardTestID)
			if err != nil {
				t.Fatalf("reading the configuration: %v", err)
			}
			if got.RankFieldID != tt.want {
				t.Errorf("RankFieldID = %q, want %q", got.RankFieldID, tt.want)
			}
			wantOrder := jira.OrderFilter
			if tt.want != "" {
				wantOrder = jira.OrderRank
			}
			if got.Ordering() != wantOrder {
				t.Errorf("Ordering() = %v, want %v", got.Ordering(), wantOrder)
			}
		})
	}
}

// A column's identity is its position. Two columns can share a localised name,
// one can map no statuses at all, and a constraint of zero is a constraint.
func TestBoardConfig_KeepsEveryColumnInOrderWhateverItMaps(t *testing.T) {
	t.Parallel()

	const columns = `"columnConfig":{"constraintType":"issueCount","columns":[
		{"name":"In Bearbeitung","statuses":[{"id":"10001"},{"id":"10007"}]},
		{"name":"In Bearbeitung","statuses":[{"id":"10011"}]},
		{"name":"Nothing lives here","statuses":[]},
		{"name":"Nothing mapped at all"},
		{"name":"Nothing may pass","statuses":[{"id":"10002"}],"min":0,"max":0}
	]},"ranking":{}`

	c, _ := boardClient(t, boardConfigAnswering(boardConfigWith(columns)))
	got, err := c.BoardConfig(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}

	assertColumns(t, got.Columns, []jira.Column{
		{Name: "In Bearbeitung", StatusIDs: []string{"10001", "10007"}},
		{Name: "In Bearbeitung", StatusIDs: []string{"10011"}},
		{Name: "Nothing lives here"},
		{Name: "Nothing mapped at all"},
		{Name: "Nothing may pass", StatusIDs: []string{"10002"}, Min: ptrTo(0), Max: ptrTo(0)},
	})
}

// The Agile API writes the same kind of id as a string in one call and as a
// number in another, and one configuration carries three of them: the saved
// filter, the statuses a column maps, and the rank field. No spelling may cost
// the answer, and a status carrying no id at all is not a mapping.
func TestBoardConfig_ReadsAnIDWhicheverWayTheAgileAPIWritesIt(t *testing.T) {
	t.Parallel()

	const body = `{"id":10,"name":"EX board","type":"scrum","filter":{"id":10001},
		"columnConfig":{"columns":[
			{"name":"Mixed","statuses":[
				{"id":"10001","self":"https://example.atlassian.net/rest/api/2/status/10001"},
				{"id":10002},
				{"id":""},
				{"self":"https://example.atlassian.net/rest/api/2/status/10003"}
			]}
		]},
		"ranking":{"rankCustomFieldId":"10019"}}`

	c, _ := boardClient(t, boardConfigAnswering(body))
	got, err := c.BoardConfig(t.Context(), boardTestID)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	if got.FilterID != "10001" {
		t.Errorf("FilterID = %q, want the saved filter behind the board however its id was written", got.FilterID)
	}
	if got.RankFieldID != "customfield_10019" {
		t.Errorf("RankFieldID = %q, want the rank field however its id was written", got.RankFieldID)
	}
	assertColumns(t, got.Columns, []jira.Column{
		{Name: "Mixed", StatusIDs: []string{"10001", "10002"}},
	})
}

func TestBoardConfig_NamesTheBoardItWasAskedAboutWhenTheAnswerDoesNot(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, boardConfigAnswering(`{"name":"EX Scrum board","type":"scrum","ranking":{}}`))
	got, err := c.BoardConfig(t.Context(), 4242)
	if err != nil {
		t.Fatalf("reading the configuration: %v", err)
	}
	if got.BoardID != 4242 {
		t.Errorf("BoardID = %d, want the board that was asked about", got.BoardID)
	}
}

// The Agile API writes its prose under errors, keyed by the name of a URL
// parameter, and sends errorMessages empty. That sentence is the only thing
// separating "no such board" from "not yours", so a 404 has to carry it.
func TestBoardConfig_KeepsTheSentenceAnAgile404HidesUnderAURLParameter(t *testing.T) {
	t.Parallel()

	c, _ := boardClient(t, jiratest.WithStatus(http.MethodGet, boardConfigRoute, http.StatusNotFound, "not_found_board.json"))
	_, err := c.BoardConfig(t.Context(), 99999)

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

// boardCall is one adapter method under one condition, so that every failure
// table below runs over both of them rather than over whichever was written
// first.
type boardCall struct {
	name  string
	route string
	run   func(ctx context.Context, c *Client) error
}

func boardCalls() []boardCall {
	return []boardCall{
		{
			name:  "listing the boards on a project",
			route: boardPath,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Boards(ctx, "EX")
				return err
			},
		},
		{
			name:  "reading one board's configuration",
			route: boardConfigRoute,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.BoardConfig(ctx, boardTestID)
				return err
			},
		},
	}
}

func TestBoards_ReportARefusalAsTheCapabilityItIs(t *testing.T) {
	t.Parallel()

	for _, call := range boardCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			c, _ := boardClient(t, jiratest.WithHandler(http.MethodGet, call.route,
				jsonHandler(http.StatusForbidden, boardForbidden)))
			err := call.run(t.Context(), c)

			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			if refused.Capability != jira.CapBoards {
				t.Errorf("the refusal names %q, want %q so a caller can hide the board view and say why",
					refused.Capability, jira.CapBoards)
			}
			if refused.Reason != "This account may not view the board." {
				t.Errorf("Reason = %q, want the site's own sentence", refused.Reason)
			}
		})
	}
}

func TestBoards_ReportARateLimitWithTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	for _, call := range boardCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			c, _ := boardClient(t, jiratest.WithRateLimit(http.MethodGet, call.route, 30*time.Second))
			err := call.run(t.Context(), c)

			var limited *jira.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
			}
			if limited.RetryAfter != 30*time.Second {
				t.Errorf("RetryAfter = %s, want 30s", limited.RetryAfter)
			}
			if limited.Endpoint == "" {
				t.Error("the rate limit names no endpoint, and a budget is per endpoint rather than per site")
			}
		})
	}
}

func TestBoards_ReportAHostThatNeverAnsweredAndABodyThatIsNotJSON(t *testing.T) {
	t.Parallel()

	for _, call := range boardCalls() {
		t.Run(call.name+", against a host that never answered", func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer()
			dead := s.URL()
			s.Close()
			c, _ := testClient(t, dead, WithRetry(RetryPolicy{Attempts: 1}))

			err := call.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != 0 {
				t.Errorf("Status = %d, want 0: nothing answered", broken.Status)
			}
		})

		t.Run(call.name+", against a body that will not decode", func(t *testing.T) {
			t.Parallel()

			c, _ := boardClient(t, jiratest.WithHandler(http.MethodGet, call.route,
				jsonHandler(http.StatusOK, "<html>your proxy has opinions</html>")))

			err := call.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != http.StatusOK {
				t.Errorf("Status = %d, want 200: the site answered, the body did not parse", broken.Status)
			}
		})
	}
}

func TestBoards_DoNotReachTheSiteOnceTheCallerHasGoneAway(t *testing.T) {
	t.Parallel()

	for _, call := range boardCalls() {
		t.Run(call.name, func(t *testing.T) {
			t.Parallel()

			c, s := boardClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := call.run(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want context.Canceled unwrapped", err)
			}
			if served := s.Requests(); len(served) != 0 {
				t.Errorf("the site was sent %v after the caller had gone", served)
			}
		})
	}
}

func TestBoardConfig_ComesBackWhenTheCallerIsCancelledMidFlight(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, boardConfigRoute,
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
		_, err := c.BoardConfig(ctx, boardTestID)
		failed <- err
	}()

	receive(t, "the request to reach the site", arrived)
	cancel()
	if err := receive(t, "the cancelled read to come back", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled unwrapped", err)
	}
}

// boardPages answers the board collection out of a fixed number of boards,
// reporting whichever of total and isLast the case under test asks for.
func boardPages(boards, pageSize int, reportTotal, reportIsLast bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startAt, err := strconv.Atoi(r.URL.Query().Get("startAt"))
		if err != nil {
			startAt = 0
		}
		type wireBoard struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		end := min(startAt+pageSize, boards)
		values := make([]wireBoard, 0, max(0, end-startAt))
		for id := startAt; id < end; id++ {
			values = append(values, wireBoard{ID: int64(id), Name: "Board " + strconv.Itoa(id), Type: "scrum"})
		}
		page := struct {
			StartAt    int         `json:"startAt"`
			MaxResults int         `json:"maxResults"`
			Total      *int        `json:"total,omitempty"`
			IsLast     *bool       `json:"isLast,omitempty"`
			Values     []wireBoard `json:"values"`
		}{StartAt: startAt, MaxResults: pageSize, Values: values}
		if reportTotal {
			page.Total = &boards
		}
		if reportIsLast {
			last := end >= boards
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

// assertColumns names every column that differs rather than the first, because a
// column config is read as a whole and one wrong column is the finding.
func assertColumns(t *testing.T, got, want []jira.Column) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("read %d columns, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Name != want[i].Name {
			t.Errorf("column %d is named %q, want %q", i, got[i].Name, want[i].Name)
		}
		if !slices.Equal(got[i].StatusIDs, want[i].StatusIDs) {
			t.Errorf("column %d maps %v, want %v", i, got[i].StatusIDs, want[i].StatusIDs)
		}
		if !sameBound(got[i].Min, want[i].Min) {
			t.Errorf("column %d has Min %s, want %s", i, boundText(got[i].Min), boundText(want[i].Min))
		}
		if !sameBound(got[i].Max, want[i].Max) {
			t.Errorf("column %d has Max %s, want %s", i, boundText(got[i].Max), boundText(want[i].Max))
		}
	}
}

func sameBound(got, want *int) bool {
	if got == nil || want == nil {
		return got == want
	}
	return *got == *want
}

func boundText(bound *int) string {
	if bound == nil {
		return "no constraint"
	}
	return strconv.Itoa(*bound)
}

func ptrTo[T any](v T) *T { return &v }
