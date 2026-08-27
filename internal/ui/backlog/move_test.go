package backlog

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// chunker records what each move call was asked to carry and can refuse one call
// by number. Everything it does not intercept is the fake's, so the issues
// really do move and the view really does redraw them.
type chunker struct {
	*jiratest.Fake

	mu       sync.Mutex
	sprint   []int
	backlog  []int
	failAt   int
	failWith error
}

func newChunker(f *jiratest.Fake) *chunker { return &chunker{Fake: f, failAt: -1} }

func (c *chunker) MoveToSprint(ctx context.Context, id int64, keys []string) error {
	if err := c.record(&c.sprint, keys); err != nil {
		return err
	}
	return c.Fake.MoveToSprint(ctx, id, keys)
}

func (c *chunker) MoveToBacklog(ctx context.Context, keys []string) error {
	if err := c.record(&c.backlog, keys); err != nil {
		return err
	}
	return c.Fake.MoveToBacklog(ctx, keys)
}

func (c *chunker) record(into *[]int, keys []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := len(*into)
	*into = append(*into, len(keys))
	if c.failWith != nil && at == c.failAt {
		return c.failWith
	}
	return nil
}

func (c *chunker) calls() (sprint, backlog []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.sprint...), append([]int(nil), c.backlog...)
}

// endpointCap is what docs/API-NOTES.md records: POST /sprint/{id}/issue and
// POST /backlog/issue both refuse a call carrying more than fifty issues.
//
// It is spelt out here rather than read off the view's own constant, because a
// test that compares a number against itself cannot notice the number being
// wrong.
const endpointCap = 50

// A selection bigger than one call is sent in calls the endpoint accepts, and
// the number of them is the number the confirm line promised.
func TestMove_SendsASelectionInCallsTheEndpointAccepts(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(140))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.loadAll()
	dr.pickWholeBacklog()

	want := len(dr.m.picked)
	if want <= endpointCap {
		t.Fatalf("the fixture leaves %d issues to schedule, which is one call; this test is about more than one", want)
	}
	calls := (want + endpointCap - 1) / endpointCap
	dr.key("m")
	dr.m.destAt = 0
	dr.key("enter")
	mustContain(t, dr.view(), strconv.Itoa(calls)+" calls")
	dr.key("y")

	sprint, _ := c.calls()
	if len(sprint) != calls {
		t.Errorf("moving %d issues took %d calls, want %d of at most %d issues each",
			want, len(sprint), calls, endpointCap)
	}
	sent := 0
	for i, n := range sprint {
		if n > endpointCap {
			t.Errorf("call %d carried %d issues; both move endpoints refuse more than %d", i, n, endpointCap)
		}
		if n == 0 {
			t.Errorf("call %d carried nothing", i)
		}
		sent += n
	}
	if sent != want {
		t.Errorf("the calls carried %d issues between them, want %d", sent, want)
	}
	mustContain(t, dr.view(), "moved "+count(want, "issue")+" into ")
}

