package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const issuePath = "/rest/api/3/issue"

// issueDetailExpand asks the issue endpoint to say what each field holds.
// Without it a custom field's value is read by its shape alone, which cannot
// tell a number from a number-shaped string or a sprint array from an option
// array.
const issueDetailExpand = "schema"

// transitionFieldExpand is what makes a transition report its screen. Without
// it every transition comes back with an empty fields object and a screen with
// a required field looks like a move that needs nothing.
const transitionFieldExpand = "transitions.fields"

// unwritableFields are the two fields the port promises an update cannot
// change. Neither is refused by the edit endpoint in a way a caller can act on:
// a status written as a field is rejected as not on the screen, and a project
// is accepted and ignored. Refusing them here keeps IssuePatch honest whichever
// route a field ID took into it.
var unwritableFields = map[string]string{
	"status":  "status is not a writable field; move the issue with Transition",
	"project": "an issue cannot change project through an edit; use BulkMove",
}

// apiIssueDetail is one issue read whole. The schema block arrives only because
// the request expanded it, and is keyed by field ID.
type apiIssueDetail struct {
	apiIssue
	Schema map[string]apiFieldSchema `json:"schema"`
}

// apiIssueRef is all the create endpoint answers with: no fields, no status,
// nothing that says what the site stored.
type apiIssueRef struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// apiIssueWrite is the body both the edit and the transition endpoints take.
// Values are held as raw JSON because a field's wire shape depends on what the
// field holds, and the encoder that knows that is fieldJSON.
type apiIssueWrite struct {
	Fields     map[string]json.RawMessage `json:"fields,omitempty"`
	Transition *apiTransitionRef          `json:"transition,omitempty"`
}

type apiTransitionRef struct {
	ID string `json:"id"`
}

// Issue reads one issue.
//
// This is the wide read: the endpoint answers with every field the site
// defines, so the issue comes back with jira.AllFields as its mask. On a site
// with ninety custom fields that is ninety keys per issue, most of them null,
// which is why a list asks through Search with a narrow field set instead and
// this method is for the one issue somebody has open.
func (c *Client) Issue(ctx context.Context, key string) (jira.Issue, error) {
	id, err := issueKey(key)
	if err != nil {
		return jira.Issue{}, err
	}
	r := request{
		method: http.MethodGet,
		path:   issuePath + "/" + id,
		query:  url.Values{"expand": {issueDetailExpand}},
		kind:   "issue",
		id:     id,
	}
	var detail apiIssueDetail
	if err := c.doJSON(ctx, r, &detail); err != nil {
		return jira.Issue{}, err
	}
	return decodeIssue(detail.apiIssue, detail.Schema, jira.AllFields()), nil
}

// CreateIssue creates an issue and reads it back.
//
// The create response carries an id, a key and nothing else, so the issue as
// stored takes a second request — the site fills in the status, the reporter,
// every default and whatever a workflow post-function did.
//
// A failure of that second request is not reported: the issue exists by then,
// and an error would read as "nothing was created" to a caller whose retry
// would make a second one. What comes back instead is the identity the site
// gave it, carrying the zero field mask, which says exactly that no field on
// it was read.
func (c *Client) CreateIssue(ctx context.Context, in jira.IssueInput) (jira.Issue, error) {
	fields, err := createFields(in)
	if err != nil {
		return jira.Issue{}, err
	}
	r := request{
		method: http.MethodPost,
		path:   issuePath,
		body:   apiIssueWrite{Fields: fields},
		kind:   "issue",
		id:     in.ProjectKey,
	}
	var ref apiIssueRef
	if err := c.doJSON(ctx, r, &ref); err != nil {
		return jira.Issue{}, err
	}
	if stored, readBack := c.Issue(ctx, ref.Key); readBack == nil {
		return stored, nil
	}
	return jira.Issue{ID: ref.ID, Key: ref.Key}, nil
}

// UpdateIssue applies a sparse patch through PUT /issue/{key}.
//
// Only what the patch names reaches the wire. A nil pointer contributes no key
// at all, which is the whole reason IssuePatch is shaped the way it is: the
// edit endpoint reads a key set to null as "empty this field", so a patch built
// by reading an issue and writing the struct back blanks every field the read
// never asked for. jira.Issue.Requested is what a caller checks a field against
// before putting it in the patch; this method's half of that bargain is never
// to invent a key.
//
// A patch that would change nothing sends no request. It cannot be expressed on
// the wire — the endpoint refuses an empty fields object — and there is nothing
// to ask the site for.
func (c *Client) UpdateIssue(ctx context.Context, key string, in jira.IssuePatch) error {
	id, err := issueKey(key)
	if err != nil {
		return err
	}
	if in.IsEmpty() {
		return nil
	}
	fields, err := patchFields(in)
	if err != nil {
		return err
	}
	r := request{
		method: http.MethodPut,
		path:   issuePath + "/" + id,
		body:   apiIssueWrite{Fields: fields},
		kind:   "issue",
		id:     id,
	}
	if in.Notify != nil {
		r.query = url.Values{"notifyUsers": {strconv.FormatBool(*in.Notify)}}
	}
	_, err = c.do(ctx, r)
	return err
}

