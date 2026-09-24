package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// scriptedDoer answers each request with the next reply in its script and
// records the URL every request went to, without a server.
type scriptedDoer struct {
	mu      sync.Mutex
	replies []scripted
	sent    []string
}

type scripted struct {
	status int
	header http.Header
	body   string
	err    error
}

func (d *scriptedDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, req.URL.String())
	if len(d.replies) == 0 {
		return nil, errors.New("the script has no reply left")
	}
	next := d.replies[0]
	d.replies = d.replies[1:]
	if next.err != nil {
		return nil, next.err
	}
	header := next.header
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: next.status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(next.body)),
		Request:    req,
	}, nil
}

func (d *scriptedDoer) urls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.sent...)
}

func reply(status int, header http.Header, body string) scripted {
	return scripted{status: status, header: header, body: body}
}

func wantValidation(t *testing.T, err error, field string) {
	t.Helper()

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if _, ok := invalid.For(field); !ok {
		t.Errorf("the refusal %v names no %s", invalid, field)
	}
}

func TestFieldJSON_RefusesANumberJSONCannotCarry(t *testing.T) {
	t.Parallel()

	for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		raw, err := fieldJSON("customfield_1", jira.FieldValue{Kind: jira.KindNumber, Number: n})
		if raw != nil {
			t.Errorf("fieldJSON(%v) wrote %s, want nothing written", n, raw)
		}
		wantValidation(t, err, "customfield_1")
	}
	raw, err := fieldJSON("customfield_1", jira.FieldValue{Kind: jira.KindNumber, Number: 2.5})
	if err != nil || string(raw) != "2.5" {
		t.Errorf("fieldJSON(2.5) = %s, %v, want 2.5", raw, err)
	}
}

func TestUpdateIssue_SendsNothingForANumberThatIsNotFinite(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()
	c, _ := testClient(t, s.URL())

	points := jira.FieldRef{ID: "customfield_1"}
	err := c.UpdateIssue(t.Context(), testIssueKey, jira.IssuePatch{
		Fields: jira.FieldSet{}.With(points, jira.FieldValue{Kind: jira.KindNumber, Number: math.NaN()}),
	})
	wantValidation(t, err, "customfield_1")
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests, want none: a NaN reaches Jira as null and empties the field", served)
	}
}

func TestMustJSON_FailsTheBodyRatherThanWritingNull(t *testing.T) {
	t.Parallel()

	broken := map[string]json.RawMessage{"id": json.RawMessage("{not json")}
	if raw := mustJSON(broken); len(raw) != 0 {
		t.Fatalf("mustJSON(invalid) = %s, want the empty message that fails the body", raw)
	}
	if raw := jsonArray([]json.RawMessage{jsonObject("id", "1"), mustJSON(broken)}); len(raw) != 0 {
		t.Errorf("jsonArray with a failed member = %s, want it empty too", raw)
	}
	_, _, err := encodeBody(request{
		method: http.MethodPut,
		path:   issuePath + "/EX-1",
		body:   apiIssueWrite{Fields: map[string]json.RawMessage{"customfield_1": mustJSON(broken)}},
	})
	if err == nil {
		t.Error("a body carrying a value that failed to encode was encoded, and would have reached Jira")
	}
	if got := string(jsonArray([]json.RawMessage{jsonObject("id", "1"), jsonObject("id", "2")})); got != `[{"id":"1"},{"id":"2"}]` {
		t.Errorf("jsonArray = %s", got)
	}
}

func TestNew_AllowsPlainHTTPOnlyOnLoopback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		site string
		ok   bool
	}{
		{site: "http://example.atlassian.net", ok: false},
		{site: "http://10.0.0.8:8080", ok: false},
		{site: "http://localhost.example.net", ok: false},
		{site: "http://127.0.0.1:8080", ok: true},
		{site: "http://127.8.0.1", ok: true},
		{site: "http://[::1]:8080", ok: true},
		{site: "http://localhost:2990", ok: true},
		{site: "http://LOCALHOST", ok: true},
		{site: "https://example.atlassian.net", ok: true},
	}
	for _, tt := range tests {
		_, err := New(tt.site, testEmail, testToken)
		switch {
		case tt.ok && err != nil:
			t.Errorf("New(%q) refused: %v", tt.site, err)
		case !tt.ok && err == nil:
			t.Errorf("New(%q) built a client that would send the token in the clear", tt.site)
		case !tt.ok && strings.Contains(err.Error(), testToken):
			t.Errorf("New(%q) put the token in its error: %v", tt.site, err)
		}
	}
}

