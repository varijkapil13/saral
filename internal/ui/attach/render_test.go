package attach

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestPane_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		files         []file
		chafa         string
		drive         func(*driver)
		golden        string
	}{
		"the files on an issue": {
			width: 120, height: 24, golden: "files_120x24.golden",
		},
		"a narrow terminal": {
			width: 80, height: 24, golden: "files_80x24.golden",
		},
		"nothing attached": {
			width: 120, height: 24, files: []file{}, golden: "empty_120x24.golden",
		},
		"a file named and measured, because nothing here can draw it": {
			width: 120, height: 24, golden: "described_120x24.golden",
			drive: func(dr *driver) { dr.onto("screenshot.png"); dr.key("enter") },
		},
		"half-blocks from chafa": {
			width: 120, height: 24, chafa: "chafa", golden: "halfblocks_120x24.golden",
			drive: func(dr *driver) { dr.onto("screenshot.png"); dr.key("enter") },
		},
		"the preview with the list folded away": {
			width: 120, height: 24, chafa: "chafa", golden: "grown_120x24.golden",
			drive: func(dr *driver) { dr.onto("screenshot.png"); dr.key("enter", "z") },
		},
		"a path being typed": {
			width: 120, height: 24, golden: "typing_120x24.golden",
			drive: func(dr *driver) { dr.key("u"); dr.typeText("/tmp/notes.txt") },
		},
		"a deletion waiting for an answer": {
			width: 120, height: 24, golden: "confirm_120x24.golden",
			drive: func(dr *driver) { dr.onto("handover.pdf"); dr.key("d") },
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			files := tc.files
			if files == nil {
				files = sampleFiles()
			}
			f := newFake(3)
			if len(files) > 0 {
				attached(t, f, "PROJ-1", files...)
			}
			dr := newDriver(t, testDeps(f), tc.width, tc.height, WithIssue("PROJ-1"))
			dr.m.tools.chafa = tc.chafa
			if tc.chafa != "" {
				dr.seen.out = []byte(strings.Repeat("##########\n", 8))
			}
			if tc.drive != nil {
				tc.drive(dr)
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

func TestPane_FailureGolden(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	f.FailNext(&jira.TransportError{
		Op:  "list the attachments on PROJ-1",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
	})
	dr := newDriver(t, testDeps(f), 120, 24, WithIssue("PROJ-1"))

	golden(t, "failed_120x24.golden", dr.view())
}

func TestPane_RefusedWriteGolden(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	d := testDeps(f)
	d.Caps.Attachments = jira.Capability{Reason: "attachments are switched off on this site"}
	dr := newDriver(t, d, 120, 24, WithIssue("PROJ-1"))

	golden(t, "readonly_120x24.golden", dr.view())
}

// Every row is exactly as wide as the pane, whatever is in it, or the selected
// row's highlight stops short of the edge.
func TestPane_EveryRowFillsTheWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 100, 120, 200} {
		f := newFake(3)
		attached(t, f, "PROJ-1", sampleFiles()...)
		dr := newDriver(t, testDeps(f), width, 24, WithIssue("PROJ-1"))
		lines := strings.Split(dr.view(), "\n")[headHeight:]
		for i := range dr.m.files {
			if got := ansi.StringWidth(lines[i]); got != width {
				t.Errorf("at %d columns row %d is %d wide: %q", width, i, got, lines[i])
			}
		}
	}
}

// The pane keeps to the box it was given at every size and in every state,
// including one too narrow for the columns beside the name.
func TestPane_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{40, 10}, {80, 20}, {120, 30}, {200, 60}, {80, 4}} {
		for state, drive := range map[string]func(*driver){
			"listing":    func(*driver) {},
			"previewing": func(dr *driver) { dr.onto("screenshot.png"); dr.key("enter") },
			"grown":      func(dr *driver) { dr.key("z") },
			"typing":     func(dr *driver) { dr.key("u"); dr.typeText("/tmp/x") },
			"confirming": func(dr *driver) { dr.key("d") },
		} {
			f := newFake(3)
			attached(t, f, "PROJ-1", sampleFiles()...)
			dr := newDriver(t, testDeps(f), size.w, size.h, WithIssue("PROJ-1"))
			drive(dr)

			lines := strings.Split(dr.m.View(), "\n")
			if len(lines) != size.h {
				t.Errorf("%s at %dx%d is %d lines", state, size.w, size.h, len(lines))
			}
			for _, line := range lines {
				if got := ansi.StringWidth(line); got > size.w {
					t.Errorf("%s at %dx%d draws a %d-column line: %q", state, size.w, size.h, got, line)
				}
			}
		}
	}
}

// The caption is the preview's title, so it has to follow the cursor rather than
// keep naming the file that was looked at first.
func TestPane_TheCaptionNamesTheFileTheCursorIsOn(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	dr.onto("screenshot.png")
	mustContain(t, dr.view(), "screenshot.png")

	dr.onto("handover.pdf")
	frame := dr.view()
	if strings.Count(frame, "handover.pdf") < 2 {
		t.Errorf("the caption did not follow the cursor:\n%s", frame)
	}
}

