package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

const boardPath = "/rest/agile/1.0/board"

// boardPageSize is how many boards one request asks for.
const boardPageSize = 50

// boardBound is how many boards a walk will take before it stops. A project has
// a handful; the bound is there so that an endpoint answering something
// unexpected cannot become an unbounded read on a first-paint path.
const boardBound = 200

// boardIssuePageSize is the page length a board issue read asks for when the
// caller names none. The Agile API echoes the number sent rather than capping it
// silently, so it is a length and not a suggestion.
const boardIssuePageSize = 50

var _ jira.BoardReader = (*Client)(nil)

func boardConfigPath(boardID int64) string {
	return boardPath + "/" + strconv.FormatInt(boardID, 10) + "/configuration"
}

func boardIssuesPath(boardID int64) string {
	return boardPath + "/" + strconv.FormatInt(boardID, 10) + "/issue"
}

func boardBacklogPath(boardID int64) string {
	return boardPath + "/" + strconv.FormatInt(boardID, 10) + "/backlog"
}

// Boards lists the boards that draw on a project.
//
// The project key narrows the listing rather than scoping it, and it is required
// here because a call without one lists every board on the site, which is not a
// first-paint read. A board built from a saved filter is listed when that filter
// names the project, and it arrives carrying no location at all, so
// Board.ProjectKey is empty on one and no caller may key a board by its project.
//
// A board's type is whatever the site called it. A team-managed board reports
// "simple", so the value is passed through rather than matched against a list.
func (c *Client) Boards(ctx context.Context, projectKey string) ([]jira.Board, error) {
	project := strings.TrimSpace(projectKey)
	if project == "" {
		return nil, &jira.ValidationError{Fields: []jira.FieldError{{
			Field:   "projectKey",
			Message: "a project is required: a listing with no project asks the site for every board it has",
		}}}
	}

	query := url.Values{"projectKeyOrId": []string{project}}
	op := http.MethodGet + " " + boardPath
	page, err := offsetPages(ctx, c, func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   boardPath,
			query:  pagedQuery(query, startAt, boardPageSize),
			kind:   "project",
			id:     project,
		}
	}, func(resp *response) ([]jira.Board, int, bool, error) {
		rows, total, isLast, err := decodeAgilePage[apiBoard](resp, op)
		if err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.Board, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.domain())
		}
		return out, total, isLast, nil
	})
	if err != nil {
		return nil, boardRefusal(err)
	}
	boards, err := jira.Collect(ctx, page, boardBound)
	if err != nil {
		return nil, boardRefusal(err)
	}
	return boards, nil
}

// BoardConfig reads a board's columns, estimation field and rank field.
//
// Everything in the answer is optional and an absence is an ordinary answer
// rather than an unset value, so nothing here is defaulted into existence. A
// Kanban board sends no estimation object at all, which is a different answer
// from a Scrum board that turned estimation off and reports type "none": the
// first leaves BoardConfig.Estimation nil, the second fills it. A board ordered
// by its filter sends an empty ranking object, so rank is detected on the field
// id inside it and never on the object being there; without one the order comes
// from the saved filter and rows cannot be reordered at all.
//
// The statuses a column maps arrive as bare ids: no name, no category, and no
// hint that a live status may be mapped to no column at all. A status is
// resolved to its category through the catalogue, never through this response,
// and a column's identity is its position — two columns on one board may share
// a localised name, and one column may span two categories.
func (c *Client) BoardConfig(ctx context.Context, boardID int64) (jira.BoardConfig, error) {
	path := boardConfigPath(boardID)
	var body apiBoardConfig
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   path,
		kind:   "board",
		id:     strconv.FormatInt(boardID, 10),
	}, &body)
	if err != nil {
		return jira.BoardConfig{}, boardRefusal(err)
	}
	return body.domain(boardID), nil
}

