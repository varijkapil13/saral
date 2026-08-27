package cloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

	// testAttachmentID is the id the issue fixture's attachment carries, written
	// as a string there and as a number by the standalone read.
	testAttachmentID = "10501"

	// attachmentMetaBody is a site with attachments on and a small cap, so a test
	// can be over the limit without holding a real file's worth of bytes.
	attachmentMetaBody = `{"enabled":true,"uploadLimit":10485760}`

	// uploadedAttachments is what a successful upload answers: 200, a bare array,
	// and the id as a string.
	uploadedAttachments = `[{"id":"10801","filename":"notes.txt","mimeType":"text/plain","size":11,
		"created":"2026-03-01T09:15:00.000+0000","content":"https://example.atlassian.net/rest/api/3/attachment/content/10801",
		"author":{"accountId":"5b10a2844c20165700ede21g","displayName":"Example User","active":true}}]`
)

// attachmentBytes is what the content endpoint serves. It is long enough that
// one copy takes several reads, which is what makes progress reporting visible.
var attachmentBytes = bytes.Repeat([]byte("saral attachment "), 5000)

func attachmentRoutes(opts ...jiratest.ServerOption) []jiratest.ServerOption {
	return append([]jiratest.ServerOption{
		jiratest.WithHandler(http.MethodGet, attachmentMetaRoute, jsonHandler(http.StatusOK, attachmentMetaBody)),
		jiratest.WithHandler(http.MethodPost, attachmentUploadRoute, jsonHandler(http.StatusOK, uploadedAttachments)),
		jiratest.WithHandler(http.MethodGet, attachmentContentRoute, attachmentContentHandler(attachmentBytes)),
		jiratest.WithHandler(http.MethodDelete, attachmentDeleteRoute, jsonHandler(http.StatusNoContent, "")),
	}, opts...)
}

func attachmentClient(t *testing.T, opts ...jiratest.ServerOption) (*Client, *jiratest.Server) {
	t.Helper()

	s := jiratest.NewServer(attachmentRoutes(opts...)...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c, s
}

// attachmentContentHandler answers the content endpoint the way a site asked not
// to redirect does: the whole file with a 200, and the tail with a 206 when the
// request carries a Range.
func attachmentContentHandler(data []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, ranged := attachmentRangeFrom(r)
		switch {
		case ranged && from > int64(len(data)):
			w.Header().Set("Content-Range", "bytes */"+strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		case ranged:
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(data)-1, len(data)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(data[from:])
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		}
	}
}

func attachmentRangeFrom(r *http.Request) (from int64, ranged bool) {
	value := strings.TrimPrefix(r.Header.Get("Range"), "bytes=")
	if value == r.Header.Get("Range") {
		return 0, false
	}
	start, _, _ := strings.Cut(value, "-")
	parsed, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// attachmentCall is one of the four methods this file covers, in a shape the
// failure tables can drive. namesCapability marks the two writes: a refused read
// must not report the whole feature as unavailable.
type attachmentCall struct {
	name            string
	method          string
	route           string
	namesCapability bool
	run             func(ctx context.Context, c *Client) error
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
			name: "Upload", method: http.MethodPost, route: attachmentUploadRoute, namesCapability: true,
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
			namesCapability: true,
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

func TestAttachments_NormalisesAnIdWrittenAsANumberAndAsAString(t *testing.T) {
	t.Parallel()

	// The same attachment, read two ways: the upload writes its id as a string,
	// an issue read of the standalone endpoint's shape writes it as a number.
	const numbered = `{"key":"EX-1","fields":{"attachment":[{"id":10801,"filename":"notes.txt","size":11}]}}`

	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, issueRoute, jsonHandler(http.StatusOK, numbered)))
	ctx := t.Context()

	listed, err := c.Attachments(ctx, testIssueKey)
	if err != nil || len(listed) != 1 {
		t.Fatalf("Attachments: %v (%d)", err, len(listed))
	}
	uploaded, err := c.Upload(ctx, testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")})
	if err != nil || len(uploaded) != 1 {
		t.Fatalf("Upload: %v (%d)", err, len(uploaded))
	}
	if listed[0].ID != "10801" {
		t.Errorf("a numeric id read as %q, want %q", listed[0].ID, "10801")
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
			Name: "notes.txt", Size: 11,
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

	c, s := attachmentClient(t)
	got, err := c.Upload(t.Context(), testIssueKey, files)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(got) != 1 || got[0].ID != "10801" || got[0].Filename != "notes.txt" {
		t.Fatalf("the bare array answer read as %+v", got)
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
	if parts[0].filename != "notes.txt" || parts[0].body != "hello saral" {
		t.Errorf("first part = %+v", parts[0])
	}
	if parts[1].filename != "trace.log" || parts[1].body != "lines" {
		t.Errorf("second part = %+v", parts[1])
	}
}

func TestUpload_ReadsTheSiteSizeCapAndRefusesAnOversizedFileWithoutSendingIt(t *testing.T) {
	t.Parallel()

	c, s := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentMetaRoute,
		jsonHandler(http.StatusOK, `{"enabled":true,"uploadLimit":8}`)))

	_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "nine byte")})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if !strings.Contains(invalid.Error(), "8") {
		t.Errorf("the refusal must quote the site's own number, got %q", invalid.Error())
	}
	for _, served := range s.Requests() {
		if served.Method == http.MethodPost {
			t.Fatalf("the oversized file was uploaded anyway: %v", served)
		}
	}
}