// A move that stops part way through says which half moved, in the site's own
// words, and leaves the half that did not still picked so the same gesture tries
// them again.
func TestMove_ReportsBothHalvesOfAPartialMove(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(140))
	c.failAt, c.failWith = 1, &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/sprint"}
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.loadAll()
	dr.pickWholeBacklog()

	whole := len(dr.m.picked)
	wanted := make([]string, 0, whole)
	for i := range dr.m.rows {
		if !dr.m.rows[i].head {
			if key := dr.m.issues[dr.m.rows[i].issue].Key; dr.m.picked[key] {
				wanted = append(wanted, key)
			}
		}
	}
	dr.key("m")
	dr.m.destAt = 0
	sprintName := dr.m.groups[0].name
	dr.key("enter", "y")

	sprint, _ := c.calls()
	if len(sprint) != 2 {
		t.Fatalf("the move made %d calls; it must stop on the one that was refused rather than carry on", len(sprint))
	}
	moved, pending := wanted[:endpointCap], wanted[endpointCap:]
	mustContain(t, dr.view(),
		"moved "+strconv.Itoa(len(moved))+" of "+count(whole, "issue"),
		"the other "+strconv.Itoa(len(pending))+" did not move",
		"retry in 30s",
	)
	for _, key := range moved {
		if got := dr.groupOf(key); got != sprintName {
			t.Errorf("%s was in an accepted call and is drawn under %q, want %q", key, got, sprintName)
		}
		if dr.m.picked[key] {
			t.Errorf("%s moved and is still picked", key)
		}
	}
	for _, key := range pending {
		if got := dr.groupOf(key); got != backlogName {
			t.Errorf("%s did not move and is drawn under %q", key, got)
		}
		if !dr.m.picked[key] {
			t.Errorf("%s did not move and is no longer picked, so nothing can try it again", key)
		}
	}
	if len(dr.m.picked) != len(pending) {
		t.Errorf("%d issues are picked after the failure, want the %d that did not move",
			len(dr.m.picked), len(pending))
	}
	// The status line is transient and the count is not.
	dr.key("j")
	mustContain(t, dr.view(), "did not move")
}

// A refusal on the very first call moved nothing, and says so rather than
// reporting a move.
func TestMove_ARefusalOnTheFirstCallMovesNothing(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(12))
	c.failAt, c.failWith = 0, &jira.CapabilityError{
		Capability: jira.CapBoards, Reason: "you need Schedule Issues on PROJ",
	}
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.cursorTo("row:PROJ-1")
	dr.key("space", "m")
	dr.m.destAt = 0
	dr.key("enter", "y")

	mustContain(t, dr.view(), "moved 0 of 1 issue", "you need Schedule Issues on PROJ")
	if got := dr.groupOf("PROJ-1"); got != backlogName {
		t.Errorf("nothing moved and PROJ-1 is drawn under %q", got)
	}
}

// The confirm cannot be skipped: no single stroke and no single click moves an
// issue, and the step that does names what will change.
func TestMove_NothingMovesUntilTheConfirmIsAnswered(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(12))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.cursorTo("row:PROJ-1")

	for _, stroke := range []string{"space", "v", "x", "y", "enter", "j", "k", "G", "home", "end"} {
		dr.key(stroke)
		if sprint, backlog := c.calls(); len(sprint)+len(backlog) != 0 {
			t.Fatalf("%q moved something from the browsing state", stroke)
		}
	}
	dr.cursorTo("row:PROJ-1")
	dr.key("space", "m")
	if dr.m.mode != choosing {
		t.Fatalf("m left the view in mode %d, want the destination chooser", dr.m.mode)
	}
	dr.key("enter")
	if dr.m.mode != confirming {
		t.Fatalf("choosing a destination left the view in mode %d, want the confirm", dr.m.mode)
	}
	if sprint, backlog := c.calls(); len(sprint)+len(backlog) != 0 {
		t.Fatal("choosing a destination moved something before the confirm was answered")
	}
	mustContain(t, dr.view(), "Move 1 issue into ", "y go ahead", "esc cancel")

	// esc leaves them where they are.
	dr.key("esc")
	if sprint, backlog := c.calls(); len(sprint)+len(backlog) != 0 {
		t.Fatal("cancelling the confirm moved something")
	}
	if dr.m.mode != browsing {
		t.Errorf("esc left the view in mode %d", dr.m.mode)
	}
}

func TestMove_TheBacklogIsItsOwnEndpoint(t *testing.T) {
	t.Parallel()
	f := newFake(12)
	active, _ := sprintIDs(t, f)
	if err := f.MoveToSprint(t.Context(), active, []string{"PROJ-1"}); err != nil {
		t.Fatalf("seeding the sprint: %v", err)
	}
	c := newChunker(f)
	dr := newDriver(t, testDeps(c), 120, 24)
	if got := dr.groupOf("PROJ-1"); got == backlogName {
		t.Fatalf("PROJ-1 was seeded into a sprint and is drawn under %q", got)
	}

	dr.cursorTo("row:PROJ-1")
	dr.key("space", "m")
	dr.m.destAt = len(dr.m.groups) - 1
	dr.key("enter", "y")

	sprint, backlog := c.calls()
	if len(backlog) != 1 || len(sprint) != 0 {
		t.Errorf("moving to the backlog made %d sprint calls and %d backlog calls", len(sprint), len(backlog))
	}
	if got := dr.groupOf("PROJ-1"); got != backlogName {
		t.Errorf("PROJ-1 was moved to the backlog and is drawn under %q", got)
	}
}

