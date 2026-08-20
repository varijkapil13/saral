package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	searchJQLPath        = "/rest/api/3/search/jql"
	approximateCountPath = "/rest/api/3/search/approximate-count"
)

// schemaExpand is what a search asks for when it needs to be told what a field
// holds. Both it and names are sent only when asked for, and each issue's own
// expand string lists them either way, so the presence of the block in the
// response is the only thing that says whether it was really expanded.
const schemaExpand = "schema"

// apiSearchBody is the POST body /search/jql takes. fields is not optional in
// any useful sense: the endpoint answers with an id and a key without it.
type apiSearchBody struct {
	JQL           string   `json:"jql"`
	Fields        []string `json:"fields"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
	MaxResults    int      `json:"maxResults,omitempty"`
	Expand        string   `json:"expand,omitempty"`
	Properties    []string `json:"properties,omitempty"`
}

// apiCountBody is the POST body /search/approximate-count takes.
type apiCountBody struct {
	JQL string `json:"jql"`
}

// apiSearchPage is one page of /search/jql. Three keys arrive in practice —
// issues, nextPageToken and isLast — which the embedded envelope already reads;
// schema arrives only when the request expanded it.
type apiSearchPage struct {
	tokenEnvelope[apiIssue]
	Schema map[string]apiFieldSchema `json:"schema"`
}

// apiIssue keeps the fields object as raw JSON because its keys are the site's
// own field IDs, most of which no client can know the names of.
type apiIssue struct {
	ID     string                     `json:"id"`
	Key    string                     `json:"key"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type apiFieldSchema struct {
	Type     string `json:"type"`
	Items    string `json:"items"`
	System   string `json:"system"`
	Custom   string `json:"custom"`
	CustomID int    `json:"customId"`
}

func (s apiFieldSchema) domain() jira.FieldSchema {
	return jira.FieldSchema{
		Type:     s.Type,
		Items:    s.Items,
		System:   s.System,
		Custom:   s.Custom,
		CustomID: s.CustomID,
	}
}

// Search runs a JQL query through POST /rest/api/3/search/jql, the endpoint
// that replaced the 410 Gone /search.
//
// Query.Fields must name the fields the caller wants. An empty list is refused
// rather than sent: the endpoint answers such a request with an id and a key
// per issue, which looks like a result set and is not one.
//
// Pages are walked by jira.Cursor, whose repeated-token guard is what makes the
// walk terminate. The token is reachable only from inside the returned Page, on
// purpose. Base64url-decoding one yields the sort column, the last row's sort
// value and the whole JQL string, so it is neither a stable identifier for a
// result set nor safe to store, log, or send with a different query.
func (c *Client) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	fields := uniqueStrings(q.Fields)
	if len(fields) == 0 {
		return jira.Page[jira.Issue]{}, errSearchNeedsFields()
	}
	body := apiSearchBody{
		JQL:        strings.TrimSpace(q.JQL),
		Fields:     fields,
		MaxResults: max(0, q.MaxResults),
		Expand:     searchExpand(q.Expand, fields),
		Properties: uniqueStrings(q.Properties),
	}
	build := func(token string) request {
		page := body
		page.NextPageToken = token
		return request{
			method: http.MethodPost,
			path:   searchJQLPath,
			body:   page,
			kind:   "search",
			// A POST that only reads, so it may be replayed after a 5xx and
			// shared with an identical search already in the air.
			repeatable: true,
		}
	}
	op := http.MethodPost + " " + searchJQLPath
	return cursorPages(ctx, c, build, func(resp *response) ([]jira.Issue, string, error) {
		return decodeSearchPage(resp, op)
	})
}

// ApproximateCount reports roughly how many issues a JQL query matches, through
// POST /rest/api/3/search/approximate-count — which, unlike search itself, has
// no /jql segment.
//
// It is a separate call because /search/jql reports no total at all, and it is
// worth making only where a number is genuinely needed: a list that renders
// "142+" from the pages it has already fetched needs no count.
func (c *Client) ApproximateCount(ctx context.Context, jql string) (int, error) {
	var answer struct {
		Count int `json:"count"`
	}
	r := request{
		method:     http.MethodPost,
		path:       approximateCountPath,
		body:       apiCountBody{JQL: strings.TrimSpace(jql)},
		kind:       "search",
		repeatable: true,
	}
	if err := c.doJSON(ctx, r, &answer); err != nil {
		return 0, err
	}
	return answer.Count, nil
}

