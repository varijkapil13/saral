package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// What moving issues between projects adds to this adapter's claims.
var (
	_ jira.TaskWatcher = (*Client)(nil)
	_ jira.Relocator   = (*Client)(nil)
)

const (
	bulkMovePath = "/rest/api/3/bulk/issues/move"
	// bulkQueueRoot is where a bulk operation reports its progress, and it is
	// not where taskRoot's registry reports: the two answer bodies that do not
	// decode as each other, and only the endpoint says which one is coming.
	bulkQueueRoot = "/rest/api/3/bulk/queue/"
	taskRoot      = "/rest/api/3/task/"
	// bulkMoveMax is the endpoint's own cap on one submission.
	bulkMoveMax = 1000
)

func bulkQueuePath(id string) string { return bulkQueueRoot + url.PathEscape(id) }

// apiBulkMove is the move payload. One call moves issues from anywhere to a
// single project and issue type, which is the one target the port describes.
type apiBulkMove struct {
	SendBulkNotification   bool                     `json:"sendBulkNotification"`
	TargetToSourcesMapping map[string]apiMoveTarget `json:"targetToSourcesMapping"`
}

// apiMoveTarget is one destination. All four infer flags are required, and each
// one says whether the mapping beside it is being sent: the endpoint documents a
// remap as defined only when its flag is false, so a payload carrying both is
// contradicting itself.
type apiMoveTarget struct {
	InferClassificationDefaults bool              `json:"inferClassificationDefaults"`
	InferFieldDefaults          bool              `json:"inferFieldDefaults"`
	InferStatusDefaults         bool              `json:"inferStatusDefaults"`
	InferSubtaskTypeDefault     bool              `json:"inferSubtaskTypeDefault"`
	IssueIDsOrKeys              []string          `json:"issueIdsOrKeys"`
	TargetMandatoryFields       []apiMoveFields   `json:"targetMandatoryFields,omitempty"`
	TargetStatus                []apiMoveStatuses `json:"targetStatus,omitempty"`
}

// apiMoveFields is one target group's mandatory-field values.
type apiMoveFields struct {
	Fields map[string]apiMandatoryField `json:"fields"`
}

// apiMandatoryField is one value in the shape this endpoint takes, which is not
// the shape the edit endpoint takes: every value is wrapped, a raw one is a list
// of strings however few it holds, and a rich-text one says so and carries the
// document. retain is the alternative to a value — it keeps the source issue's
// — so it is false wherever a value is being sent.
type apiMandatoryField struct {
	Retain bool            `json:"retain"`
	Type   string          `json:"type"`
	Value  json.RawMessage `json:"value"`
}

// The two field types the move endpoint takes.
const (
	moveFieldRaw = "raw"
	moveFieldADF = "adf"
)

// apiMoveStatuses is the remap the endpoint takes: keyed by the target status
// id, holding the source status ids that land on it.
type apiMoveStatuses struct {
	Statuses map[string][]string `json:"statuses"`
}

// apiBulkSubmit is the whole of what a submit answers.
type apiBulkSubmit struct {
	TaskID flexString `json:"taskId"`
}

// BulkMove submits a cross-project move and returns the task that runs it.
//
// This endpoint is the only way an issue changes project — a field write cannot
// — and it is asynchronous, so nothing has moved when this returns. The ref
// carries the endpoint that reports the move's progress, because the submit
// answers a bare task id with no link in it and the two progress registries are
// not interchangeable.
//
// Three things about the blast radius a caller cannot discover from the request:
//
//   - Subtasks of every issue named travel with their parents and are retyped to
//     the target project's own subtask type. They also count towards the
//     thousand the endpoint takes, so a move of nine hundred parents can be
//     refused by the site although this refuses nothing.
//   - Any value in Fields opts the whole group out of retaining mandatory
//     fields from the source, so the set has to hold every mandatory field of
//     the target and not only the ones the caller wants to change.
//   - Any entry in StatusMap does the same for statuses: it must then map every
//     source status the target's workflow does not have.
func (c *Client) BulkMove(ctx context.Context, in jira.MoveRequest) (jira.TaskRef, error) {
	body, err := bulkMoveBody(in)
	if err != nil {
		return jira.TaskRef{}, err
	}
	r := request{
		method: http.MethodPost,
		path:   bulkMovePath,
		body:   body,
		kind:   "the bulk move endpoint",
		id:     bulkMovePath,
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		return jira.TaskRef{}, bulkMoveRefusal(err)
	}
	var submitted apiBulkSubmit
	if err := resp.decode(r.op(), &submitted); err != nil {
		return jira.TaskRef{}, err
	}
	id := strings.TrimSpace(string(submitted.TaskID))
	if id == "" {
		return jira.TaskRef{}, &jira.TransportError{
			Op:     r.op(),
			Status: resp.status,
			Err:    errors.New("the move was accepted without naming a task, so there is nothing left to report its progress from"),
		}
	}
	return jira.TaskRef{ID: id, URL: c.endpoint(request{path: bulkQueuePath(id)})}, nil
}