func TestUpload_RefusesWhenTheSiteHasAttachmentsSwitchedOff(t *testing.T) {
	t.Parallel()

	c, s := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentMetaRoute,
		jsonHandler(http.StatusOK, `{"enabled":false,"uploadLimit":10485760}`)))

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

func TestUpload_ReportsThePlainTextXSRF404AsTheGuardAndNotAsAMissingIssue(t *testing.T) {
	t.Parallel()

	t.Run("a 404 whose body is not JSON at all", func(t *testing.T) {
		t.Parallel()

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

	const problem = `{"type":"about:blank","title":"Bad Request","status":400,
		"detail":"The multipart request did not contain a part named file"}`

	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodPost, attachmentUploadRoute,
		jsonHandler(http.StatusBadRequest, problem)))

	_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{testFile("notes.txt", "hello saral")})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if !strings.Contains(invalid.Error(), "part named file") {
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

	c, s := attachmentClient(t)
	var into bytes.Buffer
	var progress []int64
	err := c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{
		Progress: func(written int64) { progress = append(progress, written) },
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(into.Bytes(), attachmentBytes) {
		t.Fatalf("downloaded %d bytes, want the %d the site served", into.Len(), len(attachmentBytes))
	}
	if len(progress) < 2 {
		t.Fatalf("progress was reported %d times for %d bytes, want it as the copy goes", len(progress), len(attachmentBytes))
	}
	for i := 1; i < len(progress); i++ {
		if progress[i] <= progress[i-1] {
			t.Fatalf("progress must be cumulative and increasing, got %v", progress)
		}
	}
	if last := progress[len(progress)-1]; last != int64(len(attachmentBytes)) {
		t.Errorf("the last progress call reported %d, want the whole %d", last, len(attachmentBytes))
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

	const from = 4096
	c, s := attachmentClient(t)
	var into bytes.Buffer
	if err := c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{From: from}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(into.Bytes(), attachmentBytes[from:]) {
		t.Fatalf("resumed download wrote %d bytes, want the %d after the offset", into.Len(), len(attachmentBytes)-from)
	}
	sent := sentTo(t, s, http.MethodGet, "/rest/api/3/attachment/content/"+testAttachmentID)
	if got, want := sent.Header.Get("Range"), "bytes=4096-"; got != want {
		t.Errorf("Range = %q, want %q", got, want)
	}
}

func TestDownload_DropsTheBytesTheCallerHasWhenTheSiteIgnoresTheRange(t *testing.T) {
	t.Parallel()

	const from = 17
	// A site that answers the whole file to a ranged request is within its
	// rights, and writing it as it stands doubles the bytes the caller has.
	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(attachmentBytes)
		}))

	var into bytes.Buffer
	if err := c.Download(t.Context(), testAttachmentID, &into, jira.DownloadOptions{From: from}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(into.Bytes(), attachmentBytes[from:]) {
		t.Fatalf("wrote %d bytes from an ignored range, want the %d after the offset",
			into.Len(), len(attachmentBytes)-from)
	}
}