func TestRetry_HandsBackARetryAfterLongerThanThePolicyWaits(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithRateLimit(http.MethodGet, "/rest/api/3/field", time.Hour))
	defer s.Close()
	c, clock := testClient(t, s.URL())

	_, err := c.do(t.Context(), fieldRequest())
	after, limited := jira.RetryAfter(err)
	if !limited || after != time.Hour {
		t.Fatalf("got %v, want the rate limit with the hour the site asked for", err)
	}
	if waits := clock.waited(); len(waits) != 0 {
		t.Errorf("the client waited %v, want no wait at all", waits)
	}
	if served := len(s.Requests()); served != 1 {
		t.Errorf("the site served %d requests, want 1", served)
	}
}

func TestDo_RefusesABodyLargerThanTheCeilingWithoutRetrying(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 65))
	}))
	defer s.Close()
	c, _ := testClient(t, s.URL())
	c.responseCeil = 64

	_, err := c.do(t.Context(), fieldRequest())
	var broken *jira.TransportError
	if !errors.As(err, &broken) || !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("got %T (%v), want a transport failure naming the ceiling", err, err)
	}
	if served := len(s.Requests()); served != 1 {
		t.Errorf("the site served %d requests, want 1: the same answer would come back as large", served)
	}

	c.responseCeil = 65
	if _, err := c.do(t.Context(), fieldRequest()); err != nil {
		t.Errorf("a body exactly at the ceiling: %v", err)
	}
}

func TestDo_GivesUpOnABodyThatStopsArriving(t *testing.T) {
	t.Parallel()

	release, letGo := gate()
	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	defer closeServer(t, s)
	defer letGo()
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	c.readIdle = 50 * time.Millisecond

	_, err := c.do(t.Context(), fieldRequest())
	var broken *jira.TransportError
	if !errors.As(err, &broken) || !errors.Is(err, errReadIdle) {
		t.Fatalf("got %T (%v), want a transport failure saying the site stopped sending", err, err)
	}
}

func TestDo_KeepsReadingABodyThatKeepsArriving(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, "/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte(`[`))
		for i := range 12 {
			if i > 0 {
				_, _ = w.Write([]byte(`,`))
			}
			_, _ = w.Write([]byte(`{"id":"f` + strconv.Itoa(i) + `"}`))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(25 * time.Millisecond)
		}
		_, _ = w.Write([]byte(`]`))
	}))
	defer s.Close()
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	c.readIdle = 200 * time.Millisecond

	var fields []struct {
		ID string `json:"id"`
	}
	if err := c.doJSON(t.Context(), fieldRequest(), &fields); err != nil {
		t.Fatalf("a body that took longer than the idle bound in total, but never paused that long: %v", err)
	}
	if len(fields) != 12 {
		t.Errorf("read %d fields, want 12", len(fields))
	}
}