func errSearchNeedsFields() error {
	return &jira.ValidationError{Fields: []jira.FieldError{{
		Field:   "fields",
		Message: "a search must name the fields it wants; /search/jql returns almost nothing without them",
	}}}
}

func decodeSearchPage(resp *response, op string) ([]jira.Issue, string, error) {
	var page apiSearchPage
	if err := resp.decode(op, &page); err != nil {
		return nil, "", err
	}
	raw := page.items()
	issues := make([]jira.Issue, 0, len(raw))
	for i := range raw {
		issues = append(issues, decodeIssue(raw[i], page.Schema))
	}
	return issues, page.next(), nil
}

// searchExpand builds the expand list for one search.
//
// The schema block is asked for only when the query names a field this client
// has no decoder for, because that is the only case where the response has to
// say what a value holds. A list view asking for six system fields pays nothing
// for it.
func searchExpand(requested, fields []string) string {
	out := uniqueStrings(requested)
	if needsSchema(fields) && !slices.Contains(out, schemaExpand) {
		out = append(out, schemaExpand)
	}
	return strings.Join(out, ",")
}

func needsSchema(fields []string) bool {
	for _, id := range fields {
		if _, ok := systemFields[id]; !ok {
			return true
		}
	}
	return false
}

// uniqueStrings trims a caller's list and drops blanks and repeats, keeping the
// order it was given in so that a request body is the same on every attempt.
func uniqueStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" && !slices.Contains(out, trimmed) {
			out = append(out, trimmed)
		}
	}
	return out
}

// decodeIssue maps one issue off the wire.
//
// It cannot fail. A field whose value is not the shape its schema claims, or
// whose schema this client does not model, is kept as jira.KindUnknown carrying
// the JSON it arrived as: a dropped field is one a user cannot tell is missing,
// and one odd custom field must not blank an issue. Unset fields arrive as an
// explicit null and are skipped, so a narrow field list really does produce an
// issue with everything else absent.
func decodeIssue(in apiIssue, schema map[string]apiFieldSchema) jira.Issue {
	iss := jira.Issue{ID: in.ID, Key: in.Key}
	values := make(map[string]jira.FieldValue, len(in.Fields))
	for id, raw := range in.Fields {
		if isJSONNull(raw) {
			continue
		}
		if read, ok := systemFields[id]; ok && read(&iss, raw) {
			continue
		}
		if value, ok := decodeFieldValue(raw, schema[id].domain()); ok {
			values[id] = value
		}
	}
	if len(values) > 0 {
		iss.Fields = jira.NewFieldSet(values)
	}
	return iss
}

// systemFields are the fields that land on the Issue struct rather than in its
// FieldSet. A reader returns false when the value is not the shape the field is
// documented to hold, which sends it to the FieldSet as an unknown instead of
// losing it.
//
// Membership doubles as the answer to whether a query needs the schema block,
// which is why this is a table and not a switch.
var systemFields = map[string]func(*jira.Issue, json.RawMessage) bool{
	"summary":        readSummary,
	"description":    readDescription,
	"project":        readProject,
	"issuetype":      readIssueType,
	"status":         readStatus,
	"priority":       readPriority,
	"resolution":     readResolution,
	"assignee":       readAssignee,
	"reporter":       readReporter,
	"labels":         readLabels,
	"components":     readComponents,
	"fixVersions":    readFixVersions,
	"parent":         readParent,
	"subtasks":       readSubtasks,
	"issuelinks":     readLinks,
	"duedate":        readDue,
	"created":        readCreated,
	"updated":        readUpdated,
	"resolutiondate": readResolved,
	"timetracking":   readTimeTracking,
}

