package issue

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// ownCache points the split this test chooses at a directory of its own. The
// pane writes the split it is left with to the cache directory and this package
// shares one, so a test that chooses a split writes somewhere private — and
// t.Setenv rules out t.Parallel, which is why nothing in this file runs in
// parallel.
func ownCache(t *testing.T) {
	t.Helper()
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
}

// splitPane is a pane with an issue read in full and a thread beside it, at a
// size the sidebar fits at.
func splitPane(t *testing.T, w, h int) (*driver, kernel.Deps, *jiratest.Fake) {
	t.Helper()

	f := newFake(8)
	d := testDeps(f)
	addComment(t, f, "PROJ-3", "Reproduced on staging, twice.")
	full := readIssue(t, f, "PROJ-3")
	full.Description = longDoc(20)
	dr := newDriver(t, d, seedOf(t, f, "PROJ-3"), w, h)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})
	return dr, d, f
}

// boundary is where the divider was drawn, resolved the way a press resolves:
// by zone lookup rather than by arithmetic. The manager records on a goroutine
// of its own, so the zone is looked for until it appears.
func boundary(t *testing.T, dr *driver, d kernel.Deps) *zone.ZoneInfo {
	t.Helper()

	id := dr.m.zones.ID(dividerZone)
	deadline := time.Now().Add(10 * time.Second)
	for {
		d.Zones.Scan(dr.m.View())
		if at := d.Zones.Get(id); !at.IsZero() {
			return at
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing on screen is marked %q", id)
		}
		runtime.Gosched()
	}
}

