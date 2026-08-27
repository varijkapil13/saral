package attach

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// signedURL stands in for the media URL a download redirects through: a
// credential with about ten minutes on it. It is written onto the rows this pane
// holds so that a test can prove no frame, no status line and no argument list
// ever carries one.
const signedURL = "https://media.example.invalid/file/9f2?token=SIGNED-CREDENTIAL-42"

func loadedPane(t *testing.T, files ...file) (*driver, *jiratest.Fake) {
	t.Helper()
	if len(files) == 0 {
		files = sampleFiles()
	}
	f := newFake(3)
	attached(t, f, "PROJ-1", files...)
	dr := newDriver(t, testDeps(f), 120, 30, WithIssue("PROJ-1"))
	return dr, f
}

func TestPane_ListsWhatIsAttachedToTheIssue(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)

	want := []string{"screenshot.png", "handover.pdf", "capture"}
	got := dr.names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the pane lists %v, want %v", got, want)
	}
	mustContain(t, dr.view(), "the files attached to PROJ-1", "screenshot.png", "3 files")
}

// A preview costs a round trip, so it is asked for rather than followed. Walking
// the list must not fan a download out per cursor movement.
func TestPane_MovingTheCursorAsksTheSiteForNothing(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.key("j", "j", "k", "end", "home")

	if got := countCalls(f, "Download"); got != 0 {
		t.Errorf("walking the list downloaded %d times, want none", got)
	}

	dr.onto("screenshot.png")
	dr.key("enter")
	if got := countCalls(f, "Download"); got != 1 {
		t.Errorf("asking for a preview downloaded %d times, want once", got)
	}
}

// The file is already on disk from the first look, and an attachment is never
// rewritten in place, so the second look costs nothing.
func TestPane_AFileAlreadyFetchedIsNotFetchedAgain(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.onto("screenshot.png")
	dr.key("enter")
	dr.key("z", "z")
	dr.key("enter")

	if got := countCalls(f, "Download"); got != 1 {
		t.Errorf("the same file was downloaded %d times, want once", got)
	}
}

func TestPane_AnImageTooLargeToDrawInlineSaysSoAndNamesTheWayRound(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.onto("screenshot.png")
	dr.m.files[0].Size = previewLimit + 1
	dr.key("enter")

	if got := countCalls(f, "Download"); got != 0 {
		t.Errorf("a file past the inline limit was downloaded %d times, want not at all", got)
	}
	status := dr.lastStatus()
	mustContain(t, status.Text, "too large to draw inline", defaultKeys().Open.Help().Key)
	if status.Level != kernel.LevelWarn {
		t.Errorf("the refusal came out at level %d, want a warning", status.Level)
	}
}

// Everything that is not an image goes to the desktop's own handler, and what is
// handed over is a file this program wrote — never the URL the port redirected
// through.
func TestPane_ANonImageIsHandedToTheDesktopAsALocalFile(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	dr.onto("handover.pdf")
	dr.key("enter")

	handed := dr.seen.handedOver()
	if len(handed) != 1 {
		t.Fatalf("the desktop was handed %d files, want one: %v", len(handed), handed)
	}
	if !strings.HasSuffix(handed[0], "handover.pdf") {
		t.Errorf("the desktop was handed %q, want a local file named after the attachment", handed[0])
	}
	if _, err := os.Stat(handed[0]); err != nil {
		t.Errorf("the file handed over is not there: %v", err)
	}
}

// The one rule this whole package exists under: the signed media URL is a
// credential. It may not reach a frame, a status line, another process, or the
// desktop handler.
func TestPane_TheSignedURLNeverReachesAFrameAStatusOrAnotherProcess(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	dr.m.tools.chafa = "chafa"
	for i := range dr.m.files {
		dr.m.files[i].ContentURL = signedURL
		dr.m.files[i].ThumbnailURL = signedURL
	}

	for _, name := range []string{"screenshot.png", "handover.pdf", "capture"} {
		dr.onto(name)
		dr.key("enter")
		mustNotContain(t, dr.view(), signedURL)
		dr.key("z")
		mustNotContain(t, dr.view(), signedURL)
		dr.key("z")
	}
	dr.key("d")
	mustNotContain(t, dr.view(), signedURL)
	dr.key("esc")

	for _, status := range dr.statuses {
		if strings.Contains(status.Text, signedURL) {
			t.Errorf("a status line carried the signed URL: %q", status.Text)
		}
	}
	for _, argv := range dr.seen.argv() {
		if strings.Contains(strings.Join(argv, " "), signedURL) {
			t.Errorf("a process was started with the signed URL: %v", argv)
		}
	}
	for _, path := range dr.seen.handedOver() {
		if strings.Contains(path, signedURL) {
			t.Errorf("the desktop handler was handed the signed URL: %q", path)
		}
	}
}