// BoardIssues lists what a board is showing.
//
// The endpoint applies the board's saved filter and its column mapping at the
// site, which is the whole reason for this read: the filter is arbitrary JQL
// only the site can run, and BoardConfig reports its id and nothing else. The
// order is the board's rank order, which is why nothing here sorts.
//
// What the endpoint does not apply is the board's own sub-query — a board whose
// sub-query hid resolved work on released versions answered eighteen issues
// against the sixteen on screen — so BoardQuery.SubQuery goes out as the
// endpoint's jql parameter, alongside any of the board's own quick filters the
// caller has toggled on. Each term is bracketed: the site ANDs the parameter
// onto the board's filter, and a term with an OR at the top of it would widen
// the board rather than narrow it. See boardJQL.
//
// The answer carries no schema block, so a custom field arrives typed by the
// shape of its value.
func (c *Client) BoardIssues(ctx context.Context, boardID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	return c.boardIssuePages(ctx, boardIssuesPath(boardID), boardID, q)
}

// BoardBacklog lists a board's backlog, which is the same endpoint shape with a
// different set behind it: the issues the board's filter matches that no active
// or future sprint holds. The site decides that, and nothing about it can be
// read off a page of issues.
func (c *Client) BoardBacklog(ctx context.Context, boardID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	return c.boardIssuePages(ctx, boardBacklogPath(boardID), boardID, q)
}

func (c *Client) boardIssuePages(ctx context.Context, path string, boardID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	if err := boardIDCheck(boardID); err != nil {
		return jira.Page[jira.Issue]{}, err
	}
	fields := uniqueStrings(q.Fields)
	if len(fields) == 0 {
		return jira.Page[jira.Issue]{}, errBoardIssuesNeedFields()
	}
	mask := jira.NewFieldMask(fields)
	query := url.Values{"fields": []string{strings.Join(fields, ",")}}
	if jql := boardJQL(q); jql != "" {
		query.Set("jql", jql)
	}
	size := boardIssuePageSize
	if q.MaxResults > 0 {
		size = q.MaxResults
	}
	id := strconv.FormatInt(boardID, 10)
	op := http.MethodGet + " " + path
	// The refusal is named inside the fetch rather than around the walk, so that
	// a 403 on the fourth page reads the way the one on the first page does.
	return jira.Offset(ctx, func(ctx context.Context, startAt int) ([]jira.Issue, int, bool, error) {
		resp, err := c.do(ctx, request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(query, startAt, size),
			kind:   "board",
			id:     id,
		})
		if err != nil {
			return nil, -1, false, boardRefusal(err)
		}
		rows, total, isLast, err := decodeAgilePage[apiIssue](resp, op)
		if err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.Issue, 0, len(rows))
		for i := range rows {
			out = append(out, decodeIssue(rows[i], nil, mask))
		}
		return out, total, isLast, nil
	})
}

// boardJQL is the endpoint's jql parameter: the sub-query and every toggled
// quick filter, each bracketed and ANDed, in that order. Bracketing each term
// separately is what SubQuery already relies on — an OR at the top of one term
// would otherwise widen the board rather than narrow it — and a quick filter's
// JQL is no safer to trust unbracketed than the sub-query is.
func boardJQL(q jira.BoardQuery) string {
	terms := make([]string, 0, 1+len(q.QuickFilters))
	if sub := strings.TrimSpace(q.SubQuery); sub != "" {
		terms = append(terms, "("+sub+")")
	}
	for _, qf := range q.QuickFilters {
		if qf = strings.TrimSpace(qf); qf != "" {
			terms = append(terms, "("+qf+")")
		}
	}
	return strings.Join(terms, " AND ")
}

func errBoardIssuesNeedFields() error {
	return &jira.ValidationError{Fields: []jira.FieldError{{
		Field:   "fields",
		Message: "a board issue read must name the fields it wants; the endpoint answers with every field the site has without them",
	}}}
}

// apiBoard is one row of the board collection. location is a pointer because a
// board built from a saved filter carries no location key at all.
type apiBoard struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Location *struct {
		ProjectKey string `json:"projectKey"`
	} `json:"location"`
}

func (b apiBoard) domain() jira.Board {
	out := jira.Board{ID: b.ID, Name: b.Name, Type: jira.BoardType(b.Type)}
	if b.Location != nil {
		out.ProjectKey = b.Location.ProjectKey
	}
	return out
}

// apiBoardConfig is the board configuration. Estimation is a pointer because a
// Kanban board sends none at all, which is a different answer from a Scrum board
// reporting type "none".
type apiBoardConfig struct {
	ID           int64               `json:"id"`
	Name         string              `json:"name"`
	Type         string              `json:"type"`
	Filter       apiBoardFilter      `json:"filter"`
	SubQuery     apiBoardSubQuery    `json:"subQuery"`
	ColumnConfig apiBoardColumns     `json:"columnConfig"`
	Estimation   *apiBoardEstimation `json:"estimation"`
	Ranking      apiBoardRanking     `json:"ranking"`
}

