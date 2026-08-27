package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	testProject = "EX"
	// The three versions versions.json holds: released with dates, unreleased
	// in flight, and archived.
	testReleasedVersionID = "10099"
	testVersionID         = "10100"
	testArchivedVersionID = "10098"
	// testTargetVersionID is the version a release moves open issues onto. The
	// shared route answers version_one.json whatever id it is asked for, which
	// is all a target read wants to know: whether an issue may carry it.
	testTargetVersionID = "10102"
	// testCreatedVersionID is the version version_created.json mints, and
	// testUnresolvedOpen the count version_unresolved_count.json answers with.
	testCreatedVersionID = "10101"
	testUnresolvedOpen   = 8

	versionsRoute       = "/rest/api/3/project/{key}/version"
	versionsPluralRoute = "/rest/api/3/project/{key}/versions"
	projectRoute        = "/rest/api/3/project/{key}"
	versionCreateRoute  = "/rest/api/3/version"
	versionRoute        = "/rest/api/3/version/{id}"
	unresolvedRoute     = "/rest/api/3/version/{id}/unresolvedIssueCount"

	// The literal paths the requests land on, which is what the server records.
	testVersionsURL   = "/rest/api/3/project/" + testProject + "/version"
	testProjectURL    = "/rest/api/3/project/" + testProject
	testVersionURL    = "/rest/api/3/version/" + testVersionID
	testTargetURL     = "/rest/api/3/version/" + testTargetVersionID
	testUnresolvedURL = testVersionURL + "/unresolvedIssueCount"
)

// testProjectAnswer is all a create needs from the project: its numeric id, in
// the string the project endpoint writes it as.
const testProjectAnswer = `{"self":"https://example.atlassian.net/rest/api/3/project/10000","id":"10000","key":"EX","name":"Example"}`