func TestDownload_ReportsAResumePastTheEndAsTheOffsetItIs(t *testing.T) {
	t.Parallel()

	tests := map[string]jiratest.ServerOption{
		"refused by Jira": nil,
		"refused by the media host": jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
			func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Range", "bytes */"+strconv.Itoa(len(attachmentBytes)))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			}),
	}
	for name, opt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []jiratest.ServerOption
			if opt != nil {
				opts = append(opts, opt)
			}
			c, _ := attachmentClient(t, opts...)
			err := c.Download(t.Context(), testAttachmentID, io.Discard,
				jira.DownloadOptions{From: int64(len(attachmentBytes)) + 1})
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if len(invalid.Fields) != 1 || invalid.Fields[0].Field != "from" {
				t.Errorf("the refusal names %+v, want the offset it was given", invalid.Fields)
			}
		})
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
		attachmentContentHandler(attachmentBytes)(w, r)
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
	if !bytes.Equal(into.Bytes(), attachmentBytes[from:]) {
		t.Fatalf("the redirect gave %d bytes, want the %d after the offset", into.Len(), len(attachmentBytes)-from)
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
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attachmentContentHandler(attachmentBytes)(w, r)
	}))
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
	if seen[0] != string(attachmentBytes) || seen[1] != string(attachmentBytes[1:]) {
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

func TestDownload_StopsWhereTheCancelFoundItAndReturnsTheContextsOwnError(t *testing.T) {
	t.Parallel()

	arrived, announce := gate()
	release, letGo := gate()
	s := jiratest.NewServer(attachmentRoutes(jiratest.WithHandler(http.MethodGet, attachmentContentRoute,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(attachmentBytes[:64])
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
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
	defer cancel()

	failed := make(chan error, 1)
	var into bytes.Buffer
	go func() {
		failed <- c.Download(ctx, testAttachmentID, &into, jira.DownloadOptions{})
	}()

	receive(t, "the download to reach the site", arrived)
	cancel()
	if err := receive(t, "the cancelled download to come back", failed); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %T (%v), want context.Canceled unwrapped", err, err)
	}
	if into.Len() > len(attachmentBytes) {
		t.Errorf("a cancelled download wrote %d bytes, more than the file has", into.Len())
	}
}

func TestDownload_GivesBackItsConcurrencySlotWhenTheStreamCloses(t *testing.T) {
	t.Parallel()

	// One slot, three downloads: if closing a stream did not release the slot the
	// second would never start, so the bound is what fails this rather than a hang.
	s := jiratest.NewServer(attachmentRoutes(jiratest.WithStatus(http.MethodDelete, attachmentDeleteRoute,
		http.StatusNoContent, ""))...)
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
			attachmentContentHandler(attachmentBytes)(w, r)
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
			switch {
			case call.namesCapability && refused.Capability != jira.CapAttachments:
				t.Errorf("a refused write named %q, want %q", refused.Capability, jira.CapAttachments)
			case !call.namesCapability && refused.Capability != "":
				t.Errorf("a refused read named %q; reading an attachment needs no capability of its own",
					refused.Capability)
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

// attachmentPart is one part of a recorded multipart body.
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