// Nothing is picked and the cursor is on an issue: that issue is what a move
// moves, so the gesture works before anybody has learnt about the selection.
func TestMove_MovesTheIssueUnderTheCursorWhenNothingIsPicked(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(12))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.cursorTo("row:PROJ-4")
	dr.key("m")
	dr.m.destAt = 0
	dr.key("enter", "y")

	if got := dr.groupOf("PROJ-4"); got != dr.m.groups[0].name {
		t.Errorf("PROJ-4 is drawn under %q after being moved into %q", got, dr.m.groups[0].name)
	}
	if sprint, _ := c.calls(); len(sprint) != 1 || sprint[0] != 1 {
		t.Errorf("the move carried %v issues, want one call of one", sprint)
	}
}

func TestMove_SaysThereIsNothingToMoveWhenTheCursorIsOnASection(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(12))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.cursorTo("head:0")
	dr.key("m")
	if dr.m.mode != browsing {
		t.Errorf("m over an empty section opened the chooser with nothing to move")
	}
}

// A move part way through is something the user would lose, so the kernel is
// told to refuse the quit and the switch that would throw it away.
func TestMove_RefusesToBeThrownAwayWhileItIsRunning(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(140))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.loadAll()
	dr.pickWholeBacklog()
	dr.key("m")
	dr.m.destAt = 0
	dr.key("enter")

	if reason, blocked := dr.m.BlocksClose(); blocked {
		t.Fatalf("the view refused a close before the move had started: %s", reason)
	}
	cmd := dr.hold(keyPress("y"))
	if dr.m.mode != movingIssues {
		t.Fatalf("answering the confirm left the view in mode %d", dr.m.mode)
	}
	reason, blocked := dr.m.BlocksClose()
	if !blocked {
		t.Fatal("a move with calls still out did not refuse to be thrown away")
	}
	mustContain(t, reason, "a move is still going", "still with Jira")

	// A move in flight has nothing of its own to offer and says so by
	// advertising nothing.
	if set, _ := dr.m.LiveKeys(); !set.IsZero() {
		t.Errorf("a move in flight advertises %v", set.Acts)
	}
	mustContain(t, ansi.Strip(dr.m.View()), "batch 1 of "+strconv.Itoa(batches(len(dr.m.mv.keys))))

	dr.run(cmd)
	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Error("the move finished and the view still refuses to be thrown away")
	}
}

// A key pressed while the calls are still out is refused rather than starting
// something the move's own context would be cancelled by.
func TestMove_RefusesEveryKeyAndEveryFetchWhileItIsRunning(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(140))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.loadAll()
	dr.pickWholeBacklog()
	dr.key("m")
	dr.m.destAt = 0
	dr.key("enter")
	cmd := dr.hold(keyPress("y"))

	before := dr.m.cursor
	for _, stroke := range []string{"j", "k", "G", "space", "x", "m", "esc"} {
		if follow := dr.hold(keyPress(stroke)); follow != nil {
			t.Errorf("%q started something while a move was in flight", stroke)
		}
	}
	if dr.m.cursor != before {
		t.Errorf("a key moved the cursor from %d to %d while a move was in flight", before, dr.m.cursor)
	}
	reads := countCalls(c.Fake, "Boards")
	if follow := dr.hold(kernel.RefreshMsg{}); follow == nil {
		t.Error("a refresh during a move said nothing at all")
	}
	if got := countCalls(c.Fake, "Boards"); got != reads {
		t.Error("a refresh during a move re-read the board, which would cancel the move's own context")
	}
	dr.run(cmd)
}