func readSummary(iss *jira.Issue, raw json.RawMessage) bool {
	var summary string
	if !readJSON(raw, &summary) {
		return false
	}
	iss.Summary = summary
	return true
}

func readDescription(iss *jira.Issue, raw json.RawMessage) bool {
	doc, err := adf.Unmarshal(raw)
	if err != nil {
		return false
	}
	iss.Description = doc
	return true
}

func readProject(iss *jira.Issue, raw json.RawMessage) bool {
	var project apiProject
	if !readJSON(raw, &project) {
		return false
	}
	iss.Project = jira.ProjectRef{ID: project.ID, Key: project.Key, Name: project.Name}
	return true
}

func readIssueType(iss *jira.Issue, raw json.RawMessage) bool {
	var typ apiIssueType
	if !readJSON(raw, &typ) {
		return false
	}
	iss.Type = typ.domain()
	return true
}

func readStatus(iss *jira.Issue, raw json.RawMessage) bool {
	var status apiStatus
	if !readJSON(raw, &status) {
		return false
	}
	iss.Status = status.domain()
	return true
}

func readPriority(iss *jira.Issue, raw json.RawMessage) bool {
	var priority apiNamed
	if !readJSON(raw, &priority) {
		return false
	}
	iss.Priority = &jira.Priority{ID: priority.ID, Name: priority.Name}
	return true
}

func readResolution(iss *jira.Issue, raw json.RawMessage) bool {
	var resolution apiNamed
	if !readJSON(raw, &resolution) {
		return false
	}
	iss.Resolution = &jira.Resolution{ID: resolution.ID, Name: resolution.Name}
	return true
}

func readAssignee(iss *jira.Issue, raw json.RawMessage) bool {
	user, ok := readUser(raw)
	if !ok {
		return false
	}
	iss.Assignee = &user
	return true
}

func readReporter(iss *jira.Issue, raw json.RawMessage) bool {
	user, ok := readUser(raw)
	if !ok {
		return false
	}
	iss.Reporter = &user
	return true
}

func readLabels(iss *jira.Issue, raw json.RawMessage) bool {
	var labels []string
	if !readJSON(raw, &labels) {
		return false
	}
	iss.Labels = labels
	return true
}

func readComponents(iss *jira.Issue, raw json.RawMessage) bool {
	var components []apiNamed
	if !readJSON(raw, &components) {
		return false
	}
	out := make([]jira.Component, 0, len(components))
	for _, c := range components {
		out = append(out, jira.Component{ID: c.ID, Name: c.Name})
	}
	iss.Components = out
	return true
}

func readFixVersions(iss *jira.Issue, raw json.RawMessage) bool {
	var versions []apiVersion
	if !readJSON(raw, &versions) {
		return false
	}
	out := make([]jira.Version, 0, len(versions))
	for _, v := range versions {
		out = append(out, v.domain())
	}
	iss.FixVersions = out
	return true
}

func readParent(iss *jira.Issue, raw json.RawMessage) bool {
	var parent apiLinkedIssue
	if !readJSON(raw, &parent) {
		return false
	}
	ref := parent.domain()
	iss.Parent = &ref
	return true
}

func readSubtasks(iss *jira.Issue, raw json.RawMessage) bool {
	var subtasks []apiLinkedIssue
	if !readJSON(raw, &subtasks) {
		return false
	}
	out := make([]jira.IssueRef, 0, len(subtasks))
	for _, s := range subtasks {
		out = append(out, s.domain())
	}
	iss.Subtasks = out
	return true
}

func readLinks(iss *jira.Issue, raw json.RawMessage) bool {
	var links []apiIssueLink
	if !readJSON(raw, &links) {
		return false
	}
	out := make([]jira.IssueLink, 0, len(links))
	for _, l := range links {
		if link, ok := l.domain(); ok {
			out = append(out, link)
		}
	}
	iss.Links = out
	return true
}

func readDue(iss *jira.Issue, raw json.RawMessage) bool {
	var value string
	if !readJSON(raw, &value) {
		return false
	}
	due, err := jira.ParseDate(value)
	if err != nil {
		return false
	}
	iss.Due = due
	return true
}