// versionEcho answers a version write with the version as written, which is what
// a site does: the keys the body carried come back, and nothing this client
// never sent appears. No fixture can answer a write with what the write said.
func versionEcho(w http.ResponseWriter, r *http.Request) {
	sent := map[string]json.RawMessage{}
	_ = json.NewDecoder(r.Body).Decode(&sent)
	out := map[string]json.RawMessage{
		"id":        json.RawMessage(`"` + r.PathValue("id") + `"`),
		"name":      json.RawMessage(`"2026.3"`),
		"projectId": json.RawMessage(`10000`),
		"archived":  json.RawMessage(`false`),
		"released":  json.RawMessage(`false`),
	}
	for key, value := range sent {
		out[key] = value
	}
	body, _ := json.Marshal(out)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// versionSearchAnswering builds the sweep's search answer: the open issues on a
// version, in one page. The shared search route answers a fixed page of issues
// that has nothing to do with any version, so a sweep brings its own.
func versionSearchAnswering(keys ...string) http.HandlerFunc {
	issues := make([]string, 0, len(keys))
	for i, key := range keys {
		issues = append(issues, `{"id":"`+strconv.Itoa(20000+i)+`","key":"`+key+
			`","fields":{"fixVersions":[{"id":"`+testVersionID+`","name":"2026.3"}]}}`)
	}
	return jsonHandler(http.StatusOK, onePage(strings.Join(issues, ",")))
}

// unresolvedAnswering answers the count endpoint with a number of this client's
// choosing, which is what puts a release on either side of its own gate.
func unresolvedAnswering(count int) http.HandlerFunc {
	return jsonHandler(http.StatusOK, `{"issuesCount":`+strconv.Itoa(count)+
		`,"issuesUnresolvedCount":`+strconv.Itoa(count)+`}`)
}

// versionOpenKeys are the issues a sweep finds open, in the order it walks them.
func versionOpenKeys() []string { return []string{"EX-4", "EX-8", "EX-12"} }

// sweeping makes the count and the sweep's search agree about what is open. A
// release refuses when they do not, so a test of the sweep has to say both.
func sweeping(keys ...string) []jiratest.ServerOption {
	return []jiratest.ServerOption{
		jiratest.WithHandler(http.MethodGet, unresolvedRoute, unresolvedAnswering(len(keys))),
		jiratest.WithHandler(http.MethodPost, searchPath, versionSearchAnswering(keys...)),
	}
}

// issueWritesRefusedAfter serves n issue edits and refuses everything after
// them, which is how a sweep is made to fail on the write of this test's choice.
func issueWritesRefusedAfter(n int64) http.HandlerFunc {
	var served atomic.Int64
	return func(w http.ResponseWriter, _ *http.Request) {
		if served.Add(1) > n {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errorMessages":["You do not have permission to edit issues in this project."]}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// versionRoutes answers what pkg/jira/jiratest has no route for. The versions
// list, the create, the version read and the count are the shared fixtures.
func versionRoutes() []jiratest.ServerOption {
	return []jiratest.ServerOption{
		// Nothing shared answers one project, and a create resolves the
		// project's own id before it posts.
		jiratest.WithHandler(http.MethodGet, projectRoute, jsonHandler(http.StatusOK, testProjectAnswer)),
		jiratest.WithHandler(http.MethodPut, versionRoute, versionEcho),
		jiratest.WithHandler(http.MethodPut, issueRoute, jsonHandler(http.StatusNoContent, "")),
		// The plural path is routed too, answering what it answers, so that
		// reaching it is a wrong answer rather than a 404 nobody has to explain.
		jiratest.WithHandler(http.MethodGet, versionsPluralRoute,
			jsonHandler(http.StatusOK, `[{"id":"10099","name":"2026.2","projectId":10000}]`)),
	}
}

func versionClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(append(versionRoutes(), opts...)...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

// wireOrder is what the site was asked, in order, so a test can say that the
// release came last rather than only that it came.
func wireOrder(s *jiratest.Server) []string {
	served := s.Requests()
	out := make([]string, 0, len(served))
	for _, r := range served {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

func releaseInput(policy jira.UnresolvedPolicy, target string) jira.ReleaseInput {
	return jira.ReleaseInput{Unresolved: policy, MoveToVersionID: target}
}

// releasedTheVersion reports whether the release itself reached the site.
func releasedTheVersion(s *jiratest.Server) bool {
	return slices.Contains(wireOrder(s), http.MethodPut+" "+testVersionURL)
}

// issueWrites is how many issue edits reached the site, which is how many
// issues a sweep rewrote.
func issueWrites(s *jiratest.Server) int {
	n := 0
	for _, sent := range wireOrder(s) {
		if strings.HasPrefix(sent, http.MethodPut+" "+issuePath+"/") {
			n++
		}
	}
	return n
}

func TestVersions_ReadsThePagedProjectEndpointIncludingWhatIsArchived(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t)
	got, err := c.Versions(t.Context(), testProject)
	if err != nil {
		t.Fatalf("listing versions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d versions, want the 3 the fixture holds", len(got))
	}

	first := got[0]
	if first.ID != testReleasedVersionID || first.Name != "2026.2" {
		t.Errorf("the first version is %s %q, want the one the project lists first", first.ID, first.Name)
	}
	if first.ProjectID != "10000" {
		t.Errorf("the project id read as %q, want the number in the answer as a string", first.ProjectID)
	}
	if !first.Released {
		t.Error("a released version came back unreleased")
	}
	if want := (jira.Date{Year: 2026, Month: time.January, Day: 5}); first.StartDate != want {
		t.Errorf("the start date read as %v, want %v", first.StartDate, want)
	}
	if want := (jira.Date{Year: 2026, Month: time.February, Day: 27}); first.ReleaseDate != want {
		t.Errorf("the release date read as %v, want %v", first.ReleaseDate, want)
	}

	archived := got[2]
	if archived.ID != testArchivedVersionID || !archived.Archived {
		t.Errorf("the archived version came back as %s archived=%v; the list is the source a picker filters, not one that hides it",
			archived.ID, archived.Archived)
	}
	if !archived.StartDate.IsZero() {
		t.Errorf("a version with no start date grew one: %v", archived.StartDate)
	}
	for _, v := range got {
		if v.Unresolved != nil {
			t.Errorf("version %s arrived with an unresolved count of %d; no version read reports one, and a number here reads as nobody-asked being zero",
				v.ID, *v.Unresolved)
		}
	}

	sent := s.Requests()
	if len(sent) != 1 {
		t.Fatalf("listing three versions took %d requests: %v", len(sent), wireOrder(s))
	}
	if sent[0].Path != testVersionsURL {
		t.Errorf("the versions were read from %q; the plural path answers a bare array that cannot page", sent[0].Path)
	}
	for _, r := range sent {
		if strings.HasSuffix(r.Path, "/versions") {
			t.Errorf("the read went to %q, one letter and a whole top-level type away from the paged endpoint", r.Path)
		}
	}
	query, err := url.ParseQuery(sent[0].Query)
	if err != nil {
		t.Fatalf("reading the query sent: %v", err)
	}
	if got := query.Get("orderBy"); got != "sequence" {
		t.Errorf("the walk asked for order %q, want sequence: offsets over an order the site does not define are not stable between pages", got)
	}
	if got := query.Get("maxResults"); got != "50" {
		t.Errorf("the walk asked for maxResults %q, want 50", got)
	}
	if query.Has("expand") {
		t.Errorf("the read expanded %q; the status buckets are not the release gate and the port has nowhere to put them", query.Get("expand"))
	}
}

func TestVersions_WalksEveryPageAndStops(t *testing.T) {
	t.Parallel()

	pages := []string{
		`{"startAt":0,"maxResults":50,"total":4,"isLast":false,"values":[
			{"id":"1","name":"1.0","projectId":10000},{"id":"2","name":"2.0","projectId":10000}]}`,
		`{"startAt":50,"maxResults":50,"total":4,"isLast":true,"values":[
			{"id":"3","name":"3.0","projectId":10000},{"id":"4","name":"4.0","projectId":10000}]}`,
	}
	c, s := versionClient(t, jiratest.WithHandler(http.MethodGet, versionsRoute,
		func(w http.ResponseWriter, r *http.Request) {
			page := 0
			if r.URL.Query().Get("startAt") != "" {
				page = 1
			}
			jsonHandler(http.StatusOK, pages[page])(w, r)
		}))

	got, err := c.Versions(t.Context(), testProject)
	if err != nil {
		t.Fatalf("listing versions: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d versions over two pages, want 4: %v", len(got), wireOrder(s))
	}
	if got[3].Name != "4.0" {
		t.Errorf("the last version is %q, want the one on the second page", got[3].Name)
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the walk took %d requests, want one per page", served)
	}
}

func TestVersions_EndsAWalkOnAPageWithNothingOnIt(t *testing.T) {
	t.Parallel()

	// A total the site is wrong about is the shape that loops forever if the
	// walk trusts it: it claims forty and then sends nothing.
	c, s := versionClient(t, jiratest.WithHandler(http.MethodGet, versionsRoute,
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("startAt") == "" {
				jsonHandler(http.StatusOK, `{"startAt":0,"maxResults":50,"total":40,"values":[{"id":"1","name":"1.0"}]}`)(w, r)
				return
			}
			jsonHandler(http.StatusOK, `{"startAt":50,"maxResults":50,"total":40,"values":[]}`)(w, r)
		}))

	got, err := c.Versions(t.Context(), testProject)
	if err != nil {
		t.Fatalf("listing versions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d versions, want the one the site actually sent", len(got))
	}
	if served := len(s.Requests()); served != 2 {
		t.Errorf("the walk took %d requests; it should have stopped on the empty page", served)
	}
}

func TestVersions_ABareArrayIsNotAPageOfVersions(t *testing.T) {
	t.Parallel()

	// What the plural endpoint answers. Reaching it by mistake must fail rather
	// than read as a project with no versions.
	c, s := versionClient(t, jiratest.WithHandler(http.MethodGet, versionsRoute,
		jsonHandler(http.StatusOK, `[{"id":"10099","name":"2026.2","projectId":10000}]`)))

	_, err := c.Versions(t.Context(), testProject)
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if broken.Status != http.StatusOK {
		t.Errorf("the failure reports HTTP %d, want the 200 whose body would not read", broken.Status)
	}
	if requested := s.Requests()[0].Path; strings.HasSuffix(requested, "/versions") {
		t.Errorf("the adapter asked for %q, the plural path", requested)
	}
}

func TestVersions_RefuseAnEmptyProjectKeyWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t)
	_, err := c.Versions(t.Context(), "   ")
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, named := invalid.For("projectKey"); !named {
		t.Errorf("the refusal does not name the field: %v", invalid)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("a call with no project key still made %d requests", served)
	}
}

func TestVersions_NamesTheProjectWhenThereIsNone(t *testing.T) {
	t.Parallel()

	c, _ := versionClient(t, jiratest.WithStatus(http.MethodGet, versionsRoute, http.StatusNotFound, "problem_no_endpoint.json"))
	_, err := c.Versions(t.Context(), "NOPE")
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "project" || missing.ID != "NOPE" {
		t.Errorf("the failure names %s %s rather than the project asked for", missing.Kind, missing.ID)
	}
}

func TestSaveVersion_CreatesAgainstTheProjectIdItResolves(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t)
	got, err := c.SaveVersion(t.Context(), jira.VersionInput{
		ProjectKey:  testProject,
		Name:        "2026.4",
		Description: "Next one",
		StartDate:   jira.Date{Year: 2026, Month: time.April, Day: 6},
	})
	if err != nil {
		t.Fatalf("creating a version: %v", err)
	}
	if got.ID != testCreatedVersionID || got.ProjectID != "10000" {
		t.Errorf("the created version came back as %s on project %q", got.ID, got.ProjectID)
	}

	order := wireOrder(s)
	if len(order) != 2 || order[0] != http.MethodGet+" "+testProjectURL || order[1] != http.MethodPost+" "+versionCreateRoute {
		t.Fatalf("a create went %v, want the project id read and then the version posted", order)
	}
	body := sentBody(t, sentTo(t, s, http.MethodPost, versionCreateRoute))
	if got, ok := body["projectId"].(float64); !ok || got != 10000 {
		t.Errorf("the create sent projectId %v (%T); the endpoint takes the id as a number and the key it takes instead is deprecated",
			body["projectId"], body["projectId"])
	}
	if body["name"] != "2026.4" || body["startDate"] != "2026-04-06" {
		t.Errorf("the create sent %v", body)
	}
	for _, key := range []string{"released", "overdue", "id", "project", "releaseDate", "archived"} {
		if _, present := body[key]; present {
			t.Errorf("the create sent %q, which it has no business writing: %v", key, body)
		}
	}
}

func TestSaveVersion_UpdatesWhatTheInputSaysAndNothingElse(t *testing.T) {
	t.Parallel()

	archived := true
	for _, tt := range []struct {
		name    string
		in      jira.VersionInput
		present map[string]any
		absent  []string
	}{
		{
			name: "a rename that leaves the archive flag alone",
			in: jira.VersionInput{
				ID: testVersionID, Name: "2026.3.1", Description: "Renamed",
				StartDate:   jira.Date{Year: 2026, Month: time.March, Day: 2},
				ReleaseDate: jira.Date{Year: 2026, Month: time.March, Day: 30},
			},
			present: map[string]any{
				"name": "2026.3.1", "description": "Renamed",
				"startDate": "2026-03-02", "releaseDate": "2026-03-30",
			},
			absent: []string{"archived", "released", "overdue", "projectId", "id"},
		},
		{
			name:    "archiving one",
			in:      jira.VersionInput{ID: testVersionID, Name: "2026.3", Archived: &archived},
			present: map[string]any{"archived": true, "description": "", "startDate": nil, "releaseDate": nil},
			absent:  []string{"released", "overdue", "projectId"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := versionClient(t)
			if _, err := c.SaveVersion(t.Context(), tt.in); err != nil {
				t.Fatalf("updating a version: %v", err)
			}
			if order := wireOrder(s); len(order) != 1 || order[0] != http.MethodPut+" "+testVersionURL {
				t.Fatalf("an update went %v, want one PUT and no project read", order)
			}
			body := sentBody(t, sentTo(t, s, http.MethodPut, testVersionURL))
			for key, want := range tt.present {
				got, present := body[key]
				if !present {
					t.Errorf("the update left %q out, so the site keeps whatever it had", key)
					continue
				}
				if got != want {
					t.Errorf("the update sent %q as %v, want %v", key, got, want)
				}
			}
			for _, key := range tt.absent {
				if _, present := body[key]; present {
					t.Errorf("the update sent %q: %v", key, body)
				}
			}
		})
	}
}

func TestSaveVersion_RefusesAVersionWithNoNameOrNoProject(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		in    jira.VersionInput
		field string
	}{
		{name: "a create with a blank name", in: jira.VersionInput{ProjectKey: testProject, Name: "  "}, field: "name"},
		{name: "an update with a blank name", in: jira.VersionInput{ID: testVersionID}, field: "name"},
		{name: "a create with no project", in: jira.VersionInput{Name: "2026.4"}, field: "projectKey"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := versionClient(t)
			_, err := c.SaveVersion(t.Context(), tt.in)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, named := invalid.For(tt.field); !named {
				t.Errorf("the refusal does not name %s: %v", tt.field, invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("a request with nothing to send still made %d: %v", served, wireOrder(s))
			}
		})
	}
}

func TestSaveVersion_ReportsAProjectThatAnswersWithNoNumericID(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t, jiratest.WithHandler(http.MethodGet, projectRoute,
		jsonHandler(http.StatusOK, `{"key":"EX","name":"Example"}`)))

	_, err := c.SaveVersion(t.Context(), jira.VersionInput{ProjectKey: testProject, Name: "2026.4"})
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if order := wireOrder(s); len(order) != 1 {
		t.Errorf("the create posted a version anyway: %v", order)
	}
}

