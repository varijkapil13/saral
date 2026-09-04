package jiratest_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
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
		{"one attachment's metadata", http.MethodGet, "/rest/api/3/attachment/10502", http.StatusOK, "attachment_meta.json"},
		{"field catalogue", http.MethodGet, "/rest/api/3/field", http.StatusOK, "field.json"},
		{"create metadata", http.MethodGet, "/rest/api/3/issue/createmeta/EX/issuetypes/10001", http.StatusOK, "createmeta_task.json"},
		{"the account", http.MethodGet, "/rest/api/3/myself", http.StatusOK, "myself.json"},
		{"site configuration", http.MethodGet, "/rest/api/3/configuration", http.StatusOK, "configuration.json"},
		{"permissions", http.MethodGet, "/rest/api/3/mypermissions?permissions=BULK_CHANGE", http.StatusOK, "mypermissions_admin.json"},
		{"project versions", http.MethodGet, "/rest/api/3/project/EX/version", http.StatusOK, "versions.json"},
		{"one version", http.MethodGet, "/rest/api/3/version/10100", http.StatusOK, "version_one.json"},
		{"a version created", http.MethodPost, "/rest/api/3/version", http.StatusCreated, "version_created.json"},
		{"a version's unresolved issues", http.MethodGet, "/rest/api/3/version/10100/unresolvedIssueCount", http.StatusOK, "version_unresolved_count.json"},
		{"a version released", http.MethodPut, "/rest/api/3/version/10100", http.StatusOK, "version_released.json"},
		{"boards", http.MethodGet, "/rest/agile/1.0/board?projectKeyOrId=EX", http.StatusOK, "board.json"},
		{"board configuration", http.MethodGet, "/rest/agile/1.0/board/10/configuration", http.StatusOK, "board_config_estimation.json"},
		{"board sprints", http.MethodGet, "/rest/agile/1.0/board/10/sprint", http.StatusOK, "sprint_page.json"},
		{"one sprint", http.MethodGet, "/rest/agile/1.0/sprint/41", http.StatusOK, "sprint_one.json"},
		{"a sprint created", http.MethodPost, "/rest/agile/1.0/sprint", http.StatusCreated, "sprint_created.json"},
		{"a sprint updated in part", http.MethodPost, "/rest/agile/1.0/sprint/42", http.StatusOK, "sprint_updated.json"},
		{"board issues", http.MethodGet, "/rest/agile/1.0/board/10/issue", http.StatusOK, "board_issues.json"},
		{"board backlog", http.MethodGet, "/rest/agile/1.0/board/10/backlog", http.StatusOK, "board_issues.json"},
		{"board epics", http.MethodGet, "/rest/agile/1.0/board/10/epic", http.StatusOK, "board_epics.json"},
		{"board quick filters", http.MethodGet, "/rest/agile/1.0/board/10/quickfilter", http.StatusOK, "board_quickfilters.json"},
		{"plans refused", http.MethodGet, "/rest/api/3/plans/plan", http.StatusForbidden, "plans_403.json"},
		{"bulk move submitted", http.MethodPost, "/rest/api/3/bulk/issues/move", http.StatusCreated, "bulkmove_submit.json"},
		{"generic task", http.MethodGet, "/rest/api/3/task/11072", http.StatusOK, "task_complete.json"},
		{"bulk queue task", http.MethodGet, "/rest/api/3/bulk/queue/10641", http.StatusOK, "bulkmove_task_complete.json"},
		{"a site-wide account search", http.MethodGet, "/rest/api/3/user/search?query=ex", http.StatusOK, "user_search.json"},
		{"an assignable account search", http.MethodGet, "/rest/api/3/user/assignable/search?project=EX&query=", http.StatusOK, "user_assignable.json"},
		{"accounts by id", http.MethodGet, "/rest/api/3/user/bulk?accountId=5b10a2844c20165700ede21g", http.StatusOK, "user_bulk.json"},
		{"a project's statuses", http.MethodGet, "/rest/api/3/project/EX/statuses", http.StatusOK, "project_statuses.json"},
		{"the site's priorities", http.MethodGet, "/rest/api/3/priority/search", http.StatusOK, "priority_search.json"},
		{"the site's labels", http.MethodGet, "/rest/api/3/label", http.StatusOK, "labels.json"},
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

