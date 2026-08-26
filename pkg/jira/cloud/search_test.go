package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// listFields is a realistic narrow field set: what a row needs, and no more.
var listFields = []string{"summary", "status", "assignee", "priority", "updated", "issuetype"}

func listQuery() jira.Query {
	return jira.Query{JQL: "project = EX ORDER BY updated DESC", Fields: listFields, MaxResults: 50}
}

// sentBody reads the JSON body of one request the fixture server recorded.
func sentBody(t *testing.T, r jiratest.Request) map[string]any {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatalf("reading the body of %s %s: %v", r.Method, r.Path, err)
	}
	return body
}

// jsonHandler answers a route with bytes written in the test itself, for the
// shapes no committed fixture carries.
func jsonHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func searchServing(body string) *jiratest.Server {
	return jiratest.NewServer(jiratest.WithHandler(http.MethodPost, searchJQLPath, jsonHandler(http.StatusOK, body)))
}

// onePage wraps issue JSON in the envelope /search/jql answers with, which
// carries three keys and no total.
func onePage(issues string) string {
	return `{"issues":[` + issues + `],"isLast":true}`
}

func firstIssue(t *testing.T, page jira.Page[jira.Issue]) jira.Issue {
	t.Helper()

	if len(page.Items) == 0 {
		t.Fatal("the page carried no issues")
	}
	return page.Items[0]
}

func searchOnce(t *testing.T, s *jiratest.Server, q jira.Query) jira.Page[jira.Issue] {
	t.Helper()

	c, _ := testClient(t, s.URL())
	page, err := c.Search(t.Context(), q)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	return page
}

func TestSearch_RefusesAQueryThatNamesNoFieldsWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.Search(t.Context(), jira.Query{JQL: "project = EX", Fields: []string{"  "}})

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v, want a *jira.ValidationError: an empty field list is a caller error", err)
	}
	if _, ok := invalid.For("fields"); !ok {
		t.Errorf("the failure does not name the fields list: %v", invalid)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests; a query that cannot be answered must not be sent", served)
	}

	// The fake refuses the same query, and the two adapters have to agree about
	// why, or a view written against one reads differently against the other.
	// The whitespace matters: a field list that trims away to nothing is a list
	// that names no fields, and both adapters have to see that the same way.
	for _, fields := range [][]string{nil, {"  "}, {"", " \t "}} {
		_, fakeErr := jiratest.New().Search(t.Context(), jira.Query{JQL: "project = EX", Fields: fields})
		if fakeErr == nil || fakeErr.Error() != err.Error() {
			t.Errorf("with Fields %q the fake refuses it as %v; the cloud adapter as %v", fields, fakeErr, err)
		}
	}
}

// TestSearch_TellsEveryIssueWhichFieldsWereAskedFor is the half of a narrow read
// that the issue itself cannot express: a nil assignee on an issue read without
// the assignee is not an unassigned issue, and only the mask says which it is.
func TestSearch_TellsEveryIssueWhichFieldsWereAskedFor(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	want := slices.Sorted(slices.Values(listFields))
	first := searchOnce(t, s, listQuery())
	all, err := jira.Collect(t.Context(), first, 0)
	if err != nil {
		t.Fatalf("walking the pages: %v", err)
	}
	if len(all) < 3 {
		t.Fatalf("the fixture pages carried %d issues, want the walk to reach page two", len(all))
	}

	for _, issue := range all {
		if issue.Requested.Wide() {
			t.Errorf("%s claims every field was asked for", issue.Key)
		}
		if got := issue.Requested.IDs(); !slices.Equal(got, want) {
			t.Errorf("%s was read with %q, want exactly %q", issue.Key, got, want)
		}
		if !issue.Requested.Has("assignee") {
			t.Errorf("%s does not know the assignee was asked for, so an unassigned issue reads as unfetched", issue.Key)
		}
		if issue.Requested.Has("labels") {
			t.Errorf("%s claims labels were asked for; they were not, so its empty labels mean nothing", issue.Key)
		}
	}
}