func TestUnresolvedCount_ReadsTheKeyTheAnswerActuallyCarries(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t)
	got, err := c.UnresolvedCount(t.Context(), testVersionID)
	if err != nil {
		t.Fatalf("counting what is open: %v", err)
	}
	if got != testUnresolvedOpen {
		t.Errorf("got %d open issues, want the %d version_unresolved_count.json carries under issuesUnresolvedCount",
			got, testUnresolvedOpen)
	}
	if order := wireOrder(s); len(order) != 1 || order[0] != "GET /rest/api/3/version/"+testVersionID+"/unresolvedIssueCount" {
		t.Errorf("the count went %v", order)
	}
}

func TestUnresolvedCount_WillNotReadAnAnswerWithoutTheCountAsZero(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		body string
	}{
		// The path's own spelling, which is not the key's.
		{name: "the count spelled the way the path is", body: `{"issuesCount":18,"unresolvedIssueCount":3}`},
		{name: "the totals and nothing else", body: `{"issuesCount":18}`},
		{name: "a body that stops half way", body: `{"issuesUnresolvedCount":`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := versionClient(t, jiratest.WithHandler(http.MethodGet, unresolvedRoute, jsonHandler(http.StatusOK, tt.body)))
			got, err := c.UnresolvedCount(t.Context(), testVersionID)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %d and %T (%v), want a *jira.TransportError: a silent zero reads as a version with nothing open on it", got, err, err)
			}
			if got != 0 {
				t.Errorf("the failed count returned %d", got)
			}
		})
	}
}