// Task reports on a long-running task, at whichever progress endpoint its ref
// names.
//
// A bulk operation reports on its own queue and everything else on the generic
// task registry. Those two bodies share three key names, agree on the type of
// none of them, and each reads as a mostly-empty version of the other — so the
// shape is chosen by the endpoint, and an answer that is not the one the
// endpoint promises fails the call rather than arriving half-decoded.
func (c *Client) Task(ctx context.Context, ref jira.TaskRef) (jira.TaskStatus, error) {
	endpoint, err := c.taskEndpoint(ref)
	if err != nil {
		return jira.TaskStatus{}, err
	}
	r := request{
		method: http.MethodGet,
		path:   endpoint.path,
		kind:   "task",
		id:     strings.TrimSpace(ref.ID),
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		if endpoint.bulk {
			return jira.TaskStatus{}, bulkMoveRefusal(err)
		}
		return jira.TaskStatus{}, err
	}
	if endpoint.bulk {
		var body apiBulkProgress
		if err := resp.decode(r.op(), &body); err != nil {
			return jira.TaskStatus{}, err
		}
		if body.TaskID == "" || body.Status == "" {
			return jira.TaskStatus{}, notTheProgressShape(r, resp.status, "taskId and status", "a bulk operation's queue")
		}
		return body.domain(ref), nil
	}
	var body apiTaskProgress
	if err := resp.decode(r.op(), &body); err != nil {
		return jira.TaskStatus{}, err
	}
	if body.ID == "" || body.Status == "" {
		return jira.TaskStatus{}, notTheProgressShape(r, resp.status, "id and status", "the task registry")
	}
	return body.domain(ref), nil
}

// progressEndpoint is where a task is polled and which of the two progress
// shapes answers there.
type progressEndpoint struct {
	path string
	bulk bool
}

// taskEndpoint reads the progress endpoint off a ref.
//
// A submit answers an id and no link, so the ref's URL is the only record of
// which registry the task is in. Rebuilding a path from the id alone would pick
// one of the two at random, which is why a ref without a URL is refused instead
// of guessed at.
func (c *Client) taskEndpoint(ref jira.TaskRef) (progressEndpoint, error) {
	raw := strings.TrimSpace(ref.URL)
	if raw == "" {
		return progressEndpoint{}, invalidField("task",
			"this task carries no progress endpoint; poll the ref the submit returned rather than one rebuilt from the id")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return progressEndpoint{}, invalidField("task", "the task's progress endpoint is not a URL: "+raw)
	}
	if parsed.Host != "" && parsed.Host != c.base.Host {
		return progressEndpoint{}, invalidField("task",
			"the task is on "+parsed.Host+" and this client is connected to "+c.base.Host)
	}
	path := strings.TrimPrefix(parsed.Path, c.base.Path)
	switch {
	case len(path) > len(bulkQueueRoot) && strings.HasPrefix(path, bulkQueueRoot):
		return progressEndpoint{path: path, bulk: true}, nil
	case len(path) > len(taskRoot) && strings.HasPrefix(path, taskRoot):
		return progressEndpoint{path: path}, nil
	default:
		return progressEndpoint{}, invalidField("task",
			"the task's progress endpoint is neither the bulk queue nor the task registry: "+raw)
	}
}

// apiBulkProgress is what the bulk queue answers.
//
// Every count is absent when it is zero, so nothing here may be required. Its
// clock is not read at all: the port carries no instant for a task, and the
// published schema calls those three keys date-time strings while a site sends
// epoch millis.
type apiBulkProgress struct {
	TaskID                          flexString          `json:"taskId"`
	Status                          string              `json:"status"`
	ProgressPercent                 int                 `json:"progressPercent"`
	TotalIssueCount                 int                 `json:"totalIssueCount"`
	ProcessedAccessibleIssues       []json.RawMessage   `json:"processedAccessibleIssues"`
	FailedAccessibleIssues          map[string][]string `json:"failedAccessibleIssues"`
	InvalidOrInaccessibleIssueCount int                 `json:"invalidOrInaccessibleIssueCount"`
}

