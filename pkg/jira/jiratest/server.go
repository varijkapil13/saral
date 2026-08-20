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
	{http.MethodGet, "/rest/api/3/field", srvFixtureHandler(http.StatusOK, "field.json")},
	{http.MethodGet, "/rest/api/3/issue/createmeta/{projectIdOrKey}/issuetypes/{issueTypeId}", srvCreateMeta},
	{http.MethodGet, "/rest/api/3/myself", srvFixtureHandler(http.StatusOK, "myself.json")},
	{http.MethodGet, "/rest/api/3/configuration", srvFixtureHandler(http.StatusOK, "configuration.json")},
	{http.MethodGet, "/rest/api/3/mypermissions", srvFixtureHandler(http.StatusOK, "mypermissions_admin.json")},
	{http.MethodGet, "/rest/api/3/project/{key}/versions", srvFixtureHandler(http.StatusOK, "versions.json")},
	{http.MethodGet, "/rest/agile/1.0/board", srvFixtureHandler(http.StatusOK, "board.json")},
	{http.MethodGet, "/rest/agile/1.0/board/{id}/configuration", srvFixtureHandler(http.StatusOK, "board_config_estimation.json")},
	{http.MethodGet, "/rest/agile/1.0/board/{id}/sprint", srvFixtureHandler(http.StatusOK, "sprint_page.json")},
	{http.MethodGet, "/rest/api/3/plans/plan", srvFixtureHandler(http.StatusForbidden, "plans_403.json")},
	{http.MethodPost, "/rest/api/3/bulk/issues/move", srvFixtureHandler(http.StatusCreated, "bulkmove_submit.json")},
	{http.MethodGet, "/rest/api/3/task/{id}", srvFixtureHandler(http.StatusOK, "bulkmove_task_complete.json")},
	// The bulk move task is polled here, not on /task/{id}; both are served so
	// an adapter that follows the submit response and one that does not are
	// both exercised.
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

func srvCreateMeta(w http.ResponseWriter, r *http.Request) {
	name := "createmeta_task.json"
	if r.PathValue("issueTypeId") == srvBugIssueTypeID {
		name = "createmeta_bug.json"
	}
	srvServeFixture(w, http.StatusOK, name)
}

func srvNotFound(w http.ResponseWriter, r *http.Request) {
	srvWriteError(w, http.StatusNotFound, "No route matches "+r.Method+" "+r.URL.Path+" on this fixture server.")
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

func srvWriteJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The client having hung up is not something a fixture server can act on.
	_, _ = w.Write(body)
}