func TestUnresolvedCount_NamesTheVersionWhenThereIsNone(t *testing.T) {
	t.Parallel()

	c, _ := versionClient(t, jiratest.WithStatus(http.MethodGet, unresolvedRoute, http.StatusNotFound, "problem_no_endpoint.json"))
	_, err := c.UnresolvedCount(t.Context(), "40404")
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "version" || missing.ID != "40404" {
		t.Errorf("the failure names %s %s rather than the version asked for", missing.Kind, missing.ID)
	}
}

func TestUnresolvedCount_RefusesAnEmptyVersionWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t)
	if _, err := c.UnresolvedCount(t.Context(), ""); err == nil {
		t.Fatal("counting what is open on no version at all succeeded")
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("a call with no version id still made %d requests", served)
	}
}

func TestReleaseVersion_ReleasesAnywayWithoutTouchingAnIssue(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t)
	got, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.ReleaseAnyway, ""))
	if err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if !got.Released {
		t.Error("the released version came back unreleased")
	}
	if got.Unresolved == nil || *got.Unresolved != testUnresolvedOpen {
		t.Errorf("the release reports %v open issues left on it, want the %d it left alone",
			got.Unresolved, testUnresolvedOpen)
	}
	want := []string{
		http.MethodGet + " " + testUnresolvedURL,
		http.MethodPut + " " + testVersionURL,
	}
	if order := wireOrder(s); !slices.Equal(order, want) {
		t.Fatalf("a release anyway went %v, want %v: the count is read and nothing is rewritten", order, want)
	}
	body := sentBody(t, sentTo(t, s, http.MethodPut, testVersionURL))
	if body["released"] != true {
		t.Errorf("the release sent released=%v", body["released"])
	}
	if body["releaseDate"] != "2026-03-02" {
		t.Errorf("the release is dated %v, want the day the clock says", body["releaseDate"])
	}
	for _, key := range []string{"name", "description", "startDate", "archived", "overdue", "projectId"} {
		if _, present := body[key]; present {
			t.Errorf("the release also wrote %q, which it was never asked to change: %v", key, body)
		}
	}
}

func TestReleaseVersion_TakesTheDateTheCallerGave(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t)
	in := jira.ReleaseInput{ReleaseDate: jira.Date{Year: 2026, Month: time.February, Day: 28}}
	got, err := c.ReleaseVersion(t.Context(), testVersionID, in)
	if err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if body := sentBody(t, sentTo(t, s, http.MethodPut, testVersionURL)); body["releaseDate"] != "2026-02-28" {
		t.Errorf("the release is dated %v, want the caller's own date", body["releaseDate"])
	}
	if got.ReleaseDate != in.ReleaseDate {
		t.Errorf("the version came back dated %v, want %v", got.ReleaseDate, in.ReleaseDate)
	}
}

