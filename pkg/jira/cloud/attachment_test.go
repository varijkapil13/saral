package cloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

const (
	attachmentMetaRoute    = "/rest/api/3/attachment/meta"
	attachmentContentRoute = "/rest/api/3/attachment/content/{id}"
	attachmentDeleteRoute  = "/rest/api/3/attachment/{id}"
	attachmentUploadRoute  = "/rest/api/3/issue/{key}/attachments"
	// attachmentMediaPath is the fixture server's stand-in for the media host a
	// real site redirects a download to, serving the same bytes over ServeContent.
	attachmentMediaPath = "/media/attachment/content/"

	// testAttachmentID is the id the shared issue fixture's attachment carries.
	testAttachmentID = "10501"

	// attachmentMetaBody stands in for a fixture the shared server does not have:
	// there is no route for GET /rest/api/3/attachment/meta, and the request
	// falls through to /attachment/{id}, whose fixture is one attachment and
	// therefore reads as a site with attachments switched off.
	attachmentMetaBody     = `{"enabled":true,"uploadLimit":10485760}`
	attachmentMetaOffBody  = `{"enabled":false,"uploadLimit":10485760}`
	attachmentMetaTinyBody = `{"enabled":true,"uploadLimit":8}`
)

// attachmentServed is what the shared content and media routes stream.
var attachmentServed = []byte(jiratest.AttachmentContent)

// attachmentLong is long enough that one copy takes several reads, which is what
// makes progress reporting visible. The shared body is one read.
var attachmentLong = bytes.Repeat([]byte("saral attachment "), 5000)

// attachmentRoutes adds the one route the shared server is missing to whatever a
// test overrides. Every other route an attachment touches is the shared one.
func attachmentRoutes(opts ...jiratest.ServerOption) []jiratest.ServerOption {
	return append([]jiratest.ServerOption{
		jiratest.WithHandler(http.MethodGet, attachmentMetaRoute, jsonHandler(http.StatusOK, attachmentMetaBody)),
	}, opts...)
}

func attachmentClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(attachmentRoutes(opts...)...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

// attachmentServes answers a range request the way the shared server does, over
// http.ServeContent, so a test cannot drift from the rule a real host applies.
func attachmentServes(data []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	}
}

// attachmentRedirects sends the download on to the shared server's own media
// route, addressed from the request so that one server can play both hosts.
func attachmentRedirects(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Location", "http://"+r.Host+attachmentMediaPath+r.PathValue("id")+"?token=fixture-signed-token")
	w.WriteHeader(http.StatusSeeOther)
}

// attachmentCall is one of the four methods, in a shape the failure tables drive.
type attachmentCall struct {
	name   string
	method string
	route  string
	run    func(ctx context.Context, c *Client) error
}

func attachmentCalls() []attachmentCall {
	return []attachmentCall{
		{
			name: "Attachments", method: http.MethodGet, route: issueRoute,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Attachments(ctx, testIssueKey)
				return err
			},
		},
		{
			name: "Upload", method: http.MethodPost, route: attachmentUploadRoute,
			run: func(ctx context.Context, c *Client) error {
				_, err := c.Upload(ctx, testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")})
				return err
			},
		},
		{
			name: "Download", method: http.MethodGet, route: attachmentContentRoute,
			run: func(ctx context.Context, c *Client) error {
				return c.Download(ctx, testAttachmentID, io.Discard, jira.DownloadOptions{})
			},
		},
		{
			name: "DeleteAttachment", method: http.MethodDelete, route: attachmentDeleteRoute,
			run: func(ctx context.Context, c *Client) error {
				return c.DeleteAttachment(ctx, testAttachmentID)
			},
		},
	}
}

func testFile(name, body string) jira.FileRef {
	return jira.FileRef{
		Name: name,
		Size: int64(len(body)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil },
	}
}

// unsizedFile is a file whose length is not known before it is read, which is
// what a pipe or a stream is: the size guard has to hold without FileRef.Size.
func unsizedFile(name, body string, reads *atomic.Int64) jira.FileRef {
	return jira.FileRef{
		Name: name,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(&countingReader{from: strings.NewReader(body), read: reads}), nil
		},
	}
}

type countingReader struct {
	from *strings.Reader
	read *atomic.Int64
}

func (r *countingReader) Read(b []byte) (int, error) {
	n, err := r.from.Read(b)
	if r.read != nil {
		r.read.Add(int64(n))
	}
	return n, err
}

func TestAttachments_ReadsWhatTheIssueCarriesAndAsksForThatFieldAlone(t *testing.T) {
	t.Parallel()

	c, s := attachmentClient(t)
	got, err := c.Attachments(t.Context(), testIssueKey)
	if err != nil {
		t.Fatalf("Attachments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want the one attachment the issue carries, got %+v", got)
	}
	one := got[0]
	if one.ID != testAttachmentID || one.Filename != "har-capture.har" || one.MimeType != "application/json" {
		t.Errorf("read the attachment as %+v", one)
	}
	if one.Size != 184320 {
		t.Errorf("size = %d, want 184320", one.Size)
	}
	if one.Created.IsZero() {
		t.Error("the created time was not read")
	}
	if one.Author.AccountID == "" {
		t.Errorf("the author was not read: %+v", one.Author)
	}
	if one.ContentURL == "" {
		t.Error("the content URL was not read, so there is nothing to download from")
	}
	if one.ThumbnailURL != "" {
		t.Errorf("thumbnail = %q, want the empty string the fixture sends: a thumbnail is never synthesised", one.ThumbnailURL)
	}

	sent := sentTo(t, s, http.MethodGet, "/rest/api/3/issue/"+testIssueKey)
	query, err := url.ParseQuery(sent.Query)
	if err != nil {
		t.Fatalf("reading the query: %v", err)
	}
	if fields := query["fields"]; len(fields) != 1 || fields[0] != "attachment" {
		t.Errorf("asked for fields %v, want attachment alone", fields)
	}
	if _, wide := query["expand"]; wide {
		t.Errorf("the read expanded %q, which a list of attachments has no use for", query["expand"])
	}
}

