package jiratest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

type srvReply struct {
	status int
	header http.Header
	body   []byte
}

func srvDo(t *testing.T, s *jiratest.Server, method, target, body string) srvReply {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, s.URL()+target, rdr)
	if err != nil {
		t.Fatalf("building %s %s: %v", method, target, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s %s: %v", method, target, err)
	}
	return srvReply{status: resp.StatusCode, header: resp.Header, body: got}
}

func srvNewServer(t *testing.T, opts ...jiratest.ServerOption) *jiratest.Server {
	t.Helper()
	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	return s
}

func srvFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := jiratest.Fixture(name)
	if err != nil {
		t.Fatalf("Fixture(%q): %v", name, err)
	}
	return b
}

func srvDecode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding %s: %v", body, err)
	}
	return out
}

func TestServer_ServesEveryDefaultRouteWithItsFixture(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	cases := []struct {
		name    string
		method  string
		target  string
		status  int
		fixture string
	}{
		{"search first page", http.MethodPost, "/rest/api/3/search/jql", http.StatusOK, "search_page1.json"},
		{"approximate count", http.MethodPost, "/rest/api/3/search/approximate-count", http.StatusOK, "approximate_count.json"},
		{"one issue", http.MethodGet, "/rest/api/3/issue/EX-1", http.StatusOK, "issue_rich_adf.json"},
		{"issue comments", http.MethodGet, "/rest/api/3/issue/EX-1/comment", http.StatusOK, "comments.json"},
		{"issue transitions", http.MethodGet, "/rest/api/3/issue/EX-1/transitions", http.StatusOK, "transitions.json"},
		{"field catalogue", http.MethodGet, "/rest/api/3/field", http.StatusOK, "field.json"},
		{"create metadata", http.MethodGet, "/rest/api/3/issue/createmeta/EX/issuetypes/10001", http.StatusOK, "createmeta_task.json"},
		{"the account", http.MethodGet, "/rest/api/3/myself", http.StatusOK, "myself.json"},
		{"site configuration", http.MethodGet, "/rest/api/3/configuration", http.StatusOK, "configuration.json"},
		{"permissions", http.MethodGet, "/rest/api/3/mypermissions?permissions=BULK_CHANGE", http.StatusOK, "mypermissions_admin.json"},
		{"project versions", http.MethodGet, "/rest/api/3/project/EX/version", http.StatusOK, "versions.json"},
		{"boards", http.MethodGet, "/rest/agile/1.0/board?projectKeyOrId=EX", http.StatusOK, "board.json"},
		{"board configuration", http.MethodGet, "/rest/agile/1.0/board/10/configuration", http.StatusOK, "board_config_estimation.json"},
		{"board sprints", http.MethodGet, "/rest/agile/1.0/board/10/sprint", http.StatusOK, "sprint_page.json"},
		{"plans refused", http.MethodGet, "/rest/api/3/plans/plan", http.StatusForbidden, "plans_403.json"},
		{"bulk move submitted", http.MethodPost, "/rest/api/3/bulk/issues/move", http.StatusCreated, "bulkmove_submit.json"},
		{"generic task", http.MethodGet, "/rest/api/3/task/11072", http.StatusOK, "task_complete.json"},
		{"bulk queue task", http.MethodGet, "/rest/api/3/bulk/queue/10641", http.StatusOK, "bulkmove_task_complete.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := srvDo(t, s, tc.method, tc.target, "")
			if got.status != tc.status {
				t.Errorf("status = %d, want %d", got.status, tc.status)
			}
			if ct := got.header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if want := srvFixture(t, tc.fixture); !bytes.Equal(got.body, want) {
				t.Errorf("body is not %s verbatim", tc.fixture)
			}
		})
	}
}

func TestServer_ChainsTheSecondSearchPageOnThePageOneToken(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	first := srvDo(t, s, http.MethodPost, "/rest/api/3/search/jql",
		`{"jql":"project = EX ORDER BY key","fields":["summary","status"],"maxResults":2}`)
	if first.status != http.StatusOK {
		t.Fatalf("first page status = %d, want 200", first.status)
	}
	token, _ := srvDecode(t, first.body)["nextPageToken"].(string)
	if token == "" {
		t.Fatal("the first page carries no nextPageToken")
	}

	second := srvDo(t, s, http.MethodPost, "/rest/api/3/search/jql",
		`{"jql":"project = EX ORDER BY key","fields":["summary","status"],"nextPageToken":`+strconv.Quote(token)+`}`)
	if second.status != http.StatusOK {
		t.Fatalf("second page status = %d, want 200", second.status)
	}
	page := srvDecode(t, second.body)
	if _, ok := page["nextPageToken"]; ok {
		t.Error("the second page still offers a nextPageToken")
	}
	if last, _ := page["isLast"].(bool); !last {
		t.Error("the second page does not set isLast")
	}
	if !bytes.Equal(second.body, srvFixture(t, "search_page2.json")) {
		t.Error("the second page is not search_page2.json verbatim")
	}
}

