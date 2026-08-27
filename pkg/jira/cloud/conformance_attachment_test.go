package cloud

import (
	"bytes"
	"errors"
	"fmt"
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
// are properties rather than fixtures compared to each other.
type attachBuilder func(*testing.T) jira.Attacher

type attachCase struct {
	name  string
	cloud attachBuilder
	fake  attachBuilder
	check func(*testing.T, jira.Attacher)
}

const (
	conformMissingID    = "99999"
	conformMissingIssue = "EX-404"
)

// conformStored are the ids attachment_upload.json names, which is what an upload
// against the shared route hands back and therefore what the rest of the API is
// asked for afterwards.
var conformStored = []string{"10503", "10504"}

// conformFiles is the two-file upload attachment_upload.json is the answer to:
// the same names and the same lengths, so that "what came back is what was sent"
// is a property of the adapter that replays the fixture and of the one that
// stores what it was given.
func conformFiles() []jira.FileRef {
	return []jira.FileRef{
		testFile("rollout-notes.csv", strings.Repeat("r", 1904)),
		testFile("checkout-timeout-after.png", strings.Repeat("p", 41207)),
	}
}

// attachFromSite builds the cloud adapter over the shared fixture server, with
// the content and delete routes narrowed to the attachments this site has so
// that an id nobody has is a 404 rather than a fixture.
func attachFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.Attacher {
	t.Helper()

	missing, err := jiratest.Fixture("problem_no_endpoint.json")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	known := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			for _, id := range conformStored {
				if r.PathValue("id") == id {
					next(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(missing)
		}
	}
	s := jiratest.NewServer(attachmentRoutes(append([]jiratest.ServerOption{
		jiratest.WithHandler(http.MethodGet, attachmentContentRoute, known(attachmentServes(attachmentServed))),
		jiratest.WithHandler(http.MethodDelete, attachmentDeleteRoute, known(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})),
	}, opts...)...)...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))
	return c
}

func attachFake(t *testing.T, opts ...jiratest.Option) jira.Attacher {
	t.Helper()
	return conformFake(t, append([]jiratest.Option{
		jiratest.WithIssues(jiratest.GenFor(conformProject, 2)),
	}, opts...)...)
}

// conformUpload puts the fixture's two files on the issue in whichever adapter,
// and returns the attachments it says it stored.
func conformUpload(t *testing.T, a jira.Attacher) []jira.Attachment {
	t.Helper()

	files := conformFiles()
	stored, err := a.Upload(t.Context(), conformProject+"-1", files)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(stored) != len(files) {
		t.Fatalf("Upload stored %d attachments for %d files: %+v", len(stored), len(files), stored)
	}
	return stored
}