func TestAttachments_ReadsAnIssueWithNoAttachmentFieldAsNoneRatherThanAsAFailure(t *testing.T) {
	t.Parallel()

	// No shared fixture is an issue without attachments: issue_rich_adf.json
	// carries one, and these are the same absence written three ways.
	bodies := map[string]string{
		"a field the issue does not carry": `{"key":"EX-1","fields":{}}`,
		"a field the site sent as null":    `{"key":"EX-1","fields":{"attachment":null}}`,
		"a field the site sent empty":      `{"key":"EX-1","fields":{"attachment":[]}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, issueRoute, jsonHandler(http.StatusOK, body)))
			got, err := c.Attachments(t.Context(), testIssueKey)
			if err != nil {
				t.Fatalf("Attachments: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("got %+v, want no attachments", got)
			}
		})
	}
}

func TestAttachment_NormalisesTheIdTheStandaloneReadWritesAsANumber(t *testing.T) {
	t.Parallel()

	// The shared fixtures are the two spellings: attachment_meta.json is the
	// standalone read, whose id is a JSON number, and attachment_upload.json is
	// the upload, whose id is a string.
	numbered, err := jiratest.Fixture("attachment_meta.json")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	var read apiAttachment
	if err := json.Unmarshal(numbered, &read); err != nil {
		t.Fatalf("decoding the standalone read: %v", err)
	}
	if got := read.domain().ID; got != "10502" {
		t.Errorf("a numeric id read as %q, want %q as a string", got, "10502")
	}

	stringed, err := jiratest.Fixture("attachment_upload.json")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	var uploaded []apiAttachment
	if err := json.Unmarshal(stringed, &uploaded); err != nil {
		t.Fatalf("decoding the upload: %v", err)
	}
	if len(uploaded) == 0 || uploaded[0].domain().ID != "10503" {
		t.Errorf("the upload's ids read as %+v", uploaded)
	}
}

func TestAttachments_ComparesAnIdFromAReadAgainstOneFromAnUpload(t *testing.T) {
	t.Parallel()

	// The same attachment written both ways: a number on the issue read, a string
	// from the upload. No shared fixture puts a numeric attachment id on an issue
	// read, which is the only shape Attachments can be asked for.
	const numbered = `{"key":"EX-1","fields":{"attachment":[{"id":10503,"filename":"rollout-notes.csv","size":1904}]}}`

	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, issueRoute, jsonHandler(http.StatusOK, numbered)))
	ctx := t.Context()

	listed, err := c.Attachments(ctx, testIssueKey)
	if err != nil || len(listed) != 1 {
		t.Fatalf("Attachments: %v (%d)", err, len(listed))
	}
	uploaded, err := c.Upload(ctx, testIssueKey, []jira.FileRef{testFile("rollout-notes.csv", "hello saral")})
	if err != nil || len(uploaded) == 0 {
		t.Fatalf("Upload: %v (%d)", err, len(uploaded))
	}
	if listed[0].ID != "10503" {
		t.Errorf("a numeric id read as %q, want %q", listed[0].ID, "10503")
	}
	if listed[0].ID != uploaded[0].ID {
		t.Errorf("the same attachment is %q from a read and %q from the upload; the two spellings must normalise",
			listed[0].ID, uploaded[0].ID)
	}
}

func TestUpload_SendsEveryFileInAPartNamedFileAndTurnsTheXSRFGuardOff(t *testing.T) {
	t.Parallel()

	opens := make([]atomic.Int64, 2)
	files := []jira.FileRef{
		{
			Name: "rollout-notes.csv", Size: 11,
			Open: func() (io.ReadCloser, error) {
				opens[0].Add(1)
				return io.NopCloser(strings.NewReader("hello saral")), nil
			},
		},
		{
			Name: "trace.log", Size: 5,
			Open: func() (io.ReadCloser, error) {
				opens[1].Add(1)
				return io.NopCloser(strings.NewReader("lines")), nil
			},
		},
	}

	// The shared upload route refuses a missing XSRF header with a plain-text 404
	// and a wrongly named part with an RFC 7807 400 before it answers, so getting
	// the fixture back is itself the assertion that the request shape is right.
	c, s := attachmentClient(t)
	got, err := c.Upload(t.Context(), testIssueKey, files)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("the bare array answer read as %+v, want the fixture's two rows", got)
	}
	if got[0].ID != "10503" || got[0].Filename != "rollout-notes.csv" || got[0].MimeType != "text/csv" {
		t.Errorf("the first stored attachment read as %+v", got[0])
	}
	if got[1].ID != "10504" || got[1].Size != 41207 {
		t.Errorf("the second stored attachment read as %+v", got[1])
	}
	if got[0].ThumbnailURL != "" {
		t.Errorf("a text file was given the thumbnail %q, which the fixture does not send", got[0].ThumbnailURL)
	}
	if got[1].ThumbnailURL == "" {
		t.Error("the image's thumbnail was dropped")
	}
	if got[0].Author.AccountID == "" || got[0].Created.IsZero() {
		t.Errorf("the upload's author and time were not read: %+v", got[0])
	}
	for i := range opens {
		if n := opens[i].Load(); n != 1 {
			t.Errorf("file %d was opened %d times, want exactly once", i, n)
		}
	}

	sent := sentTo(t, s, http.MethodPost, "/rest/api/3/issue/"+testIssueKey+"/attachments")
	if guard := sent.Header.Get("X-Atlassian-Token"); guard != "no-check" {
		t.Errorf("X-Atlassian-Token = %q, want no-check: without it the site answers 404", guard)
	}
	parts := attachmentPartsOf(t, sent)
	if len(parts) != 2 {
		t.Fatalf("sent %d parts, want one per file: %+v", len(parts), parts)
	}
	for _, part := range parts {
		if part.name != "file" {
			t.Errorf("part named %q, want file repeated: any other name is refused", part.name)
		}
	}
	if parts[0].filename != "rollout-notes.csv" || parts[0].body != "hello saral" {
		t.Errorf("first part = %+v", parts[0])
	}
	if parts[1].filename != "trace.log" || parts[1].body != "lines" {
		t.Errorf("second part = %+v", parts[1])
	}
}

func TestUpload_TakesAFileAtTheSiteCapAndRefusesOneOverItWithoutSendingIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		file    jira.FileRef
		refused bool
	}{
		{name: "a file of exactly the cap, which the site accepts", file: testFile("notes.txt", "12345678")},
		{name: "a file one byte over the cap", file: testFile("notes.txt", "123456789"), refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentMetaRoute,
				jsonHandler(http.StatusOK, attachmentMetaTinyBody)))
			_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{tt.file})
			posted := false
			for _, served := range s.Requests() {
				if served.Method == http.MethodPost {
					posted = true
				}
			}
			if !tt.refused {
				if err != nil {
					t.Fatalf("a file of exactly the cap was refused: %v", err)
				}
				if !posted {
					t.Error("a file the site accepts was never sent")
				}
				return
			}
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if !strings.Contains(invalid.Error(), "8") {
				t.Errorf("the refusal must quote the site's own number, got %q", invalid.Error())
			}
			if posted {
				t.Error("the oversized file was uploaded anyway")
			}
		})
	}
}

func TestUpload_ReadsOneByteMoreThanItMayKeepOfAFileOfUnknownSize(t *testing.T) {
	t.Parallel()

	var reads atomic.Int64
	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentMetaRoute,
		jsonHandler(http.StatusOK, attachmentMetaTinyBody)))

	_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{
		unsizedFile("notes.txt", strings.Repeat("x", 4096), &reads),
	})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if got := reads.Load(); got != 9 {
		t.Errorf("read %d bytes of a file it may keep 8 of, want the 9 that prove it is too big", got)
	}
}

func TestUpload_RefusesAFileTooLargeToSendWithoutOpeningIt(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"a site whose cap the file is over":       attachmentMetaTinyBody,
		"a site that did not say what it accepts": "",
	}
	for name, meta := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []jiratest.ServerOption{jiratest.WithStatus(http.MethodGet, attachmentMetaRoute,
				http.StatusInternalServerError, "")}
			if meta != "" {
				opts = []jiratest.ServerOption{jiratest.WithHandler(http.MethodGet, attachmentMetaRoute,
					jsonHandler(http.StatusOK, meta))}
			}
			c, s := attachmentClient(t, opts...)

			var opened atomic.Int64
			_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{{
				Name: "capture.mov",
				Size: attachmentBufferCeiling + 1,
				Open: func() (io.ReadCloser, error) {
					opened.Add(1)
					return io.NopCloser(strings.NewReader("never read")), nil
				},
			}})
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if n := opened.Load(); n != 0 {
				t.Errorf("the file was opened %d times; os.Stat already said it cannot be sent", n)
			}
			for _, served := range s.Requests() {
				if served.Method == http.MethodPost {
					t.Errorf("the file was uploaded anyway: %v", served)
				}
			}
		})
	}
}

func TestAttachmentBody_HoldsNoMoreOfAnUploadThanTheClientsOwnCeiling(t *testing.T) {
	t.Parallel()

	const ceiling = 10
	six := func(name string) jira.FileRef { return unsizedFile(name, "123456", nil) }

	t.Run("one file inside the ceiling and no site cap at all", func(t *testing.T) {
		t.Parallel()

		body, contentType, err := attachmentBody([]jira.FileRef{six("notes.txt")}, attachmentLimitUnknown, ceiling)
		if err != nil {
			t.Fatalf("attachmentBody: %v", err)
		}
		if !bytes.Contains(body, []byte("123456")) {
			t.Error("the file's bytes are not in the body")
		}
		if !strings.HasPrefix(contentType, "multipart/form-data") {
			t.Errorf("Content-Type = %q", contentType)
		}
	})

	t.Run("two files that only overrun together", func(t *testing.T) {
		t.Parallel()

		_, _, err := attachmentBody([]jira.FileRef{six("one.txt"), six("two.txt")}, attachmentLimitUnknown, ceiling)
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("got %T (%v), want a *jira.ValidationError: two files of 6 bytes do not fit in 10", err, err)
		}
		if !strings.Contains(invalid.Error(), "two.txt") {
			t.Errorf("the refusal must name the file that did not fit, got %q", invalid.Error())
		}
	})

	t.Run("a declared size over the ceiling with no site cap", func(t *testing.T) {
		t.Parallel()

		_, _, err := attachmentBody([]jira.FileRef{{
			Name: "capture.mov",
			Size: ceiling + 1,
			Open: func() (io.ReadCloser, error) {
				t.Error("a file too big to hold was opened")
				return io.NopCloser(strings.NewReader("")), nil
			},
		}}, attachmentLimitUnknown, ceiling)
		var invalid *jira.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
		}
	})
}

func TestUpload_RefusesWhenTheSiteHasAttachmentsSwitchedOff(t *testing.T) {
	t.Parallel()

	c, s := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentMetaRoute,
		jsonHandler(http.StatusOK, attachmentMetaOffBody)))

	_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")})
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
	}
	if refused.Capability != jira.CapAttachments {
		t.Errorf("the refusal names %q, want %q", refused.Capability, jira.CapAttachments)
	}
	for _, served := range s.Requests() {
		if served.Method == http.MethodPost {
			t.Fatalf("the upload went ahead on a site with attachments off: %v", served)
		}
	}
}

func TestUpload_GoesAheadWhenTheSiteWillNotSayWhatItAcceptsAndStopsOnA429(t *testing.T) {
	t.Parallel()

	t.Run("a probe that did not answer leaves the size to Jira", func(t *testing.T) {
		t.Parallel()

		c, s := attachmentClient(t, jiratest.WithStatus(http.MethodGet, attachmentMetaRoute,
			http.StatusInternalServerError, ""))
		if _, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")}); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		sentTo(t, s, http.MethodPost, "/rest/api/3/issue/"+testIssueKey+"/attachments")
	})

	t.Run("a throttled probe stops the upload", func(t *testing.T) {
		t.Parallel()

		c, s := attachmentClient(t, jiratest.WithRateLimit(http.MethodGet, attachmentMetaRoute, 30*time.Second))
		_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")})
		var limited *jira.RateLimitError
		if !errors.As(err, &limited) {
			t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
		}
		if limited.RetryAfter != 30*time.Second {
			t.Errorf("RetryAfter = %s, want 30s", limited.RetryAfter)
		}
		for _, served := range s.Requests() {
			if served.Method == http.MethodPost {
				t.Fatalf("the upload was sent into a site that had just refused a read: %v", served)
			}
		}
	})
}

func TestUpload_ReadsTheSitesAttachmentSettingsOnceASession(t *testing.T) {
	t.Parallel()

	c, s := attachmentClient(t)
	ctx := t.Context()
	for i := range 3 {
		if _, err := c.Upload(ctx, testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")}); err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}
	probes, uploads := 0, 0
	for _, served := range s.Requests() {
		switch {
		case served.Path == attachmentMetaRoute:
			probes++
		case served.Method == http.MethodPost:
			uploads++
		}
	}
	if uploads != 3 {
		t.Errorf("%d uploads were sent, want 3", uploads)
	}
	if probes != 1 {
		t.Errorf("the site's attachment settings were read %d times, want once: they do not move within a session", probes)
	}
}

func TestUpload_ReportsThePlainTextXSRF404AsTheGuardAndNotAsAMissingIssue(t *testing.T) {
	t.Parallel()

	t.Run("a 404 whose body is not JSON at all", func(t *testing.T) {
		t.Parallel()

		// The shared upload route answers exactly this when the header is absent,
		// which this client always sends, so the guard's answer is replayed here.
		c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodPost, attachmentUploadRoute,
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("XSRF check failed"))
			}))

		_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")})
		var missing *jira.NotFoundError
		if errors.As(err, &missing) {
			t.Fatalf("the XSRF guard was reported as %v, which tells the user to look for an issue that is there", missing)
		}
		var broken *jira.TransportError
		if !errors.As(err, &broken) {
			t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
		}
		if broken.Status != http.StatusNotFound {
			t.Errorf("Status = %d, want the 404 the site really sent: the status is the classification", broken.Status)
		}
	})

	t.Run("a 404 that names the issue", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodPost, attachmentUploadRoute,
			jsonHandler(http.StatusNotFound, `{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`)))

		_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")})
		var missing *jira.NotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
		}
		if missing.Kind != "issue" || missing.ID != testIssueKey {
			t.Errorf("the 404 named %s %s, want issue %s", missing.Kind, missing.ID, testIssueKey)
		}
	})
}

func TestUpload_ReportsARFC7807RefusalOfThePartNameAsTheValuesBeingWrong(t *testing.T) {
	t.Parallel()

	// The shape the shared upload route refuses a wrongly named part with, which
	// this client cannot provoke: it names every part "file".
	const problem = `{"type":"about:blank","title":"Bad Request","status":400,
		"detail":"The multipart request carries no part named \"file\"."}`

	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodPost, attachmentUploadRoute,
		jsonHandler(http.StatusBadRequest, problem)))

	_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if !strings.Contains(invalid.Error(), `part named "file"`) {
		t.Errorf("the site's own sentence was dropped: %q", invalid.Error())
	}
}

func TestUpload_RefusesAnEmptyListAndAFileItCannotOpenWithoutARequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files []jira.FileRef
	}{
		{name: "nothing to send", files: nil},
		{name: "a file with no name", files: []jira.FileRef{{Name: "  ", Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("x")), nil
		}}}},
		{name: "a file with no way to read it", files: []jira.FileRef{{Name: "notes.txt", Size: 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := attachmentClient(t)
			_, err := c.Upload(t.Context(), testIssueKey, tt.files)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if served := s.Requests(); len(served) != 0 {
				t.Errorf("an upload with nothing to send made %d requests: %v", len(served), served)
			}
		})
	}
}

func TestUpload_IsNeverReplayedAfterAServerFailure(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(attachmentRoutes(jiratest.WithStatus(http.MethodPost, attachmentUploadRoute,
		http.StatusBadGateway, ""))...)
	defer closeServer(t, s)
	c, _ := testClient(t, s.URL())

	if _, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")}); err == nil {
		t.Fatal("Upload: want the 502 reported")
	}
	uploads := 0
	for _, served := range s.Requests() {
		if served.Method == http.MethodPost {
			uploads++
		}
	}
	if uploads != 1 {
		t.Errorf("the upload was sent %d times; a replayed one attaches the file twice", uploads)
	}
}

func TestDownload_StreamsTheWholeFileAndReportsProgressAsItGoes(t *testing.T) {
	t.Parallel()

	// The shared body is one read, and progress as the copy goes needs several.
	c, s := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		attachmentServes(attachmentLong)))
	var into bytes.Buffer
	var progress []int64
	err := c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{
		Progress: func(written int64) { progress = append(progress, written) },
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(into.Bytes(), attachmentLong) {
		t.Fatalf("downloaded %d bytes, want the %d the site served", into.Len(), len(attachmentLong))
	}
	if len(progress) < 2 {
		t.Fatalf("progress was reported %d times for %d bytes, want it as the copy goes", len(progress), len(attachmentLong))
	}
	for i := 1; i < len(progress); i++ {
		if progress[i] <= progress[i-1] {
			t.Fatalf("progress must be cumulative and increasing, got %v", progress)
		}
	}
	if last := progress[len(progress)-1]; last != int64(len(attachmentLong)) {
		t.Errorf("the last progress call reported %d, want the whole %d", last, len(attachmentLong))
	}

	sent := sentTo(t, s, http.MethodGet, "/rest/api/3/attachment/content/"+testAttachmentID)
	query, err := url.ParseQuery(sent.Query)
	if err != nil {
		t.Fatalf("reading the query: %v", err)
	}
	if got := query.Get("redirect"); got != "false" {
		t.Errorf("redirect = %q, want false: it is what keeps the bytes on the Jira host", got)
	}
	if ranged := sent.Header.Get("Range"); ranged != "" {
		t.Errorf("a download from the first byte sent Range: %q", ranged)
	}
}

func TestDownload_ResumesFromAnOffsetWithARangeHeader(t *testing.T) {
	t.Parallel()

	const from = 17
	c, s := attachmentClient(t)
	var into bytes.Buffer
	if err := c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{From: from}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(into.Bytes(), attachmentServed[from:]) {
		t.Fatalf("resumed download wrote %d bytes, want the %d after the offset", into.Len(), len(attachmentServed)-from)
	}
	sent := sentTo(t, s, http.MethodGet, "/rest/api/3/attachment/content/"+testAttachmentID)
	if got, want := sent.Header.Get("Range"), "bytes=17-"; got != want {
		t.Errorf("Range = %q, want %q", got, want)
	}
}

func TestDownload_DropsTheBytesTheCallerHasWhenTheSiteIgnoresTheRange(t *testing.T) {
	t.Parallel()

	const from = 17
	// A site that answers the whole file to a ranged request is within its
	// rights, and writing it as it stands doubles the bytes the caller has.
	whole := jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(attachmentServed)
		})

	t.Run("an offset inside the file", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t, whole)
		var into bytes.Buffer
		if err := c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{From: from}); err != nil {
			t.Fatalf("Download: %v", err)
		}
		if !bytes.Equal(into.Bytes(), attachmentServed[from:]) {
			t.Fatalf("wrote %d bytes from an ignored range, want the %d after the offset",
				into.Len(), len(attachmentServed)-from)
		}
	})

	t.Run("an offset the whole file does not reach", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t, whole)
		var into bytes.Buffer
		err := c.Download(t.Context(), testAttachmentID, &into,
			jira.DownloadOptions{From: int64(len(attachmentServed)) + 1})
		assertRefusesTheOffset(t, err)
		if !strings.Contains(err.Error(), strconv.Itoa(len(attachmentServed))) {
			t.Errorf("the refusal must say how long the file was, got %q", err.Error())
		}
		if into.Len() != 0 {
			t.Errorf("a download that could not resume wrote %d bytes", into.Len())
		}
	})
}

func TestDownload_ResumingAtTheEndIsAFinishedDownloadAndNotARefusal(t *testing.T) {
	t.Parallel()

	// RFC 7233 makes bytes=N- unsatisfiable at N equal to the length as well as
	// past it, so a caller resuming a temp file that already holds every byte —
	// the copy finished, the rename did not — is answered 416 by any real range
	// server, http.ServeContent included.
	tests := map[string][]jiratest.ServerOption{
		"refused by Jira": nil,
		"refused by the media host": {jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
			attachmentRedirects)},
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, _ := attachmentClient(t, opts...)

			var into bytes.Buffer
			err := c.Download(t.Context(), testAttachmentID, &into,
				jira.DownloadOptions{From: int64(len(attachmentServed))})
			if err != nil {
				t.Fatalf("resuming at the end is a finished download, not a failure: %v", err)
			}
			if into.Len() != 0 {
				t.Errorf("resuming at the end wrote %d bytes", into.Len())
			}
		})
	}
}

func TestDownload_ReportsAResumePastTheEndAsTheOffsetItIs(t *testing.T) {
	t.Parallel()

	t.Run("refused by Jira", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t)
		err := c.Download(t.Context(), testAttachmentID, io.Discard,
			jira.DownloadOptions{From: int64(len(attachmentServed)) + 1})
		assertRefusesTheOffset(t, err)
		if !strings.Contains(err.Error(), strconv.Itoa(len(attachmentServed))) {
			t.Errorf("the site said how long the file is in Content-Range; the refusal must too, got %q", err.Error())
		}
	})

	t.Run("refused by the media host", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
			attachmentRedirects))
		err := c.Download(t.Context(), testAttachmentID, io.Discard,
			jira.DownloadOptions{From: int64(len(attachmentServed)) + 1})
		assertRefusesTheOffset(t, err)
		if !strings.Contains(err.Error(), strconv.Itoa(len(attachmentServed))) {
			t.Errorf("the media host said how long the file is; the refusal must too, got %q", err.Error())
		}
	})

	t.Run("refused with no length to name", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
			func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			}))
		err := c.Download(t.Context(), testAttachmentID, io.Discard, jira.DownloadOptions{From: 4096})
		assertRefusesTheOffset(t, err)
	})
}

func assertRefusesTheOffset(t *testing.T, err error) {
	t.Helper()

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != "from" {
		t.Errorf("the refusal names %+v, want the offset it was given", invalid.Fields)
	}
}

func TestDownload_FollowsARedirectWithoutTheJiraCredentialAndNeverNamesTheURL(t *testing.T) {
	t.Parallel()

	const secret = "signature-that-is-a-credential"
	var mediaAuth atomic.Value
	mediaAuth.Store("")
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaAuth.Store(r.Header.Get("Authorization"))
		if r.URL.Query().Get("token") != secret {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		attachmentServes(attachmentServed)(w, r)
	}))
	t.Cleanup(media.Close)

	signed := media.URL + "/media/10501?token=" + secret
	c, s := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", signed)
			w.WriteHeader(http.StatusSeeOther)
		}))

	var into bytes.Buffer
	const from = 32
	if err := c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{From: from}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(into.Bytes(), attachmentServed[from:]) {
		t.Fatalf("the redirect gave %d bytes, want the %d after the offset", into.Len(), len(attachmentServed)-from)
	}
	if got := mediaAuth.Load().(string); got != "" {
		t.Errorf("the media host was sent Authorization %q; the Jira credential must not leave the Jira host", got)
	}
	for _, served := range s.Requests() {
		if strings.Contains(served.Query, secret) {
			t.Errorf("the signed URL was sent back to Jira: %v", served)
		}
	}
}

func TestDownload_AsksJiraForAFreshRedirectRatherThanReusingASignedURL(t *testing.T) {
	t.Parallel()

	var issued atomic.Int64
	var seen [2]string
	media := httptest.NewServer(attachmentServes(attachmentServed))
	t.Cleanup(media.Close)

	c, s := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, _ *http.Request) {
			n := issued.Add(1)
			w.Header().Set("Location", media.URL+"/media/10501?token=signature-"+strconv.FormatInt(n, 10))
			w.WriteHeader(http.StatusSeeOther)
		}))

	ctx := t.Context()
	for i := range seen {
		var into bytes.Buffer
		if err := c.Download(ctx, testAttachmentID, &into, jira.DownloadOptions{From: int64(i)}); err != nil {
			t.Fatalf("download %d: %v", i, err)
		}
		seen[i] = into.String()
	}
	if seen[0] != string(attachmentServed) || seen[1] != string(attachmentServed[1:]) {
		t.Error("both downloads must come back with the bytes their own range asked for")
	}
	if got := issued.Load(); got != 2 {
		t.Errorf("Jira was asked for a redirect %d times, want one per download: a stored URL expires in minutes", got)
	}
	redirects := 0
	for _, served := range s.Requests() {
		if served.Path == "/rest/api/3/attachment/content/"+testAttachmentID {
			redirects++
		}
	}
	if redirects != 2 {
		t.Errorf("the content endpoint was asked %d times, want one per download", redirects)
	}
}

func TestDownload_ReportsARedirectItCannotFollowAsATransportFailure(t *testing.T) {
	t.Parallel()

	tests := map[string]http.HandlerFunc{
		"a redirect naming nowhere": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusSeeOther)
		},
		"a redirect naming something that is not an address": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/not/an/absolute/url")
			w.WriteHeader(http.StatusSeeOther)
		},
	}
	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute, handler))
			err := c.Download(t.Context(), testAttachmentID, io.Discard, jira.DownloadOptions{})
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
		})
	}
}

func TestDownload_ReportsAnExpiredSignatureAsAFailureAndNotAsAPermission(t *testing.T) {
	t.Parallel()

	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(media.Close)

	const secret = "signature-that-has-expired"
	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", media.URL+"/media/10501?token="+secret)
			w.WriteHeader(http.StatusSeeOther)
		}))

	err := c.Download(t.Context(), testAttachmentID, io.Discard, jira.DownloadOptions{})
	var refused *jira.CapabilityError
	if errors.As(err, &refused) {
		t.Fatalf("a media host's 403 was reported as %v, which sends the user to an administrator for nothing", refused)
	}
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if broken.Status != http.StatusForbidden {
		t.Errorf("Status = %d, want 403", broken.Status)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the signed URL reached an error message: %q", err.Error())
	}
}

func TestDownload_KeepsTheSignedURLOutOfAFailureToReachTheMediaHost(t *testing.T) {
	t.Parallel()

	const secret = "signature-that-is-a-credential"
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	gone := dead.URL
	dead.Close()

	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", gone+"/media/10501?token="+secret)
			w.WriteHeader(http.StatusSeeOther)
		}))

	err := c.Download(t.Context(), testAttachmentID, io.Discard, jira.DownloadOptions{})
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if broken.Op != attachmentMediaOp {
		t.Errorf("Op = %q, want the media host: it is the one that did not answer", broken.Op)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the signed URL reached an error message: %q", err.Error())
	}
	if strings.Contains(err.Error(), gone) {
		t.Errorf("the media address reached an error message: %q", err.Error())
	}
}

func TestDownload_NamesTheHostTheCopyBrokeOnAndNotTheOneItAskedFirst(t *testing.T) {
	t.Parallel()

	const secret = "signature-that-is-a-credential"
	// A body that stops short of its own Content-Length breaks the copy mid-way,
	// which is the failure a resume exists for.
	shortBody := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(attachmentLong)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(attachmentLong[:64])
	}

	t.Run("the copy broke on the Jira host", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute, shortBody))
		var into bytes.Buffer
		broken := assertBrokeMidBody(t, c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{}))
		if want := http.MethodGet + " " + attachmentContentPath(testAttachmentID); broken.Op != want {
			t.Errorf("Op = %q, want %q", broken.Op, want)
		}
		if into.Len() == 0 {
			t.Error("what did arrive before the break must stay in the writer, so a resume has somewhere to carry on from")
		}
	})

	t.Run("the copy broke on the media host", func(t *testing.T) {
		t.Parallel()

		media := httptest.NewServer(http.HandlerFunc(shortBody))
		t.Cleanup(media.Close)
		c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", media.URL+"/media/10501?token="+secret)
				w.WriteHeader(http.StatusSeeOther)
			}))

		var into bytes.Buffer
		broken := assertBrokeMidBody(t, c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{}))
		if broken.Op != attachmentMediaOp {
			t.Errorf("Op = %q, want the media host: a copy that broke there did not break on the Jira endpoint", broken.Op)
		}
		if strings.Contains(broken.Error(), secret) {
			t.Errorf("the signed URL reached an error message: %q", broken.Error())
		}
		if into.Len() == 0 {
			t.Error("what did arrive before the break must stay in the writer, so a resume has somewhere to carry on from")
		}
	})
}

func assertBrokeMidBody(t *testing.T, err error) *jira.TransportError {
	t.Helper()

	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	return broken
}

func TestDownload_RefusesAnIdAWriterAndAnOffsetItCannotUseWithoutARequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		id    string
		into  io.Writer
		opt   jira.DownloadOptions
		field string
	}{
		{name: "no id", id: "  ", into: io.Discard, field: "id"},
		{name: "nowhere to write", id: testAttachmentID, into: nil, field: "writer"},
		{name: "an offset before the first byte", id: testAttachmentID, into: io.Discard,
			opt: jira.DownloadOptions{From: -1}, field: "from"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, s := attachmentClient(t)
			err := c.Download(t.Context(), tt.id, tt.into, tt.opt)
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if len(invalid.Fields) != 1 || invalid.Fields[0].Field != tt.field {
				t.Errorf("the refusal names %+v, want %s", invalid.Fields, tt.field)
			}
			if served := s.Requests(); len(served) != 0 {
				t.Errorf("a download that cannot be made asked the site %d times: %v", len(served), served)
			}
		})
	}
}

type writerFunc func(b []byte) (int, error)

func (w writerFunc) Write(b []byte) (int, error) { return w(b) }

func TestDownload_StopsWhereTheCancelFoundItAndReturnsTheContextsOwnError(t *testing.T) {
	t.Parallel()

	// Cancelling from inside the writer puts the cancel where a user's does: the
	// copy is under way and part of the file is already on disk.
	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		attachmentServes(attachmentLong)))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var into bytes.Buffer
	var writes int
	stopping := writerFunc(func(b []byte) (int, error) {
		writes++
		if writes == 1 {
			cancel()
		}
		return into.Write(b)
	})

	err := c.Download(ctx, testAttachmentID, stopping, jira.DownloadOptions{})
	assertTheCallersOwnAnswer(t, err)
	if into.Len() == 0 {
		t.Error("nothing was written before the cancel, so this measured the wrong stop")
	}
	if into.Len() == len(attachmentLong) {
		t.Errorf("the whole %d bytes were written after a cancel on the first chunk", into.Len())
	}
}

func TestDownload_ReturnsTheContextsOwnErrorWhenTheSiteNeverAnswered(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	s := jiratest.NewServer(attachmentRoutes(jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(_ http.ResponseWriter, r *http.Request) {
			announce()
			<-r.Context().Done()
		}))...)
	defer closeServer(t, s)

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	failed := make(chan error, 1)
	go func() {
		failed <- c.Download(ctx, testAttachmentID, io.Discard, jira.DownloadOptions{})
	}()
	receive(t, "the download to reach the site", arrived)
	cancel()
	assertTheCallersOwnAnswer(t, receive(t, "the cancelled download to come back", failed))
}

// assertTheCallersOwnAnswer holds a cancel to coming back as it is: wrapped in a
// transport failure it reads as the site breaking, and invites a retry.
func assertTheCallersOwnAnswer(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %T (%v), want context.Canceled", err, err)
	}
	var broken *jira.TransportError
	if errors.As(err, &broken) {
		t.Errorf("the cancel came back wrapped as %v", broken)
	}
}

func TestDownload_GivesBackItsConcurrencySlotWhenTheStreamCloses(t *testing.T) {
	t.Parallel()

	// One slot, three downloads: if closing a stream did not release the slot the
	// second would never start, so the bound is what fails this rather than a hang.
	s := jiratest.NewServer(attachmentRoutes()...)
	defer closeServer(t, s)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}), WithMaxConcurrent(1))

	for i := range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), wedgeBound)
		err := c.Download(ctx, testAttachmentID, io.Discard, jira.DownloadOptions{})
		cancel()
		if err != nil {
			t.Fatalf("download %d: %v", i, err)
		}
	}
}

func TestDownload_GivesBackItsConcurrencySlotWhenTheSiteRefuses(t *testing.T) {
	t.Parallel()

	answered := atomic.Bool{}
	s := jiratest.NewServer(attachmentRoutes(jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, r *http.Request) {
			if answered.CompareAndSwap(false, true) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			attachmentServes(attachmentServed)(w, r)
		}))...)
	defer closeServer(t, s)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}), WithMaxConcurrent(1))

	ctx, cancel := context.WithTimeout(t.Context(), wedgeBound)
	defer cancel()
	if err := c.Download(ctx, testAttachmentID, io.Discard, jira.DownloadOptions{}); err == nil {
		t.Fatal("Download: want the 500 reported")
	}
	if err := c.Download(ctx, testAttachmentID, io.Discard, jira.DownloadOptions{}); err != nil {
		t.Fatalf("the download after a refused one: %v", err)
	}
}

func TestDownload_GivesBackItsConcurrencySlotAcrossTheRedirectHandoff(t *testing.T) {
	t.Parallel()

	// The redirect is two requests on one slot: the first has to be closed before
	// the media host is asked, or a client with one slot wedges on its own gate.
	s := jiratest.NewServer(attachmentRoutes(jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		attachmentRedirects))...)
	defer closeServer(t, s)

	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}), WithMaxConcurrent(1))
	for i := range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), wedgeBound)
		var into bytes.Buffer
		err := c.Download(ctx, testAttachmentID, &into, jira.DownloadOptions{})
		cancel()
		if err != nil {
			t.Fatalf("redirected download %d: %v", i, err)
		}
		if !bytes.Equal(into.Bytes(), attachmentServed) {
			t.Fatalf("redirected download %d gave %d bytes", i, into.Len())
		}
	}
}

func TestDownload_GivesBackItsConcurrencySlotWhenTheRangeIsRefused(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(attachmentRoutes()...)
	defer closeServer(t, s)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}), WithMaxConcurrent(1))

	for i := range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), wedgeBound)
		err := c.Download(ctx, testAttachmentID, io.Discard,
			jira.DownloadOptions{From: int64(len(attachmentServed)) + 1})
		cancel()
		assertRefusesTheOffset(t, err)
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("refused download %d never got a slot", i)
		}
	}
}

func TestDownload_RetriesAThrottledContentRequestAndKeepsTheBytes(t *testing.T) {
	t.Parallel()

	var asked atomic.Int64
	s := jiratest.NewServer(attachmentRoutes(jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, r *http.Request) {
			if asked.Add(1) == 1 {
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"errorMessages":["Rate limit exceeded."],"errors":{}}`))
				return
			}
			attachmentServes(attachmentServed)(w, r)
		}))...)
	defer closeServer(t, s)

	c, clock := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 3}))
	var into bytes.Buffer
	if err := c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(into.Bytes(), attachmentServed) {
		t.Errorf("the retried download wrote %d bytes, want the %d served", into.Len(), len(attachmentServed))
	}
	if got := asked.Load(); got != 2 {
		t.Errorf("the content endpoint was asked %d times, want the throttled one and the one that answered", got)
	}
	if waits := clock.waited(); len(waits) != 1 || waits[0] != 30*time.Second {
		t.Errorf("waited %v, want the 30s the site asked for", waits)
	}
}