// A request that reaches the site and no handler is answered in RFC 7807, not in
// Jira's own error shape.
func TestServer_UnroutedPathAnswersTheProblemJSONARealSiteAnswers(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	const target = "/rest/api/3/there/is/no/such/thing"
	got := srvDo(t, s, http.MethodGet, target, "")
	if got.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got.status)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	body := srvDecode(t, got.body)
	if _, ok := body["errorMessages"]; ok {
		t.Error("the body carries errorMessages, which is the envelope a Jira handler answers in")
	}
	detail, _ := body["detail"].(string)
	if !strings.Contains(detail, http.MethodGet) || !strings.Contains(detail, target) {
		t.Errorf("detail = %q, want the method and path that reached no endpoint", detail)
	}
	if title, _ := body["title"].(string); title != http.StatusText(http.StatusNotFound) {
		t.Errorf("title = %q, want the status spelt out", title)
	}
	if status, _ := body["status"].(float64); int(status) != http.StatusNotFound {
		t.Errorf("status in the body = %v, want 404", status)
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

// srvGet never follows a redirect, which is the only way to see one.
func srvGet(t *testing.T, s *jiratest.Server, target string, header http.Header) srvReply {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL()+target, http.NoBody)
	if err != nil {
		t.Fatalf("building GET %s: %v", target, err)
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading GET %s: %v", target, err)
	}
	return srvReply{status: resp.StatusCode, header: resp.Header, body: got}
}

// An empty xsrf sends no X-Atlassian-Token at all.
func srvPostFile(t *testing.T, s *jiratest.Server, target, part, xsrf string) srvReply {
	t.Helper()

	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	file, err := mp.CreateFormFile(part, "rollout-notes.csv")
	if err != nil {
		t.Fatalf("building the %q part: %v", part, err)
	}
	if _, err := file.Write([]byte("id,phase\n10001,two\n")); err != nil {
		t.Fatalf("writing the %q part: %v", part, err)
	}
	if err := mp.Close(); err != nil {
		t.Fatalf("closing the multipart body: %v", err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL()+target, &body)
	if err != nil {
		t.Fatalf("building the upload: %v", err)
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())
	if xsrf != "" {
		req.Header.Set("X-Atlassian-Token", xsrf)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("posting the upload: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the upload's answer: %v", err)
	}
	return srvReply{status: resp.StatusCode, header: resp.Header, body: got}
}

func TestServer_AttachmentUploadNeedsTheXSRFHeaderAndAPartNamedFile(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)
	const target = "/rest/api/3/issue/EX-1/attachments"

	for _, tc := range []struct {
		name string
		xsrf string
	}{
		{"without the header the answer is a 404 that is not JSON at all", ""},
		{"a token that is not the site's own is refused just the same", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := srvPostFile(t, s, target, "file", tc.xsrf)

			if got.status != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", got.status)
			}
			if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("Content-Type = %q, want the plain-text body a JSON decode fails on", ct)
			}
			if !strings.Contains(string(got.body), "XSRF") {
				t.Errorf("body = %q, want the site's own words for the guard", got.body)
			}
		})
	}

	t.Run("a part under any other name is RFC 7807", func(t *testing.T) {
		t.Parallel()
		got := srvPostFile(t, s, target, "attachment", "no-check")

		if got.status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", got.status)
		}
		if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("Content-Type = %q, want application/problem+json", ct)
		}
		body := srvDecode(t, got.body)
		if detail, _ := body["detail"].(string); detail == "" {
			t.Error("the refusal carries no detail, which is the only part that says anything")
		}
		if _, ok := body["errorMessages"]; ok {
			t.Error("the refusal carries errorMessages, and a problem+json body does not")
		}
	})

	t.Run("a part named file answers the array, not one object", func(t *testing.T) {
		t.Parallel()
		got := srvPostFile(t, s, target, "file", "no-check")

		if got.status != http.StatusOK {
			t.Fatalf("status = %d, want 200", got.status)
		}
		if !bytes.Equal(got.body, srvFixture(t, "attachment_upload.json")) {
			t.Error("the upload is not serving attachment_upload.json verbatim")
		}
	})
}

func TestServer_AttachmentsSwitchedOffRefuseTheUploadInTheClassicEnvelope(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t, jiratest.WithStatus(http.MethodPost, "/rest/api/3/issue/{key}/attachments", http.StatusForbidden, "attachment_disabled.json"))

	got := srvPostFile(t, s, "/rest/api/3/issue/EX-1/attachments", "file", "no-check")
	if got.status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got.status)
	}
	if !bytes.Equal(got.body, srvFixture(t, "attachment_disabled.json")) {
		t.Error("body is not attachment_disabled.json verbatim")
	}
}

func TestServer_DeletingAnAttachmentAnswersNoContentAndNoBody(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	got := srvDo(t, s, http.MethodDelete, "/rest/api/3/attachment/10502", "")
	if got.status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", got.status)
	}
	if len(got.body) != 0 {
		t.Errorf("body = %q, want empty", got.body)
	}
}

// srvMediaStandIn mirrors the unexported route the fake's redirect points at.
const srvMediaStandIn = "/media/attachment/content/"