func readCreated(iss *jira.Issue, raw json.RawMessage) bool {
	var at timestamp
	if !readJSON(raw, &at) {
		return false
	}
	iss.Created = at.Time
	return true
}

func readUpdated(iss *jira.Issue, raw json.RawMessage) bool {
	var at timestamp
	if !readJSON(raw, &at) {
		return false
	}
	iss.Updated = at.Time
	return true
}

func readResolved(iss *jira.Issue, raw json.RawMessage) bool {
	var at timestamp
	if !readJSON(raw, &at) {
		return false
	}
	iss.Resolved = at.ptr()
	return true
}

// readTimeTracking reads the estimates. An issue with none carries an empty
// object rather than a null, so an all-zero reading is Jira saying there are no
// estimates and leaves the pointer nil.
func readTimeTracking(iss *jira.Issue, raw json.RawMessage) bool {
	var tracking apiTimeTracking
	if !readJSON(raw, &tracking) {
		return false
	}
	if tracking == (apiTimeTracking{}) {
		return true
	}
	iss.TimeTracking = &jira.TimeTracking{
		OriginalEstimate:  tracking.OriginalEstimateSeconds,
		RemainingEstimate: tracking.RemainingEstimateSeconds,
		TimeSpent:         tracking.TimeSpentSeconds,
	}
	return true
}

type apiProject struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

// apiNamed is the id-and-name shape priorities, resolutions, components,
// versions and groups all arrive in.
type apiNamed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type apiIssueType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Subtask bool   `json:"subtask"`
	IconURL string `json:"iconUrl"`
}

func (t apiIssueType) domain() jira.IssueType {
	return jira.IssueType{ID: t.ID, Name: t.Name, Subtask: t.Subtask, IconURL: t.IconURL}
}

// apiStatus is a workflow status. Its iconUrl is deliberately not read: on a
// status it is the site root rather than an image, and the glyph a view needs
// comes from the category key.
type apiStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category *struct {
		ID   int    `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"statusCategory"`
}

// domain reads the category from inside the status. The same answer arrives as
// a field of its own beside it, and that copy is not the one to read.
func (s apiStatus) domain() jira.Status {
	out := jira.Status{ID: s.ID, Name: s.Name}
	if s.Category != nil {
		out.Category = jira.ParseStatusCategory(s.Category.Key)
	}
	return out
}

type apiUser struct {
	AccountID   string            `json:"accountId"`
	DisplayName string            `json:"displayName"`
	Email       string            `json:"emailAddress"`
	Active      bool              `json:"active"`
	TimeZone    string            `json:"timeZone"`
	AvatarURLs  map[string]string `json:"avatarUrls"`
}

// avatarSizes are read in this order so that the URL chosen does not depend on
// map iteration. A search response carries no avatars at all.
var avatarSizes = []string{"48x48", "32x32", "24x24", "16x16"}

func (u apiUser) domain() jira.User {
	out := jira.User{
		AccountID:   u.AccountID,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		Active:      u.Active,
	}
	for _, size := range avatarSizes {
		if url := u.AvatarURLs[size]; url != "" {
			out.AvatarURL = url
			break
		}
	}
	// A zone this machine has no database for is a rendering detail, never a
	// reason to fail an issue.
	if u.TimeZone != "" {
		if loc, err := time.LoadLocation(u.TimeZone); err == nil {
			out.TimeZone = loc
		}
	}
	return out
}

func readUser(raw json.RawMessage) (jira.User, bool) {
	var user apiUser
	if !readJSON(raw, &user) || user.AccountID == "" {
		return jira.User{}, false
	}
	return user.domain(), true
}

