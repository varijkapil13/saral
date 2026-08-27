package cloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

var (
	_ jira.SprintReader  = (*Client)(nil)
	_ jira.SprintManager = (*Client)(nil)
)

const (
	sprintCollectionPath = "/rest/agile/1.0/sprint"
	backlogIssuesPath    = "/rest/agile/1.0/backlog/issue"
	// sprintPageSize is the page length asked for. The Agile API echoes the
	// number sent rather than capping it silently, so it is a length and not a
	// suggestion.
	sprintPageSize = 50
	// sprintMoveChunk is the most issues either move endpoint accepts. A call
	// over the cap is refused whole, so it is a hard limit.
	sprintMoveChunk = 50
)

// sprintPath is one sprint. The id is an integer, so nothing in it needs escaping.
func sprintPath(id int64) string {
	return sprintCollectionPath + "/" + strconv.FormatInt(id, 10)
}

func sprintIssuesPath(id int64) string {
	return sprintPath(id) + "/issue"
}

func boardSprintsPath(boardID int64) string {
	return "/rest/agile/1.0/board/" + strconv.FormatInt(boardID, 10) + "/sprint"
}

// apiSprint is one sprint as either the list or the member endpoint answers.
//
// Every date is optional, and an absent one means the date is not set rather
// than that the read failed. They arrive UTC-normalised with a Z whatever offset
// was sent, which neither API's own layout parses, hence timestamp.
type apiSprint struct {
	ID            int64     `json:"id"`
	State         string    `json:"state"`
	Name          string    `json:"name"`
	Goal          string    `json:"goal"`
	StartDate     timestamp `json:"startDate"`
	EndDate       timestamp `json:"endDate"`
	CompleteDate  timestamp `json:"completeDate"`
	OriginBoardID int64     `json:"originBoardId"`
}

func (s apiSprint) domain() jira.Sprint {
	return jira.Sprint{
		ID:      s.ID,
		BoardID: s.OriginBoardID,
		Name:    s.Name,
		Goal:    s.Goal,
		// Folded here because every write below refuses on a state it cannot
		// read, and the same three states are spelled upper-case on the sprint
		// field an issue carries.
		State:    jira.SprintState(strings.ToLower(strings.TrimSpace(s.State))),
		Start:    s.StartDate.ptr(),
		End:      s.EndDate.ptr(),
		Complete: s.CompleteDate.ptr(),
	}
}

// onBoard is the sprint as read through a board, which is the board to report
// when the site named no origin board on the entry itself.
func (s apiSprint) onBoard(boardID int64) jira.Sprint {
	out := s.domain()
	if out.BoardID == 0 {
		out.BoardID = boardID
	}
	return out
}

// apiSprintCreate is what a create sends. It names no state: the endpoint makes
// a future sprint, so a state here could only be a wrong one.
type apiSprintCreate struct {
	Name          string  `json:"name"`
	OriginBoardID int64   `json:"originBoardId"`
	Goal          string  `json:"goal,omitempty"`
	StartDate     *string `json:"startDate,omitempty"`
	EndDate       *string `json:"endDate,omitempty"`
}

// apiSprintMove is the body both move endpoints take.
type apiSprintMove struct {
	Issues []string `json:"issues"`
}

// Sprints lists a board's sprints, narrowed to the states named.
//
// The states go on the wire as one comma-separated parameter: the endpoint is
// the only thing that can narrow them, and a client that filters what it walked
// pays for every closed sprint the board has ever held.
func (c *Client) Sprints(ctx context.Context, boardID int64, states ...jira.SprintState) (jira.Page[jira.Sprint], error) {
	if err := boardIDCheck(boardID); err != nil {
		return jira.Page[jira.Sprint]{}, err
	}
	path := boardSprintsPath(boardID)
	query := url.Values{}
	if named := sprintStateParam(states); named != "" {
		query.Set("state", named)
	}
	build := func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(query, startAt, sprintPageSize),
			kind:   "board",
			id:     strconv.FormatInt(boardID, 10),
		}
	}
	op := http.MethodGet + " " + path
	return offsetPages(ctx, c, build, func(resp *response) ([]jira.Sprint, int, bool, error) {
		items, total, isLast, err := decodeAgilePage[apiSprint](resp, op)
		if err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.Sprint, 0, len(items))
		for i := range items {
			out = append(out, items[i].onBoard(boardID))
		}
		return out, total, isLast, nil
	})
}

// Sprint fetches one sprint by id, including its dates.
//
// The sprint value on an issue carries an id and a name and no board to walk
// from, so this is the only route from that id to its dates.
func (c *Client) Sprint(ctx context.Context, id int64) (jira.Sprint, error) {
	if err := sprintIDCheck(id); err != nil {
		return jira.Sprint{}, err
	}
	var out apiSprint
	r := request{
		method: http.MethodGet,
		path:   sprintPath(id),
		kind:   "sprint",
		id:     strconv.FormatInt(id, 10),
	}
	if err := c.doJSON(ctx, r, &out); err != nil {
		return jira.Sprint{}, err
	}
	return out.domain(), nil
}