func TestIssueMethods_RefuseAKeyThatWouldLeaveThePath(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()
	c, _ := testClient(t, s.URL())
	ctx := t.Context()
	doc := adf.Doc{Type: "doc", Version: 1, Content: []adf.Node{{Type: "paragraph", Content: []adf.Node{{Type: "text", Text: "hi"}}}}}

	for _, key := range []string{"../../myself", "EX-1/../../myself", "..", "EX-1?x=1", "EX 1", "EX-1%2F..", "EX-"} {
		checks := map[string]func() error{
			"Issue":       func() error { _, err := c.Issue(ctx, key); return err },
			"UpdateIssue": func() error { return c.UpdateIssue(ctx, key, jira.IssuePatch{Summary: str("s")}) },
			"Transitions": func() error { _, err := c.Transitions(ctx, key); return err },
			"Transition":  func() error { return c.Transition(ctx, key, "31", jira.IssuePatch{}) },
			"EditMeta":    func() error { _, err := c.EditMeta(ctx, key); return err },
			"Attachments": func() error { _, err := c.Attachments(ctx, key); return err },
			"Comments":    func() error { _, err := c.Comments(ctx, key); return err },
			"AddComment":  func() error { _, err := c.AddComment(ctx, key, doc); return err },
		}
		for name, call := range checks {
			var invalid *jira.ValidationError
			if err := call(); !errors.As(err, &invalid) {
				t.Errorf("%s(%q) = %v, want a validation refusal", name, key, err)
			}
		}
	}
	for _, id := range []string{"../1", "10701/..", "abc", ""} {
		if err := c.DeleteComment(ctx, testIssueKey, id); !isValidation(err) {
			t.Errorf("DeleteComment(%q) = %v, want a validation refusal", id, err)
		}
		if _, err := c.EditComment(ctx, testIssueKey, id, doc); !isValidation(err) {
			t.Errorf("EditComment(%q) = %v, want a validation refusal", id, err)
		}
		if err := c.DeleteAttachment(ctx, id); !isValidation(err) {
			t.Errorf("DeleteAttachment(%q) = %v, want a validation refusal", id, err)
		}
		if _, err := c.UnresolvedCount(ctx, id); !isValidation(err) {
			t.Errorf("UnresolvedCount(%q) = %v, want a validation refusal", id, err)
		}
	}
	for _, project := range []string{"..", "EX/../x", "E X", "EX%2F"} {
		if _, err := c.Versions(ctx, project); !isValidation(err) {
			t.Errorf("Versions(%q) = %v, want a validation refusal", project, err)
		}
		if _, err := c.CreateMeta(ctx, project, "10001"); !isValidation(err) {
			t.Errorf("CreateMeta(%q) = %v, want a validation refusal", project, err)
		}
	}
	if _, err := c.CreateMeta(ctx, "EX", "../1"); !isValidation(err) {
		t.Errorf("CreateMeta with a type id of ../1 = %v, want a validation refusal", err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests, want none: %v", served, s.Requests())
	}
}

func isValidation(err error) bool {
	var invalid *jira.ValidationError
	return errors.As(err, &invalid)
}

func TestDo_RefusesADotSegmentHoweverThePathWasBuilt(t *testing.T) {
	t.Parallel()

	doer := &scriptedDoer{}
	c, _ := testClient(t, "example.atlassian.net", WithHTTPClient(doer))
	for _, path := range []string{"/rest/api/3/issue/../../myself", "/rest/api/3/issue/%2E%2E/myself", "/rest/api/3/issue/./x", "/rest/%zz"} {
		if _, err := c.do(t.Context(), request{method: http.MethodGet, path: path}); err == nil {
			t.Errorf("do(%s) sent the request", path)
		}
		if _, err := c.doStream(t.Context(), request{method: http.MethodGet, path: path}, attachmentAnswered); err == nil {
			t.Errorf("doStream(%s) sent the request", path)
		}
	}
	if sent := doer.urls(); len(sent) != 0 {
		t.Errorf("the client sent %v", sent)
	}
}

func TestEndpoint_EscapesAnInterpolatedValueExactlyOnce(t *testing.T) {
	t.Parallel()

	c, _ := testClient(t, "https://proxy.example/ji ra")
	got := c.endpoint(request{method: http.MethodGet, path: projectVersionsPath("A B:ü")})
	want := "https://proxy.example/ji%20ra/rest/api/3/project/A%20B:%C3%BC/version"
	if got != want {
		t.Errorf("endpoint = %s, want %s", got, want)
	}

	doer := &scriptedDoer{replies: []scripted{
		reply(http.StatusOK, nil, `{"id":"10701"}`),
	}}
	c, _ = testClient(t, "example.atlassian.net", WithHTTPClient(doer))
	if _, err := c.comment(t.Context(), "EX-1", "10 701"); err != nil {
		t.Fatalf("reading a comment: %v", err)
	}
	if sent := doer.urls(); len(sent) != 1 || !strings.HasSuffix(sent[0], "/issue/EX-1/comment/10%20701") {
		t.Errorf("sent %v, want the id escaped once rather than as %%2520", sent)
	}
}

func TestDelete_ReadsANotFoundAfterAFailedAttemptAsDone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		first scripted
		ok    bool
	}{
		{name: "a 5xx that may have deleted it", first: reply(http.StatusBadGateway, nil, ""), ok: true},
		{name: "a connection lost with the answer", first: scripted{err: io.ErrUnexpectedEOF}, ok: true},
		{name: "a 429, which ran nothing", first: reply(http.StatusTooManyRequests, http.Header{"Retry-After": {"1"}}, ""), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doer := &scriptedDoer{replies: []scripted{
				tt.first,
				reply(http.StatusNotFound, nil, `{"errorMessages":["gone"]}`),
			}}
			c, _ := testClient(t, "example.atlassian.net", WithHTTPClient(doer))
			err := c.DeleteComment(t.Context(), testIssueKey, testCommentID)
			var missing *jira.NotFoundError
			switch {
			case tt.ok && err != nil:
				t.Errorf("DeleteComment = %v, want success: the first attempt did the delete", err)
			case !tt.ok && !errors.As(err, &missing):
				t.Errorf("DeleteComment = %v, want not found: nothing ran before the 404", err)
			}
			if sent := len(doer.urls()); sent != 2 {
				t.Errorf("sent %d requests, want 2", sent)
			}
		})
	}

	doer := &scriptedDoer{replies: []scripted{
		reply(http.StatusNotFound, nil, `{"errorMessages":["no such comment"]}`),
	}}
	c, _ := testClient(t, "example.atlassian.net", WithHTTPClient(doer))
	var missing *jira.NotFoundError
	if err := c.DeleteComment(t.Context(), testIssueKey, testCommentID); !errors.As(err, &missing) {
		t.Errorf("a first-attempt 404 = %v, want not found", err)
	}
}