func (b apiBoardConfig) domain(boardID int64) jira.BoardConfig {
	out := jira.BoardConfig{
		BoardID:     b.ID,
		Name:        b.Name,
		Type:        jira.BoardType(b.Type),
		RankFieldID: b.Ranking.fieldID(),
		FilterID:    string(b.Filter.ID),
		SubQuery:    strings.TrimSpace(b.SubQuery.Query),
	}
	if out.BoardID == 0 {
		out.BoardID = boardID
	}
	if columns := b.ColumnConfig.Columns; len(columns) > 0 {
		out.Columns = make([]jira.Column, 0, len(columns))
		for _, column := range columns {
			out.Columns = append(out.Columns, column.domain())
		}
	}
	if b.Estimation != nil {
		estimation := b.Estimation.domain()
		out.Estimation = &estimation
	}
	return out
}

// apiBoardFilter is the saved filter behind the board. Its id is read leniently:
// the Agile API writes the same kind of id as a string in one call and as a
// number in another.
type apiBoardFilter struct {
	ID flexString `json:"id"`
}

type apiBoardSubQuery struct {
	Query string `json:"query"`
}

type apiBoardColumns struct {
	Columns []apiBoardColumn `json:"columns"`
}

// apiBoardColumn is one column. min and max are independently optional and stay
// pointers, because a constraint of zero is a constraint and not an absence.
type apiBoardColumn struct {
	Name     string           `json:"name"`
	Min      *int             `json:"min"`
	Max      *int             `json:"max"`
	Statuses []apiBoardStatus `json:"statuses"`
}

func (col apiBoardColumn) domain() jira.Column {
	out := jira.Column{Name: col.Name, Min: col.Min, Max: col.Max}
	for _, status := range col.Statuses {
		if id := string(status.ID); id != "" {
			out.StatusIDs = append(out.StatusIDs, id)
		}
	}
	return out
}

// apiBoardStatus is a status mapped into a column: an id and a self link, with
// no name and no category.
type apiBoardStatus struct {
	ID flexString `json:"id"`
}

// apiBoardEstimation is the estimation half, which a Kanban board omits
// entirely. The field id is read verbatim: a board may estimate in a system
// field with no customfield_ prefix.
type apiBoardEstimation struct {
	Type  string               `json:"type"`
	Field *apiBoardEstimateFor `json:"field"`
}

func (e apiBoardEstimation) domain() jira.Estimation {
	out := jira.Estimation{Type: jira.EstimationType(e.Type)}
	if e.Field != nil {
		out.Field = jira.FieldRef{ID: e.Field.FieldID, Name: e.Field.DisplayName}
	}
	return out
}

// apiBoardEstimateFor is the field a board estimates in. displayName moved under
// a language switch while the fieldId beside it did not, so the name is for
// rendering and the id is what identifies the field.
type apiBoardEstimateFor struct {
	FieldID     string `json:"fieldId"`
	DisplayName string `json:"displayName"`
}

// apiBoardRanking is the ranking half. A board ordered by its filter sends this
// object empty rather than omitting it.
type apiBoardRanking struct {
	RankCustomFieldID flexString `json:"rankCustomFieldId"`
}

// fieldID is the field id the rest of Jira spells this field with. The Agile API
// reports the bare custom field number here, while the field catalogue, an issue
// payload and JQL all name the same field customfield_<number>. A value that is
// not a positive integer names no field, which is a board ordered by its filter
// rather than a configuration that failed to read.
func (r apiBoardRanking) fieldID() string {
	number, err := strconv.ParseInt(string(r.RankCustomFieldID), 10, 64)
	if err != nil || number <= 0 {
		return ""
	}
	return "customfield_" + strconv.FormatInt(number, 10)
}

// boardRefusal names the capability on a 403, so a caller can tell the one
// refusal it can act on — hide the board view and say why — from every other way
// a call can fail.
func boardRefusal(err error) error {
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) || refused.Capability != "" {
		return err
	}
	return &jira.CapabilityError{Capability: jira.CapBoards, Reason: refused.Reason}
}
