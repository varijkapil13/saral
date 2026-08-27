package cloud

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for the four methods an
// attachment pane calls. The two describe different sites on purpose — the cloud
// adapter reads a replay server and the fake keeps files in a map — so the cases
// are properties: that the id an upload hands back is the id the rest of the API
// takes, that a write refused for the site's own reason names the capability,
// that an attachment nobody has is absent rather than empty, and that a resume
// past the end is refused rather than answered with nothing.
//
// The role is stated here rather than taken from pkg/jira, which declares
// neither jira.AttachmentReader nor jira.Attacher although both are named as
// existing. Being satisfied by *jiratest.Fake, which is held to the whole
// jira.Client, is what keeps these four signatures the port's own.
type attacher interface {
	Attachments(ctx context.Context, key string) ([]jira.Attachment, error)
	Upload(ctx context.Context, key string, files []jira.FileRef) ([]jira.Attachment, error)
	Download(ctx context.Context, id string, w io.Writer, opt jira.DownloadOptions) error
	DeleteAttachment(ctx context.Context, id string) error
}

type attachBuilder func(*testing.T) attacher

type attachCase struct {
	name  string
	cloud attachBuilder
	fake  attachBuilder
	check func(*testing.T, attacher)
}

const (
	// conformAttachmentID is the attachment both adapters can be asked for: it is
	// the id the upload answer carries, so it is the id an upload hands back.
	conformAttachmentID = "10801"
	conformMissingID    = "99999"
	conformMissingIssue = "EX-404"
	conformUploadName   = "notes.txt"
	conformUploadBody   = "hello saral"
)

const conformNotFound = `{"errorMessages":["The attachment with id 99999 does not exist."],"errors":{}}`

// attachFromSite builds the cloud adapter over a site that knows exactly one
// attachment, so that an id nobody has is a 404 rather than a fixture.
func attachFromSite(t *testing.T, opts ...jiratest.ServerOption) attacher {
	t.Helper()

	known := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") != conformAttachmentID {
				jsonHandler(http.StatusNotFound, conformNotFound)(w, r)
				return
			}
			next(w, r)
		}
	}
	s := jiratest.NewServer(append([]jiratest.ServerOption{
		jiratest.WithHandler(http.MethodGet, attachmentMetaRoute, jsonHandler(http.StatusOK, attachmentMetaBody)),
		jiratest.WithHandler(http.MethodPost, attachmentUploadRoute, jsonHandler(http.StatusOK, uploadedAttachments)),
		jiratest.WithHandler(http.MethodGet, attachmentContentRoute, known(attachmentContentHandler(attachmentBytes))),
		jiratest.WithHandler(http.MethodDelete, attachmentDeleteRoute, known(jsonHandler(http.StatusNoContent, ""))),
	}, opts...)...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c
}

func attachFake(t *testing.T, opts ...jiratest.Option) attacher {
	t.Helper()
	return conformFake(t, append([]jiratest.Option{
		jiratest.WithIssues(jiratest.GenFor(conformProject, 2)),
	}, opts...)...)
}

