package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// repeatingReader yields a repeating byte pattern forever, so a test of a large
// upload costs no more memory than the pattern itself: what is under test is
// that the client streams the body rather than building it, and the source
// bytes must not be the thing that defeats that.
type repeatingReader struct{ pattern []byte }

func (r repeatingReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		n += copy(p[n:], r.pattern)
	}
	return n, nil
}

func bigFile(name string, size int64) jira.FileRef {
	return jira.FileRef{
		Name: name,
		Size: size,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(io.LimitReader(repeatingReader{pattern: []byte("saral-upload-stream ")}, size)), nil
		},
	}
}

// streamingUploadServer is a bare httptest server for the one case the shared
// jiratest.Server cannot cover: it records every request body into memory,
// which is exactly what a streamed upload must not need to do.
func streamingUploadServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	fixture, err := jiratest.Fixture("attachment_upload.json")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	var received atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/3/attachment/meta", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true,"uploadLimit":0}`))
	})
	mux.HandleFunc("POST /rest/api/3/issue/{key}/attachments", func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received.Store(n)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fixture)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s, &received
}

// Not parallel: pushing 65 MiB under -race starves the wall-clock idle-read tests beside it.
func TestUpload_StreamsAFileLargerThanTheOldSixtyFourMebibyteInMemoryCeiling(t *testing.T) {
	const size = 65 << 20 // one MiB past the ceiling a buffered body used to impose
	s, received := streamingUploadServer(t)
	c, _ := testClient(t, s.URL, WithRetry(RetryPolicy{Attempts: 1}))

	start := time.Now()
	got, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{bigFile("giant.bin", size)})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("the fixture's two stored attachments read as %+v", got)
	}
	if n := received.Load(); n < size {
		t.Errorf("the site received %d bytes, want at least the %d byte file", n, size)
	}
	if elapsed > wedgeBound {
		t.Errorf("streaming a 65 MiB upload took %s, which is long enough to suspect it was buffered rather than streamed", elapsed)
	}
	t.Logf("streamed %d bytes in %s", received.Load(), elapsed)
}

func TestUpload_SetsContentLengthExactlyForSizedFilesAndLeavesItUnknownForAnUnsizedOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		files       []jira.FileRef
		wantChunked bool
	}{
		{name: "one sized file", files: []jira.FileRef{testFile("notes.txt", "hello saral")}},
		{name: "two sized files", files: []jira.FileRef{testFile("a.txt", "aaa"), testFile("b.txt", "bb")}},
		{name: "one file with no declared size", files: []jira.FileRef{unsizedFile("notes.txt", "hello saral", nil)}, wantChunked: true},
		{name: "a sized file alongside an unsized one", files: []jira.FileRef{
			testFile("a.txt", "aaa"), unsizedFile("b.txt", "bb", nil),
		}, wantChunked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var length int64
			var chunked bool
			c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodPost, attachmentUploadRoute,
				func(w http.ResponseWriter, r *http.Request) {
					length = r.ContentLength
					chunked = slices.Contains(r.TransferEncoding, "chunked")
					jsonHandler(http.StatusOK, `[]`)(w, r)
				}))
			if _, err := c.Upload(t.Context(), testIssueKey, tt.files); err != nil {
				t.Fatalf("Upload: %v", err)
			}
			if tt.wantChunked {
				if length >= 0 {
					t.Errorf("Content-Length = %d, want it left unknown so the body is sent chunked", length)
				}
				if !chunked {
					t.Error("an upload with an unsized file was not sent chunked")
				}
				return
			}
			if length <= 0 {
				t.Errorf("Content-Length = %d, want the exact size every file declared", length)
			}
			if chunked {
				t.Error("an upload whose files all declare a size was sent chunked anyway")
			}
		})
	}
}

func TestUpload_ReportsProgressCumulativelyPerFile(t *testing.T) {
	t.Parallel()

	t.Run("one file", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t)
		var progress []int64
		file := jira.FileRef{
			Name: "trace.log", Size: int64(len(attachmentLong)),
			Open:     func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(string(attachmentLong))), nil },
			Progress: func(sent int64) { progress = append(progress, sent) },
		}
		if _, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{file}); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if len(progress) < 2 {
			t.Fatalf("progress was reported %d times for %d bytes, want it as the file goes out", len(progress), len(attachmentLong))
		}
		for i := 1; i < len(progress); i++ {
			if progress[i] <= progress[i-1] {
				t.Fatalf("progress must be cumulative and increasing, got %v", progress)
			}
		}
		if last := progress[len(progress)-1]; last != int64(len(attachmentLong)) {
			t.Errorf("the last progress call reported %d, want the whole %d", last, len(attachmentLong))
		}
	})

	t.Run("two files report separately, each ending at its own size", func(t *testing.T) {
		t.Parallel()

		c, _ := attachmentClient(t)
		var first, second []int64
		files := []jira.FileRef{
			{
				Name: "a.log", Size: int64(len(attachmentLong)),
				Open:     func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(string(attachmentLong))), nil },
				Progress: func(sent int64) { first = append(first, sent) },
			},
			testFile("b.txt", "a short second file"),
		}
		files[1].Progress = func(sent int64) { second = append(second, sent) }

		if _, err := c.Upload(t.Context(), testIssueKey, files); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if len(first) == 0 || first[len(first)-1] != files[0].Size {
			t.Errorf("first file's progress ended at %v, want it to finish at %d", first, files[0].Size)
		}
		if len(second) == 0 || second[len(second)-1] != files[1].Size {
			t.Errorf("second file's progress ended at %v, want it to finish at %d", second, files[1].Size)
		}
	})
}

