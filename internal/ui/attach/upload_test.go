package attach

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func writeFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// sendNow presses the send key and hands back the upload's commands unrun, which
// is what lets a test hold an upload in flight without a sleep.
func sendNow(t *testing.T, dr *driver) []tea.Cmd {
	t.Helper()
	view, cmd := dr.m.Update(keyPress("ctrl+s"))
	dr.m = view.(*Model) //nolint:forcetypeassert // Update always returns the pane itself
	if cmd == nil {
		t.Fatal("sending the path started nothing")
	}
	cmds, ok := unwrapCmds(cmd())
	if !ok {
		t.Fatal("the upload did not come back as its two halves")
	}
	return cmds
}

// A path dragged in from a file manager arrives quoted or escaped, and the file
// it names is the one attached.
func TestPane_ADraggedPathIsReadTheWayTheShellWouldHaveReadIt(t *testing.T) {
	t.Parallel()

	for name, typed := range map[string]func(string) string{
		"escaped": func(p string) string { return strings.ReplaceAll(p, " ", `\ `) },
		"quoted":  func(p string) string { return "'" + p + "'" },
		"padded":  func(p string) string { return "  " + p + "  " },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if onWindows && name == "escaped" {
				t.Skip("a backslash is a separator on windows")
			}
			dr, _ := loadedPane(t)
			path := writeFile(t, t.TempDir(), "My Notes.txt", 12)
			dr.key("u")
			dr.typeText(typed(path))
			dr.key("ctrl+s")

			mustContain(t, dr.lastStatus().Text, "attached My Notes.txt")
			if dr.m.mode != browsing {
				t.Error("the pane is still waiting on an upload that has answered")
			}
		})
	}
}

func TestPane_TabCompletesThePathBeingTyped(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	root := tree(t, "screens/", "screens/only.png", "report-a.pdf", "report-b.pdf")

	dr.key("u")
	dr.typeText(root + "/scr")
	dr.key("tab")
	if got := dr.m.input.Value(); got != root+"/screens/" {
		t.Fatalf("tab on a directory gave %q", got)
	}
	dr.key("tab")
	if got := dr.m.input.Value(); got != root+"/screens/only.png" {
		t.Fatalf("a second tab gave %q", got)
	}
	if dr.m.mode != typing {
		t.Error("tab left the prompt")
	}

	dr.m.input.SetValue(root + "/report-")
	before := len(dr.statuses)
	dr.key("tab")
	if got := dr.m.input.Value(); got != root+"/report-" {
		t.Errorf("an ambiguous tab changed the path to %q", got)
	}
	if len(dr.statuses) == before {
		t.Fatal("an ambiguous tab said nothing about what it could have been")
	}
	mustContain(t, dr.lastStatus().Text, "2 match", "report-a.pdf", "report-b.pdf")

	dr.m.input.SetValue(root + "/nothing")
	before = len(dr.statuses)
	dr.key("tab")
	if got := dr.m.input.Value(); got != root+"/nothing" || len(dr.statuses) != before {
		t.Errorf("a tab with no match gave %q and %d statuses", got, len(dr.statuses)-before)
	}
}

func TestMatchList_CountsWhatItDoesNotName(t *testing.T) {
	t.Parallel()

	got := matchList([]string{"a", "b", "c", "d", "e", "f", "g", "h"})
	if got != "8 match: a, b, c, d, e, f and 2 more" {
		t.Errorf("matchList = %q", got)
	}
}

func TestPane_AnUploadInFlightGolden(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	dr.send(kernel.SizeMsg{Width: 120, Height: 24})
	path := writeFile(t, t.TempDir(), "notes.txt", 4096)
	dr.key("u")
	dr.typeText(path)
	_ = sendNow(t, dr)
	if !dr.m.WantsRawKeys() {
		t.Error("an upload does not claim the keys, so esc would close the pane instead of stopping it")
	}

	dr.send(sentMsg{gen: dr.m.gen, sent: 1024, steps: closedSteps()})
	golden(t, "uploading_120x24.golden", dr.view())
	mustContain(t, dr.view(), "Attaching notes.txt", "25%", "1.0 KB of 4.0 KB", stopHint)
}

func TestPane_AnUploadOfAnEmptyFileSaysNoPercentage(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	path := writeFile(t, t.TempDir(), "empty.txt", 0)
	dr.key("u")
	dr.typeText(path)
	_ = sendNow(t, dr)

	mustContain(t, dr.view(), "Attaching empty.txt")
	mustNotContain(t, dr.view(), "%")
}

func TestPane_ProgressFromAnUploadAlreadyStoppedIsDropped(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	path := writeFile(t, t.TempDir(), "notes.txt", 4096)
	dr.key("u")
	dr.typeText(path)
	_ = sendNow(t, dr)
	stale := dr.m.gen
	dr.key("esc")

	dr.send(sentMsg{gen: stale, sent: 2048, steps: closedSteps()})
	if dr.m.sent != 0 {
		t.Errorf("a stopped upload's progress moved the count to %d", dr.m.sent)
	}
}

