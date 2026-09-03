package jiratest

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed fixtures
var srvEmbedded embed.FS

// Fixtures is the embedded fixture tree, rooted at the fixture directory: an
// entry is named "myself.json", not "fixtures/myself.json".
var Fixtures fs.FS = srvFixtureFS{}

// Fixture reads one fixture. A name with no extension is read as JSON, so
// "myself" and "myself.json" name the same file.
func Fixture(name string) ([]byte, error) {
	if path.Ext(name) == "" {
		name += ".json"
	}
	b, err := fs.ReadFile(Fixtures, name)
	if err != nil {
		return nil, fmt.Errorf("jiratest: reading fixture %q: %w", name, err)
	}
	return b, nil
}

type srvFixtureFS struct{}

func (srvFixtureFS) Open(name string) (fs.File, error) {
	full, err := srvFixturePath("open", name)
	if err != nil {
		return nil, err
	}
	return srvEmbedded.Open(full)
}

func (srvFixtureFS) ReadDir(name string) ([]fs.DirEntry, error) {
	full, err := srvFixturePath("readdir", name)
	if err != nil {
		return nil, err
	}
	return srvEmbedded.ReadDir(full)
}

func (srvFixtureFS) ReadFile(name string) ([]byte, error) {
	full, err := srvFixturePath("readfile", name)
	if err != nil {
		return nil, err
	}
	return srvEmbedded.ReadFile(full)
}

func srvFixturePath(op, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return "fixtures", nil
	}
	return "fixtures/" + name, nil
}

// Request is one request the Server served, kept so a test can assert on what
// the adapter actually put on the wire — the XSRF header, the narrow field
// list, the page token it echoed back.
type Request struct {
	Method string
	Path   string
	Query  string
	Body   string
	Header http.Header
}

// Server replays fixture bytes over HTTP. It is how the cloud adapter is
// tested: real wire bytes, real headers, real status codes, on loopback.
//
//	s := jiratest.NewServer(jiratest.WithRateLimit(http.MethodGet, "/rest/api/3/field", 30*time.Second))
//	defer s.Close()
//
// A path no route covers answers 404 with a Jira-shaped error body rather than
// Go's plain-text one, so a test of the adapter's error mapping sees the bytes
// a real site would send.
type Server struct {
	ts     *httptest.Server
	routes map[string]http.HandlerFunc

	mu       sync.Mutex
	requests []Request
	once     sync.Once
}

// ServerOption overrides or adds a route before the Server starts listening.
type ServerOption func(*Server)

// NewServer starts a fixture server with the default routes, then applies the
// options in order. An option naming a method and pattern that already exist
// replaces that route; anything else adds one.
func NewServer(opts ...ServerOption) *Server {
	s := &Server{routes: make(map[string]http.HandlerFunc, len(srvDefaultRoutes)+1)}
	s.srvSet("", "/", srvNotFound)
	for _, r := range srvDefaultRoutes {
		s.srvSet(r.method, r.pattern, r.handler)
	}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	mux := http.NewServeMux()
	for _, key := range slices.Sorted(maps.Keys(s.routes)) {
		mux.HandleFunc(key, s.routes[key])
	}
	s.ts = httptest.NewServer(s.srvRecording(mux))
	return s
}

// URL is the base URL to point a client at, with no trailing slash.
func (s *Server) URL() string { return s.ts.URL }

// Close shuts the server down. It is safe to call more than once.
func (s *Server) Close() { s.once.Do(s.ts.Close) }

// Requests returns every request served so far, in order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// WithFixture answers a route with a fixture and HTTP 200.
func WithFixture(method, pattern, fixture string) ServerOption {
	return WithStatus(method, pattern, http.StatusOK, fixture)
}

// WithStatus answers a route with a fixture and a status of the caller's
// choosing. An empty fixture name sends the status with no body.
func WithStatus(method, pattern string, status int, fixture string) ServerOption {
	return WithHandler(method, pattern, srvFixtureHandler(status, fixture))
}

// WithRateLimit answers a route with 429, the rate_limited.json body and a
// Retry-After header. Anything under a second is sent as one, because
// Retry-After has no sub-second form.
func WithRateLimit(method, pattern string, retryAfter time.Duration) ServerOption {
	return WithHandler(method, pattern, srvRateLimitHandler(retryAfter))
}

// WithHandler answers a route with a handler of the caller's own, for the
// cases a fixture cannot express — a redirect, a truncated body, a hang.
func WithHandler(method, pattern string, h http.HandlerFunc) ServerOption {
	return func(s *Server) { s.srvSet(method, pattern, h) }
}

func (s *Server) srvSet(method, pattern string, h http.HandlerFunc) {
	s.routes[srvKey(method, pattern)] = h
}

