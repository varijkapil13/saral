package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	testCommentID    = "10701"
	testCommentsPath = "/rest/api/3/issue/{key}/comment"
	// One comment is routed by its literal path: Go's mux rejects
	// /issue/{key}/comment/{id} as ambiguous against the createmeta route the
	// fixture server already carries, and neither pattern is more specific.
	testCommentPath = "/rest/api/3/issue/" + testIssueKey + "/comment/" + testCommentID
	testThreadPath  = "/rest/api/3/issue/" + testIssueKey + "/comment"
)

// paragraph is the smallest document a comment can be: one line of prose.
func paragraph(text string) adf.Doc {
	return adf.NewDoc(adf.NewNode("paragraph", adf.NewText(text)))
}

// restricted is a comment the site answers with a role restriction on, carrying
// the identifier that names the role in a way no translation moves.
const restricted = `{
  "id": "10701",
  "author": {"accountId": "5b10ac8d82e05b22cc7d4ef5", "displayName": "Another User", "active": true},
  "body": {"type": "doc", "version": 1, "content": [
    {"type": "paragraph", "content": [{"type": "text", "text": "Behind a flag."}]}
  ]},
  "created": "2026-02-11T09:38:55.000+0000",
  "updated": "2026-02-11T09:41:22.104+0000",
  "visibility": {"type": "role", "value": "Entwickler", "identifier": "10002"}
}`

// unrestricted is the same comment with nothing hiding it.
const unrestricted = `{
  "id": "10701",
  "author": {"accountId": "5b10ac8d82e05b22cc7d4ef5", "displayName": "Another User", "active": true},
  "body": {"type": "doc", "version": 1, "content": [
    {"type": "paragraph", "content": [{"type": "text", "text": "Behind a flag."}]}
  ]},
  "created": "2026-02-11T09:38:55.000+0000",
  "updated": "2026-02-11T09:41:22.104+0000"
}`

// commentPageHandler answers the collection with a window over the comments it
// is given, honouring startAt the way the endpoint does.
func commentPageHandler(comments []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		maxResults, err := strconv.Atoi(r.URL.Query().Get("maxResults"))
		if err != nil || maxResults <= 0 {
			maxResults = 50
		}
		start := min(max(startAt, 0), len(comments))
		end := min(start+maxResults, len(comments))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"startAt":` + strconv.Itoa(start) +
			`,"maxResults":` + strconv.Itoa(maxResults) +
			`,"total":` + strconv.Itoa(len(comments)) +
			`,"comments":[` + strings.Join(comments[start:end], ",") + `]}`))
	}
}

// numbered builds n comments whose bodies say which one they are.
func numbered(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		id := strconv.Itoa(20000 + i)
		out = append(out, `{"id":"`+id+`",`+
			`"author":{"accountId":"5b10ac8d82e05b22cc7d4ef5","displayName":"Another User","active":true},`+
			`"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"note `+strconv.Itoa(i)+`"}]}]},`+
			`"created":"2026-02-11T09:38:55.000+0000","updated":"2026-02-11T09:38:55.000+0000"}`)
	}
	return out
}

func commentClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

func TestComments_ReadsAThreadOldestFirstWithWhateverRestrictsIt(t *testing.T) {
	t.Parallel()

	c, s := commentClient(t)
	page, err := c.Comments(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("reading the thread: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("got %d comments, want the 2 the fixture holds", len(page.Items))
	}
	if got := page.Items[0].ID; got != "10700" {
		t.Errorf("the first comment is %q, want the oldest one", got)
	}
	if got := page.Items[0].Author.DisplayName; got != "Another User" {
		t.Errorf("the author is %q", got)
	}
	if got := adf.Markdown(page.Items[0].Body); !strings.Contains(got, "basketTotal()") {
		t.Errorf("the body did not survive the decode: %q", got)
	}
	vis := page.Items[1].Visibility
	if vis == nil {
		t.Fatal("the restricted comment came back with no visibility, which reads as public")
	}
	if vis.Type != "role" || vis.Value != "Developers" {
		t.Errorf("got visibility %+v, want the role the fixture names", *vis)
	}
	if page.Items[0].Visibility != nil {
		t.Errorf("the unrestricted comment grew a restriction: %+v", *page.Items[0].Visibility)
	}
	if total, ok := page.Count(); !ok || total != 2 {
		t.Errorf("got total %d (reported %v), want the 2 the envelope claims", total, ok)
	}
	if page.HasMore() {
		t.Error("a thread the site said was 2 long claims another page")
	}

	sent, err := url.ParseQuery(s.Requests()[0].Query)
	if err != nil {
		t.Fatalf("reading the query sent: %v", err)
	}
	if got := sent.Get("orderBy"); got != "created" {
		t.Errorf("orderBy is %q; the port promises oldest first and the default order is documented nowhere", got)
	}
	if got := sent.Get("maxResults"); got != strconv.Itoa(commentPageSize) {
		t.Errorf("maxResults is %q, want an explicit page size", got)
	}
}

func TestComments_WalksEveryPageAndStopsAtTheTotal(t *testing.T) {
	t.Parallel()

	all := numbered(120)
	c, s := commentClient(t, jiratest.WithHandler(http.MethodGet, testCommentsPath, commentPageHandler(all)))

	page, err := c.Comments(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("reading the thread: %v", err)
	}
	got, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatalf("walking the thread: %v", err)
	}
	if len(got) != len(all) {
		t.Fatalf("got %d comments over the whole walk, want %d", len(got), len(all))
	}
	if got[0].ID != "20000" || got[len(got)-1].ID != "20119" {
		t.Errorf("the walk lost its order: first %q, last %q", got[0].ID, got[len(got)-1].ID)
	}
	if want := 3; len(s.Requests()) != want {
		t.Errorf("the walk took %d requests, want %d at %d a page", len(s.Requests()), want, commentPageSize)
	}
}