func TestAttachments_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []attachCase{
		{
			name:  "an upload names what was stored, by an id the rest of the API takes",
			cloud: func(t *testing.T) jira.Attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) jira.Attacher { return attachFake(t) },
			check: func(t *testing.T, a jira.Attacher) {
				stored := conformUpload(t, a)
				for i, file := range conformFiles() {
					if stored[i].ID == "" {
						t.Fatalf("attachment %d was stored with no id, so nothing can be downloaded or deleted", i)
					}
					if stored[i].Filename != file.Name {
						t.Errorf("filename = %q, want the name it was uploaded under", stored[i].Filename)
					}
					if stored[i].Size != file.Size {
						t.Errorf("size = %d, want the %d bytes uploaded", stored[i].Size, file.Size)
					}
				}
				var into bytes.Buffer
				if err := a.Download(t.Context(), stored[0].ID, &into, jira.DownloadOptions{}); err != nil {
					t.Fatalf("downloading the id the upload handed back: %v", err)
				}
				if into.Len() == 0 {
					t.Error("the download of a stored attachment wrote nothing")
				}
				if err := a.DeleteAttachment(t.Context(), stored[0].ID); err != nil {
					t.Errorf("deleting the id the upload handed back: %v", err)
				}
			},
		},
		{
			name: "a site with attachments switched off refuses an upload by name",
			cloud: func(t *testing.T) jira.Attacher {
				return attachFromSite(t, jiratest.WithHandler(http.MethodGet, attachmentMetaRoute,
					jsonHandler(http.StatusOK, attachmentMetaOffBody)))
			},
			fake: func(t *testing.T) jira.Attacher {
				return attachFake(t, jiratest.WithCapabilities(jiratest.NoAttachments))
			},
			check: func(t *testing.T, a jira.Attacher) {
				_, err := a.Upload(t.Context(), conformProject+"-1", conformFiles())
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("Upload gave %T (%v), want a *jira.CapabilityError", err, err)
				}
				if refused.Capability != jira.CapAttachments {
					t.Errorf("Upload named the capability %q, want %q", refused.Capability, jira.CapAttachments)
				}
				if strings.TrimSpace(refused.Reason) == "" {
					t.Error("Upload refused without a reason to show anybody")
				}
			},
		},
		{
			name:  "an attachment nobody has is absent rather than empty",
			cloud: func(t *testing.T) jira.Attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) jira.Attacher { return attachFake(t) },
			check: func(t *testing.T, a jira.Attacher) {
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
			cloud: func(t *testing.T) jira.Attacher {
				return attachFromSite(t, jiratest.WithStatus(http.MethodGet, issueRoute,
					http.StatusNotFound, "problem_no_endpoint.json"))
			},
			fake: func(t *testing.T) jira.Attacher { return attachFake(t) },
			check: func(t *testing.T, a jira.Attacher) {
				_, err := a.Attachments(t.Context(), conformMissingIssue)
				assertMissing(t, "Attachments", "issue", conformMissingIssue, err)
			},
		},
		{
			name:  "a resume is the tail, at the end it is finished, and past the end it is refused",
			cloud: func(t *testing.T) jira.Attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) jira.Attacher { return attachFake(t) },
			check: func(t *testing.T, a jira.Attacher) {
				stored := conformUpload(t, a)
				var whole bytes.Buffer
				if err := a.Download(t.Context(), stored[0].ID, &whole, jira.DownloadOptions{}); err != nil {
					t.Fatalf("Download: %v", err)
				}
				size := int64(whole.Len())
				if size < 4 {
					t.Fatalf("the attachment is %d bytes, too small to resume into", size)
				}

				var tail bytes.Buffer
				if err := a.Download(t.Context(), stored[0].ID, &tail, jira.DownloadOptions{From: size / 2}); err != nil {
					t.Fatalf("resumed Download: %v", err)
				}
				if got, want := tail.String(), whole.String()[size/2:]; got != want {
					t.Errorf("the resume wrote %d bytes, want the %d after the offset", len(got), len(want))
				}

				// The caller here is a resume of a temp file that already holds every
				// byte, because the copy finished and the rename did not.
				var nothing bytes.Buffer
				if err := a.Download(t.Context(), stored[0].ID, &nothing, jira.DownloadOptions{From: size}); err != nil {
					t.Errorf("resuming at the end is a finished download, not a failure: %v", err)
				}
				if nothing.Len() != 0 {
					t.Errorf("resuming at the end wrote %d bytes", nothing.Len())
				}

				err := a.Download(t.Context(), stored[0].ID, io.Discard, jira.DownloadOptions{From: size + 1})
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
			cloud: func(t *testing.T) jira.Attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) jira.Attacher { return attachFake(t) },
			check: func(t *testing.T, a jira.Attacher) {
				stored := conformUpload(t, a)
				var into bytes.Buffer
				var progress []int64
				err := a.Download(t.Context(), stored[0].ID, &into, jira.DownloadOptions{
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
				if err := a.Download(t.Context(), stored[0].ID, io.Discard, jira.DownloadOptions{}); err != nil {
					t.Errorf("a download with no Progress: %v", err)
				}
			},
		},
		{
			name:  "an argument neither adapter can use is refused rather than acted on",
			cloud: func(t *testing.T) jira.Attacher { return attachFromSite(t) },
			fake:  func(t *testing.T) jira.Attacher { return attachFake(t) },
			check: func(t *testing.T, a jira.Attacher) {
				stored := conformUpload(t, a)

				_, err := a.Upload(t.Context(), conformProject+"-1", nil)
				assertRefusesTheArgument(t, "Upload with no files", err)
				assertRefusesTheArgument(t, "DeleteAttachment with a blank id",
					a.DeleteAttachment(t.Context(), "   "))
				assertRefusesTheArgument(t, "Download with nowhere to write",
					downloadIntoNothing(t, a, stored[0].ID))
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

// downloadIntoNothing asks for a download with no writer, turning a panic into
// the failure it is: an adapter that dereferences the argument takes down
// whatever holds it, which is not the same answer as refusing the call.
func downloadIntoNothing(t *testing.T, a jira.Attacher, id string) (err error) {
	t.Helper()

	defer func() {
		if raised := recover(); raised != nil {
			err = fmt.Errorf("it panicked instead of refusing: %v", raised)
		}
	}()
	return a.Download(t.Context(), id, nil, jira.DownloadOptions{})
}

func assertRefusesTheArgument(t *testing.T, what string, err error) {
	t.Helper()

	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Errorf("%s gave %T (%v), want a *jira.ValidationError and no request made", what, err, err)
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