func TestReleaseVersion_MovesTheOpenIssuesBeforeItFlipsReleased(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t, sweeping(versionOpenKeys()...)...)
	got, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.MoveUnresolved, testTargetVersionID))
	if err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if got.Unresolved == nil || *got.Unresolved != 0 {
		t.Errorf("the release reports %v open issues left, want none", got.Unresolved)
	}

	want := []string{
		http.MethodGet + " " + testTargetURL,
		http.MethodGet + " " + testUnresolvedURL,
		"POST /rest/api/3/search/jql",
		"PUT /rest/api/3/issue/EX-4",
		"PUT /rest/api/3/issue/EX-8",
		"PUT /rest/api/3/issue/EX-12",
		http.MethodPut + " " + testVersionURL,
	}
	if order := wireOrder(s); !slices.Equal(order, want) {
		t.Fatalf("a move-and-release went\n%v\nwant\n%v", order, want)
	}

	jql, _ := sentBody(t, sentTo(t, s, http.MethodPost, searchPath))["jql"].(string)
	if !strings.Contains(jql, "fixVersion = "+testVersionID) {
		t.Errorf("the sweep searched %q; a version is named by id, never by the name it shares with another one", jql)
	}
	if !strings.Contains(jql, "resolution IS EMPTY") {
		t.Errorf("the sweep searched %q; the count that gates a release counts by resolution, so the sweep has to as well", jql)
	}

	edit := sentBody(t, sentTo(t, s, http.MethodPut, "/rest/api/3/issue/EX-4"))
	update, ok := edit["update"].(map[string]any)
	if !ok {
		t.Fatalf("the issue edit sent %v; the fields object would replace every other fix version on the issue", edit)
	}
	verbs, _ := update["fixVersions"].([]any)
	if len(verbs) != 2 {
		t.Fatalf("the issue edit sent %v verbs, want the version removed and the target added", verbs)
	}
	if !hasVersionVerb(verbs, "remove", testVersionID) || !hasVersionVerb(verbs, "add", testTargetVersionID) {
		t.Errorf("the issue edit sent %v", verbs)
	}
}

func TestReleaseVersion_StripsTheVersionFromTheOpenIssues(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t, sweeping(versionOpenKeys()...)...)
	got, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.StripUnresolved, ""))
	if err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if got.Unresolved == nil || *got.Unresolved != 0 {
		t.Errorf("the release reports %v open issues left, want none", got.Unresolved)
	}
	want := []string{
		http.MethodGet + " " + testUnresolvedURL,
		"POST /rest/api/3/search/jql",
		"PUT /rest/api/3/issue/EX-4",
		"PUT /rest/api/3/issue/EX-8",
		"PUT /rest/api/3/issue/EX-12",
		http.MethodPut + " " + testVersionURL,
	}
	if order := wireOrder(s); !slices.Equal(order, want) {
		t.Fatalf("a strip-and-release went\n%v\nwant\n%v", order, want)
	}
	verbs, _ := sentBody(t, sentTo(t, s, http.MethodPut, "/rest/api/3/issue/EX-4"))["update"].(map[string]any)["fixVersions"].([]any)
	if len(verbs) != 1 || !hasVersionVerb(verbs, "remove", testVersionID) {
		t.Errorf("stripping a version sent %v, want the one remove", verbs)
	}
}

func TestReleaseVersion_SweepsNothingWhenNothingIsOpen(t *testing.T) {
	t.Parallel()

	for _, policy := range []struct {
		name string
		in   jira.ReleaseInput
		want []string
	}{
		{
			name: "moving them", in: releaseInput(jira.MoveUnresolved, testTargetVersionID),
			// The target is still read: a move onto a version that cannot be
			// written to is refused whether or not anything needed moving.
			want: []string{
				http.MethodGet + " " + testTargetURL,
				http.MethodGet + " " + testUnresolvedURL,
				http.MethodPut + " " + testVersionURL,
			},
		},
		{
			name: "stripping it", in: releaseInput(jira.StripUnresolved, ""),
			want: []string{
				http.MethodGet + " " + testUnresolvedURL,
				http.MethodPut + " " + testVersionURL,
			},
		},
	} {
		t.Run(policy.name, func(t *testing.T) {
			t.Parallel()

			c, s := versionClient(t, jiratest.WithHandler(http.MethodGet, unresolvedRoute, unresolvedAnswering(0)))
			got, err := c.ReleaseVersion(t.Context(), testVersionID, policy.in)
			if err != nil {
				t.Fatalf("releasing: %v", err)
			}
			if got.Unresolved == nil || *got.Unresolved != 0 {
				t.Errorf("the release reports %v open issues left", got.Unresolved)
			}
			if order := wireOrder(s); !slices.Equal(order, policy.want) {
				t.Errorf("releasing a version with nothing open went %v, want %v", order, policy.want)
			}
		})
	}
}

func TestReleaseVersion_SaysHowFarASweepGotAndLeavesTheVersionUnreleased(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t, append(sweeping(versionOpenKeys()...),
		jiratest.WithHandler(http.MethodPut, issueRoute, issueWritesRefusedAfter(1)))...)
	_, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.StripUnresolved, ""))
	if err == nil {
		t.Fatal("a sweep that was refused half way through reported success")
	}
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want the refusal underneath to survive", err, err)
	}
	if !strings.Contains(err.Error(), "1 of 3") {
		t.Errorf("the failure does not say how far it got: %v", err)
	}
	if !strings.Contains(err.Error(), "EX-8") {
		t.Errorf("the failure does not name the issue it stopped on: %v", err)
	}
	if releasedTheVersion(s) {
		t.Fatalf("the version was released over issues the sweep never dealt with: %v", wireOrder(s))
	}
}

func TestReleaseVersion_ReportsAnUnknownTargetBeforeItWritesAnything(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t, jiratest.WithStatus(http.MethodGet, versionRoute, http.StatusNotFound, "problem_no_endpoint.json"))
	_, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.MoveUnresolved, testTargetVersionID))
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "version" || missing.ID != testTargetVersionID {
		t.Errorf("the failure names %s %s rather than the version the issues were moving to", missing.Kind, missing.ID)
	}
	if order := wireOrder(s); !slices.Equal(order, []string{http.MethodGet + " " + testTargetURL}) {
		t.Errorf("a move onto a version that does not exist went %v; the target is the first thing read and the last thing that happened", order)
	}
}