// TestSearch_MarksAFieldTheSiteDoesNotHaveAsRequestedAnyway pins the caveat the
// mask cannot avoid: a response carries only the fields it returned, so a field
// ID this site does not have is in the mask and never in the values.
func TestSearch_MarksAFieldTheSiteDoesNotHaveAsRequestedAnyway(t *testing.T) {
	t.Parallel()

	s := searchServing(onePage(`{"id":"1","key":"EX-1","fields":{"summary":"A row"}}`))
	defer s.Close()

	const absent = "customfield_99999"
	got := firstIssue(t, searchOnce(t, s, jira.Query{JQL: "project = EX", Fields: []string{"summary", absent}}))
	if !got.Requested.Has(absent) {
		t.Errorf("the mask dropped %s; it records what was asked for, not what the site had", absent)
	}
	if _, ok := got.Fields.ByID(absent); ok {
		t.Errorf("the site sent a value for %s, which it never echoed", absent)
	}
}

func TestSearch_WalksBothFixturePagesAndSendsTheTokenBackInTheBody(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	first := searchOnce(t, s, listQuery())
	if _, reported := first.Count(); reported {
		t.Error("the page reported a total; /search/jql sends none and never will")
	}
	if !first.HasMore() {
		t.Fatal("the first page reports no more, but the fixture hands out a token")
	}

	all, err := jira.Collect(t.Context(), first, 0)
	if err != nil {
		t.Fatalf("walking the pages: %v", err)
	}
	keys := make([]string, 0, len(all))
	for _, issue := range all {
		keys = append(keys, issue.Key)
	}
	if want := []string{"EX-1", "EX-2", "EX-3"}; !slices.Equal(keys, want) {
		t.Errorf("collected %v, want %v", keys, want)
	}

	served := s.Requests()
	if len(served) != 2 {
		t.Fatalf("the site served %d requests, want one per page", len(served))
	}
	if token, ok := sentBody(t, served[0])["nextPageToken"]; ok {
		t.Errorf("the first request carried a page token %v; there is nothing to continue from", token)
	}
	if token := sentBody(t, served[1])["nextPageToken"]; token == "" || token == nil {
		t.Error("the second request carried no page token, so it asked for page one again")
	}
}

func TestSearch_AsksForTheFieldsTheQueryNamesAndReadsBackNothingElse(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	page := searchOnce(t, s, listQuery())
	sent := sentBody(t, s.Requests()[0])
	requested, _ := sent["fields"].([]any)
	asked := make([]string, 0, len(requested))
	for _, f := range requested {
		name, _ := f.(string)
		asked = append(asked, name)
	}
	if !slices.Equal(asked, listFields) {
		t.Errorf("the request asked for %v, want exactly %v", asked, listFields)
	}

	issue := firstIssue(t, page)
	if issue.Key != "EX-1" || issue.Summary == "" || issue.Status.Name == "" || issue.Assignee == nil {
		t.Fatalf("the fields the query did ask for are missing: %+v", issue)
	}
	switch {
	case !issue.Description.IsZero():
		t.Error("the issue carries a description, which the query did not ask for")
	case issue.Labels != nil:
		t.Error("the issue carries labels, which the query did not ask for")
	case !issue.Due.IsZero():
		t.Error("the issue carries a due date, which the query did not ask for")
	case !issue.Created.IsZero():
		t.Error("the issue carries a created time, which the query did not ask for")
	case issue.TimeTracking != nil:
		t.Error("the issue carries time tracking, which the query did not ask for")
	case issue.Project.Key != "":
		t.Error("the issue carries a project, which the query did not ask for")
	case issue.Fields.Len() != 0:
		t.Errorf("the issue carries %d field values, want none: nothing outside the six was asked for", issue.Fields.Len())
	}
}

func TestSearch_ReadsAnUnassignedIssueAsUnassigned(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	page := searchOnce(t, s, listQuery())
	if len(page.Items) != 2 {
		t.Fatalf("the first fixture page carried %d issues, want 2", len(page.Items))
	}
	unassigned := page.Items[1]
	if unassigned.Key != "EX-2" {
		t.Fatalf("the second issue is %s, want EX-2", unassigned.Key)
	}
	if unassigned.Assignee != nil {
		t.Errorf("the issue is assigned to %+v; the response sent an explicit null", unassigned.Assignee)
	}
	if _, ok := unassigned.Fields.ByID("assignee"); ok {
		t.Error("a null field became a field value; an unset field is absent, not empty")
	}
}