func TestPane_TheFailurePathsKeepTheSitesOwnWords(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		says string
	}{
		"a token without the permission": {
			err:  &jira.CapabilityError{Capability: jira.CapAttachments, Reason: "you need Browse Projects here"},
			says: "you need Browse Projects here",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			says: "30s",
		},
		"a transport failure": {
			err: &jira.TransportError{
				Op:  "list attachments",
				Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			},
			says: "connection refused",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(3)
			attached(t, f, "PROJ-1", sampleFiles()...)
			f.FailNext(tc.err)
			dr := newDriver(t, testDeps(f), 120, 30, WithIssue("PROJ-1"))

			mustContain(t, dr.view(), "The site would not say.", tc.says, retryHint)
			if got := dr.lastStatus(); got.Level != kernel.LevelError {
				t.Errorf("the refusal came out at level %d, want an error", got.Level)
			}
		})
	}
}

// The pane hands the port a writer, so writing to a temporary file and renaming
// it is this side's job. A refused download must leave nothing at all rather than
// a file of the right name and the wrong length.
func TestPane_ADownloadThatFailsLeavesNoFileBehind(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.onto("screenshot.png")
	f.FailNext(&jira.TransportError{Op: "download", Err: errors.New("the connection went away")})
	dr.key("enter")

	mustContain(t, dr.lastStatus().Text, "the connection went away")
	if got := left(t, dr.m.tools.dir); len(got) != 0 {
		t.Errorf("a failed download left %v behind", got)
	}
}

func TestPane_ACancelledDownloadLeavesNoFileBehind(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.onto("screenshot.png")
	f.Delay(2 * time.Second)

	att := dr.m.files[0]
	steps := make(chan int64, 1)
	ctx, gen := dr.m.begin()
	dr.m.asked = att.ID
	cmd := download(ctx, f, dr.m.tools, dr.m.deps.Site, att, previewIntent, gen, steps)
	dr.m.Close()

	msg, _ := cmd().(failedMsg)
	if msg.err == nil {
		t.Fatal("a cancelled download came back without an error")
	}
	if got := left(t, dr.m.tools.dir); len(got) != 0 {
		t.Errorf("a cancelled download left %v behind", got)
	}
}

// left is what is in the download directory, temporary files included.
func left(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a directory that was never made is nothing left behind
		}
		out = append(out, path)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return out
}

// Losing the keyboard is not being closed: the kernel blurs a view it is pushing
// over as well as one it is discarding, and nothing about losing the keys means
// nobody wants the answer.
func TestPane_KeepsItsReadOnABlurAndDropsItOnAClose(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	view, ok := New(testDeps(f), WithIssue("PROJ-1")).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	view.tools, _ = testTools(t)
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 30})
	view, _ = next.(*Model)
	cmd := view.Init()

	next, _ = view.Update(kernel.FocusMsg{Focused: false})
	view, _ = next.(*Model)
	if msg, isFailure := answer(cmd).(failedMsg); isFailure {
		t.Fatalf("a blur gave up the read: %v", msg.err)
	}

	view.Close()
	f.Delay(time.Second)
	cmd = view.load()
	view.Close()
	if _, isFailure := answer(cmd).(failedMsg); !isFailure {
		t.Error("a closed pane's read carried on rather than being cancelled")
	}
}

// answer is what the kernel hands a view: the command's own reply with the
// envelope the kernel addresses it by taken off.
func answer(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if reply, addressed := msg.(kernel.ReplyMsg); addressed {
		return reply.Msg
	}
	return msg
}

// An answer to a question the reader has moved on from is dropped rather than
// drawn: the generation it was asked under is what says so.
func TestPane_AnAnswerToAQuestionAlreadyChangedIsDropped(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	stale := dr.m.gen
	dr.send(kernel.RefreshMsg{})

	dr.send(listedMsg{gen: stale, files: []jira.Attachment{{ID: "att-ghost", Filename: "ghost.png"}}})
	mustNotContain(t, dr.view(), "ghost.png")

	dr.send(previewMsg{gen: stale, shown: preview{id: dr.m.files[0].ID, kind: previewText, why: "stale"}})
	if dr.m.shown.kind != previewNone {
		t.Error("a preview from an answer already moved on from was drawn")
	}
}