func TestServer_AttachmentContentRedirectsUnlessAskedNotTo(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)
	const id = "10502"
	const target = "/rest/api/3/attachment/content/" + id

	t.Run("asked not to redirect, Jira answers the bytes itself", func(t *testing.T) {
		t.Parallel()
		got := srvGet(t, s, target+"?redirect=false", http.Header{})

		if got.status != http.StatusOK {
			t.Fatalf("status = %d, want 200", got.status)
		}
		if location := got.header.Get("Location"); location != "" {
			t.Errorf("Location = %q, and a client that follows redirects cannot tell that apart from Jira serving the bytes", location)
		}
		if string(got.body) != jiratest.AttachmentContent {
			t.Errorf("body = %q, want the payload", got.body)
		}
	})

	for _, tc := range []struct {
		name      string
		byteRange string
	}{
		{"a plain read", ""},
		{"a resumed read, which is redirected just the same", "bytes=10-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			header := http.Header{}
			if tc.byteRange != "" {
				header.Set("Range", tc.byteRange)
			}
			got := srvGet(t, s, target, header)

			if got.status != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", got.status)
			}
			location := got.header.Get("Location")
			if location == "" {
				t.Fatal("the redirect carries no Location")
			}
			media, err := url.Parse(location)
			if err != nil {
				t.Fatalf("Location = %q does not parse: %v", location, err)
			}
			if strings.HasPrefix(media.Path, "/rest/") {
				t.Errorf("Location = %q still sits on the Jira API, and the download really leaves it for a media host", location)
			} else if media.Path != srvMediaStandIn+id {
				t.Errorf("Location path = %q, want the media route %q", media.Path, srvMediaStandIn+id)
			}
			if len(got.body) != 0 {
				t.Errorf("the redirect carries a body: %q", got.body)
			}
			followed := srvDo(t, s, http.MethodGet, location, "")
			if followed.status != http.StatusOK {
				t.Fatalf("following the redirect answered %d, want 200", followed.status)
			}
			if string(followed.body) != jiratest.AttachmentContent {
				t.Errorf("the media URL answered %q, want the payload", followed.body)
			}
		})
	}
}

func TestServer_AttachmentContentHonoursARangeSoADownloadCanResume(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)
	const target = "/rest/api/3/attachment/content/10502?redirect=false"
	full := jiratest.AttachmentContent

	t.Run("a suffix comes back as 206 saying which bytes it is", func(t *testing.T) {
		t.Parallel()
		const from = 40
		got := srvGet(t, s, target, http.Header{"Range": {fmt.Sprintf("bytes=%d-", from)}})

		if got.status != http.StatusPartialContent {
			t.Fatalf("status = %d, want 206", got.status)
		}
		if string(got.body) != full[from:] {
			t.Errorf("body = %q, want the payload from byte %d", got.body, from)
		}
		if want := fmt.Sprintf("bytes %d-%d/%d", from, len(full)-1, len(full)); got.header.Get("Content-Range") != want {
			t.Errorf("Content-Range = %q, want %q", got.header.Get("Content-Range"), want)
		}
	})

	t.Run("a start past the end is refused rather than answered short", func(t *testing.T) {
		t.Parallel()
		got := srvGet(t, s, target, http.Header{"Range": {fmt.Sprintf("bytes=%d-", len(full)+1)}})

		if got.status != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("status = %d, want 416", got.status)
		}
	})

	t.Run("a whole read says a range would have been served", func(t *testing.T) {
		t.Parallel()
		got := srvGet(t, s, target, http.Header{})

		if got.status != http.StatusOK {
			t.Fatalf("status = %d, want 200", got.status)
		}
		if string(got.body) != full {
			t.Errorf("body = %q, want the whole payload", got.body)
		}
		if ranges := got.header.Get("Accept-Ranges"); ranges != "bytes" {
			t.Errorf("Accept-Ranges = %q, want bytes, or nothing knows a resume is possible", ranges)
		}
	})
}

func TestServer_HasNoRouteForTheSprintPUTThatNullsOmittedFields(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)
	const target = "/rest/agile/1.0/sprint/42"

	partial := srvDo(t, s, http.MethodPost, target, `{"goal":"Stream the preview."}`)
	if partial.status != http.StatusOK {
		t.Fatalf("the partial update answered %d, want 200", partial.status)
	}

	full := srvDo(t, s, http.MethodPut, target, `{"name":"EX Sprint 7","state":"closed"}`)
	if full.status != http.StatusNotFound {
		t.Fatalf("PUT answered %d; the destructive full replace must reach no route at all", full.status)
	}
	if ct := full.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want the answer an unrouted path gets", ct)
	}
	if detail, _ := srvDecode(t, full.body)["detail"].(string); !strings.Contains(detail, http.MethodPut) {
		t.Errorf("detail = %q, want the method that reached no endpoint", detail)
	}
}

func TestServer_MovingIssuesToASprintOrTheBacklogAnswersNoContent(t *testing.T) {
	t.Parallel()
	s := srvNewServer(t)

	for _, target := range []string{"/rest/agile/1.0/sprint/42/issue", "/rest/agile/1.0/backlog/issue"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			got := srvDo(t, s, http.MethodPost, target, `{"issues":["EX-1","EX-2"]}`)

			if got.status != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", got.status)
			}
			if len(got.body) != 0 {
				t.Errorf("body = %q, want empty", got.body)
			}
		})
	}
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
