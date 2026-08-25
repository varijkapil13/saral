package issue

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// paneClock is what a double-click is timed against here. It is wound forward
// rather than slept on, so a deliberate second click costs no wall time.
type paneClock struct{ at time.Time }

func (c *paneClock) now() time.Time        { return c.at }
func (c *paneClock) after(d time.Duration) { c.at = c.at.Add(d) }

func newPaneClock() *paneClock {
	return &paneClock{at: time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)}
}

func (p *panel) wheel(button tea.MouseButton, times int) {
	p.t.Helper()

	for range times {
		p.send(tea.MouseWheelMsg{Button: button, X: 4, Y: 4})
	}
}

// A field editor opened by a click nobody meant is a description handed to
// $EDITOR, so the second click has to be part of the same gesture.
func TestEdit_TwoDeliberateClicksOnARowDoNotOpenIt(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	clock := newPaneClock()
	d.Now = clock.now
	p := newPanel(t, NewEdit(d, fullIssue(t, f, "PROJ-6"), withDrafts(tempDrafts(t))), 100, 28)

	at := p.zoneAt(d, "row:labels")
	p.clickAt(at)
	if got := p.editor().row().id; got != "labels" {
		t.Fatalf("the click put the cursor on %s, want labels", got)
	}

	clock.after(time.Second)
	p.clickAt(at)

	if p.editor().stage != stageBrowse {
		t.Error("two clicks a second apart opened the field, so a second look reads as a double-click")
	}
}

func TestMove_TwoDeliberateClicksOnAMoveDoNotChooseIt(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	clock := newPaneClock()
	d.Now = clock.now
	p := newPanel(t, NewMove(d, fullIssue(t, f, "PROJ-6")), 100, 28)

	want := p.mover().moves[1]
	at := p.zoneAt(d, "move:"+want.ID)
	p.clickAt(at)
	if got := p.mover().moves[p.mover().cursor].ID; got != want.ID {
		t.Fatalf("the click put the cursor on %s, want %s", got, want.ID)
	}

	clock.after(2 * time.Second)
	p.clickAt(at)

	if p.mover().stage != moveList {
		t.Error("two clicks two seconds apart chose the move under the pointer")
	}
}

// The field rows are taller than a short terminal, and until now the pane drew
// them all and let the frame clip: the last field could be neither seen nor
// pointed at.
func TestEdit_TheWheelReachesTheFieldsAShortTerminalClipsOff(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	p := newPanel(t, NewEdit(d, fullIssue(t, f, "PROJ-6"), withDrafts(tempDrafts(t))), 100, 6)

	first := p.frame()
	if !strings.Contains(first, "Summary") {
		t.Fatalf("the first field is not on screen at all:\n%s", first)
	}
	if strings.Contains(first, "Due") {
		t.Fatalf("every field fits, so this frame proves nothing:\n%s", first)
	}

	p.wheel(tea.MouseWheelDown, 3)
	scrolled := p.frame()

	if !strings.Contains(scrolled, "Due") {
		t.Errorf("the wheel did not reach the last field:\n%s", scrolled)
	}
	if strings.Contains(scrolled, "Summary") {
		t.Errorf("the wheel scrolled nothing away:\n%s", scrolled)
	}

	p.wheel(tea.MouseWheelUp, 6)
	back := p.frame()
	if !strings.Contains(back, "Summary") {
		t.Errorf("the wheel could not get back to the first field:\n%s", back)
	}
	if got := p.editor().top; got != 0 {
		t.Errorf("the pane is scrolled to %d after going down and back up, want 0", got)
	}
}

// The keyboard and the wheel have to agree about where the pane is: a cursor
// walked past the bottom of a short terminal used to leave the screen.
func TestEdit_WalkingTheCursorDownBringsItsRowBackOnScreen(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	p := newPanel(t, NewEdit(d, fullIssue(t, f, "PROJ-6"), withDrafts(tempDrafts(t))), 100, 6)

	p.keys("j", "j", "j", "j")

	if got := p.editor().row().id; got != "duedate" {
		t.Fatalf("the cursor is on %s, want the last field", got)
	}
	if frame := p.frame(); !strings.Contains(frame, "Due") {
		t.Errorf("the row under the cursor is off screen:\n%s", frame)
	}
}

// manyMoves is a workflow with more transitions than a short terminal can draw.
// A site really can offer this many, and the pane used to draw them all and let
// the frame clip the rest.
func manyMoves(n int) []jira.Transition {
	out := make([]jira.Transition, 0, n)
	for i := 1; i <= n; i++ {
		at := strconv.Itoa(i)
		out = append(out, jira.Transition{
			ID:   "tr-" + at,
			Name: "Step " + at,
			To:   jira.Status{ID: "st-" + at, Name: "Stage " + at},
		})
	}
	return out
}

func TestMove_TheWheelScrollsTheMovesAShortTerminalClipsOff(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	p := newPanel(t, NewMove(d, fullIssue(t, f, "PROJ-6")), 100, 8)
	p.send(movesLoadedMsg{gen: p.mover().gen, moves: manyMoves(12)})

	last := "Stage 12"
	if got := p.frame(); strings.Contains(got, last) {
		t.Fatalf("twelve moves fit in eight rows, so this frame proves nothing:\n%s", got)
	}

	p.wheel(tea.MouseWheelDown, 4)

	if got := p.frame(); !strings.Contains(got, last) {
		t.Errorf("the wheel did not reach the last move:\n%s", got)
	}
	if got := p.mover().top; got == 0 {
		t.Error("the wheel moved the frame without moving the pane's own offset")
	}

	p.wheel(tea.MouseWheelUp, 8)
	if got := p.mover().top; got != 0 {
		t.Errorf("the pane is scrolled to %d after going down and back up, want the first move", got)
	}
}

// The cursor and the wheel share one offset here too: a move chosen with j has
// to be visible before enter is pressed on it.
func TestMove_WalkingTheCursorDownBringsTheMoveBackOnScreen(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	p := newPanel(t, NewMove(d, fullIssue(t, f, "PROJ-6")), 100, 8)
	p.send(movesLoadedMsg{gen: p.mover().gen, moves: manyMoves(12)})

	for range 11 {
		p.keys("j")
	}

	if got := p.mover().cursor; got != 11 {
		t.Fatalf("the cursor is on move %d, want the last one", got)
	}
	if got := p.frame(); !strings.Contains(got, "Stage 12") {
		t.Errorf("the move under the cursor is off screen:\n%s", got)
	}
}

// A wheel on a transition screen has nothing to scroll, and must not move the
// list underneath it either.
func TestMove_TheWheelLeavesATransitionScreenAlone(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	d := testDeps(f)
	p := newPanel(t, NewMove(d, fullIssue(t, f, "PROJ-6")), 100, 24)

	p.keys("j", "enter")
	if p.mover().stage == moveList {
		t.Fatalf("the move with a screen did not open one:\n%s", p.frame())
	}
	before := p.frame()

	p.wheel(tea.MouseWheelDown, 4)

	if got := p.frame(); got != before {
		t.Errorf("the wheel changed a transition screen:\n%s", got)
	}
	if got := p.mover().top; got != 0 {
		t.Errorf("the wheel scrolled the list behind the screen to %d", got)
	}
}