func TestServer_RefusesAPageTokenItNeverIssued(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	got := srvDo(t, s, http.MethodPost, "/rest/api/3/search/jql", `{"jql":"project = EX","nextPageToken":"made-up"}`)
	if got.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got.status)
	}
	if msgs, _ := srvDecode(t, got.body)["errorMessages"].([]any); len(msgs) == 0 {
		t.Error("the refusal carries no errorMessages")
	}
}

func TestServer_UnroutedPathAnswersAJiraShapedNotFound(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	got := srvDo(t, s, http.MethodGet, "/rest/api/3/there/is/no/such/thing", "")
	if got.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got.status)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	body := srvDecode(t, got.body)
	if msgs, _ := body["errorMessages"].([]any); len(msgs) == 0 {
		t.Error("the 404 carries no errorMessages")
	}
	if _, ok := body["errors"]; !ok {
		t.Error("the 404 has no errors object")
	}
}

func TestServer_WithStatusReplacesARouteWithAFailure(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t, jiratest.WithStatus(http.MethodGet, "/rest/api/3/field", http.StatusBadRequest, "validation_error.json"))

	got := srvDo(t, s, http.MethodGet, "/rest/api/3/field", "")
	if got.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got.status)
	}
	errs, _ := srvDecode(t, got.body)["errors"].(map[string]any)
	if _, ok := errs["summary"]; !ok {
		t.Errorf("the body has no per-field error for summary: %s", got.body)
	}
}

func TestServer_WithStatusAndNoFixtureSendsAnEmptyBody(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t, jiratest.WithStatus(http.MethodGet, "/rest/api/3/myself", http.StatusNoContent, ""))

	got := srvDo(t, s, http.MethodGet, "/rest/api/3/myself", "")
	if got.status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got.status)
	}
	if len(got.body) != 0 {
		t.Errorf("body = %q, want empty", got.body)
	}
}

func TestServer_WithRateLimitSendsRetryAfterAndTheRateLimitBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		retryAfter time.Duration
		want       string
	}{
		{"whole seconds pass through", 30 * time.Second, "30"},
		{"a fraction rounds up", 1500 * time.Millisecond, "2"},
		{"zero still asks for a second", 0, "1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := srvNewServer(t, jiratest.WithRateLimit(http.MethodGet, "/rest/api/3/field", tc.retryAfter))

			got := srvDo(t, s, http.MethodGet, "/rest/api/3/field", "")
			if got.status != http.StatusTooManyRequests {
				t.Fatalf("status = %d, want 429", got.status)
			}
			if ra := got.header.Get("Retry-After"); ra != tc.want {
				t.Errorf("Retry-After = %q, want %q", ra, tc.want)
			}
			if !bytes.Equal(got.body, srvFixture(t, "rate_limited.json")) {
				t.Error("body is not rate_limited.json verbatim")
			}
		})
	}
}

func TestServer_WithFixtureOverridesADefaultRoute(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t, jiratest.WithFixture(http.MethodGet, "/rest/api/3/mypermissions", "mypermissions_basic.json"))

	got := srvDo(t, s, http.MethodGet, "/rest/api/3/mypermissions", "")
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", got.status)
	}
	perms, _ := srvDecode(t, got.body)["permissions"].(map[string]any)
	if _, ok := perms["BULK_CHANGE"]; ok {
		t.Error("the basic permission set still reports BULK_CHANGE")
	}
	if _, ok := perms["EDIT_ISSUES"]; !ok {
		t.Error("the basic permission set lost EDIT_ISSUES")
	}
}

func TestServer_WithFixtureAddsARouteTheDefaultsDoNotCover(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t, jiratest.WithFixture(http.MethodGet, "/rest/agile/1.0/board/{id}/backlog", "search_page2.json"))

	got := srvDo(t, s, http.MethodGet, "/rest/agile/1.0/board/10/backlog", "")
	if got.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", got.status)
	}
	if !bytes.Equal(got.body, srvFixture(t, "search_page2.json")) {
		t.Error("the added route did not serve its fixture")
	}
}