func srvKey(method, pattern string) string {
	if method == "" {
		return pattern
	}
	return method + " " + pattern
}

func (s *Server) srvRecording(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			srvWriteError(w, http.StatusBadRequest, "the request body could not be read: "+err.Error())
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		s.mu.Lock()
		s.requests = append(s.requests, Request{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Body:   string(body),
			Header: r.Header.Clone(),
		})
		s.mu.Unlock()
		mux.ServeHTTP(w, r)
	})
}

type srvRoute struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// srvBugIssueTypeID is the issue type the createmeta fixtures call a Bug; the
// default createmeta route answers with createmeta_bug.json for it and
// createmeta_task.json for everything else.
const srvBugIssueTypeID = "10004"

var srvDefaultRoutes = []srvRoute{
	{http.MethodPost, "/rest/api/3/search/jql", srvSearch},
	{http.MethodPost, "/rest/api/3/search/approximate-count", srvFixtureHandler(http.StatusOK, "approximate_count.json")},
	{http.MethodGet, "/rest/api/3/issue/{key}", srvFixtureHandler(http.StatusOK, "issue_rich_adf.json")},
	{http.MethodGet, "/rest/api/3/issue/{key}/comment", srvFixtureHandler(http.StatusOK, "comments.json")},
	{http.MethodGet, "/rest/api/3/issue/{key}/transitions", srvFixtureHandler(http.StatusOK, "transitions.json")},
	// An attachment id is a number on this route and a string in the upload's
	// answer, which is one attachment answering in two JSON types.
	{http.MethodGet, "/rest/api/3/attachment/{id}", srvFixtureHandler(http.StatusOK, "attachment_meta.json")},
	{http.MethodPost, "/rest/api/3/issue/{key}/attachments", srvUpload},
	{http.MethodDelete, "/rest/api/3/attachment/{id}", srvFixtureHandler(http.StatusNoContent, "")},
	// The media route below is not a Jira endpoint: it stands in for the host
	// the redirect points at, which is where a 206 really comes from.
	{http.MethodGet, "/rest/api/3/attachment/content/{id}", srvAttachmentContent},
	{http.MethodGet, srvMediaPath + "{id}", srvAttachmentBytes},
	{http.MethodGet, "/rest/api/3/field", srvFixtureHandler(http.StatusOK, "field.json")},
	{http.MethodGet, "/rest/api/3/issue/createmeta/{projectIdOrKey}/issuetypes", srvFixtureHandler(http.StatusOK, "createmeta_issuetypes.json")},
	{http.MethodGet, "/rest/api/3/issue/createmeta/{projectIdOrKey}/issuetypes/{issueTypeId}", srvCreateMeta},
	{http.MethodGet, "/rest/api/3/myself", srvFixtureHandler(http.StatusOK, "myself.json")},
	// Two user searches, and the difference between them is the point: the
	// site-wide one answers every kind of account, and the assignable one answers
	// only the people, which is how a real site drops its app accounts.
	{http.MethodGet, "/rest/api/3/user/search", srvFixtureHandler(http.StatusOK, "user_search.json")},
	{http.MethodGet, "/rest/api/3/user/assignable/search", srvFixtureHandler(http.StatusOK, "user_assignable.json")},
	{http.MethodGet, "/rest/api/3/user/bulk", srvOffsetPages("user_bulk.json", "user_bulk_page2.json")},
	{http.MethodGet, "/rest/api/3/project/{key}/statuses", srvFixtureHandler(http.StatusOK, "project_statuses.json")},
	{http.MethodGet, "/rest/api/3/priority/search", srvFixtureHandler(http.StatusOK, "priority_search.json")},
	{http.MethodGet, "/rest/api/3/label", srvOffsetPages("labels.json", "labels_page2.json")},
	{http.MethodGet, "/rest/api/3/configuration", srvFixtureHandler(http.StatusOK, "configuration.json")},
	{http.MethodGet, "/rest/api/3/mypermissions", srvFixtureHandler(http.StatusOK, "mypermissions_admin.json")},
	// versions.json is a paged envelope, which is what the singular /version
	// endpoint answers; the plural /versions answers a bare array and cannot page.
	{http.MethodGet, "/rest/api/3/project/{key}/version", srvFixtureHandler(http.StatusOK, "versions.json")},
	{http.MethodPost, "/rest/api/3/version", srvFixtureHandler(http.StatusCreated, "version_created.json")},
	{http.MethodGet, "/rest/api/3/version/{id}", srvFixtureHandler(http.StatusOK, "version_one.json")},
	{http.MethodGet, "/rest/api/3/version/{id}/unresolvedIssueCount", srvFixtureHandler(http.StatusOK, "version_unresolved_count.json")},
	{http.MethodPut, "/rest/api/3/version/{id}", srvFixtureHandler(http.StatusOK, "version_released.json")},
	{http.MethodGet, "/rest/agile/1.0/board", srvFixtureHandler(http.StatusOK, "board.json")},
	{http.MethodGet, "/rest/agile/1.0/board/{id}/configuration", srvFixtureHandler(http.StatusOK, "board_config_estimation.json")},
	{http.MethodGet, "/rest/agile/1.0/board/{id}/sprint", srvFixtureHandler(http.StatusOK, "sprint_page.json")},
	{http.MethodGet, "/rest/agile/1.0/sprint/{id}", srvFixtureHandler(http.StatusOK, "sprint_one.json")},
	{http.MethodPost, "/rest/agile/1.0/sprint", srvFixtureHandler(http.StatusCreated, "sprint_created.json")},
	// There is deliberately no PUT route: it nulls every field the request omits.
	{http.MethodPost, "/rest/agile/1.0/sprint/{id}", srvFixtureHandler(http.StatusOK, "sprint_updated.json")},
	{http.MethodPost, "/rest/agile/1.0/sprint/{id}/issue", srvFixtureHandler(http.StatusNoContent, "")},
	{http.MethodPost, "/rest/agile/1.0/backlog/issue", srvFixtureHandler(http.StatusNoContent, "")},
	// Three Agile paging envelopes, one per shape: these two name their array
	// issues and send no isLast, the epics send no total, and the sprints above
	// send all four keys.
	{http.MethodGet, "/rest/agile/1.0/board/{id}/issue", srvFixtureHandler(http.StatusOK, "board_issues.json")},
	{http.MethodGet, "/rest/agile/1.0/board/{id}/backlog", srvFixtureHandler(http.StatusOK, "board_issues.json")},
	{http.MethodGet, "/rest/agile/1.0/board/{id}/epic", srvFixtureHandler(http.StatusOK, "board_epics.json")},
	{http.MethodGet, "/rest/agile/1.0/board/{id}/quickfilter", srvFixtureHandler(http.StatusOK, "board_quickfilters.json")},
	// 403 is the normal answer — the Plans API needs Administer Jira — so it is
	// the default. A test that wants the reachable case overrides the route:
	//   WithFixture(http.MethodGet, "/rest/api/3/plans/plan", "plans_ok.json")
	{http.MethodGet, "/rest/api/3/plans/plan", srvFixtureHandler(http.StatusForbidden, "plans_403.json")},
	{http.MethodPost, "/rest/api/3/bulk/issues/move", srvFixtureHandler(http.StatusCreated, "bulkmove_submit.json")},
	// Two task endpoints answering two shapes that do not decode as each other:
	// the generic one answers TaskProgressBeanObject, the bulk move is polled on
	// its own queue route. Both default to the finished state; a test picks
	// another the way it picks any other fixture:
	//   WithFixture(http.MethodGet, "/rest/api/3/task/{id}", "task_running.json")
	{http.MethodGet, "/rest/api/3/task/{id}", srvFixtureHandler(http.StatusOK, "task_complete.json")},
	{http.MethodGet, "/rest/api/3/bulk/queue/{id}", srvFixtureHandler(http.StatusOK, "bulkmove_task_complete.json")},
}

