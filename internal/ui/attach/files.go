package attach

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// downloadDirName is where the bytes of a looked-at attachment live under the
// cache directory.
const downloadDirName = "attachments"

// tools is the machine outside the terminal: where a downloaded file goes, and
// the half-block renderer if this machine has one.
//
// It is a value on the model rather than a package-level lookup so that the pane
// can be driven over a directory of the test's own and a renderer that never
// starts a process.
type tools struct {
	dir   string
	chafa string
	run   func(ctx context.Context, name string, args ...string) ([]byte, error)
	// open is the hand-off to the desktop. It is a field rather than a call so
	// that a test can prove what was handed over without a program appearing on
	// whoever is running it.
	open func(path string) tea.Cmd
}

// chafaPath finds the half-block renderer once per process. Which of the four
// ways to draw an image is available is a fact about the machine and the
// terminal, not about the frame, so it is answered at start-up and kept.
var chafaPath = sync.OnceValue(func() string {
	path, err := exec.LookPath("chafa")
	if err != nil {
		return ""
	}
	return path
})

func newTools() tools {
	return tools{dir: downloadDir(), chafa: chafaPath(), run: runCommand, open: kernel.OpenURL}
}

func downloadDir() string {
	dir, err := config.CacheDir()
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, downloadDirName)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// path is where one attachment's bytes go. The id leads so that two files of the
// same name on one issue are two files here, and the filename follows so that
// the desktop handler and the renderer both see the extension they open by.
func (t tools) path(site string, att jira.Attachment) string {
	if t.dir == "" {
		return ""
	}
	return filepath.Join(t.dir, segment(site), segment(att.ID)+"-"+segment(att.Filename))
}

// cached is the file already on disk for this attachment. The size has to match:
// an attachment is never rewritten in place, so a file of the right length is
// this one and a file of any other length is a leftover.
func (t tools) cached(site string, att jira.Attachment) (string, bool) {
	path := t.path(site, att)
	if path == "" {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() != att.Size {
		return "", false
	}
	return path, true
}

// save streams an attachment to a file of this program's own.
//
// The bytes go to a temporary file beside the target and are renamed over it, so
// a download that is cancelled or refused half way through leaves nothing rather
// than a file of the right name and the wrong length. The partial is thrown away
// rather than kept to resume from: nothing here can prove a file left behind is a
// prefix of this attachment and of nothing else.
func (t tools) save(ctx context.Context, reader jira.AttachmentReader, site string,
	att jira.Attachment, progress func(int64),
) (string, error) {
	final := t.path(site, att)
	if final == "" {
		return "", errors.New("there is nowhere on this machine to put a downloaded file")
	}
	dir := filepath.Dir(final)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := reader.Download(ctx, att.ID, tmp, jira.DownloadOptions{Progress: progress}); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(name, final); err != nil {
		return "", err
	}
	return final, nil
}

// readAtMost is the head of a file, and a refusal for one longer than limit. Both
// graphics protocols carry the bytes inside the escape sequence, so the whole
// file is what a terminal is handed and half of one is a broken image.
func readAtMost(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // the path is built from segment() under the cache directory
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(got)) > limit {
		return nil, errors.New("the file is larger than " + humanSize(limit))
	}
	return got, nil
}

// segment keeps a path segment to characters that mean the same thing on every
// filesystem, so a filename or a site name can never reach outside the download
// directory. The dot survives, because the extension is what the desktop handler
// picks a program by.
func segment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", "_.")
	}
	if out == "" {
		return "unnamed"
	}
	if strings.HasPrefix(out, ".") {
		return "_" + out
	}
	return out
}

// humanSize is a byte count somebody can read. Zero is a size and not an
// absence: an empty file is a legitimate attachment.
func humanSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	value := float64(n)
	for _, unit := range []string{"KB", "MB", "GB", "TB"} {
		value /= 1024
		if value < 1024 {
			return strconv.FormatFloat(value, 'f', sizePlaces(value), 64) + " " + unit
		}
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " PB"
}

func sizePlaces(v float64) int {
	if v < 10 {
		return 1
	}
	return 0
}