func TestSearch_ReadsTheStatusCategoryFromInsideTheStatus(t *testing.T) {
	t.Parallel()

	// The same answer arrives twice on a real response, and the copy beside the
	// status is the wrong one to read. Here the two disagree so that reading the
	// wrong one is visible.
	const issue = `{"id":"1","key":"EX-1","fields":{
		"status":{"id":"10001","name":"In Review","iconUrl":"https://example.atlassian.net/",
			"statusCategory":{"id":4,"key":"indeterminate","name":"In Progress"}},
		"statusCategory":{"id":2,"key":"new","name":"To Do"}}}`

	s := searchServing(onePage(issue))
	defer s.Close()

	got := firstIssue(t, searchOnce(t, s, jira.Query{JQL: "project = EX", Fields: []string{"status", "statusCategory"}}))
	if got.Status.Category != jira.CategoryInProgress {
		t.Errorf("the status category is %v, want %v: it comes from inside the status", got.Status.Category, jira.CategoryInProgress)
	}
	if got.Status.Name != "In Review" {
		t.Errorf("the status name is %q, want %q", got.Status.Name, "In Review")
	}
}

func TestSearch_AsksForTheSchemaOnlyWhenAFieldNeedsIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []string
		expand []string
		want   string
	}{
		{
			name:   "six system fields need no schema",
			fields: listFields,
			want:   "",
		},
		{
			name:   "a custom field can only be typed from the schema",
			fields: []string{"summary", "customfield_10032"},
			want:   "schema",
		},
		{
			name:   "the caller's own expand is kept alongside it",
			fields: []string{"summary", "customfield_10032"},
			expand: []string{"renderedFields"},
			want:   "renderedFields,schema",
		},
		{
			name:   "a caller that asked for the schema itself is not asked twice",
			fields: []string{"customfield_10032"},
			expand: []string{"schema"},
			want:   "schema",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := searchServing(onePage(""))
			defer s.Close()

			searchOnce(t, s, jira.Query{JQL: "project = EX", Fields: tt.fields, Expand: tt.expand})
			got, _ := sentBody(t, s.Requests()[0])["expand"].(string)
			if got != tt.want {
				t.Errorf("the request expanded %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearch_TypesACustomFieldFromTheSchemaTheResponseCarries(t *testing.T) {
	t.Parallel()

	const page = `{"issues":[{"id":"1","key":"EX-1","fields":{
		"customfield_20001":8,
		"customfield_20002":"2026-02-02",
		"customfield_20003":{"id":"10","value":"Ring fenced"},
		"customfield_20004":[{"id":"11","value":"Docs"},{"id":"12","value":"Runbook"}],
		"customfield_20005":{"accountId":"acct-1","displayName":"Ada Lovelace","active":true},
		"customfield_20006":[{"accountId":"acct-1","displayName":"Ada Lovelace"},{"accountId":"acct-2","displayName":"Grace Hopper"}],
		"customfield_20007":{"id":"20","value":"Region","child":{"id":"21","value":"EU"}},
		"customfield_20008":"2026-02-11T09:41:22.104+0100",
		"customfield_20009":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"a note"}]}]},
		"customfield_20010":["alpha","beta"],
		"customfield_20011":"0|i000123:"}}],
		"schema":{
			"customfield_20001":{"type":"number","custom":"x:float","customId":20001},
			"customfield_20002":{"type":"date","custom":"x:datepicker","customId":20002},
			"customfield_20003":{"type":"option","custom":"x:select","customId":20003},
			"customfield_20004":{"type":"array","items":"option","custom":"x:multicheckboxes","customId":20004},
			"customfield_20005":{"type":"user","custom":"x:userpicker","customId":20005},
			"customfield_20006":{"type":"array","items":"user","custom":"x:people","customId":20006},
			"customfield_20007":{"type":"option-with-child","custom":"x:cascadingselect","customId":20007},
			"customfield_20008":{"type":"datetime","custom":"x:datetime","customId":20008},
			"customfield_20009":{"type":"string","custom":"x:textarea","customId":20009},
			"customfield_20010":{"type":"array","items":"string","custom":"x:multiselect","customId":20010},
			"customfield_20011":{"type":"any","custom":"x:gh-lexo-rank","customId":20011}},
		"isLast":true}`

	s := searchServing(page)
	defer s.Close()

	fields := []string{
		"customfield_20001", "customfield_20002", "customfield_20003", "customfield_20004",
		"customfield_20005", "customfield_20006", "customfield_20007", "customfield_20008",
		"customfield_20009", "customfield_20010", "customfield_20011",
	}
	got := firstIssue(t, searchOnce(t, s, jira.Query{JQL: "project = EX", Fields: fields}))

	ref := func(id string) jira.FieldRef { return jira.FieldRef{ID: id} }

	if n, ok := got.Fields.Number(ref("customfield_20001")); !ok || n != 8 {
		t.Errorf("the number field read as %v (%t), want 8", n, ok)
	}
	if d, ok := got.Fields.Date(ref("customfield_20002")); !ok || d.String() != "2026-02-02" {
		t.Errorf("the date field read as %q (%t), want 2026-02-02", d, ok)
	}
	if options, ok := got.Fields.Options(ref("customfield_20003")); !ok || len(options) != 1 || options[0].Label != "Ring fenced" {
		t.Errorf("the select field read as %+v (%t)", options, ok)
	}
	if options, ok := got.Fields.Options(ref("customfield_20004")); !ok || len(options) != 2 || options[1].Label != "Runbook" {
		t.Errorf("the multi-select field read as %+v (%t)", options, ok)
	}
	if users, ok := got.Fields.Users(ref("customfield_20005")); !ok || len(users) != 1 || users[0].AccountID != "acct-1" {
		t.Errorf("the user field read as %+v (%t)", users, ok)
	}
	if users, ok := got.Fields.Users(ref("customfield_20006")); !ok || len(users) != 2 || users[1].DisplayName != "Grace Hopper" {
		t.Errorf("the multi-user field read as %+v (%t)", users, ok)
	}
	cascading, ok := got.Fields.Options(ref("customfield_20007"))
	if !ok || len(cascading) != 1 || len(cascading[0].Children) != 1 || cascading[0].Children[0].Label != "EU" {
		t.Errorf("the cascading select read as %+v (%t); the second level is a child, not a sibling", cascading, ok)
	}
	if at, ok := got.Fields.Time(ref("customfield_20008")); !ok || at.IsZero() {
		t.Errorf("the datetime field read as %v (%t)", at, ok)
	}
	doc, ok := got.Fields.Doc(ref("customfield_20009"))
	if !ok || doc.IsEmpty() {
		t.Errorf("the multi-line field read as %+v (%t); its schema says string and its value is ADF", doc, ok)
	}
	if options, ok := got.Fields.Options(ref("customfield_20010")); !ok || len(options) != 2 || options[0].Label != "alpha" {
		t.Errorf("the array-of-string field read as %+v (%t)", options, ok)
	}
	if text, ok := got.Fields.Text(ref("customfield_20011")); !ok || text != "0|i000123:" {
		t.Errorf("the any-typed field read as %q (%t); a schema of any is Jira saying to read the value itself", text, ok)
	}
}

func TestSearch_KeepsAValueItCannotTypeInsteadOfDroppingIt(t *testing.T) {
	t.Parallel()

	// A sprint field is an array of json, which is not a shape the port models.
	const page = `{"issues":[{"id":"1","key":"EX-1","fields":{
		"customfield_20020":[{"id":42,"name":"Sprint 7","state":"active"}],
		"customfield_20021":[]}}],
		"schema":{
			"customfield_20020":{"type":"array","items":"json","custom":"x:gh-sprint","customId":20020},
			"customfield_20021":{"type":"array","items":"option","custom":"x:multiselect","customId":20021}},
		"isLast":true}`

	s := searchServing(page)
	defer s.Close()

	got := firstIssue(t, searchOnce(t, s, jira.Query{JQL: "project = EX", Fields: []string{"customfield_20020", "customfield_20021"}}))

	value, ok := got.Fields.ByID("customfield_20020")
	if !ok {
		t.Fatal("the sprint field was dropped; a field this client cannot type must still leave a trace")
	}
	if value.Kind != jira.KindUnknown {
		t.Errorf("the sprint field read as kind %v, want KindUnknown", value.Kind)
	}
	if !strings.Contains(value.Text, "Sprint 7") {
		t.Errorf("the kept value is %q; it should carry what arrived", value.Text)
	}
	if _, ok := got.Fields.ByID("customfield_20021"); ok {
		t.Error("an empty array became a field value; there is nothing there to keep")
	}
}

func TestSearch_TypesACustomFieldByShapeWhenTheSiteSendsNoSchema(t *testing.T) {
	t.Parallel()

	const page = `{"issues":[{"id":"1","key":"EX-1","fields":{
		"customfield_20030":5,
		"customfield_20031":"2026-02-02",
		"customfield_20032":"0|i000123:",
		"customfield_20033":{"accountId":"acct-1","displayName":"Ada Lovelace"},
		"customfield_20034":true,
		"customfield_20035":null}}],"isLast":true}`

	s := searchServing(page)
	defer s.Close()

	fields := []string{"customfield_20030", "customfield_20031", "customfield_20032", "customfield_20033", "customfield_20034", "customfield_20035"}
	got := firstIssue(t, searchOnce(t, s, jira.Query{JQL: "project = EX", Fields: fields}))

	ref := func(id string) jira.FieldRef { return jira.FieldRef{ID: id} }
	if n, ok := got.Fields.Number(ref("customfield_20030")); !ok || n != 5 {
		t.Errorf("the number read as %v (%t), want 5", n, ok)
	}
	if d, ok := got.Fields.Date(ref("customfield_20031")); !ok || d.String() != "2026-02-02" {
		t.Errorf("the date read as %q (%t)", d, ok)
	}
	if text, ok := got.Fields.Text(ref("customfield_20032")); !ok || text != "0|i000123:" {
		t.Errorf("the rank read as %q (%t)", text, ok)
	}
	if users, ok := got.Fields.Users(ref("customfield_20033")); !ok || len(users) != 1 {
		t.Errorf("the user read as %+v (%t)", users, ok)
	}
	if value, ok := got.Fields.ByID("customfield_20034"); !ok || value.Kind != jira.KindBool || !value.Bool {
		t.Errorf("the boolean read as %+v (%t)", value, ok)
	}
	if _, ok := got.Fields.ByID("customfield_20035"); ok {
		t.Error("a null field became a field value")
	}
}

func TestSearch_ReadsAnIssueShapedLikeARealOne(t *testing.T) {
	t.Parallel()

	// The rich fixture is a bare issue read: every field on the site, unset ones
	// as an explicit null, and no schema block to type the custom fields from.
	raw, err := jiratest.Fixture("issue_rich_adf.json")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	var wire apiIssue
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("parsing the fixture: %v", err)
	}

	got := decodeIssue(wire, nil, jira.AllFields())

	if got.Key != "EX-1" || got.ID != "10001" {
		t.Errorf("the issue is %s/%s, want EX-1/10001", got.Key, got.ID)
	}
	if got.Project.Key != "EX" || got.Project.Name != "Example" {
		t.Errorf("the project read as %+v", got.Project)
	}
	if got.Status.Category != jira.CategoryInProgress {
		t.Errorf("the status category is %v, want In Progress", got.Status.Category)
	}
	if got.Description.IsEmpty() {
		t.Error("the ADF description did not survive the decode")
	}
	if got.Due.String() != "2026-03-06" {
		t.Errorf("the due date is %q, want 2026-03-06", got.Due)
	}
	if want := time.Date(2026, time.January, 28, 14, 3, 7, 412_000_000, time.UTC); !got.Created.Equal(want) {
		t.Errorf("created is %s, want %s: the platform writes an offset with no colon", got.Created, want)
	}
	if got.Resolved != nil {
		t.Errorf("the issue is resolved at %v; the fixture sends a null resolution date", got.Resolved)
	}
	if got.TimeTracking == nil || got.TimeTracking.OriginalEstimate != 28800 || got.TimeTracking.TimeSpent != 14400 {
		t.Errorf("the estimates read as %+v", got.TimeTracking)
	}
	if !slices.Equal(got.Labels, []string{"checkout", "regression"}) {
		t.Errorf("the labels read as %v", got.Labels)
	}
	if len(got.FixVersions) != 1 || got.FixVersions[0].ReleaseDate.String() != "2026-03-27" {
		t.Errorf("the fix versions read as %+v", got.FixVersions)
	}
	if len(got.Components) != 0 || got.Components == nil {
		t.Errorf("an empty components array read as %v, want an empty slice", got.Components)
	}
	if got.Parent != nil {
		t.Errorf("the issue has a parent %+v; the fixture sends a null one", got.Parent)
	}
	if len(got.Links) != 1 {
		t.Fatalf("the issue carries %d links, want 1", len(got.Links))
	}
	link := got.Links[0]
	if link.Direction != jira.LinkOutward || link.Label != "blocks" || link.Other.Key != "EX-2" {
		t.Errorf("the link read as %+v; the outward issue is the one this issue blocks", link)
	}
	if got.Assignee == nil || got.Assignee.AvatarURL != "" {
		t.Errorf("the assignee read as %+v; the fixture carries no avatars", got.Assignee)
	}
	if got.Assignee == nil || got.Assignee.Kind != jira.AccountPerson {
		t.Errorf("the assignee read as %+v; the fixture says what kind of account it is", got.Assignee)
	}
	if got.Reporter == nil || got.Reporter.Kind != jira.AccountPerson {
		t.Errorf("the reporter read as %+v; the fixture says what kind of account it is", got.Reporter)
	}

	// The custom fields have no schema here, so they are read by shape.
	if points, ok := got.Fields.Number(jira.FieldRef{ID: "customfield_10032"}); !ok || points != 5 {
		t.Errorf("the story point field read as %v (%t)", points, ok)
	}
	if start, ok := got.Fields.Date(jira.FieldRef{ID: "customfield_10041"}); !ok || start.String() != "2026-02-02" {
		t.Errorf("the target start field read as %q (%t)", start, ok)
	}
	if _, ok := got.Fields.ByID("customfield_10046"); ok {
		t.Error("a null custom field became a field value")
	}
	// The category arrives a second time as a field of its own, and its id is a
	// number where every other option's is a string.
	beside, ok := got.Fields.Options(jira.FieldRef{ID: "statusCategory"})
	if !ok || len(beside) != 1 || beside[0].ID != "4" {
		t.Errorf("the status category field beside the status read as %+v (%t)", beside, ok)
	}
}

func TestSearch_TreatsAnEmptyTimeTrackingObjectAsNoEstimates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		want *jira.TimeTracking
	}{
		{
			name: "an issue with no estimates sends an empty object rather than a null",
			json: `{}`,
			want: nil,
		},
		{
			name: "an issue with estimates sends them in seconds",
			json: `{"originalEstimate":"1d","originalEstimateSeconds":28800,"remainingEstimateSeconds":14400,"timeSpentSeconds":3600}`,
			want: &jira.TimeTracking{OriginalEstimate: 28800, RemainingEstimate: 14400, TimeSpent: 3600},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := searchServing(onePage(`{"id":"1","key":"EX-1","fields":{"timetracking":` + tt.json + `}}`))
			defer s.Close()

			got := firstIssue(t, searchOnce(t, s, jira.Query{JQL: "project = EX", Fields: []string{"timetracking"}}))
			switch {
			case tt.want == nil && got.TimeTracking != nil:
				t.Errorf("the estimates read as %+v, want none", got.TimeTracking)
			case tt.want != nil && got.TimeTracking == nil:
				t.Error("the estimates were dropped")
			case tt.want != nil && *got.TimeTracking != *tt.want:
				t.Errorf("the estimates read as %+v, want %+v", *got.TimeTracking, *tt.want)
			}
		})
	}
}