// srvPageToken is the token search_page1.json hands out, read from the fixture
// so the two can never drift apart.
var srvPageToken = sync.OnceValues(func() (string, error) {
	b, err := Fixture("search_page1.json")
	if err != nil {
		return "", err
	}
	var page struct {
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.Unmarshal(b, &page); err != nil {
		return "", fmt.Errorf("jiratest: parsing search_page1.json: %w", err)
	}
	if page.NextPageToken == "" {
		return "", errors.New("jiratest: search_page1.json carries no nextPageToken")
	}
	return page.NextPageToken, nil
})

func srvSearch(w http.ResponseWriter, r *http.Request) {
	want, err := srvPageToken()
	if err != nil {
		srvWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		srvWriteError(w, http.StatusBadRequest, "the request body could not be read: "+err.Error())
		return
	}
	var req struct {
		NextPageToken string `json:"nextPageToken"`
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			srvWriteError(w, http.StatusBadRequest, "the request body is not valid JSON: "+err.Error())
			return
		}
	}
	switch req.NextPageToken {
	case "":
		srvServeFixture(w, http.StatusOK, "search_page1.json")
	case want:
		srvServeFixture(w, http.StatusOK, "search_page2.json")
	default:
		srvWriteError(w, http.StatusBadRequest, "The nextPageToken is not one this search issued.")
	}
}