// CreateSprint creates a future sprint on a board. The answer carries the new
// sprint's id, so nothing is read back afterwards.
func (c *Client) CreateSprint(ctx context.Context, in jira.SprintInput) (jira.Sprint, error) {
	body, err := sprintCreateBody(in)
	if err != nil {
		return jira.Sprint{}, err
	}
	r := request{
		method: http.MethodPost,
		path:   sprintCollectionPath,
		body:   body,
		kind:   "board",
		id:     strconv.FormatInt(in.BoardID, 10),
	}
	var out apiSprint
	if err := c.doJSON(ctx, r, &out); err != nil {
		return jira.Sprint{}, err
	}
	return out.onBoard(in.BoardID), nil
}

// UpdateSprint changes only the fields the patch names; a nil field is left
// exactly as it is.
//
// A closed sprint takes only its name and its goal, so a patch that moves a
// date reads the sprint's state before it sends anything. A rename costs one
// call whatever state the sprint is in.
func (c *Client) UpdateSprint(ctx context.Context, id int64, in jira.SprintPatch) (jira.Sprint, error) {
	if err := sprintIDCheck(id); err != nil {
		return jira.Sprint{}, err
	}
	body, err := sprintPatchBody(in)
	if err != nil {
		return jira.Sprint{}, err
	}
	if in.Start != nil || in.End != nil {
		current, err := c.Sprint(ctx, id)
		if err != nil {
			return jira.Sprint{}, err
		}
		if err := sprintCanRedate(current, in); err != nil {
			return jira.Sprint{}, err
		}
	}
	return c.patchSprint(ctx, id, body)
}

// StartSprint moves a future sprint to active.
//
// The state machine is checked here rather than at the site, so a sprint with no
// dates is a named refusal a form can annotate instead of a localised 400
// nothing may branch on. That check is what the read before the write is for.
func (c *Client) StartSprint(ctx context.Context, id int64) (jira.Sprint, error) {
	current, err := c.Sprint(ctx, id)
	if err != nil {
		return jira.Sprint{}, err
	}
	if err := sprintCanStart(current); err != nil {
		return jira.Sprint{}, err
	}
	return c.patchSprint(ctx, id, map[string]any{"state": string(jira.SprintActive)})
}

// CompleteSprint closes an active sprint. It sends the state and nothing else:
// a completion date is not writable, and a request carrying one is refused
// whole by the site rather than corrected.
func (c *Client) CompleteSprint(ctx context.Context, id int64) (jira.Sprint, error) {
	current, err := c.Sprint(ctx, id)
	if err != nil {
		return jira.Sprint{}, err
	}
	if current.State != jira.SprintActive {
		return jira.Sprint{}, invalidField("state",
			fmt.Sprintf("only an active sprint can be completed, and this one is %s", sprintStateName(current.State)))
	}
	return c.patchSprint(ctx, id, map[string]any{"state": string(jira.SprintClosed)})
}

// MoveToSprint moves issues into a sprint. More than fifty is more than one
// call, and a failure part way through reports as a jira.PartialMoveError.
func (c *Client) MoveToSprint(ctx context.Context, sprintID int64, keys []string) error {
	if err := sprintIDCheck(sprintID); err != nil {
		return err
	}
	return c.moveIssues(ctx, request{
		method: http.MethodPost,
		path:   sprintIssuesPath(sprintID),
		kind:   "sprint",
		id:     strconv.FormatInt(sprintID, 10),
	}, keys)
}

// MoveToBacklog moves issues out of whatever sprint they are in.
func (c *Client) MoveToBacklog(ctx context.Context, keys []string) error {
	return c.moveIssues(ctx, request{
		method: http.MethodPost,
		path:   backlogIssuesPath,
		kind:   "the backlog",
		id:     backlogIssuesPath,
	}, keys)
}

// moveIssues sends keys in chunks the endpoint accepts, stopping at the first
// chunk it refuses: a refusal is about the request and not about the chunk, so
// the fourth call would be refused for whatever refused the third.
func (c *Client) moveIssues(ctx context.Context, r request, keys []string) error {
	wanted, err := sprintMoveKeys(keys)
	if err != nil {
		return err
	}
	if len(wanted) == 0 {
		return nil
	}
	for start := 0; start < len(wanted); start += sprintMoveChunk {
		chunk := wanted[start:min(start+sprintMoveChunk, len(wanted))]
		sent := r
		sent.body = apiSprintMove{Issues: slices.Clone(chunk)}
		if _, err := c.do(ctx, sent); err != nil {
			if start == 0 {
				return err
			}
			return &jira.PartialMoveError{
				Op:      r.op(),
				Moved:   slices.Clone(wanted[:start]),
				Pending: slices.Clone(wanted[start:]),
				Err:     err,
			}
		}
	}
	return nil
}