func TestSearch_MapsEveryRefusedStatusToItsTypedError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
		assert func(*testing.T, error)
	}{
		{
			name:   "a JQL Jira will not parse",
			status: http.StatusBadRequest,
			body:   "validation_error.json",
			assert: func(t *testing.T, err error) {
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %v, want a *jira.ValidationError", err)
				}
				if _, ok := invalid.For("summary"); !ok {
					t.Errorf("the field messages were lost: %v", invalid)
				}
			},
		},
		{
			name:   "a token the site rejects",
			status: http.StatusUnauthorized,
			assert: func(t *testing.T, err error) {
				var denied *jira.AuthError
				if !errors.As(err, &denied) {
					t.Fatalf("got %v, want a *jira.AuthError", err)
				}
			},
		},
		{
			name:   "a project this token cannot browse",
			status: http.StatusForbidden,
			body:   "plans_403.json",
			assert: func(t *testing.T, err error) {
				var missing *jira.CapabilityError
				if !errors.As(err, &missing) {
					t.Fatalf("got %v, want a *jira.CapabilityError: a 403 is an answer about what is possible", err)
				}
				if missing.Reason == "" {
					t.Error("the refusal carries no reason to show the user")
				}
			},
		},
		{
			name:   "an endpoint this site does not have",
			status: http.StatusNotFound,
			assert: func(t *testing.T, err error) {
				var gone *jira.NotFoundError
				if !errors.As(err, &gone) {
					t.Fatalf("got %v, want a *jira.NotFoundError", err)
				}
			},
		},
		{
			name:   "the retired search endpoint answering 410",
			status: http.StatusGone,
			assert: func(t *testing.T, err error) {
				var gone *jira.NotFoundError
				if !errors.As(err, &gone) {
					t.Fatalf("got %v, want a *jira.NotFoundError", err)
				}
			},
		},
		{
			name:   "a conflict",
			status: http.StatusConflict,
			assert: func(t *testing.T, err error) {
				var clash *jira.ConflictError
				if !errors.As(err, &clash) {
					t.Fatalf("got %v, want a *jira.ConflictError", err)
				}
			},
		},
		{
			name:   "the site failing on its own account",
			status: http.StatusInternalServerError,
			assert: func(t *testing.T, err error) {
				var broken *jira.TransportError
				if !errors.As(err, &broken) {
					t.Fatalf("got %v, want a *jira.TransportError", err)
				}
				if broken.Status != http.StatusInternalServerError {
					t.Errorf("the failure reports HTTP %d, want 500", broken.Status)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(jiratest.WithStatus(http.MethodPost, searchJQLPath, tt.status, tt.body))
			defer s.Close()

			c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
			_, err := c.Search(t.Context(), listQuery())
			if err == nil {
				t.Fatal("the search succeeded against a site that refused it")
			}
			tt.assert(t, err)
		})
	}
}