func TestReleaseVersion_RefusesADecisionItCannotCarryOut(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		id    string
		in    jira.ReleaseInput
		field string
	}{
		{
			name: "moving the open issues nowhere", id: testVersionID,
			in: releaseInput(jira.MoveUnresolved, "  "), field: "moveToVersionId",
		},
		{
			name: "moving them onto the version being released", id: testVersionID,
			in: releaseInput(jira.MoveUnresolved, testVersionID), field: "moveToVersionId",
		},
		{
			name: "a policy this client does not have", id: testVersionID,
			in: releaseInput(jira.UnresolvedPolicy(42), ""), field: "unresolved",
		},
		{
			name: "no version at all", id: " ",
			in: releaseInput(jira.ReleaseAnyway, ""), field: "versionId",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := versionClient(t)
			_, err := c.ReleaseVersion(t.Context(), tt.id, tt.in)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, named := invalid.For(tt.field); !named {
				t.Errorf("the refusal does not name %s: %v", tt.field, invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("a release that cannot be carried out still made %d requests: %v", served, wireOrder(s))
			}
		})
	}
}

func TestReleaseVersion_WillNotRewriteMoreIssuesThanItSaysItWill(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t, jiratest.WithHandler(http.MethodGet, unresolvedRoute, unresolvedAnswering(1001)))
	_, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.StripUnresolved, ""))
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if !strings.Contains(invalid.Error(), "1000") {
		t.Errorf("the refusal does not say that a thousand is the limit: %v", invalid)
	}
	if order := wireOrder(s); !slices.Equal(order, []string{http.MethodGet + " " + testUnresolvedURL}) {
		t.Errorf("a release too big to carry out went %v, want the count and nothing else", order)
	}
}

// versionCall is one of the four methods this file covers, in the shape the
// failure tables drive. route is what a test makes misbehave, and it is the
// last request each call makes so that the ones before it still answer.
type versionCall struct {
	name    string
	method  string
	route   string
	decodes bool
	run     func(ctx context.Context, c *Client) error
}

func versionCalls() []versionCall {
	return []versionCall{
		{
			name: "listing versions", method: http.MethodGet, route: versionsRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Versions(ctx, testProject)
				return err
			},
		},
		{
			name: "creating a version", method: http.MethodPost, route: versionCreateRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.SaveVersion(ctx, jira.VersionInput{ProjectKey: testProject, Name: "2026.4"})
				return err
			},
		},
		{
			name: "updating a version", method: http.MethodPut, route: versionRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.SaveVersion(ctx, jira.VersionInput{ID: testVersionID, Name: "2026.3"})
				return err
			},
		},
		{
			name: "counting what is open", method: http.MethodGet, route: unresolvedRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.UnresolvedCount(ctx, testVersionID)
				return err
			},
		},
		{
			name: "releasing a version", method: http.MethodPut, route: versionRoute, decodes: true,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.ReleaseVersion(ctx, testVersionID, releaseInput(jira.ReleaseAnyway, ""))
				return err
			},
		},
		{
			name: "releasing after a sweep", method: http.MethodPut, route: issueRoute,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.ReleaseVersion(ctx, testVersionID, releaseInput(jira.StripUnresolved, ""))
				return err
			},
		},
	}
}

func TestVersion_RefusalBecomesTheSentenceTheUserReads(t *testing.T) {
	t.Parallel()

	for _, tc := range versionCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := `{"errorMessages":["You do not have permission to administer this project."],"errors":{}}`
			c, _ := versionClient(t, append(sweeping(versionOpenKeys()...),
				jiratest.WithHandler(tc.method, tc.route, jsonHandler(http.StatusForbidden, body)))...)

			err := tc.run(t.Context(), c)
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			if !strings.Contains(refused.Error(), "administer this project") {
				t.Errorf("the reason lost the site's own wording: %q", refused.Error())
			}
		})
	}
}

func TestVersion_RateLimitCarriesTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	for _, tc := range versionCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := versionClient(t, append(sweeping(versionOpenKeys()...),
				jiratest.WithRateLimit(tc.method, tc.route, 30*time.Second))...)

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

func TestVersion_AFailingSiteIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range versionCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := versionClient(t, append(sweeping(versionOpenKeys()...), jiratest.WithHandler(tc.method, tc.route,
				jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))...)

			err := tc.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != http.StatusBadGateway {
				t.Errorf("the failure reports HTTP %d", broken.Status)
			}
		})
	}
}

func TestVersion_AHostThatNeverAnswersIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range versionCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dead := jiratest.NewServer()
			site := dead.URL()
			dead.Close()
			c, _ := testClient(t, site, WithRetry(RetryPolicy{Attempts: 1}))

			err := tc.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != 0 {
				t.Errorf("a host that never answered reports HTTP %d", broken.Status)
			}
		})
	}
}

func TestVersion_ABodyThisClientCannotReadIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range versionCalls() {
		if !tc.decodes {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := versionClient(t, append(sweeping(versionOpenKeys()...), jiratest.WithHandler(tc.method, tc.route,
				jsonHandler(http.StatusOK, `<html>your proxy has opinions</html>`)))...)

			err := tc.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
		})
	}
}

func TestVersion_ReturnsTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	for _, tc := range versionCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := versionClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := tc.run(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("a cancelled call still reached the site %d times", served)
			}
		})
	}
}