func TestComments_StopsOnAnEmptyPageWhateverTheTotalClaims(t *testing.T) {
	t.Parallel()

	body := `{"startAt":0,"maxResults":50,"total":900,"comments":[]}`
	c, _ := commentClient(t, jiratest.WithHandler(http.MethodGet, testCommentsPath, jsonHandler(http.StatusOK, body)))

	page, err := c.Comments(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("reading the thread: %v", err)
	}
	if page.HasMore() {
		t.Error("an empty page left the walk open, which against a truncating endpoint never terminates")
	}
}

func TestComments_ReadsAThreadWithNoTotalWithoutInventingOne(t *testing.T) {
	t.Parallel()

	body := `{"startAt":0,"maxResults":50,"comments":[` + unrestricted + `]}`
	c, _ := commentClient(t, jiratest.WithHandler(http.MethodGet, testCommentsPath, jsonHandler(http.StatusOK, body)))

	page, err := c.Comments(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("reading the thread: %v", err)
	}
	if _, ok := page.Count(); ok {
		t.Error("a page whose envelope carried no total reported one")
	}
	if page.HasMore() {
		t.Error("a page with no total and nothing after it claims another page")
	}
}

func TestAddComment_SendsTheDocumentAndNothingThatWouldRestrictIt(t *testing.T) {
	t.Parallel()

	c, s := commentClient(t, jiratest.WithHandler(http.MethodPost, testCommentsPath,
		jsonHandler(http.StatusCreated, unrestricted)))

	got, err := c.AddComment(t.Context(), testIssueKey, paragraph("Behind a flag."))
	if err != nil {
		t.Fatalf("adding a comment: %v", err)
	}
	if got.ID != testCommentID {
		t.Errorf("the comment came back as %q", got.ID)
	}
	if got.Visibility != nil {
		t.Errorf("a comment nobody restricted came back restricted: %+v", *got.Visibility)
	}

	sent := sentBody(t, sentTo(t, s, http.MethodPost, testThreadPath))
	if _, ok := sent["visibility"]; ok {
		t.Error("a new comment sent a visibility key, which restricts it to whatever that names")
	}
	body, ok := sent["body"].(map[string]any)
	if !ok || body["type"] != "doc" {
		t.Fatalf("the body sent is not an ADF document: %v", sent["body"])
	}
}

func TestAddComment_RefusesAnEmptyDocumentWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	c, s := commentClient(t)

	for _, tc := range []struct {
		name string
		body adf.Doc
	}{
		{name: "a document nobody built", body: adf.Doc{}},
		{name: "a document with no blocks in it", body: adf.NewDoc()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.AddComment(t.Context(), testIssueKey, tc.body)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want a *jira.ValidationError", err)
			}
			if _, ok := invalid.For("body"); !ok {
				t.Errorf("the failure does not name the body: %v", invalid)
			}
		})
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests for a comment with nothing in it", served)
	}
}