func pressAt(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func motionAt(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func releaseAt(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// sideOf is the sidebar's width as this frame has it.
func sideOf(m *Model) int { return m.lay.boxes[regionDetails].w }

// The gesture the whole packet is about: grab the boundary, move it, and have
// the panes follow. Both floors are measurements of what content needs, so a
// pointer dragged past either one stops there rather than squeezing a pane into
// something nobody can read.
func TestSplit_ADragMovesTheBoundaryAndStopsAtBothFloors(t *testing.T) {
	ownCache(t)

	dr, d, _ := splitPane(t, 120, 30)
	at := boundary(t, dr, d)
	if want := 120 - sideOf(dr.m) - divider; at.StartX != want {
		t.Fatalf("the divider is marked at column %d, want %d", at.StartX, want)
	}
	if at.StartX != at.EndX {
		t.Errorf("the divider is marked %d columns wide, want the one it draws", at.EndX-at.StartX+1)
	}

	y := at.StartY + 2
	dr.send(pressAt(at.StartX, y))
	if !dr.m.drag.Active() {
		t.Fatal("a press on the divider started no drag")
	}
	if dr.m.focus != regionDesc {
		t.Errorf("the press moved the keyboard to region %d; the divider belongs to neither side", dr.m.focus)
	}

	for _, tc := range []struct {
		name string
		x    int
		side int
	}{
		{name: "four columns to the left", x: at.StartX - 4, side: 44},
		{name: "twenty to the left", x: at.StartX - 20, side: 60},
		{name: "past what the description can give up", x: at.StartX - 40, side: 65},
		{name: "three columns to the right", x: at.StartX + 3, side: 37},
		{name: "past what the sidebar can give up", x: at.StartX + 40, side: 35},
	} {
		dr.send(motionAt(tc.x, y))
		if got := sideOf(dr.m); got != tc.side {
			t.Errorf("%s put the sidebar at %d cells, want %d", tc.name, got, tc.side)
		}
		if got := dr.m.lay.boxes[regionDesc].w; got < descMin {
			t.Errorf("%s left the description %d cells, under its floor of %d", tc.name, got, descMin)
		}
	}

	dr.send(releaseAt(at.StartX-15, y))
	if dr.m.drag.Active() {
		t.Error("the release left a drag under way")
	}
	if got := sideOf(dr.m); got != 55 {
		t.Errorf("the release settled the sidebar at %d cells, want the 55 the pointer was over", got)
	}
	// And the frame really is drawn to the split, not merely recorded at it.
	if got := lineWidth(t, dr, 0); got != 120 {
		t.Errorf("a row is %d cells wide after the drag, want the terminal's 120", got)
	}
}

// A pointer that leaves the frame is still holding the divider it grabbed. The
// gesture ends where the pointer is, which for a release above the pane means
// the horizontal distance it travelled and nothing about the vertical.
func TestSplit_AReleaseOutsideTheFrameStillEndsTheDrag(t *testing.T) {
	ownCache(t)

	dr, d, _ := splitPane(t, 120, 30)
	at := boundary(t, dr, d)

	dr.send(pressAt(at.StartX, at.StartY+1))
	dr.send(releaseAt(at.StartX-12, -40))

	if dr.m.drag.Active() {
		t.Error("a release off the top of the screen left the drag under way")
	}
	if got := sideOf(dr.m); got != 52 {
		t.Errorf("the sidebar ended at %d cells, want the 52 the pointer had reached", got)
	}
}

// A resize takes the width the delta was measured against away, so the gesture
// it belongs to is dropped rather than applied against a pane of another size.
func TestSplit_AResizeMidGestureCancelsTheDrag(t *testing.T) {
	ownCache(t)

	dr, d, _ := splitPane(t, 120, 30)
	at := boundary(t, dr, d)

	dr.send(pressAt(at.StartX, at.StartY+1))
	dr.send(motionAt(at.StartX-16, at.StartY+1))
	if got := sideOf(dr.m); got != 56 {
		t.Fatalf("the drag moved the sidebar to %d cells, want 56 before the resize", got)
	}

	dr.send(kernel.SizeMsg{Width: 140, Height: 30})
	if dr.m.drag.Active() {
		t.Error("the resize left the drag under way")
	}
	if got, want := sideOf(dr.m), sideWidth(140, 0); got != want {
		t.Errorf("the cancelled drag left the sidebar at %d cells, want the %d the width chooses", got, want)
	}

	// The release the terminal still owes us applies nothing.
	dr.send(releaseAt(at.StartX-30, at.StartY+1))
	if got, want := sideOf(dr.m), sideWidth(140, 0); got != want {
		t.Errorf("the release after the cancel moved the sidebar to %d cells, want %d", got, want)
	}
}

// A key ends a gesture too, and then does its own job from where the press
// started rather than from wherever the pointer had wandered.
func TestSplit_AKeyCancelsTheDragAndThenActsOnTheSplitItFound(t *testing.T) {
	ownCache(t)

	dr, d, _ := splitPane(t, 120, 30)
	at := boundary(t, dr, d)
	was := sideOf(dr.m)

	dr.send(pressAt(at.StartX, at.StartY+1))
	dr.send(motionAt(at.StartX-20, at.StartY+1))
	dr.key("<")

	if dr.m.drag.Active() {
		t.Error("a keypress left the drag under way")
	}
	if got := sideOf(dr.m); got != was+splitStep {
		t.Errorf("the sidebar is %d cells, want %d — one step from where the press found it", got, was+splitStep)
	}
}

// The help overlay swallows everything from the mouse while it is up, so a
// release can go missing. The next press is what ends the gesture it belonged
// to, rather than that gesture's stale delta being applied to it.
func TestSplit_APressElsewhereEndsAGestureWhoseReleaseNeverArrived(t *testing.T) {
	ownCache(t)

	dr, d, _ := splitPane(t, 120, 30)
	at := boundary(t, dr, d)
	was := sideOf(dr.m)

	dr.send(pressAt(at.StartX, at.StartY+1))
	dr.send(motionAt(at.StartX-20, at.StartY+1))

	fields := regionZone(t, dr, d, regionDetails)
	dr.send(pressAt(fields.StartX+2, fields.StartY+1))
	if dr.m.drag.Active() {
		t.Error("a press in the fields left the divider held")
	}
	if got := sideOf(dr.m); got != was {
		t.Errorf("the abandoned gesture left the sidebar at %d cells, want the %d the press found", got, was)
	}

	dr.send(releaseAt(at.StartX-40, at.StartY+1))
	if got := sideOf(dr.m); got != was {
		t.Errorf("the stale release moved the sidebar to %d cells, want %d", got, was)
	}
}

// A release nobody pressed for, and a press on the boundary that never moved,
// are the two gestures that must leave the split exactly as they found it — a
// one-column target is easy to click by accident, and pinning a share there
// would quietly stop the split following the width.
func TestSplit_AClickOnTheBoundaryChoosesNothing(t *testing.T) {
	ownCache(t)

	dr, d, _ := splitPane(t, 120, 30)
	at := boundary(t, dr, d)

	dr.send(pressAt(at.StartX, at.StartY+1))
	dr.send(releaseAt(at.StartX, at.StartY+1))
	if dr.m.split != 0 {
		t.Errorf("a click on the boundary pinned a share of %d, want the width still choosing", dr.m.split)
	}

	dr.key("<", "<")
	chosen := dr.m.split
	dr.send(releaseAt(4, 4))
	if dr.m.split != chosen {
		t.Errorf("a release with nothing held moved the split from %d to %d", chosen, dr.m.split)
	}
}

// The keyboard route is the same state by another road, because a drag is
// mouse-only and docs/UX.md asks for three ways to everything.
func TestSplit_TheKeysReachTheSameStateAsTheDrag(t *testing.T) {
	ownCache(t)

	// Both panes are built before either is touched: the first to choose a split
	// writes it, and the second would otherwise open on it.
	byKey, _, _ := splitPane(t, 120, 30)
	byDrag, d, _ := splitPane(t, 120, 30)
	at := boundary(t, byDrag, d)

	byKey.key("<", "<", "<")
	byDrag.send(pressAt(at.StartX, at.StartY+1))
	byDrag.send(releaseAt(at.StartX-3*splitStep, at.StartY+1))

	if byKey.m.split != byDrag.m.split {
		t.Errorf("three strokes left the split at %d and the same drag at %d", byKey.m.split, byDrag.m.split)
	}
	if byKey.view() != byDrag.view() {
		t.Errorf("the two routes draw different frames\n--- keys ---\n%s\n--- drag ---\n%s",
			byKey.view(), byDrag.view())
	}
}

// The palette is the third way, and it reaches the same three gestures the keys
// do rather than a second implementation of them.
func TestSplit_ThePaletteReachesTheSameThreeGestures(t *testing.T) {
	ownCache(t)

	dr, _, _ := splitPane(t, 120, 30)
	was := sideOf(dr.m)

	dr.send(WidenSidebarMsg{})
	if got := sideOf(dr.m); got != was+splitStep {
		t.Errorf("widening the sidebar from the palette gave it %d cells, want %d", got, was+splitStep)
	}
	dr.send(WidenDescriptionMsg{})
	if got := sideOf(dr.m); got != was {
		t.Errorf("widening the description from the palette left the sidebar at %d cells, want %d", got, was)
	}
	dr.send(WidenSidebarMsg{})
	dr.send(ResetSplitMsg{})
	if dr.m.split != 0 {
		t.Errorf("resetting from the palette left a chosen split of %d", dr.m.split)
	}
	if got, want := sideOf(dr.m), sideWidth(120, 0); got != want {
		t.Errorf("the reset left the sidebar at %d cells, want the %d the width chooses", got, want)
	}
}

// Both floors refuse in words. Silence there is indistinguishable from a key
// that is not bound at all, which is the thing a reader cannot act on.
func TestSplit_AGestureWithNowhereLeftToGoSaysSo(t *testing.T) {
	ownCache(t)

	dr, _, _ := splitPane(t, 120, 30)
	for range 10 {
		dr.key("<")
	}
	if got := dr.lastStatus().Text; !strings.Contains(got, "description") {
		t.Errorf("running the description into its floor said %q", got)
	}
	if got := dr.m.lay.boxes[regionDesc].w; got != descMin {
		t.Errorf("the description settled at %d cells, want its floor of %d", got, descMin)
	}
	for range 10 {
		dr.key(">")
	}
	if got := dr.lastStatus().Text; !strings.Contains(got, "fields") {
		t.Errorf("running the sidebar into its floor said %q", got)
	}
	if got := sideOf(dr.m); got != sideMin {
		t.Errorf("the sidebar settled at %d cells, want its floor of %d", got, sideMin)
	}

	dr.key("=")
	dr.key("=")
	if got := dr.lastStatus().Text; !strings.Contains(got, "already") {
		t.Errorf("resetting a split that is already the default said %q", got)
	}
}

// Below the breakpoint the regions take the screen in turn, so there is no
// boundary on it. The keys say that rather than doing nothing, and there is
// nothing marked for a press to land on.
func TestSplit_TheNarrowModeSaysThereIsNoSplitToMove(t *testing.T) {
	ownCache(t)

	dr, d, _ := splitPane(t, 80, 20)
	for _, stroke := range []string{"<", ">", "="} {
		dr.key(stroke)
		got := dr.lastStatus().Text
		if !strings.Contains(got, "turn") || !strings.Contains(got, "tab") {
			t.Errorf("%q at 80 columns said %q, which does not say why or what to do instead", stroke, got)
		}
		if dr.m.split != 0 {
			t.Errorf("%q at 80 columns chose a split of %d anyway", stroke, dr.m.split)
		}
	}

	id := dr.m.zones.ID(dividerZone)
	d.Zones.Scan(dr.m.View())
	if at := d.Zones.Get(id); !at.IsZero() {
		t.Errorf("the narrow mode marked a divider at %d,%d", at.StartX, at.StartY)
	}
	if dr.m.drag.Active() {
		t.Error("something started a drag with no divider on screen")
	}
}

// The choice is a share of the pane rather than a column count, so it means the
// same thing in a window of another size — and it comes back as the column it
// was chosen at, at every width a terminal reaches.
func TestSplit_AShareSurvivesTheWidthItWasChosenAt(t *testing.T) {
	ownCache(t)

	for w := wideAt; w <= 400; w++ {
		for sideW := sideMin; sideW <= max(w-divider-descMin, sideMin); sideW++ {
			if got := sideWidth(w, shareOf(w, sideW)); got != sideW {
				t.Fatalf("%d cells at %d columns came back as %d", sideW, w, got)
			}
		}
	}

	dr, _, _ := splitPane(t, 120, 30)
	dr.key("<", "<")
	chosen := dr.m.split
	if got := sideOf(dr.m); got != 48 {
		t.Fatalf("two strokes put the sidebar at %d cells, want 48", got)
	}
	dr.send(kernel.SizeMsg{Width: 200, Height: 30})
	if dr.m.split != chosen {
		t.Errorf("a resize changed the share from %d to %d", chosen, dr.m.split)
	}
	if got, want := sideOf(dr.m), int(chosen)*200/config.SplitScale; got != want {
		t.Errorf("the share gives %d cells of 200, want about %d", got, want)
	}
}

// At ninety columns the two floors meet, so there is exactly one legal split and
// the gesture says so rather than pretending to move.
func TestSplit_AtTheBreakpointThereIsOnlyOneLegalSplit(t *testing.T) {
	ownCache(t)

	dr, _, _ := splitPane(t, wideAt, 28)
	was := sideOf(dr.m)
	for _, stroke := range []string{"<", ">"} {
		dr.key(stroke)
		if got := sideOf(dr.m); got != was {
			t.Errorf("%q at %d columns moved the sidebar to %d cells; the floors leave it %d",
				stroke, wideAt, got, was)
		}
		if got := dr.lastStatus().Text; !strings.Contains(got, "narrow") {
			t.Errorf("%q at %d columns said %q", stroke, wideAt, got)
		}
	}
}

// The whole point of writing it down: the next pane opens where the last one was
// left. The write is a command, so this only holds once the driver has run it.
func TestSplit_TheChoiceIsThereWhenTheNextPaneOpens(t *testing.T) {
	ownCache(t)

	dr, _, f := splitPane(t, 120, 30)
	dr.key("<", "<")
	chosen := dr.m.split
	if chosen == 0 {
		t.Fatal("two strokes chose nothing")
	}

	next, ok := New(testDeps(f), seedOf(t, f, "PROJ-3")).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if next.split != chosen {
		t.Errorf("a fresh pane opened at %d, want the %d the last one was left at", next.split, chosen)
	}

	dr.key("=")
	after, ok := New(testDeps(f), seedOf(t, f, "PROJ-3")).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if after.split != 0 {
		t.Errorf("a fresh pane opened at %d after the split was reset", after.split)
	}
}

// A machine with nowhere to write is a first run, another copy of Saral holding
// the directory, or a home that is not writable. The split still moves; it is
// only not remembered, and it says so once rather than on every stroke.
func TestSplit_SaysOnceWhenItCannotBeRemembered(t *testing.T) {
	t.Setenv("SARAL_CACHE_DIR", "/dev/null/nowhere")

	dr, _, _ := splitPane(t, 120, 30)
	was := sideOf(dr.m)
	dr.key("<")

	if got := sideOf(dr.m); got != was+splitStep {
		t.Errorf("the sidebar is %d cells, want %d; a failed write must not undo the split", got, was+splitStep)
	}
	said := 0
	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "not being remembered") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("a split that cannot be written said so %d times, want once", said)
	}
	dr.key("<", "<")
	said = 0
	for _, status := range dr.statuses {
		if strings.Contains(status.Text, "not being remembered") {
			said++
		}
	}
	if said != 1 {
		t.Errorf("two more strokes brought the warning back; it was said %d times", said)
	}
}

// Goldens at the three widths the sidebar exists at, with the split moved off
// the one the width chooses. Ninety is in here because it is the width at which
// the two floors meet, so what it proves is that the frame there is the same one
// either way.
func TestSplit_Goldens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		w, h    int
		strokes []string
	}{
		{name: "split_90x28", w: 90, h: 28, strokes: []string{"<", "<"}},
		{name: "split_100x28", w: 100, h: 28, strokes: []string{"<"}},
		{name: "split_120x38", w: 120, h: 38, strokes: []string{"<", "<", "<"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ownCache(t)
			dr, _, _ := splitPane(t, tc.w, tc.h)
			dr.key(tc.strokes...)
			golden(t, tc.name+".golden", dr.view())
		})
	}
}

// lineWidth is how wide one row of the pane is drawn, measured the only way a
// display string may be measured.
func lineWidth(t *testing.T, dr *driver, row int) int {
	t.Helper()

	rows := strings.Split(dr.view(), "\n")
	if row+headerHeight >= len(rows) {
		t.Fatalf("the frame has %d rows, so row %d of the pane is not in it", len(rows), row)
	}
	return ansi.StringWidth(rows[row+headerHeight])
}