func TestDoStream_ReadsABoundedReasonOutOfARefusalItStreamsNothingOf(t *testing.T) {
	t.Parallel()

	reason := func(n int) string {
		body, err := json.Marshal(map[string]any{
			"errorMessages": []string{"the site refused this: " + strings.Repeat("z", n)},
			"errors":        map[string]string{},
		})
		if err != nil {
			t.Fatalf("building the body: %v", err)
		}
		return string(body)
	}

	t.Run("a reason longer than one read and shorter than the bound", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
			jsonHandler(http.StatusInternalServerError, reason(8<<10))))
		err := c.Download(t.Context(), testAttachmentID, io.Discard, jira.DownloadOptions{})
		var broken *jira.TransportError
		if !errors.As(err, &broken) {
			t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
		}
		if !strings.Contains(err.Error(), "the site refused this") {
			t.Errorf("the site's own sentence was not read out of the refusal: %q", err.Error())
		}
	})

	t.Run("a body past the bound", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
			jsonHandler(http.StatusInternalServerError, reason(4*streamErrorLimit))))
		err := c.Download(t.Context(), testAttachmentID, io.Discard, jira.DownloadOptions{})
		var broken *jira.TransportError
		if !errors.As(err, &broken) {
			t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
		}
		if broken.Status != http.StatusInternalServerError {
			t.Errorf("Status = %d, want the 500 the site sent: a body too long to read cannot change that", broken.Status)
		}
		if len(err.Error()) > streamErrorLimit {
			t.Errorf("the error carries %d bytes of the site's body", len(err.Error()))
		}
	})
}