// conformUpload puts one file on the issue in whichever adapter, and returns the
// attachment it says it stored.
func conformUpload(t *testing.T, a attacher) jira.Attachment {
	t.Helper()

	stored, err := a.Upload(t.Context(), conformProject+"-1", []jira.FileRef{
		testFile(conformUploadName, conformUploadBody),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("Upload stored %d attachments for one file: %+v", len(stored), stored)
	}
	return stored[0]
}

func TestAttachments_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []attachCase{
		{
			name:  "an upload names what was stored, by an id the rest of the API takes",
			cloud: func(t *testing.T) attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) attacher { return attachFake(t) },
			check: func(t *testing.T, a attacher) {
				stored := conformUpload(t, a)
				if stored.ID == "" {
					t.Fatal("the upload named no id, so nothing can be downloaded or deleted")
				}
				if stored.Filename != conformUploadName {
					t.Errorf("filename = %q, want the name it was uploaded under", stored.Filename)
				}
				if stored.Size != int64(len(conformUploadBody)) {
					t.Errorf("size = %d, want the %d bytes uploaded", stored.Size, len(conformUploadBody))
				}
				var into bytes.Buffer
				if err := a.Download(t.Context(), stored.ID, &into, jira.DownloadOptions{}); err != nil {
					t.Fatalf("downloading the id the upload handed back: %v", err)
				}
				if into.Len() == 0 {
					t.Error("the download of a stored attachment wrote nothing")
				}
				if err := a.DeleteAttachment(t.Context(), stored.ID); err != nil {
					t.Errorf("deleting the id the upload handed back: %v", err)
				}
			},
		},
		{
			name: "a site with attachments switched off refuses a write by name",
			cloud: func(t *testing.T) attacher {
				return attachFromSite(t,
					jiratest.WithStatus(http.MethodPost, attachmentUploadRoute, http.StatusForbidden, "plans_403.json"),
					jiratest.WithStatus(http.MethodDelete, attachmentDeleteRoute, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) attacher {
				return attachFake(t, jiratest.WithCapabilities(jiratest.NoAttachments))
			},
			check: func(t *testing.T, a attacher) {
				_, err := a.Upload(t.Context(), conformProject+"-1", []jira.FileRef{
					testFile(conformUploadName, conformUploadBody),
				})
				assertNamesAttachments(t, "Upload", err)
				assertNamesAttachments(t, "DeleteAttachment", a.DeleteAttachment(t.Context(), conformAttachmentID))
			},
		},
		{
			name:  "an attachment nobody has is absent rather than empty",
			cloud: func(t *testing.T) attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) attacher { return attachFake(t) },
			check: func(t *testing.T, a attacher) {
				var into bytes.Buffer
				err := a.Download(t.Context(), conformMissingID, &into, jira.DownloadOptions{})
				assertMissing(t, "Download", "attachment", conformMissingID, err)
				if into.Len() != 0 {
					t.Errorf("a download that failed wrote %d bytes", into.Len())
				}
				assertMissing(t, "DeleteAttachment", "attachment", conformMissingID,
					a.DeleteAttachment(t.Context(), conformMissingID))
			},
		},
		{
			name: "an issue nobody has is refused as that issue",
			cloud: func(t *testing.T) attacher {
				return attachFromSite(t, jiratest.WithStatus(http.MethodGet, issueRoute,
					http.StatusNotFound, "problem_no_endpoint.json"))
			},
			fake: func(t *testing.T) attacher { return attachFake(t) },
			check: func(t *testing.T, a attacher) {
				_, err := a.Attachments(t.Context(), conformMissingIssue)
				assertMissing(t, "Attachments", "issue", conformMissingIssue, err)
			},
		},
		{
			name:  "a resume is the tail, and a resume past the end is refused",
			cloud: func(t *testing.T) attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) attacher { return attachFake(t) },
			check: func(t *testing.T, a attacher) {
				stored := conformUpload(t, a)
				var whole bytes.Buffer
				if err := a.Download(t.Context(), stored.ID, &whole, jira.DownloadOptions{}); err != nil {
					t.Fatalf("Download: %v", err)
				}
				size := int64(whole.Len())
				if size < 4 {
					t.Fatalf("the attachment is %d bytes, too small to resume into", size)
				}

				var tail bytes.Buffer
				if err := a.Download(t.Context(), stored.ID, &tail, jira.DownloadOptions{From: size / 2}); err != nil {
					t.Fatalf("resumed Download: %v", err)
				}
				if got, want := tail.String(), whole.String()[size/2:]; got != want {
					t.Errorf("the resume wrote %d bytes, want the %d after the offset", len(got), len(want))
				}

				var nothing bytes.Buffer
				if err := a.Download(t.Context(), stored.ID, &nothing, jira.DownloadOptions{From: size}); err != nil {
					t.Errorf("resuming at the end is a finished download, not a failure: %v", err)
				}
				if nothing.Len() != 0 {
					t.Errorf("resuming at the end wrote %d bytes", nothing.Len())
				}

				err := a.Download(t.Context(), stored.ID, io.Discard, jira.DownloadOptions{From: size + 1})
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("resuming past the end gave %T (%v), want a *jira.ValidationError", err, err)
				}
				if len(invalid.Fields) != 1 || invalid.Fields[0].Field != "from" {
					t.Errorf("the refusal names %+v, want the offset it was given", invalid.Fields)
				}
			},
		},
		{
			name:  "progress reports the running total and ends at what was written",
			cloud: func(t *testing.T) attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) attacher { return attachFake(t) },
			check: func(t *testing.T, a attacher) {
				stored := conformUpload(t, a)
				var into bytes.Buffer
				var progress []int64
				err := a.Download(t.Context(), stored.ID, &into, jira.DownloadOptions{
					Progress: func(written int64) { progress = append(progress, written) },
				})
				if err != nil {
					t.Fatalf("Download: %v", err)
				}
				if len(progress) == 0 {
					t.Fatal("nothing was reported for a download of a file with bytes in it")
				}
				for i := 1; i < len(progress); i++ {
					if progress[i] <= progress[i-1] {
						t.Fatalf("progress must be cumulative and increasing, got %v", progress)
					}
				}
				if last := progress[len(progress)-1]; last != int64(into.Len()) {
					t.Errorf("the last progress call reported %d, want the %d written", last, into.Len())
				}
				// A caller that wants no progress passes none, and that is not an
				// argument a download may dereference.
				if err := a.Download(t.Context(), stored.ID, io.Discard, jira.DownloadOptions{}); err != nil {
					t.Errorf("a download with no Progress: %v", err)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open attachBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()
				tt.check(t, adapter.open(t))
			})
		}
	}
}

func assertNamesAttachments(t *testing.T, what string, err error) {
	t.Helper()

	var refused *jira.CapabilityError
	if !errors.As(err, &refused) {
		t.Fatalf("%s gave %T (%v), want a *jira.CapabilityError", what, err, err)
	}
	if refused.Capability != jira.CapAttachments {
		t.Errorf("%s named the capability %q, want %q", what, refused.Capability, jira.CapAttachments)
	}
	if strings.TrimSpace(refused.Reason) == "" {
		t.Errorf("%s refused without a reason to show anybody", what)
	}
}

func assertMissing(t *testing.T, what, kind, id string, err error) {
	t.Helper()

	var missing *jira.NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("%s gave %T (%v), want a *jira.NotFoundError", what, err, err)
	}
	if missing.Kind != kind || missing.ID != id {
		t.Errorf("%s named %s %s, want %s %s", what, missing.Kind, missing.ID, kind, id)
	}
}