func TestReleaseVersion_ComesBackWhenTheCallerCancelsMidSweep(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	s := jiratest.NewServer(append(append(versionRoutes(), sweeping(versionOpenKeys()...)...),
		jiratest.WithHandler(http.MethodPut, issueRoute, func(_ http.ResponseWriter, r *http.Request) {
			announce()
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))...)
	defer closeServer(t, s)
	defer letGo()

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	go func() {
		_, err := c.ReleaseVersion(ctx, testVersionID, releaseInput(jira.StripUnresolved, ""))
		failed <- err
	}()

	receive(t, "the first issue edit to reach the site", arrived)
	cancel()
	if err := receive(t, "the cancelled release to come back", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's own error", err)
	}
	if releasedTheVersion(s) {
		t.Errorf("a cancelled sweep released the version anyway: %v", wireOrder(s))
	}
}

func TestReleaseVersion_WillNotReleaseOverIssuesTheSweepNeverReached(t *testing.T) {
	t.Parallel()

	// The count and the search do not have to agree: the count is the project's
	// own, the search is the index's and is narrowed by what the token may see.
	for _, tt := range []struct {
		name    string
		found   []string
		rewrote int
	}{
		{name: "a search that finds fewer than the count said", found: []string{"EX-4"}, rewrote: 1},
		{name: "a search that finds none of them", found: nil, rewrote: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := versionClient(t,
				jiratest.WithHandler(http.MethodGet, unresolvedRoute, unresolvedAnswering(3)),
				jiratest.WithHandler(http.MethodPost, searchPath, versionSearchAnswering(tt.found...)))
			got, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.StripUnresolved, ""))
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %+v and %T (%v), want a *jira.ValidationError: releasing over issues nobody dealt with is the outcome this method exists to prevent",
					got, err, err)
			}
			if !strings.Contains(invalid.Error(), "3 issues were open") {
				t.Errorf("the refusal does not say how many were open: %v", invalid)
			}
			if releasedTheVersion(s) {
				t.Fatalf("the version was released anyway: %v", wireOrder(s))
			}
			if wrote := issueWrites(s); wrote != tt.rewrote {
				t.Errorf("the sweep rewrote %d issues, want the %d the search found", wrote, tt.rewrote)
			}
		})
	}
}

func TestReleaseVersion_RefusesWhenTheSearchIsStillPagingPastTheBound(t *testing.T) {
	t.Parallel()

	// The count is exactly the bound, so the guard in front of the sweep lets it
	// through. A site with more open issues than it counted is caught by the walk
	// or not at all — and a walk stopped at its limit looks like one that ended.
	var page atomic.Int64
	c, s := versionClient(t,
		jiratest.WithHandler(http.MethodGet, unresolvedRoute, unresolvedAnswering(1000)),
		jiratest.WithHandler(http.MethodPost, searchPath, func(w http.ResponseWriter, r *http.Request) {
			n := page.Add(1)
			issues := make([]string, 0, 50)
			for i := range 50 {
				id := strconv.FormatInt(n*1000+int64(i), 10)
				issues = append(issues, `{"id":"`+id+`","key":"EX-`+id+`","fields":{"fixVersions":[]}}`)
			}
			// A fresh token per page: a repeated one is read as exhaustion.
			jsonHandler(http.StatusOK, `{"issues":[`+strings.Join(issues, ",")+
				`],"nextPageToken":"tok-`+strconv.FormatInt(n, 10)+`"}`)(w, r)
		}))

	_, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.StripUnresolved, ""))
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if !strings.Contains(invalid.Error(), "1000") {
		t.Errorf("the refusal does not say that a thousand is the limit: %v", invalid)
	}
	if wrote := issueWrites(s); wrote != 0 {
		t.Errorf("a release it refused to carry out rewrote %d issues", wrote)
	}
	if releasedTheVersion(s) {
		t.Fatalf("the version was released over a sweep that never finished: %v", wireOrder(s))
	}
	if searches := page.Load(); searches < 2 {
		t.Errorf("the search was asked %d times, so nothing was paged and this proves nothing", searches)
	}
}

func TestReleaseVersion_SweepsEveryIssueTheSearchFoundEvenWhenTheCountSaidFewer(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t,
		jiratest.WithHandler(http.MethodGet, unresolvedRoute, unresolvedAnswering(1)),
		jiratest.WithHandler(http.MethodPost, searchPath, versionSearchAnswering(versionOpenKeys()...)))
	got, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.StripUnresolved, ""))
	if err != nil {
		t.Fatalf("releasing a version whose count is behind its own search: %v", err)
	}
	if wrote := issueWrites(s); wrote != len(versionOpenKeys()) {
		t.Errorf("the sweep rewrote %d issues, want every one the search found", wrote)
	}
	if !releasedTheVersion(s) {
		t.Errorf("nothing was left open and the version was still not released: %v", wireOrder(s))
	}
	if got.Unresolved == nil || *got.Unresolved != 0 {
		t.Errorf("the release reports %v open issues left, want none", got.Unresolved)
	}
}

func TestReleaseVersion_TheFirstWriteASweepCannotMakeComesBackAsItIs(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t, append(sweeping(versionOpenKeys()...),
		jiratest.WithHandler(http.MethodPut, issueRoute, issueWritesRefusedAfter(0)))...)
	_, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.StripUnresolved, ""))
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
	if err.Error() != refused.Error() {
		t.Errorf("a sweep that changed nothing said %q; there is no progress to report, so the typed refusal is the whole answer", err)
	}
	if releasedTheVersion(s) {
		t.Fatalf("the version was released over a sweep that wrote nothing: %v", wireOrder(s))
	}
}

func TestUnresolvedCount_WillNotReadANegativeAnswerAsACount(t *testing.T) {
	t.Parallel()

	c, _ := versionClient(t, jiratest.WithHandler(http.MethodGet, unresolvedRoute, unresolvedAnswering(-1)))
	got, err := c.UnresolvedCount(t.Context(), testVersionID)
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %d and %T (%v), want a *jira.TransportError", got, err, err)
	}
	if got != 0 {
		t.Errorf("the failed count returned %d", got)
	}
}

func TestReleaseVersion_RefusesAVersionThisSiteCouldNotHaveMinted(t *testing.T) {
	t.Parallel()

	// A version id is a number on every Jira site, and the sweep's JQL has no
	// way to say "by id" for anything else: a quoted value is matched against
	// version names, which are neither unique nor stable.
	for _, id := range []string{"2026.3", "ver-EX-1", "10100 OR project = OTHER"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			c, s := versionClient(t)
			_, err := c.ReleaseVersion(t.Context(), id, releaseInput(jira.StripUnresolved, ""))
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if _, named := invalid.For("versionId"); !named {
				t.Errorf("the refusal does not name the field: %v", invalid)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("a release of something that is not a version id still made %d requests: %v", served, wireOrder(s))
			}
		})
	}
}