func TestDelete_StillReportsAForbiddenAndATransportFailure(t *testing.T) {
	t.Parallel()

	doer := &scriptedDoer{replies: []scripted{
		reply(http.StatusBadGateway, nil, ""),
		reply(http.StatusForbidden, nil, `{"errorMessages":["no"]}`),
	}}
	c, _ := testClient(t, "example.atlassian.net", WithHTTPClient(doer))
	var refused *jira.CapabilityError
	if err := c.DeleteAttachment(t.Context(), testAttachmentID); !errors.As(err, &refused) {
		t.Errorf("a 403 after a 502 = %v, want the capability refusal", err)
	}

	failing := &stubDoer{err: io.ErrUnexpectedEOF}
	c, _ = testClient(t, "example.atlassian.net", WithHTTPClient(failing))
	var broken *jira.TransportError
	if err := c.DeleteAttachment(t.Context(), testAttachmentID); !errors.As(err, &broken) {
		t.Errorf("a delete that never got an answer = %v, want a transport failure", err)
	}
}

func TestCoalesce_AReadAfterAWriteNeverJoinsAReadFromBeforeIt(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	var reads atomic.Int64
	s := jiratest.NewServer(
		jiratest.WithHandler(http.MethodGet, "/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
			if reads.Add(1) == 1 {
				announce()
				<-release
				_, _ = w.Write([]byte(`[{"id":"before"}]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":"after"}]`))
		}),
		jiratest.WithHandler(http.MethodPut, "/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	defer closeServer(t, s)
	defer letGo()
	c, _ := testClient(t, s.URL())
	stale := make(chan error, 1)
	go func() {
		_, err := c.do(t.Context(), fieldRequest())
		stale <- err
	}()
	receive(t, "the read from before the write to reach the site", arrived)

	if _, err := c.do(t.Context(), request{method: http.MethodPut, path: "/rest/api/3/field", body: map[string]string{"x": "y"}}); err != nil {
		t.Fatalf("the write: %v", err)
	}

	fresh := make(chan *response, 1)
	go func() {
		resp, err := c.do(t.Context(), fieldRequest())
		if err != nil {
			t.Errorf("the read after the write: %v", err)
		}
		fresh <- resp
	}()
	waitUntil(t, "the read after the write to reach the site on its own", func() bool { return reads.Load() == 2 })
	resp := receive(t, "the read after the write", fresh)
	letGo()
	if err := receive(t, "the read from before the write", stale); err != nil {
		t.Fatalf("the read from before the write: %v", err)
	}
	if resp == nil || !strings.Contains(string(resp.body), "after") {
		t.Errorf("the read after the write was handed %q, the answer to a read from before it", resp.body)
	}
}

func TestUserLocation_LoadsEachZoneOnce(t *testing.T) {
	t.Parallel()

	first := userLocation("Pacific/Chatham")
	if first == nil || first.String() != "Pacific/Chatham" {
		t.Fatalf("userLocation = %v, want Pacific/Chatham", first)
	}
	if again := userLocation("Pacific/Chatham"); again != first {
		t.Error("a second read of one zone loaded it again")
	}
	if bad := userLocation("Not/AZone"); bad != nil {
		t.Errorf("an unknown zone = %v, want none", bad)
	}
	if _, kept := locations.Load("Not/AZone"); kept {
		t.Error("an unknown zone was kept, so a site could grow the cache without bound")
	}
}

func BenchmarkUserDomain(b *testing.B) {
	u := apiUser{AccountID: "5b10a2844c20165700ede21g", DisplayName: "Sam", TimeZone: "Europe/Berlin"}
	b.ReportAllocs()
	for b.Loop() {
		_ = u.domain()
	}
}

func TestDownload_RefusesARedirectAwayFromHTTPS(t *testing.T) {
	t.Parallel()

	for _, location := range []string{"http://media.example/file?token=t", "ftp://media.example/file", "//media.example/file"} {
		doer := &scriptedDoer{replies: []scripted{
			reply(http.StatusSeeOther, http.Header{"Location": {location}}, ""),
			reply(http.StatusOK, nil, "leaked"),
		}}
		c, _ := testClient(t, "example.atlassian.net", WithHTTPClient(doer))
		var got bytes.Buffer
		err := c.Download(t.Context(), testAttachmentID, &got, jira.DownloadOptions{})
		var broken *jira.TransportError
		if !errors.As(err, &broken) {
			t.Errorf("a redirect to %s = %v, want a transport failure", location, err)
		}
		if err != nil && strings.Contains(err.Error(), "token=t") {
			t.Errorf("the error carried the signed address: %v", err)
		}
		if sent := doer.urls(); len(sent) != 1 {
			t.Errorf("a redirect to %s was followed: %v", location, sent)
		}
		if got.Len() != 0 {
			t.Errorf("wrote %q", got.String())
		}
	}
	if !attachmentSchemeHolds("https", "HTTPS") || !attachmentSchemeHolds("http", "https") || !attachmentSchemeHolds("http", "http") {
		t.Error("a redirect that keeps or raises the transport was refused")
	}
}

func TestDownload_RefusesAPartialAnswerThatStartsElsewhere(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from int64
		span string
	}{
		{name: "from the start when a resume was asked for", from: 5, span: "bytes 0-9/10"},
		{name: "past the byte asked for", from: 5, span: "bytes 6-9/10"},
		{name: "a partial answer to a whole read", from: 0, span: "bytes 3-9/10"},
		{name: "no Content-Range at all", from: 5, span: ""},
		{name: "a Content-Range that is not bytes", from: 5, span: "items 5-9/10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			header := http.Header{}
			if tt.span != "" {
				header.Set("Content-Range", tt.span)
			}
			doer := &scriptedDoer{replies: []scripted{
				reply(http.StatusPartialContent, header, "0123456789"),
			}}
			c, _ := testClient(t, "example.atlassian.net", WithHTTPClient(doer))
			var got bytes.Buffer
			err := c.Download(t.Context(), testAttachmentID, &got, jira.DownloadOptions{From: tt.from})
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Errorf("got %v, want a transport failure", err)
			}
			if got.Len() != 0 {
				t.Errorf("wrote %q after the caller's own bytes", got.String())
			}
		})
	}

	doer := &scriptedDoer{replies: []scripted{
		reply(http.StatusPartialContent, http.Header{"Content-Range": {"bytes 5-9/10"}}, "56789"),
	}}
	c, _ := testClient(t, "example.atlassian.net", WithHTTPClient(doer))
	var got bytes.Buffer
	if err := c.Download(t.Context(), testAttachmentID, &got, jira.DownloadOptions{From: 5}); err != nil || got.String() != "56789" {
		t.Errorf("a partial answer from the byte asked for = %q, %v", got.String(), err)
	}
}