// domain reports the queue's progress. Failed carries issue ids and not issue
// keys, because ids are what the body holds and nothing on it turns one into a
// key.
func (p apiBulkProgress) domain(ref jira.TaskRef) jira.TaskStatus {
	out := jira.TaskStatus{
		Ref:      jira.TaskRef{ID: string(p.TaskID), URL: ref.URL},
		State:    jira.TaskState(p.Status),
		Progress: percent(p.ProgressPercent),
		Message:  p.counts(),
	}
	if len(p.FailedAccessibleIssues) > 0 {
		out.Failed = slices.Sorted(maps.Keys(p.FailedAccessibleIssues))
	}
	return out
}

// counts is the sentence to put in front of somebody, built from the numbers
// because the queue body carries no prose and a task's own prose is not safe to
// show: a description has reported zero issues for a run of sixty, and a message
// is sometimes an unresolved i18n key.
//
// The total gates that sentence: a body carries both counts or neither, never a
// processed list without the total it is out of, so a task part way through says
// nothing about issues rather than reporting none processed beside a progress bar
// at 65 per cent.
func (p apiBulkProgress) counts() string {
	parts := make([]string, 0, 3)
	if p.TotalIssueCount > 0 {
		parts = append(parts,
			strconv.Itoa(len(p.ProcessedAccessibleIssues))+" of "+strconv.Itoa(p.TotalIssueCount)+" issues processed")
	}
	if failed := len(p.FailedAccessibleIssues); failed > 0 {
		parts = append(parts, strconv.Itoa(failed)+" failed")
	}
	if p.InvalidOrInaccessibleIssueCount > 0 {
		parts = append(parts, strconv.Itoa(p.InvalidOrInaccessibleIssueCount)+" invalid or not visible to this account")
	}
	return strings.Join(parts, ", ")
}

// apiTaskProgress is what the generic task registry answers. result is unread:
// its shape belongs to an operation a poller cannot identify.
type apiTaskProgress struct {
	ID       flexString `json:"id"`
	Status   string     `json:"status"`
	Message  string     `json:"message"`
	Progress int        `json:"progress"`
}

func (p apiTaskProgress) domain(ref jira.TaskRef) jira.TaskStatus {
	return jira.TaskStatus{
		Ref:      jira.TaskRef{ID: string(p.ID), URL: ref.URL},
		State:    jira.TaskState(p.Status),
		Progress: percent(p.Progress),
		Message:  prose(p.Message),
	}
}

// notTheProgressShape is a 200 from a progress endpoint that answered the other
// registry's body. Both are valid JSON, so this is not a decode failure: it is
// an answer this client cannot use, which is what a transport error is for.
func notTheProgressShape(r request, status int, missing, registry string) error {
	return &jira.TransportError{
		Op:     r.op(),
		Status: status,
		Err:    errors.New("the answer carries no " + missing + ", so it is not the progress " + registry + " reports"),
	}
}

// bulkMoveBody builds the payload for one target.
//
// The mapping is keyed by the destination project and issue type joined with a
// comma, and a duplicate key is dropped without failing the request, so a comma
// in either half is refused here rather than left to corrupt the key.
func bulkMoveBody(in jira.MoveRequest) (apiBulkMove, error) {
	keys, err := moveKeys(in.Keys)
	if err != nil {
		return apiBulkMove{}, err
	}
	project := strings.TrimSpace(in.TargetProjectKey)
	issueType := strings.TrimSpace(in.TargetIssueTypeID)
	switch {
	case project == "":
		return apiBulkMove{}, invalidField("project", "a target project is required to move an issue")
	case issueType == "":
		return apiBulkMove{}, invalidField("issuetype",
			"a target issue type id is required; resolve it from createmeta rather than by name")
	case strings.Contains(project, ","), strings.Contains(issueType, ","):
		return apiBulkMove{}, invalidField("project",
			"neither the target project nor the target issue type may contain a comma: the endpoint keys its mapping by project,issuetype")
	}
	statuses, err := moveStatuses(in.StatusMap)
	if err != nil {
		return apiBulkMove{}, err
	}
	fields, err := moveFields(in.Fields)
	if err != nil {
		return apiBulkMove{}, err
	}

	target := apiMoveTarget{
		// The port carries no data classification, so the target's own stands.
		InferClassificationDefaults: true,
		InferFieldDefaults:          len(fields) == 0,
		InferStatusDefaults:         len(statuses) == 0,
		// A subtask left behind when its parent leaves is orphaned.
		InferSubtaskTypeDefault: true,
		IssueIDsOrKeys:          keys,
	}
	if len(fields) > 0 {
		target.TargetMandatoryFields = []apiMoveFields{{Fields: fields}}
	}
	if len(statuses) > 0 {
		target.TargetStatus = []apiMoveStatuses{{Statuses: statuses}}
	}
	return apiBulkMove{
		SendBulkNotification:   in.Notify,
		TargetToSourcesMapping: map[string]apiMoveTarget{project + "," + issueType: target},
	}, nil
}