// Transitions lists the moves available on this issue right now.
//
// It is per issue and per moment: which transitions exist depends on the
// issue's current status, on conditions a workflow evaluates against this
// issue, and on who is asking. Nothing here is cacheable as a site-wide
// workflow, and a transition is applied by the id in this answer — the names
// are localised, so a German site calls every one of them something else.
func (c *Client) Transitions(ctx context.Context, key string) ([]jira.Transition, error) {
	id, err := issueKey(key)
	if err != nil {
		return nil, err
	}
	r := request{
		method: http.MethodGet,
		path:   issuePath + "/" + id + "/transitions",
		query:  url.Values{"expand": {transitionFieldExpand}},
		kind:   "issue",
		id:     id,
	}
	var answer struct {
		Transitions []apiTransition `json:"transitions"`
	}
	if err := c.doJSON(ctx, r, &answer); err != nil {
		return nil, err
	}
	out := make([]jira.Transition, 0, len(answer.Transitions))
	for _, t := range answer.Transitions {
		if t.unavailable() {
			continue
		}
		out = append(out, t.domain())
	}
	return out, nil
}

// Transition moves an issue by transition id, filling in the transition screen
// from the patch.
//
// The move itself is the transition; a status written as a field is refused
// here rather than sent, the same as it is on an edit.
func (c *Client) Transition(ctx context.Context, key, transitionID string, in jira.IssuePatch) error {
	id, err := issueKey(key)
	if err != nil {
		return err
	}
	move := strings.TrimSpace(transitionID)
	if move == "" {
		return &jira.ValidationError{Fields: []jira.FieldError{{
			Field:   "transition",
			Message: "a transition id is required; match it by id, never by the localised name",
		}}}
	}
	fields, err := patchFields(in)
	if err != nil {
		return err
	}
	r := request{
		method: http.MethodPost,
		path:   issuePath + "/" + id + "/transitions",
		body:   apiIssueWrite{Fields: fields, Transition: &apiTransitionRef{ID: move}},
		kind:   "issue",
		id:     id,
	}
	_, err = c.do(ctx, r)
	return err
}

// apiTransition is one workflow move. Its fields object is keyed by field ID
// and carries the same shape createmeta uses for a create screen.
type apiTransition struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	To   apiStatus `json:"to"`
	// HasScreen says a screen exists; fields says what is on it. A screen whose
	// fields are all optional still sets this.
	HasScreen bool `json:"hasScreen"`
	// IsAvailable is sent by the endpoint on every transition and is false only
	// when the caller asked for unavailable ones too. A pointer so that a body
	// that omits it is not read as unavailable.
	IsAvailable *bool                         `json:"isAvailable"`
	Fields      map[string]apiTransitionField `json:"fields"`
}

func (t apiTransition) unavailable() bool { return t.IsAvailable != nil && !*t.IsAvailable }

func (t apiTransition) domain() jira.Transition {
	return jira.Transition{
		ID:        t.ID,
		Name:      t.Name,
		To:        t.To.domain(),
		HasScreen: t.HasScreen,
		Fields:    transitionFields(t.Fields),
	}
}

// apiTransitionField is one field on a transition screen.
type apiTransitionField struct {
	Required        bool           `json:"required"`
	Schema          apiFieldSchema `json:"schema"`
	Name            string         `json:"name"`
	Key             string         `json:"key"`
	FieldID         string         `json:"fieldId"`
	Operations      []string       `json:"operations"`
	HasDefaultValue bool           `json:"hasDefaultValue"`
	AllowedValues   []apiOption    `json:"allowedValues"`
	AutoCompleteURL string         `json:"autoCompleteUrl"`
}

// transitionFields flattens the screen's field map into a stable order:
// required fields first, then by field ID. The wire shape is a JSON object, so
// it has no order of its own, and a form whose rows moved between two reads of
// the same screen would be unusable.
func transitionFields(in map[string]apiTransitionField) []jira.FieldMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]jira.FieldMeta, 0, len(in))
	for _, id := range slices.Sorted(maps.Keys(in)) {
		meta := in[id]
		out = append(out, meta.domain(id))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Required != out[j].Required {
			return out[i].Required
		}
		return out[i].Field.ID < out[j].Field.ID
	})
	return out
}