// A resize throws the preview away: both graphics protocols are told the geometry
// rather than measuring it, so one drawn for the old box is the wrong size.
func TestPane_AResizeThrowsThePreviewAway(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	dr.m.tools.chafa = "chafa"
	dr.onto("screenshot.png")
	dr.key("enter")
	if dr.m.shown.kind == previewNone {
		t.Fatal("nothing was drawn to begin with")
	}

	dr.send(kernel.SizeMsg{Width: 100, Height: 20})
	if dr.m.shown.kind != previewNone {
		t.Error("a preview drawn for the old box survived the resize")
	}
}

// The date is the Jira account's timezone and not the machine's, which is what a
// site in another zone depends on.
func TestPane_DatesAreTheAccountsTimezone(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", file{name: "shot.png", body: "x"})
	d := testDeps(f)
	zone, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Skipf("no timezone database here: %v", err)
	}
	d.Caps.TimeZone = zone
	dr := newDriver(t, d, 120, 24, WithIssue("PROJ-1"))

	dr.m.files[0].Created = time.Date(2026, time.March, 2, 22, 0, 0, 0, time.UTC)
	dr.m.memo.reset()
	mustContain(t, dr.view(), "2026-03-03")
}

// The preview does not fetch, so a pane holding nothing yet has to say what the
// key does rather than looking like a read that never came back.
func TestPane_ThePreviewSaysWhatTheKeyWillDoBeforeAnythingIsFetched(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	dr.onto("screenshot.png")
	mustContain(t, dr.view(), "shows screenshot.png here")

	dr.onto("handover.pdf")
	mustContain(t, dr.view(), "opens handover.pdf in whatever this desktop opens it with")
}

// The kind of empty is named, and it keeps being named after the status line has
// been overwritten.
func TestPane_TheEmptyStatesAreNamedApart(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		build func(*testing.T) *driver
		says  string
	}{
		"no issue behind the pane": {
			build: func(t *testing.T) *driver { return newDriver(t, testDeps(newFake(3)), 120, 24) },
			says:  "There is no issue behind this pane.",
		},
		"no connection in the session": {
			build: func(t *testing.T) *driver {
				return newDriver(t, testDeps(nil), 120, 24, WithIssue("PROJ-1"))
			},
			says: "No Jira connection in this session yet.",
		},
		"nothing attached": {
			build: func(t *testing.T) *driver {
				return newDriver(t, testDeps(newFake(3)), 120, 24, WithIssue("PROJ-1"))
			},
			says: "Nothing is attached to PROJ-1.",
		},
		"the site refused": {
			build: func(t *testing.T) *driver {
				f := newFake(3)
				f.FailNext(&jira.CapabilityError{Capability: jira.CapAttachments, Reason: "no Browse Projects"})
				return newDriver(t, testDeps(f), 120, 24, WithIssue("PROJ-1"))
			},
			says: "The site would not say.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := tc.build(t)
			mustContain(t, dr.view(), tc.says)
		})
	}
}

// A read still out is its own state, distinct from a read that came back with
// nothing: all of them drawing one sentence is how a dead host looked like an
// empty issue.
func TestPane_AReadStillOutSaysSoRatherThanSayingNothingIsAttached(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	attached(t, f, "PROJ-1", sampleFiles()...)
	f.Delay(time.Second)
	view, ok := New(testDeps(f), WithIssue("PROJ-1")).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	view.tools, _ = testTools(t)
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 24})
	view, _ = next.(*Model)
	_ = view.Init()

	frame := ansi.Strip(view.View())
	mustContain(t, frame, "Reading what is attached", "asking the site")
	mustNotContain(t, frame, "Nothing is attached", "Nothing has been asked")
	view.Close()
}

// Nothing has been asked of the site yet is its own answer too, and it is what a
// pane that has never had an issue read shows.
func TestPane_NothingAskedYetIsItsOwnSentence(t *testing.T) {
	t.Parallel()

	view, ok := New(testDeps(newFake(3)), WithIssue("PROJ-1")).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	view.tools, _ = testTools(t)
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 24})
	view, _ = next.(*Model)

	mustContain(t, ansi.Strip(view.View()), "Nothing has been asked of Jira yet.")
}

// A cached file is a real one on disk, so a preview after a restart draws without
// a round trip. This is the half of it that proves the path is the one save used.
func TestPane_APreviewComesFromTheFileOnDiskWhenThereIsOne(t *testing.T) {
	t.Parallel()

	dr, f := loadedPane(t)
	dr.m.tools.chafa = "chafa"
	att := dr.m.files[0]
	path := dr.m.tools.path(dr.m.deps.Site, att)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("z", int(att.Size))), 0o600); err != nil {
		t.Fatal(err)
	}

	dr.onto("screenshot.png")
	dr.key("enter")

	if got := countCalls(f, "Download"); got != 0 {
		t.Errorf("a file already on disk was downloaded %d times", got)
	}
	if dr.m.shown.kind != previewCells {
		t.Errorf("the file on disk was not drawn: kind %d, why %q", dr.m.shown.kind, dr.m.shown.why)
	}
}