func TestMove_WithoutAConnectionItSaysSoRatherThanFailing(t *testing.T) {
	t.Parallel()
	d := testDeps(nil)
	d.Jira = nil
	dr := newDriver(t, d, 100, 20)
	dr.send(MoveMsg{})
	if dr.m.mode != browsing {
		t.Errorf("a move opened the chooser with no connection behind it")
	}
	mustContain(t, dr.lastStatus().Text+strings.Join(statusTexts(dr), " "), "no Jira connection")
}

func statusTexts(dr *driver) []string {
	out := make([]string, 0, len(dr.statuses))
	for _, s := range dr.statuses {
		out = append(out, s.Text)
	}
	return out
}

// Dragging a row nobody picked is about that row. Dragging one of the picked
// ones is about all of them, and neither gesture quietly moves something the
// confirm did not name.
func TestMove_ADragMovesWhatItGrabbedOrTheWholeSelectionItIsPartOf(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		pick  []string
		grab  string
		wants []string
	}{
		"a row nobody picked": {
			pick: []string{"row:PROJ-6", "row:PROJ-7"}, grab: "row:PROJ-1", wants: []string{"PROJ-1"},
		},
		"one of the picked rows": {
			pick: []string{"row:PROJ-6", "row:PROJ-7"}, grab: "row:PROJ-7",
			wants: []string{"PROJ-6", "PROJ-7"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := newChunker(seeded(t))
			dr := newDriver(t, testDeps(c), 120, 24)
			for _, row := range tc.pick {
				dr.cursorTo(row)
				dr.key("space")
			}
			for i := range dr.m.rows {
				if dr.m.zoneOf(i) == tc.grab {
					got, ok := dr.m.draggedKeys(tc.grab)
					if !ok {
						t.Fatalf("the drag found nothing on %q", tc.grab)
					}
					if strings.Join(got, ",") != strings.Join(tc.wants, ",") {
						t.Errorf("dragging %q would move %v, want %v", tc.grab, got, tc.wants)
					}
					return
				}
			}
			t.Fatalf("no row is marked %q", tc.grab)
		})
	}
}

// A move takes the picks off the issues it moved and leaves the rest alone, so a
// selection somebody built up is not thrown away by a drag of one row.
func TestMove_OnlyUnpicksWhatItMoved(t *testing.T) {
	t.Parallel()
	c := newChunker(seeded(t))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.cursorTo("row:PROJ-6")
	dr.key("space")
	dr.cursorTo("row:PROJ-7")
	dr.key("space")

	dr.m.wanted = []string{"PROJ-6"}
	dr.m.destAt, dr.m.mode = 0, confirming
	dr.key("y")

	if dr.m.picked["PROJ-6"] {
		t.Error("PROJ-6 moved and is still picked")
	}
	if !dr.m.picked["PROJ-7"] {
		t.Error("PROJ-7 did not move and lost its pick")
	}
}

// A project change is a fact rather than a request, so a move it interrupts says
// how far it got instead of disappearing.
func TestMove_AProjectChangeMidMoveSaysWhatWasLeftBehind(t *testing.T) {
	t.Parallel()
	c := newChunker(newFake(140))
	dr := newDriver(t, testDeps(c), 120, 24)
	dr.loadAll()
	dr.pickWholeBacklog()
	dr.key("m")
	dr.m.destAt = 0
	dr.key("enter")
	cmd := dr.hold(keyPress("y"))

	dr.send(kernel.ProjectMsg{Project: "OTHER"})
	mustContain(t, dr.view(), "was left after 0 of", "moved to another project")
	if dr.m.mode != browsing {
		t.Errorf("the view is still in mode %d after the project changed under a move", dr.m.mode)
	}
	// The chunk that was already with the site lands on a view that has moved
	// on, and is dropped rather than drawn.
	dr.run(cmd)
	if dr.m.mv != nil {
		t.Error("a chunk of the abandoned move was picked back up")
	}
}