// The fake waits out its delay on the upload's own context, so the upload is
// held in flight until esc cancels it, and only the cancellation can end it.
func TestPane_EscStopsAnUploadInFlightAndSaysSoRatherThanFailing(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	path := writeFile(t, t.TempDir(), "notes.txt", 4096)
	dr.key("u")
	dr.typeText(path)
	f.Delay(time.Hour)
	cmds := sendNow(t, dr)

	answers := make(chan tea.Msg, len(cmds))
	for _, cmd := range cmds {
		go func() { answers <- answer(cmd) }()
	}
	dr.key("esc")

	for range cmds {
		if msg := <-answers; msg != nil {
			dr.send(msg)
		}
	}
	mustContain(t, dr.lastStatus().Text, "upload cancelled")
	if got := dr.lastStatus().Level; got == kernel.LevelError {
		t.Error("a stop the reader asked for was reported as an error")
	}
	if dr.m.mode != browsing || dr.m.failure != nil {
		t.Errorf("after the stop the pane is in mode %d with failure %v", dr.m.mode, dr.m.failure)
	}
	mustNotContain(t, dr.view(), "Attaching", "The site would not say.")
	for _, name := range dr.names() {
		if name == "notes.txt" {
			t.Error("a cancelled upload was put on the list")
		}
	}
}

// Anything that asks the site for something cancels whatever is in flight, so
// the palette's actions wait until the upload has answered.
func TestPane_ThePaletteCannotCancelAnUploadByAskingForSomethingElse(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	path := writeFile(t, t.TempDir(), "notes.txt", 4096)
	dr.key("u")
	dr.typeText(path)
	_ = sendNow(t, dr)
	gen := dr.m.gen

	for _, msg := range []tea.Msg{ShowMsg{}, OpenOutsideMsg{}, UploadMsg{}, DeleteMsg{}, kernel.RefreshMsg{}} {
		dr.send(msg)
		if dr.m.gen != gen || dr.m.mode != uploading {
			t.Fatalf("%T cancelled the upload", msg)
		}
		mustContain(t, dr.lastStatus().Text, "notes.txt is still on its way up", stopHint)
	}
	dr.key("j", "d", "u")
	if dr.m.gen != gen || dr.m.mode != uploading {
		t.Error("a key other than esc cancelled the upload")
	}
}

func TestPane_AnUploadTheSiteRefusesIsReportedInItsOwnWords(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		says string
	}{
		"a token without the permission": {
			err:  &jira.CapabilityError{Capability: jira.CapAttachments, Reason: "you may not attach files here"},
			says: "you may not attach files here",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			says: "30s",
		},
		"a transport failure": {
			err: &jira.TransportError{
				Op:  "upload",
				Err: &net.OpError{Op: "write", Net: "tcp", Err: errors.New("connection reset by peer")},
			},
			says: "connection reset by peer",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dr, f := loadedPane(t)
			path := writeFile(t, t.TempDir(), "notes.txt", 64)
			dr.key("u")
			dr.typeText(path)
			f.FailNext(tc.err)
			dr.key("ctrl+s")

			if got := dr.lastStatus(); got.Level != kernel.LevelError || !strings.Contains(got.Text, tc.says) {
				t.Errorf("the refusal came out as %+v, want an error saying %q", got, tc.says)
			}
			if dr.m.mode != browsing {
				t.Error("a refused upload left the pane waiting on it")
			}
			mustContain(t, dr.view(), tc.says)
			mustNotContain(t, dr.view(), "Attaching")
		})
	}
}

// progressDouble reports every byte of the file one at a time, which is what a
// large file does to a callback that forwards every write.
type progressDouble struct {
	jira.Attacher
	posted int
	steps  chan int64
}

func (p *progressDouble) Upload(_ context.Context, _ string, files []jira.FileRef) ([]jira.Attachment, error) {
	for sent := int64(1); sent <= files[0].Size; sent++ {
		files[0].Progress(sent)
		select {
		case <-p.steps:
			p.posted++
		default:
		}
	}
	return []jira.Attachment{{ID: "att-1", Filename: files[0].Name, Size: files[0].Size}}, nil
}

func TestUpload_PostsProgressOnlyWhenThePercentageMoves(t *testing.T) {
	t.Parallel()

	steps := make(chan int64, 1)
	double := &progressDouble{steps: steps}
	file := jira.FileRef{
		Name: "big.bin", Size: 1000,
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil },
	}
	msg := upload(t.Context(), double, "PROJ-1", file, 1, steps)()

	if _, ok := msg.(uploadedMsg); !ok {
		t.Fatalf("the upload answered %T", msg)
	}
	if double.posted != 101 {
		t.Errorf("a thousand writes posted %d steps, want one per percentage (101)", double.posted)
	}
	if _, open := <-steps; open {
		t.Error("the progress channel was left open, so its waiter never ends")
	}
}