func TestUpload_RefusesASizedFileWhoseReadBetraysItsDeclaredSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file jira.FileRef
	}{
		{
			name: "reads shorter than declared",
			file: jira.FileRef{Name: "short.txt", Size: 20, Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("too short")), nil
			}},
		},
		{
			name: "reads longer than declared",
			file: jira.FileRef{Name: "long.txt", Size: 5, Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("this is much longer than five bytes")), nil
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := attachmentClient(t)
			_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{tt.file})
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
			}
			if !strings.Contains(invalid.Error(), "changed while it was being sent") {
				t.Errorf("the refusal does not say the file changed mid-upload: %q", invalid.Error())
			}
		})
	}
}

func TestUpload_CancellingMidBodyReturnsTheContextsOwnError(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(attachmentRoutes()...)
	defer closeServer(t, s)
	c, _ := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 1}))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	body := string(attachmentLong)
	first := true
	file := jira.FileRef{
		Name: "trace.log", Size: int64(len(body)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil },
		Progress: func(int64) {
			if first {
				first = false
				cancel()
			}
		},
	}

	_, err := c.Upload(ctx, testIssueKey, []jira.FileRef{file})
	assertTheCallersOwnAnswer(t, err)
}

func TestUpload_RetriesA429WithEveryFileReopenedAndProgressRestarted(t *testing.T) {
	t.Parallel()

	var asked atomic.Int64
	var opens atomic.Int64
	s := jiratest.NewServer(attachmentRoutes(jiratest.WithHandler(http.MethodPost, attachmentUploadRoute,
		func(w http.ResponseWriter, r *http.Request) {
			if asked.Add(1) == 1 {
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"errorMessages":["Rate limit exceeded."],"errors":{}}`))
				return
			}
			jsonHandler(http.StatusOK, `[{"id":"10503","filename":"notes.txt"}]`)(w, r)
		}))...)
	defer closeServer(t, s)

	c, clock := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 2}))

	var progress []int64
	file := jira.FileRef{
		Name: "notes.txt", Size: 11,
		Open: func() (io.ReadCloser, error) {
			opens.Add(1)
			return io.NopCloser(strings.NewReader("hello saral")), nil
		},
		Progress: func(sent int64) { progress = append(progress, sent) },
	}
	got, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{file})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(got) != 1 || got[0].ID != "10503" {
		t.Errorf("got %+v, want the attachment the second attempt stored", got)
	}
	if n := opens.Load(); n != 2 {
		t.Errorf("the file was opened %d times, want 2: each attempt reads it afresh", n)
	}
	if waits := clock.waited(); len(waits) != 1 || waits[0] != 5*time.Second {
		t.Errorf("waited %v, want the 5s the site asked for", waits)
	}
	for _, p := range progress {
		if p > 11 {
			t.Fatalf("progress reported %d for an 11 byte file, want it bounded by the file's own size on every attempt, got %v", p, progress)
		}
	}
}

func TestUpload_RefusesAnUnsizedFileOverTheSiteCap(t *testing.T) {
	t.Parallel()

	c, _ := attachmentClient(t, jiratest.WithHandler(http.MethodGet, attachmentMetaRoute,
		jsonHandler(http.StatusOK, attachmentMetaTinyBody)))

	var progress []int64
	var reads atomic.Int64
	_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{{
		Name: "notes.txt",
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(&countingReader{from: strings.NewReader(strings.Repeat("x", 4096)), read: &reads}), nil
		},
		Progress: func(sent int64) { progress = append(progress, sent) },
	}})
	var invalid *jira.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %T (%v), want a *jira.ValidationError", err, err)
	}
	if len(progress) == 0 {
		t.Error("no progress was reported for a file read up to the cap before it was refused")
	}
	if last := progress[len(progress)-1]; last > 9 {
		t.Errorf("progress reported %d, want it bounded by the one byte over the 8 byte cap this client reads", last)
	}
}

func TestUpload_ReportsAFileThatCannotBeOpenedWithoutRetrying(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("disk gone")
	var opens atomic.Int64
	s := jiratest.NewServer(attachmentRoutes()...)
	defer closeServer(t, s)
	c, clock := testClient(t, s.URL(), WithRetry(RetryPolicy{Attempts: 3}))

	_, err := c.Upload(t.Context(), testIssueKey, []jira.FileRef{{
		Name: "broken.bin", Size: 10,
		Open: func() (io.ReadCloser, error) {
			opens.Add(1)
			return nil, sentinel
		},
	}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want it to carry the open failure", err)
	}
	if n := opens.Load(); n != 1 {
		t.Errorf("the file was opened %d times, want exactly once: an open failure is not retried", n)
	}
	if waits := clock.waited(); len(waits) != 0 {
		t.Errorf("waited %v; an open failure must not go through a backoff", waits)
	}
}