func TestEditComment_KeepsTheRestrictionTheCommentAlreadyHad(t *testing.T) {
	t.Parallel()

	c, s := commentClient(t,
		jiratest.WithHandler(http.MethodGet, testCommentPath, jsonHandler(http.StatusOK, restricted)),
		jiratest.WithHandler(http.MethodPut, testCommentPath, jsonHandler(http.StatusOK, restricted)),
	)

	got, err := c.EditComment(t.Context(), testIssueKey, testCommentID, paragraph("Behind a flag, still."))
	if err != nil {
		t.Fatalf("editing: %v", err)
	}
	if got.Visibility == nil || got.Visibility.Type != "role" {
		t.Fatalf("the edited comment came back as %+v", got.Visibility)
	}

	sent := sentBody(t, sentTo(t, s, http.MethodPut, testCommentPath))
	vis, ok := sent["visibility"].(map[string]any)
	if !ok {
		t.Fatalf("the edit sent no visibility, which publishes a comment one role could see: %v", sent)
	}
	if vis["type"] != "role" || vis["value"] != "Entwickler" {
		t.Errorf("the edit rewrote the restriction: %v", vis)
	}
	if vis["identifier"] != "10002" {
		t.Errorf("the edit dropped the identifier, which is the half of a role name no translation moves: %v", vis)
	}
}

func TestEditComment_LeavesAnUnrestrictedCommentUnrestricted(t *testing.T) {
	t.Parallel()

	c, s := commentClient(t,
		jiratest.WithHandler(http.MethodGet, testCommentPath, jsonHandler(http.StatusOK, unrestricted)),
		jiratest.WithHandler(http.MethodPut, testCommentPath, jsonHandler(http.StatusOK, unrestricted)),
	)

	if _, err := c.EditComment(t.Context(), testIssueKey, testCommentID, paragraph("Reworded.")); err != nil {
		t.Fatalf("editing: %v", err)
	}
	sent := sentBody(t, sentTo(t, s, http.MethodPut, testCommentPath))
	if _, ok := sent["visibility"]; ok {
		t.Errorf("the edit invented a restriction on a comment that had none: %v", sent)
	}
}

// An edit is a read and then a write. A read that fails must not be followed by
// a PUT built from what the client guessed instead.
func TestEditComment_DoesNotWriteWhenItCannotReadWhatItWouldReplace(t *testing.T) {
	t.Parallel()

	c, s := commentClient(t,
		jiratest.WithStatus(http.MethodGet, testCommentPath, http.StatusForbidden, "plans_403.json"),
		jiratest.WithHandler(http.MethodPut, testCommentPath, jsonHandler(http.StatusOK, unrestricted)),
	)

	_, err := c.EditComment(t.Context(), testIssueKey, testCommentID, paragraph("Reworded."))
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
	for _, r := range s.Requests() {
		if r.Method == http.MethodPut {
			t.Fatal("the edit wrote after failing to read what it was replacing")
		}
	}
}

func TestEditComment_RefusesAnEmptyDocumentBeforeReadingAnything(t *testing.T) {
	t.Parallel()

	c, s := commentClient(t)

	_, err := c.EditComment(t.Context(), testIssueKey, testCommentID, adf.NewDoc())
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v, want a *jira.ValidationError", err)
	}
	if served := len(s.Requests()); served != 0 {
		t.Errorf("the site served %d requests for an edit that emptied the comment", served)
	}
}

func TestDeleteComment_AcceptsTheEmptyAnswerTheEndpointGives(t *testing.T) {
	t.Parallel()

	c, s := commentClient(t, jiratest.WithStatus(http.MethodDelete, testCommentPath, http.StatusNoContent, ""))

	if err := c.DeleteComment(t.Context(), testIssueKey, testCommentID); err != nil {
		t.Fatalf("deleting: %v", err)
	}
	sent := sentTo(t, s, http.MethodDelete, testCommentPath)
	if want := "/rest/api/3/issue/EX-1/comment/10701"; sent.Path != want {
		t.Errorf("deleted %q, want %q", sent.Path, want)
	}
}

func TestDeleteComment_ReportsACommentThatIsNotThereAsNotThere(t *testing.T) {
	t.Parallel()

	c, _ := commentClient(t, jiratest.WithHandler(http.MethodDelete, testCommentPath,
		jsonHandler(http.StatusNotFound, `{"errorMessages":["Comment does not exist."],"errors":{}}`)))

	err := c.DeleteComment(t.Context(), testIssueKey, testCommentID)
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "comment" || missing.ID != testCommentID {
		t.Errorf("the failure names %s %s rather than the comment", missing.Kind, missing.ID)
	}
}

// call is one adapter method under test, named so a subtest reads as prose.
type commentCall struct {
	name   string
	method string
	path   string
	run    func(context.Context, *Client) error
}