func (f apiTransitionField) domain(mapKey string) jira.FieldMeta {
	id := firstNonEmpty(f.FieldID, f.Key, mapKey)
	allowed := make([]jira.Option, 0, len(f.AllowedValues))
	for _, v := range f.AllowedValues {
		if option, ok := v.domain(); ok {
			allowed = append(allowed, option)
		}
	}
	return jira.FieldMeta{
		Field:           jira.FieldRef{ID: id, Name: f.Name, Schema: f.Schema.domain()},
		Name:            f.Name,
		Required:        f.Required,
		HasDefault:      f.HasDefaultValue,
		Operations:      slices.Clone(f.Operations),
		AllowedValues:   allowed,
		AutoCompleteURL: f.AutoCompleteURL,
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func issueKey(key string) (string, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return "", &jira.ValidationError{Fields: []jira.FieldError{{
			Field:   "issueIdOrKey",
			Message: "an issue key or id is required",
		}}}
	}
	return trimmed, nil
}

func invalidField(field, message string) error {
	return &jira.ValidationError{Fields: []jira.FieldError{{Field: field, Message: message}}}
}

// createFields builds the fields object for a new issue.
func createFields(in jira.IssueInput) (map[string]json.RawMessage, error) {
	project := strings.TrimSpace(in.ProjectKey)
	issueType := strings.TrimSpace(in.IssueTypeID)
	switch {
	case project == "":
		return nil, invalidField("project", "a project key is required to create an issue")
	case issueType == "":
		return nil, invalidField("issuetype", "an issue type id is required; resolve it from createmeta rather than by name")
	case strings.TrimSpace(in.Summary) == "":
		return nil, invalidField("summary", "summary is required")
	}

	out := make(map[string]json.RawMessage, in.Fields.Len()+7)
	set := func(id string, raw json.RawMessage) { out[id] = raw }
	set("project", jsonObject("key", project))
	set("issuetype", jsonObject("id", issueType))
	set("summary", mustJSON(in.Summary))
	if !in.Description.IsZero() {
		doc, err := adf.Marshal(in.Description)
		if err != nil {
			return nil, fmt.Errorf("cloud: encoding the description: %w", err)
		}
		set("description", doc)
	}
	if parent := strings.TrimSpace(in.ParentKey); parent != "" {
		set("parent", jsonObject("key", parent))
	}
	if in.Labels != nil {
		set("labels", mustJSON(in.Labels))
	}
	if assignee := strings.TrimSpace(in.Assignee); assignee != "" {
		set("assignee", jsonObject("accountId", assignee))
	}
	if err := addFieldSet(out, in.Fields); err != nil {
		return nil, err
	}
	return out, nil
}

// patchFields builds the fields object for an edit or a transition screen.
//
// Every key here was named by the patch. Nothing is defaulted in, and a nil
// pointer produces nothing at all rather than a null, because null is how the
// endpoint is told to empty a field.
func patchFields(in jira.IssuePatch) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, in.Fields.Len()+6)
	set := func(id string, raw json.RawMessage) error {
		if _, dup := out[id]; dup {
			return invalidField(id, "this patch sets "+id+" twice")
		}
		if reason, refused := unwritableFields[id]; refused {
			return invalidField(id, reason)
		}
		out[id] = raw
		return nil
	}

	if in.Summary != nil {
		if strings.TrimSpace(*in.Summary) == "" {
			return nil, invalidField("summary", "summary cannot be emptied")
		}
		if err := set("summary", mustJSON(*in.Summary)); err != nil {
			return nil, err
		}
	}
	if in.Description != nil {
		doc, err := adf.Marshal(*in.Description)
		if err != nil {
			return nil, fmt.Errorf("cloud: encoding the description: %w", err)
		}
		if err := set("description", doc); err != nil {
			return nil, err
		}
	}
	if in.Assignee != nil {
		if err := set("assignee", refOrNull("accountId", *in.Assignee)); err != nil {
			return nil, err
		}
	}
	if in.Labels != nil {
		if err := set("labels", mustJSON(*in.Labels)); err != nil {
			return nil, err
		}
	}
	if in.PriorityID != nil {
		if err := set("priority", refOrNull("id", *in.PriorityID)); err != nil {
			return nil, err
		}
	}
	if in.Due != nil {
		if err := set("duedate", dateOrNull(*in.Due)); err != nil {
			return nil, err
		}
	}
	if err := addFieldSet(out, in.Fields); err != nil {
		return nil, err
	}
	for _, ref := range in.Clear {
		id := strings.TrimSpace(ref.ID)
		if id == "" {
			return nil, invalidField("fields", "a field to clear has no id")
		}
		if id == "summary" {
			return nil, invalidField("summary", "summary cannot be emptied")
		}
		if err := set(id, json.RawMessage("null")); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// addFieldSet encodes the custom and system fields a caller carried in a
// FieldSet, keyed by the site's own field IDs.
func addFieldSet(out map[string]json.RawMessage, fields jira.FieldSet) error {
	for _, id := range fields.IDs() {
		if _, dup := out[id]; dup {
			return invalidField(id, "this patch sets "+id+" twice")
		}
		if reason, refused := unwritableFields[id]; refused {
			return invalidField(id, reason)
		}
		value, ok := fields.ByID(id)
		if !ok {
			continue
		}
		raw, err := fieldJSON(id, value)
		if err != nil {
			return err
		}
		out[id] = raw
	}
	return nil
}

// fieldJSON writes one field value in the shape the field holds.
//
// An option is written by id wherever it has one, because an id is the same on
// a site in any language and a value is not. An option with no id is a plain
// string: that is the only thing it can have come from, since a labels-like
// array of bare strings is the one shape that produces options without ids.
func fieldJSON(id string, v jira.FieldValue) (json.RawMessage, error) {
	switch v.Kind {
	case jira.KindText:
		return mustJSON(v.Text), nil
	case jira.KindNumber:
		return mustJSON(v.Number), nil
	case jira.KindBool:
		return mustJSON(v.Bool), nil
	case jira.KindDate:
		return dateOrNull(v.Date), nil
	case jira.KindTime:
		return mustJSON(v.Time.Format(platformTimeLayout)), nil
	case jira.KindDoc:
		doc, err := adf.Marshal(v.Doc)
		if err != nil {
			return nil, fmt.Errorf("cloud: encoding %s: %w", id, err)
		}
		return doc, nil
	case jira.KindOption:
		if len(v.Options) == 0 {
			return json.RawMessage("null"), nil
		}
		return optionJSON(v.Options[0]), nil
	case jira.KindOptions:
		out := make([]json.RawMessage, 0, len(v.Options))
		for _, option := range v.Options {
			out = append(out, optionJSON(option))
		}
		return mustJSON(out), nil
	case jira.KindUser:
		if len(v.Users) == 0 {
			return json.RawMessage("null"), nil
		}
		return jsonObject("accountId", v.Users[0].AccountID), nil
	case jira.KindUsers:
		out := make([]json.RawMessage, 0, len(v.Users))
		for _, user := range v.Users {
			out = append(out, jsonObject("accountId", user.AccountID))
		}
		return mustJSON(out), nil
	case jira.KindUnknown:
		// The bytes this client could not type are the bytes it was given, and
		// sending them back is the only faithful thing to do with them. One
		// that is not JSON was not read off the wire, so it is a caller error
		// rather than something to put in a request body.
		if !json.Valid([]byte(v.Text)) {
			return nil, invalidField(id, "this value is not the JSON it was read as, so it cannot be written back")
		}
		return json.RawMessage(v.Text), nil
	case jira.KindEmpty:
		return nil, invalidField(id, "this value carries nothing; name the field in IssuePatch.Clear to empty it")
	default:
		return nil, invalidField(id, "this client has no way to write a value of this kind")
	}
}

func optionJSON(o jira.Option) json.RawMessage {
	if o.ID == "" {
		return mustJSON(o.Label)
	}
	if len(o.Children) > 0 && o.Children[0].ID != "" {
		return mustJSON(map[string]any{"id": o.ID, "child": map[string]string{"id": o.Children[0].ID}})
	}
	return jsonObject("id", o.ID)
}

// refOrNull writes the {"key": value} reference Jira takes for a user or a
// priority, and null when the caller passed nothing — which is how a field that
// holds a reference is emptied.
func refOrNull(key, value string) json.RawMessage {
	if strings.TrimSpace(value) == "" {
		return json.RawMessage("null")
	}
	return jsonObject(key, value)
}

func dateOrNull(d jira.Date) json.RawMessage {
	if d.IsZero() {
		return json.RawMessage("null")
	}
	return mustJSON(d.String())
}

func jsonObject(key, value string) json.RawMessage {
	return mustJSON(map[string]string{key: value})
}

// mustJSON encodes a value that cannot fail to encode: a string, a number, a
// bool, a slice of those, or a map of raw messages this package built itself.
// Anything that could fail goes through a path that returns an error instead.
func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}