func TestDeleteAttachment_RemovesOneByIdAlone(t *testing.T) {
	t.Parallel()

	c, s := attachmentClient(t)
	if err := c.DeleteAttachment(t.Context(), testAttachmentID); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}
	sent := sentTo(t, s, http.MethodDelete, "/rest/api/3/attachment/"+testAttachmentID)
	if sent.Body != "" {
		t.Errorf("the delete sent a body: %q", sent.Body)
	}
}

func TestDeleteAttachment_ReportsAnAttachmentThatIsNotThereAsThatAttachment(t *testing.T) {
	t.Parallel()

	c, _ := attachmentClient(t, jiratest.WithStatus(http.MethodDelete, attachmentDeleteRoute,
		http.StatusNotFound, "problem_no_endpoint.json"))
	err := c.DeleteAttachment(t.Context(), testAttachmentID)
	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
	}
	if missing.Kind != "attachment" || missing.ID != testAttachmentID {
		t.Errorf("the 404 named %s %s, want attachment %s", missing.Kind, missing.ID, testAttachmentID)
	}
}

func TestAttachments_ReportsABodyItCannotReadAsATransportFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call attachmentCall
		body string
	}{
		{name: "an issue read served HTML", call: attachmentCalls()[0], body: "<html>your proxy has opinions</html>"},
		{name: "an upload answered with an object", call: attachmentCalls()[1], body: `{"id":"10801"}`},
		{name: "an upload answered with truncated JSON", call: attachmentCalls()[1], body: `[{"id":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := attachmentClient(t, jiratest.WithHandler(tt.call.method, tt.call.route,
				jsonHandler(http.StatusOK, tt.body)))
			err := tt.call.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != http.StatusOK {
				t.Errorf("Status = %d, want the 200 the site answered with", broken.Status)
			}
		})
	}
}

func TestAttachmentCalls_ReportARefusalRateLimitAndTransportFailureAsThemselves(t *testing.T) {
	t.Parallel()

	for _, call := range attachmentCalls() {
		t.Run(call.name+"/a token the site refuses", func(t *testing.T) {
			t.Parallel()

			c, _ := attachmentClient(t, jiratest.WithStatus(call.method, call.route,
				http.StatusForbidden, "plans_403.json"))
			err := call.run(t.Context(), c)
			var refused *jira.CapabilityError
			if !errors.As(err, &refused) {
				t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
			}
			// Attaching and deleting are project permissions, and CapAttachments is
			// the site-wide switch: a 403 here is not that switch, and naming it
			// would send somebody to an administrator who has already said yes.
			if refused.Capability != "" {
				t.Errorf("a 403 named the capability %q, want none: the site switch is read from the settings, not guessed from a status",
					refused.Capability)
			}
			if strings.TrimSpace(refused.Reason) == "" {
				t.Error("the refusal carries no reason to show anybody")
			}
		})

		t.Run(call.name+"/a site that is throttling", func(t *testing.T) {
			t.Parallel()

			c, _ := attachmentClient(t, jiratest.WithRateLimit(call.method, call.route, 30*time.Second))
			err := call.run(t.Context(), c)
			var limited *jira.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("got %T (%v), want a *jira.RateLimitError", err, err)
			}
			if limited.RetryAfter != 30*time.Second {
				t.Errorf("RetryAfter = %s, want the 30s the site asked for", limited.RetryAfter)
			}
		})

		t.Run(call.name+"/a host that never answered", func(t *testing.T) {
			t.Parallel()

			s := jiratest.NewServer(attachmentRoutes()...)
			dead := s.URL()
			s.Close()
			c, _ := testClient(t, dead, WithRetry(RetryPolicy{Attempts: 1}))
			err := call.run(t.Context(), c)
			var broken *jira.TransportError
			if !errors.As(err, &broken) {
				t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
			}
			if broken.Status != 0 {
				t.Errorf("Status = %d, want 0: nothing answered", broken.Status)
			}
		})

		t.Run(call.name+"/a caller who has already left", func(t *testing.T) {
			t.Parallel()

			c, s := attachmentClient(t)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := call.run(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %T (%v), want context.Canceled unwrapped", err, err)
			}
			if served := s.Requests(); len(served) != 0 {
				t.Errorf("a cancelled call reached the site %d times: %v", len(served), served)
			}
		})
	}
}

func TestAttachmentProgress_WritesNothingMoreOnceTheContextHasEnded(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	var into bytes.Buffer
	reported := 0
	counted := &attachmentProgress{ctx: ctx, dst: &into, report: func(int64) { reported++ }}

	if n, err := counted.Write([]byte("first")); n != 5 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	cancel()
	n, err := counted.Write([]byte("second"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Write after a cancel = %v, want the context's own error", err)
	}
	if n != 0 {
		t.Errorf("Write reported %d bytes written after a cancel", n)
	}
	if into.String() != "first" {
		t.Errorf("the writer holds %q; a cancelled download must stop writing even when the body is already buffered", into.String())
	}
	if reported != 1 {
		t.Errorf("progress was reported %d times, want the one write that happened", reported)
	}
}

func TestNewRequest_KeepsTheClientsOwnCredentialOverACallersHeader(t *testing.T) {
	t.Parallel()

	c, s := attachmentClient(t)
	ctx := t.Context()
	callers := http.Header{"Authorization": {"Bearer not-the-clients-own"}}

	if _, err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/rest/api/3/myself",
		header: callers.Clone(),
	}); err != nil {
		t.Fatalf("the buffered path: %v", err)
	}
	open, err := c.doStream(ctx, request{
		method: http.MethodGet,
		path:   attachmentContentPath(testAttachmentID),
		query:  url.Values{"redirect": {"false"}},
		header: callers.Clone(),
	}, attachmentAnswered)
	if err != nil {
		t.Fatalf("the streaming path: %v", err)
	}
	open.close()

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testEmail+":"+testToken))
	served := s.Requests()
	if len(served) != 2 {
		t.Fatalf("the site served %d requests, want one per path", len(served))
	}
	for _, sent := range served {
		if got := sent.Header.Get("Authorization"); got != want {
			t.Errorf("%s %s carried Authorization %q, want the client's own credential",
				sent.Method, sent.Path, got)
		}
	}
}

func TestClient_ReportsARedirectOnAnOrdinaryEndpointRatherThanFollowingIt(t *testing.T) {
	t.Parallel()

	// Nothing but a download is redirected by a real site, and this client follows
	// none of them: a 3xx anywhere else is reported rather than chased with the
	// credential still attached.
	var followed atomic.Int64
	c, _ := attachmentClient(t,
		jiratest.WithHandler(http.MethodGet, "/rest/api/3/myself", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/rest/api/3/myself/moved")
			w.WriteHeader(http.StatusFound)
		}),
		jiratest.WithHandler(http.MethodGet, "/rest/api/3/myself/moved", func(w http.ResponseWriter, r *http.Request) {
			followed.Add(1)
			jsonHandler(http.StatusOK, `{"accountId":"5b10a2844c20165700ede21g"}`)(w, r)
		}))

	_, err := c.Me(t.Context())
	var broken *jira.TransportError
	if !errors.As(err, &broken) {
		t.Fatalf("got %T (%v), want a *jira.TransportError", err, err)
	}
	if broken.Status != http.StatusFound {
		t.Errorf("Status = %d, want the 302 the site sent", broken.Status)
	}
	if n := followed.Load(); n != 0 {
		t.Errorf("the redirect was followed %d times", n)
	}
}

type uploadedPart struct {
	name     string
	filename string
	body     string
}

func attachmentPartsOf(t *testing.T, sent jiratest.Request) []uploadedPart {
	t.Helper()

	kind, params, err := mime.ParseMediaType(sent.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("reading the upload's Content-Type %q: %v", sent.Header.Get("Content-Type"), err)
	}
	if kind != "multipart/form-data" {
		t.Fatalf("Content-Type = %q, want multipart/form-data", kind)
	}
	reader := multipart.NewReader(strings.NewReader(sent.Body), params["boundary"])
	var out []uploadedPart
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("reading the upload's parts: %v", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("reading a part's bytes: %v", err)
		}
		out = append(out, uploadedPart{name: part.FormName(), filename: part.FileName(), body: string(body)})
	}
}