func moveKeys(keys []string) ([]string, error) {
	switch {
	case len(keys) == 0:
		return nil, invalidField("issues", "a bulk move needs at least one issue")
	case len(keys) > bulkMoveMax:
		return nil, invalidField("issues",
			"a bulk move takes at most "+strconv.Itoa(bulkMoveMax)+" issues counting the subtasks that travel with them, and this one names "+
				strconv.Itoa(len(keys))+" before any subtask")
	}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			return nil, invalidField("issues", "an issue to move has no key")
		}
		out = append(out, trimmed)
	}
	return out, nil
}

// moveStatuses inverts the port's from-to remap into the shape the endpoint
// takes. Both halves are ids: one site held four pairs of distinct status ids
// sharing a display name, so a status matched by name is matched wrongly.
func moveStatuses(in []jira.StatusMapping) (map[string][]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(in))
	seen := make(map[string]string, len(in))
	for _, mapping := range in {
		source := strings.TrimSpace(mapping.FromStatusID)
		target := strings.TrimSpace(mapping.ToStatusID)
		switch {
		case source == "":
			return nil, invalidField("status",
				"a status remap names no status to remap; statuses are remapped by id, never by name")
		case target == "":
			return nil, invalidField("status",
				"the status "+source+" is remapped to nothing; statuses are remapped by id, never by name")
		}
		if already, dup := seen[source]; dup {
			if already == target {
				continue
			}
			return nil, invalidField("status", "the status "+source+" is remapped to both "+already+" and "+target)
		}
		seen[source] = target
		out[target] = append(out[target], source)
	}
	return out, nil
}

// unmovableFields are the two fields a move takes from the request and not from
// a field value. unwritableFields refuses the same two for an edit, with
// sentences that send the reader to BulkMove, which is no use inside it.
var unmovableFields = map[string]string{
	"project": "the project an issue moves to is MoveRequest.TargetProjectKey, not a field value",
	"status":  "a status is remapped by MoveRequest.StatusMap, never written as a field value",
}

// moveFields encodes the mandatory-field values one target group carries.
//
// Nothing here reuses the edit endpoint's encoder. That one writes what PUT
// /issue/{key} takes — 5 for a number, {"id": …} for an option, an ADF document
// bare — and this endpoint takes every value wrapped in {retain, type, value}
// with a raw one as a list of strings. The two shapes are close enough that a
// payload built by the wrong encoder is accepted by the schema and wrong on the
// wire.
func moveFields(in jira.FieldSet) (map[string]apiMandatoryField, error) {
	ids := in.IDs()
	out := make(map[string]apiMandatoryField, len(ids))
	for _, id := range ids {
		if reason, refused := unmovableFields[id]; refused {
			return nil, invalidField(id, reason)
		}
		value, held := in.ByID(id)
		if !held {
			continue
		}
		encoded, err := moveFieldValue(id, value)
		if err != nil {
			return nil, err
		}
		out[id] = encoded
	}
	return out, nil
}

func moveFieldValue(id string, v jira.FieldValue) (apiMandatoryField, error) {
	if v.Kind == jira.KindDoc {
		doc, err := adf.Marshal(v.Doc)
		if err != nil {
			return apiMandatoryField{}, fmt.Errorf("cloud: encoding %s: %w", id, err)
		}
		return apiMandatoryField{Type: moveFieldADF, Value: doc}, nil
	}
	values, err := moveFieldStrings(id, v)
	if err != nil {
		return apiMandatoryField{}, err
	}
	return apiMandatoryField{Type: moveFieldRaw, Value: mustJSON(values)}, nil
}