func TestPane_UploadAndDeleteAreHiddenWithTheReasonWhenTheSiteHasThemOff(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := testDeps(f)
	d.Caps.Attachments = jira.Capability{Reason: "attachments are switched off on this site"}
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))

	set, _ := dr.m.LiveKeys()
	if acts := actsOf(set); strings.Contains(acts, "attach") || strings.Contains(acts, "delete") {
		t.Errorf("the footer offers a write the site refuses: %s", acts)
	}

	dr.key("u")
	mustContain(t, dr.lastStatus().Text, "attachments are switched off on this site")
	if dr.m.mode != browsing {
		t.Error("u opened the prompt on a site that has attachments off")
	}

	dr.key("d")
	mustContain(t, dr.lastStatus().Text, "attachments are switched off on this site")
	if dr.m.mode != browsing {
		t.Error("d opened the confirmation on a site that has attachments off")
	}
}

// Nothing here re-reads the issue to find out what the upload did: the upload's
// own answer is the attachments as stored, and confirming a write by reading it
// back is how a screen reports a different failure from the one that happened.
func TestPane_UploadingPutsWhatTheSiteStoredOnTheListWithoutReadingItBack(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	before := countCalls(f, "Attachments")

	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("something to attach"), 0o600); err != nil {
		t.Fatal(err)
	}

	dr.key("u")
	if dr.m.mode != typing {
		t.Fatal("u did not open the prompt")
	}
	if !dr.m.WantsRawKeys() {
		t.Error("the prompt does not claim the keys, so a path loses every letter it shares with a binding")
	}
	dr.typeText(path)
	dr.key("ctrl+s")

	mustContain(t, dr.lastStatus().Text, "attached notes.txt")
	if got := dr.names(); got[len(got)-1] != "notes.txt" {
		t.Errorf("the list ends with %q, want the file just attached", got[len(got)-1])
	}
	if got := countCalls(f, "Attachments"); got != before {
		t.Errorf("the upload re-read the issue %d times", got-before)
	}
	if got := countCalls(f, "Upload"); got != 2 {
		t.Errorf("the pane uploaded %d times (one of them is the seeding), want two", got)
	}
}

func TestPane_APathThatIsNotThereIsRefusedBeforeAnythingIsSent(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	before := countCalls(f, "Upload")

	dr.key("u")
	dr.typeText(filepath.Join(t.TempDir(), "nothing-here.png"))
	dr.key("ctrl+s")

	if got := countCalls(f, "Upload"); got != before {
		t.Error("a path that is not there was still sent to the site")
	}
	mustContain(t, dr.lastStatus().Text, "nothing-here.png")
}

// Nothing destructive without a named confirmation, and the key that opens the
// confirmation is not the key that answers it.
func TestPane_DeletingTakesAConfirmationAndSaysWhatElseItBreaks(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.onto("handover.pdf")

	dr.key("d")
	if dr.m.mode != confirming {
		t.Fatal("d did not open the confirmation")
	}
	mustContain(t, dr.view(), "Delete handover.pdf?", deleteHint, keepHint)
	if got := countCalls(f, "DeleteAttachment"); got != 0 {
		t.Fatalf("d deleted the file %d times without an answer", got)
	}

	dr.key("d", "d", "j", "enter")
	if got := countCalls(f, "DeleteAttachment"); got != 0 {
		t.Fatalf("a key that is not the confirm deleted the file")
	}

	dr.key("y")
	if got := countCalls(f, "DeleteAttachment"); got != 1 {
		t.Fatalf("the confirm deleted %d times, want once", got)
	}
	if got := strings.Join(dr.names(), ","); got != "screenshot.png,capture" {
		t.Errorf("the list is %q after the deletion", got)
	}
	mustContain(t, dr.lastStatus().Text, "deleted handover.pdf", "will be refused")
}

func TestPane_EscapingTheConfirmationLeavesTheFileWhereItIs(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.onto("handover.pdf")
	dr.key("d", "esc")

	if dr.m.mode != browsing {
		t.Error("esc did not close the confirmation")
	}
	if got := countCalls(f, "DeleteAttachment"); got != 0 {
		t.Error("esc deleted the file")
	}
	mustContain(t, dr.lastStatus().Text, "where it was")
	if len(dr.m.files) != 3 {
		t.Errorf("the list lost a row: %v", dr.names())
	}
}