func TestSearch_ReportsARateLimitWithTheIntervalTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithRateLimit(http.MethodPost, searchJQLPath, 30*time.Second))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.Search(t.Context(), listQuery())

	var limited *jira.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("got %v, want a *jira.RateLimitError", err)
	}
	if limited.RetryAfter != 30*time.Second {
		t.Errorf("the countdown is %s, want 30s", limited.RetryAfter)
	}
	if limited.Endpoint != searchJQLPath {
		t.Errorf("the limit names %q, want %q", limited.Endpoint, searchJQLPath)
	}
}

func TestSearch_TreatsABodyThatWillNotDecodeAsATransportFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, searchJQLPath, jsonHandler(http.StatusOK, `{"issues":[{"key":`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	_, err := c.Search(t.Context(), listQuery())

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %v, want a *jira.TransportError: the call reached Jira and came back unusable", err)
	}
}

func TestSearch_KeepsThePageTokenOutOfWhatItReports(t *testing.T) {
	t.Parallel()

	// The token is not opaque: base64url-decoding one yields the whole JQL. A
	// failure fetching the page after the first must not carry it anywhere a log
	// or a status line can pick it up.
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodPost, searchJQLPath, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			NextPageToken string `json:"nextPageToken"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.NextPageToken == "" {
			jsonHandler(http.StatusOK, `{"issues":[],"nextPageToken":"c2VjcmV0LWpxbC10b2tlbg==","isLast":false}`)(w, r)
			return
		}
		jsonHandler(http.StatusInternalServerError, `{"errorMessages":["boom"]}`)(w, r)
	}))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	first, err := c.Search(t.Context(), listQuery())
	if err != nil {
		t.Fatalf("fetching the first page: %v", err)
	}
	if _, err = first.Next(t.Context()); err == nil {
		t.Fatal("the second page succeeded against a site that refused it")
	}
	if strings.Contains(err.Error(), "c2VjcmV0LWpxbC10b2tlbg==") {
		t.Errorf("the failure quotes the page token, which carries the query: %v", err)
	}
}

func TestSearch_ReturnsTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c, _ := testClient(t, s.URL())
	_, err := c.Search(ctx, listQuery())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests after the caller had already gone", served)
	}
}

func TestApproximateCount_AsksTheEndpointWithNoJQLSegment(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	count, err := c.ApproximateCount(t.Context(), "  project = EX  ")
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 153 {
		t.Errorf("the count is %d, want the fixture's 153", count)
	}

	served := s.Requests()
	if len(served) != 1 {
		t.Fatalf("the site served %d requests, want 1", len(served))
	}
	if served[0].Path != approximateCountPath {
		t.Errorf("the count went to %q, want %q: the count endpoint has no /jql segment", served[0].Path, approximateCountPath)
	}
	if jql := sentBody(t, served[0])["jql"]; jql != "project = EX" {
		t.Errorf("the request asked for %q, want the trimmed query", jql)
	}
}

func TestApproximateCount_MapsARefusalToItsTypedError(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithStatus(http.MethodPost, approximateCountPath, http.StatusForbidden, "plans_403.json"))
	defer s.Close()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	_, err := c.ApproximateCount(t.Context(), "project = EX")

	var missing *jira.CapabilityError
	if !errors.As(err, &missing) {
		t.Fatalf("got %v, want a *jira.CapabilityError", err)
	}
}

func TestUniqueStrings_TrimsBlanksAndRepeatsAndKeepsTheOrderGiven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nothing at all", in: nil, want: []string{}},
		{name: "blanks are not fields", in: []string{" ", ""}, want: []string{}},
		{name: "a repeat is asked for once", in: []string{"summary", "summary"}, want: []string{"summary"}},
		{name: "the order given is kept", in: []string{"status", "summary"}, want: []string{"status", "summary"}},
		{name: "surrounding space is not part of a name", in: []string{" summary "}, want: []string{"summary"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := uniqueStrings(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("uniqueStrings(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func BenchmarkDecodeSearchPage(b *testing.B) {
	raw, err := jiratest.Fixture("search_page1.json")
	if err != nil {
		b.Fatalf("reading the fixture: %v", err)
	}
	resp := &response{status: http.StatusOK, body: raw}
	mask := jira.NewFieldMask(listFields)

	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := decodeSearchPage(resp, "POST "+searchJQLPath, mask); err != nil {
			b.Fatalf("decoding: %v", err)
		}
	}
}

func TestDecodeIssue_WithoutASchemaKeepsAnUnlabelledArrayAsItsBytes(t *testing.T) {
	t.Parallel()

	// A bare GET /issue/{key} carries no schema block, so every custom value is
	// inferred. An attachment array is objects with an id and a filename and no
	// value or name anywhere — inferring options from it produces one blank row
	// per attachment and throws the JSON away.
	const wire = `{"id":"10001","key":"EX-1","fields":{"attachment":[
		{"id":"10501","filename":"har-capture.har"},
		{"id":"10502","filename":"screenshot.png"}]}}`

	var in apiIssue
	if err := json.Unmarshal([]byte(wire), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := decodeIssue(in, nil, jira.AllFields())

	ref := jira.FieldRef{ID: "attachment"}
	if opts, ok := got.Fields.Options(ref); ok {
		t.Errorf("read as %d options %+v; jira.Option promises a picker never renders blank labels", len(opts), opts)
	}
	value, ok := got.Fields.Get(ref)
	if !ok {
		t.Fatal("the field was dropped entirely; an unreadable value must still leave a trace")
	}
	if value.Kind != jira.KindUnknown {
		t.Errorf("kind is %v, want KindUnknown", value.Kind)
	}
	if !strings.Contains(value.Text, "har-capture.har") {
		t.Errorf("the bytes are gone: %q", value.Text)
	}
}
