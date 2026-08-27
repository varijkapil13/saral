package attach

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

// halfWriter is an attachment reader that writes some of the file and then gives
// up, which is what a cancelled or refused download looks like from this side.
type halfWriter struct {
	wrote string
	err   error
}

func (h halfWriter) Attachments(context.Context, string) ([]jira.Attachment, error) {
	return nil, errors.New("not used")
}

func (h halfWriter) Download(_ context.Context, _ string, w io.Writer, _ jira.DownloadOptions) error {
	if _, err := io.WriteString(w, h.wrote); err != nil {
		return err
	}
	return h.err
}

// The port hands this side a writer, so the temporary-file-then-rename is this
// side's to do. A download that stops half way must leave nothing at all: a file
// of the right name and half the length is worse than no file, because the next
// look would trust it.
func TestSave_ADownloadThatStopsHalfWayLeavesNothing(t *testing.T) {
	t.Parallel()

	tools, _ := testTools(t)
	att := jira.Attachment{ID: "att-1", Filename: "shot.png", Size: 100}
	reader := halfWriter{wrote: "half of it", err: errors.New("the connection went away")}

	if _, err := tools.save(t.Context(), reader, "example.atlassian.net", att, nil); err == nil {
		t.Fatal("save reported success on a download that failed")
	}
	if got := left(t, tools.dir); len(got) != 0 {
		t.Errorf("%v was left behind", got)
	}
	if _, ok := tools.cached("example.atlassian.net", att); ok {
		t.Error("a failed download left something the next look would use")
	}
}

func TestSave_RenamesTheWholeFileIntoPlaceAndReportsWhereItIs(t *testing.T) {
	t.Parallel()

	tools, _ := testTools(t)
	body := "the whole of it"
	att := jira.Attachment{ID: "att-1", Filename: "shot.png", Size: int64(len(body))}

	path, err := tools.save(t.Context(), halfWriter{wrote: body}, "example.atlassian.net", att, nil)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // the path came back from save
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("the file holds %q, want %q", got, body)
	}
	if only := left(t, tools.dir); len(only) != 1 {
		t.Errorf("the download directory holds %v, want just the file", only)
	}
	if cached, ok := tools.cached("example.atlassian.net", att); !ok || cached != path {
		t.Errorf("the file that was just saved is not the cached one: %q, %v", cached, ok)
	}
}

// A file of the wrong length is a leftover from some other run, not this
// attachment: an attachment is never rewritten in place, so the length is enough
// to tell them apart.
func TestCached_RefusesAFileOfTheWrongLength(t *testing.T) {
	t.Parallel()

	tools, _ := testTools(t)
	att := jira.Attachment{ID: "att-1", Filename: "shot.png", Size: 4}
	path := tools.path("example.atlassian.net", att)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("much longer than four"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := tools.cached("example.atlassian.net", att); ok {
		t.Error("a file of the wrong length was taken for this attachment")
	}
	att.Size = int64(len("much longer than four"))
	if _, ok := tools.cached("example.atlassian.net", att); !ok {
		t.Error("a file of exactly the right length was not taken for this attachment")
	}
}

func TestSave_WithNowhereToWriteSaysSoRatherThanFailingSilently(t *testing.T) {
	t.Parallel()

	var nowhere tools
	att := jira.Attachment{ID: "att-1", Filename: "shot.png", Size: 4}

	_, err := nowhere.save(t.Context(), halfWriter{wrote: "byte"}, "site", att, nil)
	if err == nil {
		t.Fatal("save reported success with nowhere to put the file")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("the refusal says %q, which does not say what is wrong", err)
	}
}

// A filename is whatever the uploader's machine allowed, so it is a name for one
// path segment and never a path.
func TestSegment_CannotReachOutsideTheDownloadDirectory(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"../../etc/passwd", "..", "...", "/etc/passwd", `..\..\windows`,
		".hidden", "", "a/b/c", "shot.png", "café.png",
	} {
		got := segment(in)
		switch {
		case got == "":
			t.Errorf("segment(%q) is empty, which is not a path segment", in)
		case strings.ContainsAny(got, `/\`):
			t.Errorf("segment(%q) = %q, which is more than one segment", in, got)
		case strings.Contains(got, ".."):
			t.Errorf("segment(%q) = %q, which walks upwards", in, got)
		case strings.HasPrefix(got, "."):
			t.Errorf("segment(%q) = %q, which the download directory would hide", in, got)
		}
	}
	if got := segment("shot.png"); got != "shot.png" {
		t.Errorf("segment kept %q; the extension is what the desktop handler picks a program by", got)
	}
}

func TestReadAtMost_RefusesAFileLongerThanTheLimit(t *testing.T) {
	t.Parallel()

	path := wrote(t, "big", []byte("0123456789"))
	if _, err := readAtMost(path, 4); err == nil {
		t.Error("a file over the limit was read anyway")
	}
	got, err := readAtMost(path, 10)
	if err != nil || string(got) != "0123456789" {
		t.Errorf("a file exactly at the limit came back as %q, %v", got, err)
	}
}

func TestHumanSize(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{2048, "2.0 KB"},
		{20 * 1024, "20 KB"},
		{1024 * 1024, "1.0 MB"},
		{8 << 20, "8.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	} {
		if got := humanSize(tc.in); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The download directory follows the cache directory, so a session with nowhere
// to write gets a pane that says so rather than one that writes to the working
// directory.
func TestDownloadDir_SitsUnderTheCacheDirectory(t *testing.T) {
	t.Setenv("SARAL_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	got := downloadDir()
	if filepath.Base(got) != downloadDirName {
		t.Errorf("downloadDir() = %q, want it under a %q directory", got, downloadDirName)
	}
	if !strings.Contains(got, "cache") {
		t.Errorf("downloadDir() = %q, which is not under the cache directory", got)
	}
}