// A deletion the site refuses keeps the row: a partial failure that is swallowed
// leaves a screen claiming something that did not happen.
func TestPane_ADeletionTheSiteRefusesKeepsTheRowAndSaysWhy(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.onto("handover.pdf")
	f.FailNext(&jira.CapabilityError{
		Capability: jira.CapAttachments,
		Reason:     "you may only delete your own attachments here",
	})
	dr.key("d", "y")

	if got := strings.Join(dr.names(), ","); got != "screenshot.png,handover.pdf,capture" {
		t.Errorf("a refused deletion changed the list to %q", got)
	}
	mustContain(t, dr.lastStatus().Text, "you may only delete your own attachments here")
	mustContain(t, dr.view(), "you may only delete your own attachments here")
}

func TestPane_RawKeysAreClaimedOnlyWhileTypingOrConfirming(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	if dr.m.WantsRawKeys() {
		t.Error("the pane swallows q, r and esc while it is only listing files")
	}
	dr.key("u")
	if !dr.m.WantsRawKeys() {
		t.Error("the path prompt does not claim the keys")
	}
	dr.key("esc")
	dr.key("d")
	if !dr.m.WantsRawKeys() {
		t.Error("the confirmation does not claim the keys, so esc pops the pane instead of keeping the file")
	}
}

func TestPane_RefreshReadsTheListAgain(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	before := countCalls(f, "Attachments")
	attached(t, f, "PROJ-1", file{name: "late.png", body: "l"})

	dr.send(kernel.RefreshMsg{})

	if got := countCalls(f, "Attachments"); got != before+1 {
		t.Errorf("a refresh read the list %d times, want once more", got-before)
	}
	mustContain(t, dr.view(), "late.png")
}

// The port reports a download in bytes, which docs/UX.md asks to be shown as a
// real number rather than as a spinner.
func TestPane_ADownloadInFlightSaysHowManyBytesHaveArrived(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	dr.onto("screenshot.png")
	att := dr.m.files[0]
	_, gen := dr.m.begin()
	dr.m.asked, dr.m.total, dr.m.written = att.ID, att.Size, att.Size/2

	dr.send(progressMsg{gen: gen, id: att.ID, written: att.Size / 2, steps: closedSteps()})
	mustContain(t, dr.view(), "Fetching screenshot.png", "1.0 KB of 2.0 KB")
}

func closedSteps() chan int64 {
	steps := make(chan int64, 1)
	close(steps)
	return steps
}

// A pane with no issue behind it is a dead end, and it says so rather than
// looking like a read that never came back.
func TestPane_WithNoIssueSaysSoAndAsksTheSiteNothing(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := newDriver(t, testDeps(f), 120, 30)

	mustContain(t, dr.view(), "There is no issue behind this pane.")
	if got := countCalls(f, "Attachments"); got != 0 {
		t.Errorf("a pane with no issue asked the site %d times", got)
	}
}

func TestPane_WithNoConnectionSaysSoRatherThanLookingLikeAHang(t *testing.T) {
	t.Parallel()

	d := testDeps(nil)
	d.Caps = jira.Capabilities{}
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))

	mustContain(t, dr.view(), "No Jira connection in this session yet.")
	if dr.m.canWrite {
		t.Error("a session with no client thinks it may attach files")
	}
}

// Only the window that fits is built, so an issue with two hundred files costs
// what one with three costs.
func TestPane_DrawsOnlyTheRowsThatFit(t *testing.T) {
	t.Parallel()

	many := make([]file, 0, 60)
	for i := range 60 {
		many = append(many, file{name: "shot-" + string(rune('a'+i%26)) + ".png", body: "x"})
	}
	f := newFake(3)
	attached(t, f, "PROJ-1", many...)
	dr := newDriver(t, testDeps(f), 120, 30, WithIssue("PROJ-1"))

	if got := len(dr.m.memo.rows); got > maxListRows {
		t.Errorf("a frame rendered %d rows for a list showing at most %d", got, maxListRows)
	}
}

// A capability answer arriving after the pane was built changes what it offers,
// because a probe finishes behind the first frame.
func TestPane_ACapabilityAnswerChangesWhatTheFooterOffers(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := testDeps(f)
	d.Caps = jira.Capabilities{TimeZone: time.UTC}
	dr := newDriver(t, d, 120, 30, WithIssue("PROJ-1"))

	before, beforeGen := dr.m.LiveKeys()
	dr.send(kernel.CapabilitiesMsg{Caps: fullCaps()})
	after, afterGen := dr.m.LiveKeys()

	if beforeGen == afterGen {
		t.Fatal("the footer will not repaint: the generation did not move with the set")
	}
	if !strings.Contains(actsOf(after), "attach") || strings.Contains(actsOf(before), "attach") {
		t.Errorf("the offer did not change with the probe: %q then %q", actsOf(before), actsOf(after))
	}
}