type apiVersion struct {
	ID          string      `json:"id"`
	ProjectID   json.Number `json:"projectId"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Archived    bool        `json:"archived"`
	Released    bool        `json:"released"`
	StartDate   string      `json:"startDate"`
	ReleaseDate string      `json:"releaseDate"`
}

func (v apiVersion) domain() jira.Version {
	out := jira.Version{
		ID:          v.ID,
		ProjectID:   v.ProjectID.String(),
		Name:        v.Name,
		Description: v.Description,
		Archived:    v.Archived,
		Released:    v.Released,
	}
	if start, err := jira.ParseDate(v.StartDate); err == nil {
		out.StartDate = start
	}
	if release, err := jira.ParseDate(v.ReleaseDate); err == nil {
		out.ReleaseDate = release
	}
	return out
}

// apiLinkedIssue is an issue referred to from another one — a parent, a subtask
// or the far end of a link. It carries a handful of fields and never the rest.
type apiLinkedIssue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary   string        `json:"summary"`
		Status    *apiStatus    `json:"status"`
		IssueType *apiIssueType `json:"issuetype"`
	} `json:"fields"`
}

func (l apiLinkedIssue) domain() jira.IssueRef {
	ref := jira.IssueRef{ID: l.ID, Key: l.Key, Summary: l.Fields.Summary}
	if l.Fields.Status != nil {
		ref.Status = l.Fields.Status.domain()
	}
	if l.Fields.IssueType != nil {
		ref.Type = l.Fields.IssueType.domain()
	}
	return ref
}

type apiIssueLink struct {
	ID   string `json:"id"`
	Type struct {
		Name    string `json:"name"`
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
	InwardIssue  *apiLinkedIssue `json:"inwardIssue"`
	OutwardIssue *apiLinkedIssue `json:"outwardIssue"`
}

// domain reads which end of the link this issue is on. Exactly one of the two
// issues is sent, and which one decides both the direction and which half of
// the type's phrasing describes the relationship.
func (l apiIssueLink) domain() (jira.IssueLink, bool) {
	out := jira.IssueLink{ID: l.ID, Type: l.Type.Name}
	switch {
	case l.OutwardIssue != nil:
		out.Direction, out.Label, out.Other = jira.LinkOutward, l.Type.Outward, l.OutwardIssue.domain()
	case l.InwardIssue != nil:
		out.Direction, out.Label, out.Other = jira.LinkInward, l.Type.Inward, l.InwardIssue.domain()
	default:
		return jira.IssueLink{}, false
	}
	return out, true
}

type apiTimeTracking struct {
	OriginalEstimateSeconds  int64 `json:"originalEstimateSeconds"`
	RemainingEstimateSeconds int64 `json:"remainingEstimateSeconds"`
	TimeSpentSeconds         int64 `json:"timeSpentSeconds"`
}

// flexString reads an identifier Jira writes as a string in one place and as a
// number in another: an option id is a string, the status category sitting in
// the same slot writes its id as a number, and the plans endpoints disagree
// with themselves. Neither should cost the value it identifies.
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*s = ""
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return fmt.Errorf("cloud: reading an identifier: %w", err)
		}
		*s = flexString(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return fmt.Errorf("cloud: reading an identifier: %w", err)
	}
	*s = flexString(number.String())
	return nil
}

// apiOption is the select-like shape. Jira writes the label as value on a
// custom option and as name on a version, component, group or priority.
type apiOption struct {
	ID    flexString `json:"id"`
	Value string     `json:"value"`
	Name  string     `json:"name"`
	Child *apiOption `json:"child"`
}

func (o apiOption) label() string {
	if o.Value != "" {
		return o.Value
	}
	return o.Name
}

func (o apiOption) domain() (jira.Option, bool) {
	// An id with nothing to show is not an option. Without this, any array of
	// objects that merely carry an id — an attachment list is the everyday one —
	// infers as a row of blank-labelled options, which is both what jira.Option
	// promises never to produce and worse than the honest KindUnknown, because
	// the raw bytes are then gone.
	if o.label() == "" {
		return jira.Option{}, false
	}
	out := jira.Option{ID: string(o.ID), Label: o.label()}
	if o.Child != nil {
		if child, ok := o.Child.domain(); ok {
			out.Children = []jira.Option{child}
		}
	}
	return out, true
}

// decodeFieldValue turns one field's JSON into a tagged value.
//
// Where the site sent a schema it is the authority, because only it can tell a
// number from a number-shaped string or an option array from a sprint array — a
// shape it names and this client has no slot for is kept verbatim rather than
// guessed at, since guessing turns a sprint into its name and drops the rest of
// it. Where there is no schema, which is every endpoint that does not offer one,
// the shape of the value is all there is to go on.
func decodeFieldValue(raw json.RawMessage, schema jira.FieldSchema) (jira.FieldValue, bool) {
	if isEmptyJSONArray(raw) {
		return jira.FieldValue{}, false
	}
	if doc, ok := asDoc(raw); ok {
		return doc, true
	}
	read := inferredValue
	if schema.Type != "" {
		read = func(raw json.RawMessage) (jira.FieldValue, bool) { return typedValue(raw, schema) }
	}
	if value, ok := read(raw); ok {
		return value, true
	}
	return jira.FieldValue{Kind: jira.KindUnknown, Text: compactJSON(raw)}, true
}

func typedValue(raw json.RawMessage, schema jira.FieldSchema) (jira.FieldValue, bool) {
	switch schema.Type {
	case "number":
		return asNumber(raw)
	case "date":
		return asDate(raw)
	case "datetime":
		return asTime(raw)
	case "string":
		return asText(raw)
	case "user":
		return asUser(raw)
	case "array":
		return asArray(raw, schema.Items)
	case "any":
		// Jira's own answer for a field whose plugin never declared a type, so
		// the value is the only thing left to read it from.
		return inferredValue(raw)
	case "option", "option-with-child", "priority", "status", "issuetype",
		"resolution", "project", "version", "component", "group", "securitylevel":
		return asOption(raw)
	default:
		return jira.FieldValue{}, false
	}
}

func asArray(raw json.RawMessage, items string) (jira.FieldValue, bool) {
	switch items {
	case "user":
		return asUsers(raw)
	case "string":
		return asLabels(raw)
	case "option", "option-with-child", "version", "component", "group",
		"priority", "issuetype", "resolution", "project":
		return asOptions(raw)
	default:
		// json, attachment, worklog, issuelinks and the rest are structures
		// this client has no slot for. The caller keeps the bytes instead.
		return jira.FieldValue{}, false
	}
}

// inferredValue reads a value with no schema to go on, which is how a custom
// field arrives from an endpoint that sends no schema block.
func inferredValue(raw json.RawMessage) (jira.FieldValue, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return jira.FieldValue{}, false
	}
	switch trimmed[0] {
	case '"':
		return asStringish(trimmed)
	case 't', 'f':
		return asBool(trimmed)
	case '{':
		if value, ok := asUser(trimmed); ok {
			return value, true
		}
		return asOption(trimmed)
	case '[':
		if value, ok := asUsers(trimmed); ok {
			return value, true
		}
		return asOptions(trimmed)
	default:
		return asNumber(trimmed)
	}
}

// asStringish reads a JSON string as the most specific thing it parses as. A
// date custom field with no schema is otherwise indistinguishable from text,
// and rendering it as text loses the ability to sort or filter on it.
func asStringish(raw json.RawMessage) (jira.FieldValue, bool) {
	if value, ok := asTime(raw); ok {
		return value, true
	}
	if value, ok := asDate(raw); ok {
		return value, true
	}
	return asText(raw)
}

func asText(raw json.RawMessage) (jira.FieldValue, bool) {
	var text string
	if !readJSON(raw, &text) {
		return jira.FieldValue{}, false
	}
	return jira.FieldValue{Kind: jira.KindText, Text: text}, true
}

func asNumber(raw json.RawMessage) (jira.FieldValue, bool) {
	var number float64
	if !readJSON(raw, &number) {
		return jira.FieldValue{}, false
	}
	return jira.FieldValue{Kind: jira.KindNumber, Number: number}, true
}

func asBool(raw json.RawMessage) (jira.FieldValue, bool) {
	var flag bool
	if !readJSON(raw, &flag) {
		return jira.FieldValue{}, false
	}
	return jira.FieldValue{Kind: jira.KindBool, Bool: flag}, true
}

func asDate(raw json.RawMessage) (jira.FieldValue, bool) {
	var text string
	if !readJSON(raw, &text) {
		return jira.FieldValue{}, false
	}
	date, err := jira.ParseDate(text)
	if err != nil {
		return jira.FieldValue{}, false
	}
	return jira.FieldValue{Kind: jira.KindDate, Date: date}, true
}

func asTime(raw json.RawMessage) (jira.FieldValue, bool) {
	var text string
	if !readJSON(raw, &text) {
		return jira.FieldValue{}, false
	}
	at, err := parseTime(text)
	if err != nil {
		return jira.FieldValue{}, false
	}
	return jira.FieldValue{Kind: jira.KindTime, Time: at}, true
}

func asDoc(raw json.RawMessage) (jira.FieldValue, bool) {
	var probe struct {
		Type string `json:"type"`
	}
	if !readJSON(raw, &probe) || probe.Type != "doc" {
		return jira.FieldValue{}, false
	}
	doc, err := adf.Unmarshal(raw)
	if err != nil {
		return jira.FieldValue{}, false
	}
	return jira.FieldValue{Kind: jira.KindDoc, Doc: doc}, true
}

func asUser(raw json.RawMessage) (jira.FieldValue, bool) {
	user, ok := readUser(raw)
	if !ok {
		return jira.FieldValue{}, false
	}
	return jira.FieldValue{Kind: jira.KindUser, Users: []jira.User{user}}, true
}

func asUsers(raw json.RawMessage) (jira.FieldValue, bool) {
	var entries []json.RawMessage
	if !readJSON(raw, &entries) || len(entries) == 0 {
		return jira.FieldValue{}, false
	}
	users := make([]jira.User, 0, len(entries))
	for _, entry := range entries {
		user, ok := readUser(entry)
		if !ok {
			return jira.FieldValue{}, false
		}
		users = append(users, user)
	}
	return jira.FieldValue{Kind: jira.KindUsers, Users: users}, true
}

func asOption(raw json.RawMessage) (jira.FieldValue, bool) {
	var option apiOption
	if !readJSON(raw, &option) {
		return jira.FieldValue{}, false
	}
	value, ok := option.domain()
	if !ok {
		return jira.FieldValue{}, false
	}
	return jira.FieldValue{Kind: jira.KindOption, Options: []jira.Option{value}}, true
}

func asOptions(raw json.RawMessage) (jira.FieldValue, bool) {
	var entries []apiOption
	if !readJSON(raw, &entries) || len(entries) == 0 {
		return jira.FieldValue{}, false
	}
	options := make([]jira.Option, 0, len(entries))
	for _, entry := range entries {
		value, ok := entry.domain()
		if !ok {
			return jira.FieldValue{}, false
		}
		options = append(options, value)
	}
	return jira.FieldValue{Kind: jira.KindOptions, Options: options}, true
}

// asLabels reads an array of plain strings, which is the shape a multi-select
// of text values and a labels-like field arrive in. They have no IDs, so the
// string is the whole option.
func asLabels(raw json.RawMessage) (jira.FieldValue, bool) {
	var entries []string
	if !readJSON(raw, &entries) || len(entries) == 0 {
		return jira.FieldValue{}, false
	}
	options := make([]jira.Option, 0, len(entries))
	for _, entry := range entries {
		options = append(options, jira.Option{Label: entry})
	}
	return jira.FieldValue{Kind: jira.KindOptions, Options: options}, true
}

func readJSON(raw json.RawMessage, out any) bool {
	return json.Unmarshal(raw, out) == nil
}

func isJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// isEmptyJSONArray reports an array with nothing in it, which is how Jira says
// a multi-valued field is unset. It carries nothing to keep.
func isEmptyJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return false
	}
	return len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) == 0
}

// compactJSON is the trace kept for a value this client cannot type, so that a
// field is visible in the UI as something rather than missing.
func compactJSON(raw json.RawMessage) string {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return string(raw)
	}
	return out.String()
}