func commentCalls() []commentCall {
	return []commentCall{
		{
			name: "reading a thread", method: http.MethodGet, path: testCommentsPath,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Comments(ctx, testIssueKey)
				return err
			},
		},
		{
			name: "adding a comment", method: http.MethodPost, path: testCommentsPath,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.AddComment(ctx, testIssueKey, paragraph("hello"))
				return err
			},
		},
		{
			name: "editing a comment", method: http.MethodGet, path: testCommentPath,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.EditComment(ctx, testIssueKey, testCommentID, paragraph("hello"))
				return err
			},
		},
		{
			name: "deleting a comment", method: http.MethodDelete, path: testCommentPath,
			run: func(ctx context.Context, c *Client) error {
				return c.DeleteComment(ctx, testIssueKey, testCommentID)
			},
		},
	}
}

func TestComments_RefusalBecomesTheSentenceTheUserReads(t *testing.T) {
	t.Parallel()

	for _, tc := range commentCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := `{"errorMessages":["You do not have permission to edit this comment."],"errors":{}}`
			c, _ := commentClient(t, jiratest.WithHandler(tc.method, tc.path, jsonHandler(http.StatusForbidden, body)))

			err := tc.run(t.Context(), c)
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			if !strings.Contains(refused.Error(), "permission to edit this comment") {
				t.Errorf("the reason lost the site's own wording: %q", refused.Error())
			}
		})
	}
}

func TestComments_RateLimitCarriesTheWaitTheSiteAskedFor(t *testing.T) {
	t.Parallel()

	for _, tc := range commentCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := commentClient(t, jiratest.WithRateLimit(tc.method, tc.path, 30*time.Second))

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

func TestComments_TransportFailureIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range commentCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := commentClient(t, jiratest.WithHandler(tc.method, tc.path,
				jsonHandler(http.StatusBadGateway, `{"errorMessages":["upstream is unwell"]}`)))

			err := tc.run(t.Context(), c)
			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if down.Status != http.StatusBadGateway {
				t.Errorf("the failure reports HTTP %d", down.Status)
			}
		})
	}
}

func TestComments_ABodyThisClientCannotReadIsATransportFailure(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "JSON that stops half way", body: `{"startAt":0,"comments":[`},
		{name: "a comment body that is not a document", body: `{"total":1,"comments":[{"id":"1","body":"just a string"}]}`},
		{name: "an envelope that is an array", body: `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := commentClient(t, jiratest.WithHandler(http.MethodGet, testCommentsPath,
				jsonHandler(http.StatusOK, tc.body)))

			_, err := c.Comments(t.Context(), testIssueKey)
			var down *jira.TransportError
			if !errors.As(err, &down) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
		})
	}
}

func TestComments_ReturnTheCallersOwnErrorWhenItCancels(t *testing.T) {
	t.Parallel()

	for _, tc := range commentCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, s := commentClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			if err := tc.run(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
			if served := len(s.Requests()); served != 0 {
				t.Errorf("the site served %d requests after the caller had already gone", served)
			}
		})
	}
}

// A write must never be replayed by the retry loop or shared with another
// caller's identical write: two identical comments are two comments.
func TestAddComment_IsNeitherRetriedNorSharedWithAnIdenticalRequest(t *testing.T) {
	t.Parallel()

	r := request{method: http.MethodPost, path: commentsPath(testIssueKey), body: apiCommentBody{}}
	if r.canRepeat() {
		t.Error("adding a comment is marked repeatable, so a 5xx would post it twice")
	}
}

func TestComments_EscapeAKeyAndAnIDThatWouldOtherwiseChangeThePath(t *testing.T) {
	t.Parallel()

	got := commentPath("EX 1/../..", "10 1")
	if strings.Contains(got, " ") || strings.Contains(got, "/../") {
		t.Errorf("the path is %q, which is not the comment that was asked for", got)
	}
}

// decodeVisibility is the half of the read that decides whether a comment reads
// as public, so its edge cases are worth naming.
func TestDecodeVisibility_ReadsAbsenceAsPublicAndNothingElse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		raw   string
		want  *jira.Visibility
		exact bool
	}{
		{name: "no key at all", raw: ``},
		{name: "an explicit null", raw: `null`},
		{name: "an empty object", raw: `{}`},
		{name: "a role", raw: `{"type":"role","value":"Developers","identifier":"10002"}`,
			want: &jira.Visibility{Type: "role", Value: "Developers"}, exact: true},
		{name: "a group named only by its identifier", raw: `{"type":"group","identifier":"jira-developers"}`,
			want: &jira.Visibility{Type: "group", Value: "jira-developers"}, exact: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := decodeVisibility(json.RawMessage(tc.raw))
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("got %+v, want nothing that would hide the comment", *got)
			case tc.want == nil:
				return
			case got == nil:
				t.Fatalf("got nothing, want %+v", *tc.want)
			case tc.exact && *got != *tc.want:
				t.Errorf("got %+v, want %+v", *got, *tc.want)
			}
		})
	}
}