// moveFieldStrings renders a value as the list of strings a raw mandatory field
// takes. An option and an account travel by id, because an id is the same on a
// site in any language and a label is not.
func moveFieldStrings(id string, v jira.FieldValue) ([]string, error) {
	switch v.Kind {
	case jira.KindText:
		if strings.TrimSpace(v.Text) == "" {
			return nil, emptyMandatory(id)
		}
		return []string{v.Text}, nil
	case jira.KindNumber:
		return []string{strconv.FormatFloat(v.Number, 'f', -1, 64)}, nil
	case jira.KindBool:
		return []string{strconv.FormatBool(v.Bool)}, nil
	case jira.KindDate:
		if v.Date.IsZero() {
			return nil, emptyMandatory(id)
		}
		return []string{v.Date.String()}, nil
	case jira.KindTime:
		if v.Time.IsZero() {
			return nil, emptyMandatory(id)
		}
		return []string{v.Time.Format(platformTimeLayout)}, nil
	case jira.KindOption, jira.KindOptions:
		return moveOptionStrings(id, v.Options)
	case jira.KindUser, jira.KindUsers:
		return moveUserStrings(id, v.Users)
	case jira.KindEmpty:
		return nil, emptyMandatory(id)
	case jira.KindUnknown:
		return nil, invalidField(id,
			"this value came off the wire as JSON this client cannot type, and a move sends a mandatory field as text: leave it out and set it with UpdateIssue once the move has finished")
	default:
		return nil, invalidField(id, "this client has no way to move a value of this kind")
	}
}

func moveOptionStrings(id string, options []jira.Option) ([]string, error) {
	if len(options) == 0 {
		return nil, emptyMandatory(id)
	}
	out := make([]string, 0, len(options))
	for _, option := range options {
		if len(option.Children) > 0 {
			return nil, invalidField(id,
				"a cascading option cannot be moved: this endpoint takes one list of ids, which cannot say which child belongs under which parent")
		}
		switch {
		case option.ID != "":
			out = append(out, option.ID)
		case option.Label != "":
			out = append(out, option.Label)
		default:
			return nil, emptyMandatory(id)
		}
	}
	return out, nil
}

func moveUserStrings(id string, users []jira.User) ([]string, error) {
	if len(users) == 0 {
		return nil, emptyMandatory(id)
	}
	out := make([]string, 0, len(users))
	for _, user := range users {
		if user.AccountID == "" {
			return nil, invalidField(id, "an account with no id cannot be moved onto a field; resolve it with FindPeople first")
		}
		out = append(out, user.AccountID)
	}
	return out, nil
}

// emptyMandatory refuses a value that means nothing for a field the target
// requires, which is the one thing targetMandatoryFields exists to fill.
func emptyMandatory(id string) error {
	return invalidField(id,
		"a mandatory field cannot be moved with nothing in it; give it a value, or name no field at all and every one of them keeps what the source issue holds")
}

// bulkMoveRefusal names the capability on a 403, so that a view can hide the
// action with the site's own sentence instead of reporting a failure. The queue
// wears it as well as the submit: both are gated on the global Bulk Change
// permission, and the generic task registry is not.
func bulkMoveRefusal(err error) error {
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) || refused.Capability != "" {
		return err
	}
	return &jira.CapabilityError{Capability: jira.CapBulkMove, Reason: refused.Reason}
}

// percent keeps the port's promise that progress is a percentage: a site that
// reports 143 has not moved 143 per cent of anything.
func percent(n int) int {
	return min(max(n, 0), 100)
}

// i18nKeyRoots are the namespaces the unresolved keys a task has been seen to
// send begin with. A key is not localised — it is the identifier the site failed
// to translate — so matching one is not matching on wording, which is the rule
// no error text may break.
var i18nKeyRoots = []string{"task", "bulk"}

// prose drops a message that is an unresolved i18n key rather than a sentence.
// Jira sends task.progress.cancellation.requested where the site's own wording
// should have been, and that is worse in front of somebody than nothing at all,
// because the state already says what happened.
//
// The shape alone is not enough to go on: design.spec.pdf and a bare URL are
// dotted, spaceless and lower case too, and dropping either loses a sentence.
func prose(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n") {
		return trimmed
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) < 3 || !slices.Contains(i18nKeyRoots, parts[0]) {
		return trimmed
	}
	for _, part := range parts {
		if part == "" || part != strings.ToLower(part) {
			return trimmed
		}
	}
	return ""
}