// srvOffsetPages replays an offset-paginated endpoint one page per startAt, so
// that a walk over it terminates on the fixtures rather than on the first page
// answered forever. A startAt past the last page answers the last page, which
// still says isLast and so still ends the walk.
func srvOffsetPages(pages ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		at := 0
		for i, name := range pages {
			body, err := Fixture(name)
			if err != nil {
				srvWriteError(w, http.StatusInternalServerError, err.Error())
				return
			}
			var page struct {
				MaxResults int `json:"maxResults"`
			}
			if err := json.Unmarshal(body, &page); err != nil {
				srvWriteError(w, http.StatusInternalServerError, "jiratest: parsing "+name+": "+err.Error())
				return
			}
			if at == srvStartAt(r) || i == len(pages)-1 {
				srvWriteJSON(w, http.StatusOK, body)
				return
			}
			at += page.MaxResults
		}
		srvWriteError(w, http.StatusInternalServerError, "jiratest: srvOffsetPages was given no pages")
	}
}

func srvStartAt(r *http.Request) int {
	at, err := strconv.Atoi(r.URL.Query().Get("startAt"))
	if err != nil || at < 0 {
		return 0
	}
	return at
}

// AttachmentContent is the payload the attachment content route streams.
const AttachmentContent = "saral fixture attachment: deterministic bytes for a resumed download\n"

// srvMediaPath stands in for the media host a real site redirects an attachment
// download to.
const srvMediaPath = "/media/attachment/content/"

const srvUploadMemory = 1 << 20

// srvUpload's two refusals are neither of them the classic error envelope a
// decoder written for the rest of the API expects.
func srvUpload(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Atlassian-Token") != "no-check" {
		w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("XSRF check failed"))
		return
	}
	if err := r.ParseMultipartForm(srvUploadMemory); err != nil {
		srvWriteProblem(w, http.StatusBadRequest, "The request body could not be read as a multipart upload.", r.URL.Path)
		return
	}
	if len(r.MultipartForm.File["file"]) == 0 {
		srvWriteProblem(w, http.StatusBadRequest, `The multipart request carries no part named "file".`, r.URL.Path)
		return
	}
	srvServeFixture(w, http.StatusOK, "attachment_upload.json")
}

func srvAttachmentContent(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("redirect") != "false" {
		w.Header().Set("Location", srvMediaPath+r.PathValue("id")+"?token=fixture-signed-token&expires=1770814500")
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	srvAttachmentBytes(w, r)
}

func srvAttachmentBytes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Time{}, strings.NewReader(AttachmentContent))
}

func srvCreateMeta(w http.ResponseWriter, r *http.Request) {
	name := "createmeta_task.json"
	if r.PathValue("issueTypeId") == srvBugIssueTypeID {
		name = "createmeta_bug.json"
	}
	srvServeFixture(w, http.StatusOK, name)
}

// srvNotFound answers a path no route covers. A real site answers that in RFC
// 7807 rather than in Jira's own error shape, because the request never reached a
// Jira handler at all.
func srvNotFound(w http.ResponseWriter, r *http.Request) {
	srvWriteProblem(w, http.StatusNotFound, "No endpoint "+r.Method+" "+r.URL.Path+".", r.URL.Path)
}

func srvFixtureHandler(status int, fixture string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		srvServeFixture(w, status, fixture)
	}
}

func srvRateLimitHandler(retryAfter time.Duration) http.HandlerFunc {
	seconds := max(1, int((retryAfter+time.Second-1)/time.Second))
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		srvServeFixture(w, http.StatusTooManyRequests, "rate_limited.json")
	}
}

func srvServeFixture(w http.ResponseWriter, status int, fixture string) {
	if fixture == "" {
		w.WriteHeader(status)
		return
	}
	body, err := Fixture(fixture)
	if err != nil {
		srvWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	srvWriteJSON(w, status, body)
}

type srvErrorBody struct {
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"`
}

func srvWriteError(w http.ResponseWriter, status int, message string) {
	body, err := json.Marshal(srvErrorBody{ErrorMessages: []string{message}, Errors: map[string]string{}})
	if err != nil {
		http.Error(w, message, status)
		return
	}
	srvWriteJSON(w, status, body)
}

// srvProblemBody is RFC 7807, which is what answers a request that reached the
// site and no handler. type is always about:blank and title is only the status
// spelt out; detail is the only part that says anything.
type srvProblemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func srvWriteProblem(w http.ResponseWriter, status int, detail, instance string) {
	body, err := json.Marshal(srvProblemBody{
		Type:     "about:blank",
		Title:    http.StatusText(status),
		Status:   status,
		Detail:   detail,
		Instance: instance,
	})
	if err != nil {
		http.Error(w, detail, status)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func srvWriteJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The client having hung up is not something a fixture server can act on.
	_, _ = w.Write(body)
}