// patchSprint is the only write this adapter makes to an existing sprint. The
// POST changes what it names and leaves the rest; the PUT beside it would null
// every key the body does not carry.
func (c *Client) patchSprint(ctx context.Context, id int64, body map[string]any) (jira.Sprint, error) {
	r := request{
		method: http.MethodPost,
		path:   sprintPath(id),
		body:   body,
		kind:   "sprint",
		id:     strconv.FormatInt(id, 10),
	}
	var out apiSprint
	if err := c.doJSON(ctx, r, &out); err != nil {
		return jira.Sprint{}, err
	}
	return out.domain(), nil
}

// sprintCanRedate refuses the dates the patch carried rather than both of them.
func sprintCanRedate(sp jira.Sprint, in jira.SprintPatch) error {
	if sp.State != jira.SprintClosed {
		return nil
	}
	var refused []jira.FieldError
	if in.Start != nil {
		refused = append(refused, jira.FieldError{
			Field:   "startDate",
			Message: "a closed sprint takes only its name and its goal",
		})
	}
	if in.End != nil {
		refused = append(refused, jira.FieldError{
			Field:   "endDate",
			Message: "a closed sprint takes only its name and its goal",
		})
	}
	if len(refused) == 0 {
		return nil
	}
	return &jira.ValidationError{Fields: refused}
}

func sprintCanStart(sp jira.Sprint) error {
	if sp.State != jira.SprintFuture {
		return invalidField("state",
			fmt.Sprintf("only a future sprint can be started, and this one is %s", sprintStateName(sp.State)))
	}
	var missing []jira.FieldError
	if sp.Start == nil {
		missing = append(missing, jira.FieldError{
			Field:   "startDate",
			Message: "a sprint cannot start without a start date; set one first",
		})
	}
	if sp.End == nil {
		missing = append(missing, jira.FieldError{
			Field:   "endDate",
			Message: "a sprint cannot start without an end date; set one first",
		})
	}
	if len(missing) > 0 {
		return &jira.ValidationError{Fields: missing}
	}
	return nil
}

// sprintCreateBody refuses what the endpoint would only refuse after a round trip.
func sprintCreateBody(in jira.SprintInput) (apiSprintCreate, error) {
	if in.BoardID <= 0 {
		return apiSprintCreate{}, invalidField("originBoardId", "a board id is required to create a sprint")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return apiSprintCreate{}, invalidField("name", "a sprint needs a name")
	}
	if err := sprintDatesInOrder(in.Start, in.End); err != nil {
		return apiSprintCreate{}, err
	}
	return apiSprintCreate{
		Name:          name,
		OriginBoardID: in.BoardID,
		Goal:          strings.TrimSpace(in.Goal),
		StartDate:     sprintDate(in.Start),
		EndDate:       sprintDate(in.End),
	}, nil
}

// sprintPatchBody is one key per field the patch names and nothing else.
func sprintPatchBody(in jira.SprintPatch) (map[string]any, error) {
	if err := sprintDatesInOrder(in.Start, in.End); err != nil {
		return nil, err
	}
	body := make(map[string]any, 4)
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, invalidField("name", "a sprint needs a name")
		}
		body["name"] = name
	}
	if in.Goal != nil {
		body["goal"] = *in.Goal
	}
	if in.Start != nil {
		body["startDate"] = *sprintDate(in.Start)
	}
	if in.End != nil {
		body["endDate"] = *sprintDate(in.End)
	}
	if len(body) == 0 {
		return nil, &jira.ValidationError{Messages: []string{
			"an update that names no field has nothing to change; a nil field in the patch means leave it alone",
		}}
	}
	return body, nil
}

// sprintMoveKeys trims the keys to move. An empty list is not an error: it is a
// move of nothing, and nothing is what it sends.
func sprintMoveKeys(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			return nil, invalidField("issues", "an issue key in the list is blank")
		}
		out = append(out, trimmed)
	}
	return out, nil
}

// sprintStateParam is the states as the endpoint's own parameter. A blank entry
// is dropped rather than sent: the endpoint refuses the whole request over one
// state it cannot read.
func sprintStateParam(states []jira.SprintState) string {
	named := make([]string, 0, len(states))
	for _, state := range states {
		if trimmed := strings.TrimSpace(string(state)); trimmed != "" {
			named = append(named, trimmed)
		}
	}
	return strings.Join(named, ",")
}

// sprintStateName is a state to put in a sentence, the unrecognised one included.
func sprintStateName(state jira.SprintState) string {
	if state == "" {
		return "in no state the site reported"
	}
	return string(state)
}

// sprintDate is a date as a sprint write sends one: the Agile layout, whose
// offset carries a colon, so a UTC instant goes out as +00:00 rather than Z.
func sprintDate(at *time.Time) *string {
	if at == nil {
		return nil
	}
	written := at.Format(agileTimeLayout)
	return &written
}

func sprintDatesInOrder(start, end *time.Time) error {
	if start == nil || end == nil {
		return nil
	}
	if end.Before(*start) {
		return invalidField("endDate", "a sprint cannot end before it starts")
	}
	return nil
}

func sprintIDCheck(id int64) error {
	if id <= 0 {
		return invalidField("sprintId", "a sprint id is required")
	}
	return nil
}

func boardIDCheck(boardID int64) error {
	if boardID <= 0 {
		return invalidField("boardId", "a board id is required")
	}
	return nil
}