func TestReleaseVersion_RefusesAMoveOntoAnArchivedVersion(t *testing.T) {
	t.Parallel()

	c, s := versionClient(t, jiratest.WithHandler(http.MethodGet, versionRoute, jsonHandler(http.StatusOK,
		`{"id":"`+testTargetVersionID+`","name":"2026.0","archived":true,"released":true,"projectId":10000}`)))
	_, err := c.ReleaseVersion(t.Context(), testVersionID, releaseInput(jira.MoveUnresolved, testTargetVersionID))
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, named := invalid.For("moveToVersionId"); !named {
		t.Errorf("the refusal does not name the field: %v", invalid)
	}
	if order := wireOrder(s); !slices.Equal(order, []string{http.MethodGet + " " + testTargetURL}) {
		t.Errorf("a move onto an archived version went %v; an issue cannot carry one, so nothing else should have happened", order)
	}
}

func TestVersions_RefusesAProjectWithMoreVersionsThanItReads(t *testing.T) {
	t.Parallel()

	var page atomic.Int64
	c, _ := versionClient(t, jiratest.WithHandler(http.MethodGet, versionsRoute,
		func(w http.ResponseWriter, r *http.Request) {
			n := page.Add(1)
			values := make([]string, 0, 50)
			for i := range 50 {
				id := strconv.FormatInt(n*1000+int64(i), 10)
				values = append(values, `{"id":"`+id+`","name":"v`+id+`","projectId":10000}`)
			}
			jsonHandler(http.StatusOK, `{"startAt":0,"maxResults":50,"isLast":false,"values":[`+
				strings.Join(values, ",")+`]}`)(w, r)
		}))

	got, err := c.Versions(t.Context(), testProject)
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %d versions and %T (%v), want a *jira.ValidationError: a prefix of the list is indistinguishable from the list",
			len(got), err, err)
	}
	if !strings.Contains(invalid.Error(), "2000") {
		t.Errorf("the refusal does not say how many it reads: %v", invalid)
	}
	if got != nil {
		t.Errorf("the refused read still handed back %d versions", len(got))
	}
}

// versionStep is a request one of the calls makes before its last one. The
// tables above only make the last request of a method misbehave, so a failure
// on any of these three has no case there.
type versionStep struct {
	name   string
	method string
	route  string
	run    func(ctx context.Context, c *Client) error
}

func versionSteps() []versionStep {
	return []versionStep{
		{
			name: "the project a create resolves", method: http.MethodGet, route: projectRoute,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.SaveVersion(ctx, jira.VersionInput{ProjectKey: testProject, Name: "2026.4"})
				return err
			},
		},
		{
			name: "the target a move reads", method: http.MethodGet, route: versionRoute,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.ReleaseVersion(ctx, testVersionID, releaseInput(jira.MoveUnresolved, testTargetVersionID))
				return err
			},
		},
		{
			name: "the search a sweep runs", method: http.MethodPost, route: searchPath,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.ReleaseVersion(ctx, testVersionID, releaseInput(jira.StripUnresolved, ""))
				return err
			},
		},
	}
}

func TestVersion_AFailureBeforeTheWriteLeavesTheSiteAsItWas(t *testing.T) {
	t.Parallel()

	for _, tc := range versionSteps() {
		for _, mode := range []struct {
			name string
			opt  func(method, route string) jiratest.ServerOption
			want func(error) bool
		}{
			{
				name: "a refusal",
				opt: func(method, route string) jiratest.ServerOption {
					return jiratest.WithHandler(method, route, jsonHandler(http.StatusForbidden,
						`{"errorMessages":["You do not have permission to administer this project."],"errors":{}}`))
				},
				want: func(err error) bool {
					var refused *jira.CapabilityError
					return errors.As(err, &refused)
				},
			},
			{
				name: "a rate limit",
				opt: func(method, route string) jiratest.ServerOption {
					return jiratest.WithRateLimit(method, route, 30*time.Second)
				},
				want: func(err error) bool {
					var limited *jira.RateLimitError
					return errors.As(err, &limited)
				},
			},
			{
				name: "a failing site",
				opt: func(method, route string) jiratest.ServerOption {
					return jiratest.WithHandler(method, route,
						jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`))
				},
				want: func(err error) bool {
					var broken *jira.TransportError
					return errors.As(err, &broken)
				},
			},
			{
				name: "a body this client cannot read",
				opt: func(method, route string) jiratest.ServerOption {
					return jiratest.WithHandler(method, route,
						jsonHandler(http.StatusOK, `<html>your proxy has opinions</html>`))
				},
				want: func(err error) bool {
					var broken *jira.TransportError
					return errors.As(err, &broken)
				},
			},
		} {
			t.Run(tc.name+", "+mode.name, func(t *testing.T) {
				t.Parallel()

				c, s := versionClient(t, append(sweeping(versionOpenKeys()...), mode.opt(tc.method, tc.route))...)
				if err := tc.run(t.Context(), c); !mode.want(err) {
					t.Fatalf("got %T (%v), want the error the site's own answer types", err, err)
				}
				for _, sent := range wireOrder(s) {
					if strings.HasPrefix(sent, http.MethodPut) || sent == http.MethodPost+" "+versionCreateRoute {
						t.Errorf("a call that failed before its own write sent %q: %v", sent, wireOrder(s))
					}
				}
			})
		}
	}
}

// hasVersionVerb reports whether one of an edit's verbs is the one asked about,
// naming the version it should.
func hasVersionVerb(verbs []any, verb, id string) bool {
	for _, raw := range verbs {
		pair, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		named, ok := pair[verb].(map[string]any)
		if ok && named["id"] == id {
			return true
		}
	}
	return false
}