func TestServer_WithHandlerTakesOverARouteEntirely(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t, jiratest.WithHandler(http.MethodGet, "/rest/api/3/myself", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream is down"))
	}))

	got := srvDo(t, s, http.MethodGet, "/rest/api/3/myself", "")
	if got.status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got.status)
	}
	if string(got.body) != "upstream is down" {
		t.Errorf("body = %q", got.body)
	}
}

func TestServer_ServesCreateMetaPerIssueType(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	task := srvDo(t, s, http.MethodGet, "/rest/api/3/issue/createmeta/EX/issuetypes/10001", "")
	bug := srvDo(t, s, http.MethodGet, "/rest/api/3/issue/createmeta/EX/issuetypes/10004", "")

	if bytes.Equal(task.body, bug.body) {
		t.Fatal("both issue types served the same create screen")
	}
	if got, want := srvRequiredFields(t, task.body), []string{"project", "issuetype", "summary"}; !slices.Equal(got, want) {
		t.Errorf("task required = %v, want %v", got, want)
	}
	if got, want := srvRequiredFields(t, bug.body), []string{"project", "issuetype", "summary", "description", "priority"}; !slices.Equal(got, want) {
		t.Errorf("bug required = %v, want %v", got, want)
	}
}

func srvRequiredFields(t *testing.T, body []byte) []string {
	t.Helper()
	var meta struct {
		Fields []struct {
			FieldID  string `json:"fieldId"`
			Required bool   `json:"required"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("decoding create metadata: %v", err)
	}
	var out []string
	for _, f := range meta.Fields {
		if f.Required {
			out = append(out, f.FieldID)
		}
	}
	return out
}

func TestServer_RecordsMethodPathQueryBodyAndHeaders(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL()+"/rest/api/3/search/jql?expand=names", strings.NewReader(`{"jql":"project = EX"}`))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.Header.Set("X-Atlassian-Token", "no-check")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("posting the search: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("draining the response: %v", err)
	}

	got := s.Requests()
	if len(got) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(got))
	}
	rec := got[0]
	if rec.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", rec.Method)
	}
	if rec.Path != "/rest/api/3/search/jql" {
		t.Errorf("Path = %q", rec.Path)
	}
	if rec.Query != "expand=names" {
		t.Errorf("Query = %q", rec.Query)
	}
	if rec.Body != `{"jql":"project = EX"}` {
		t.Errorf("Body = %q", rec.Body)
	}
	if v := rec.Header.Get("X-Atlassian-Token"); v != "no-check" {
		t.Errorf("X-Atlassian-Token = %q, want no-check", v)
	}
}

func TestServer_RequestsAreRecordedInOrderAndCopiedOut(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	srvDo(t, s, http.MethodGet, "/rest/api/3/myself", "")
	first := s.Requests()
	srvDo(t, s, http.MethodGet, "/rest/api/3/field", "")
	second := s.Requests()

	if len(first) != 1 || len(second) != 2 {
		t.Fatalf("recorded %d then %d requests, want 1 then 2", len(first), len(second))
	}
	if second[0].Path != "/rest/api/3/myself" || second[1].Path != "/rest/api/3/field" {
		t.Errorf("out of order: %q then %q", second[0].Path, second[1].Path)
	}
	first[0].Path = "mutated"
	if s.Requests()[0].Path != "/rest/api/3/myself" {
		t.Error("Requests handed out the server's own slice")
	}
}

func TestServer_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	s := jiratest.NewServer()
	s.Close()
	s.Close()
}

func TestFixture_ReadsAFixtureWithOrWithoutItsExtension(t *testing.T) {
	t.Parallel()

	withExt, err := jiratest.Fixture("myself.json")
	if err != nil {
		t.Fatalf("Fixture(myself.json): %v", err)
	}
	bare, err := jiratest.Fixture("myself")
	if err != nil {
		t.Fatalf("Fixture(myself): %v", err)
	}
	if !bytes.Equal(withExt, bare) {
		t.Error("the two spellings read different bytes")
	}
}

func TestFixture_ReportsAMissingFixture(t *testing.T) {
	t.Parallel()

	if _, err := jiratest.Fixture("no_such_fixture.json"); err == nil {
		t.Fatal("reading a fixture that does not exist succeeded")
	}
}
