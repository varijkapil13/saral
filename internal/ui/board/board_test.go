package board

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestBoard_DrawsTheColumnsTheBoardsOwnConfigurationDefines(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(24)), 120, 20)

	if !dr.m.ready {
		t.Fatalf("the board never read its configuration: %v", dr.m.failure)
	}
	if got := len(dr.m.plan.columns); got != 3 {
		t.Fatalf("the board drew %d columns, want the three its configuration maps", got)
	}
	total := 0
	for col := range dr.m.cols {
		total += dr.m.columnLen(col)
		for row := range dr.m.columnLen(col) {
			iss := dr.m.issueAt(col, row)
			at, mapped := dr.m.plan.columnOf(iss.Status.ID)
			if !mapped || at != col {
				t.Errorf("%s is in status %s and was drawn in column %d", iss.Key, iss.Status.ID, col)
			}
		}
	}
	if total+dr.m.unmapped != len(dr.m.issues) {
		t.Errorf("%d cards in columns and %d in none, out of %d read",
			total, dr.m.unmapped, len(dr.m.issues))
	}
	if dr.m.unmapped != 0 {
		t.Errorf("%d of this board's own issues landed in no column at all", dr.m.unmapped)
	}
	for col := range dr.m.cols {
		if dr.m.columnLen(col) == 0 {
			t.Errorf("column %d (%s) is empty; every status this board maps has issues in it",
				col, dr.m.plan.columns[col].name)
		}
	}
}

// A status the board maps into no column is an issue the board does not show,
// and the count is the only way a user can tell that from a project with fewer
// issues in it than they expected.
func TestBoard_AnIssueInAStatusNoColumnMapsIsCountedRatherThanDrawn(t *testing.T) {
	t.Parallel()
	cfg := jira.BoardConfig{BoardID: 3, Name: "Ledger", Type: jira.BoardScrum, Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
		{Name: "Under way", StatusIDs: []string{"10202"}},
	}}
	issues := []jira.Issue{
		{Key: "PROJ-1", Summary: "one", Status: jira.Status{ID: "10201", Name: "Triage"}},
		{Key: "PROJ-2", Summary: "two", Status: jira.Status{ID: "10202", Name: "Building"}},
		// The same display name as the column above maps, under an id it does
		// not: the shape a team-managed project mints for a second workflow.
		{Key: "PROJ-3", Summary: "three", Status: jira.Status{ID: "10204", Name: "Building"}},
	}
	_, dr := stocked(t, cfg, issues, 100, 16)

	if dr.m.unmapped != 1 {
		t.Fatalf("%d issues fell outside every column, want the one whose status the board maps nowhere", dr.m.unmapped)
	}
	if got := dr.column(1); !slices.Equal(got, []string{"PROJ-2"}) {
		t.Errorf("the second column holds %v; PROJ-3 shares a status name with PROJ-2 and not a status id", got)
	}
	mustContain(t, dr.view(), "1 in no column")
}

// The two gestures that move a card are one implementation, so the site is asked
// for exactly the same thing whichever of them was used.
func TestBoard_TheKeyboardAndThePointerMakeTheSameMove(t *testing.T) {
	t.Parallel()

	byKey := func(t *testing.T, dr *driver, d kernel.Deps) {
		t.Helper()
		dr.key("m", "l", "enter")
	}
	byPointer := func(t *testing.T, dr *driver, d kernel.Deps) {
		t.Helper()
		from := zoneOf(t, d, dr, cardZone("PROJ-3"))
		onto := zoneOf(t, d, dr, colZone(1))
		dr.send(tea.MouseClickMsg{X: from.StartX, Y: from.StartY, Button: tea.MouseLeft})
		dr.send(tea.MouseMotionMsg{X: onto.StartX, Y: onto.EndY, Button: tea.MouseLeft})
		dr.send(tea.MouseReleaseMsg{X: onto.StartX, Y: onto.EndY, Button: tea.MouseLeft})
	}

	for name, gesture := range map[string]func(*testing.T, *driver, kernel.Deps){
		"with the keys":    byKey,
		"with the pointer": byPointer,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			d := testDeps(fake)
			dr := newDriver(t, d, 120, 20)
			if got := dr.column(0); len(got) == 0 || got[0] != "PROJ-3" {
				t.Fatalf("the first column holds %v, want PROJ-3 at the top", got)
			}

			gesture(t, dr, d)

			if n := countCalls(fake, "Transitions"); n != 1 {
				t.Errorf("the site was asked for the issue's transitions %d times, want once at the moment of the drop", n)
			}
			if n := countCalls(fake, "Transition"); n != 1 {
				t.Fatalf("%d transitions were applied, want the one that lands in the column dropped on", n)
			}
			if got := dr.column(1); !slices.Contains(got, "PROJ-3") {
				t.Errorf("the second column holds %v, want PROJ-3 in it", got)
			}
			if got := dr.column(0); slices.Contains(got, "PROJ-3") {
				t.Errorf("the first column still holds %v", got)
			}
			mustContain(t, dr.lastStatus().Text, "PROJ-3", "In Progress")
		})
	}
}

// A column is reached by a transition whose target status the column maps, by
// id. The status a transition lands on is what decides it, never the words the
// column or the status are called.
func TestBoard_TheTransitionChosenIsTheOneWhoseTargetStatusTheColumnMaps(t *testing.T) {
	t.Parallel()
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
		{Name: "Under way", StatusIDs: []string{"10204"}},
	}}
	_, dr := stocked(t, cfg, []jira.Issue{
		{Key: "PROJ-1", Summary: "one", Status: jira.Status{ID: "10201", Name: "Triage"}},
	}, 100, 16)

	dr.key("m", "l")
	if dr.m.card == nil || dr.m.card.target != 1 {
		t.Fatalf("the card is not aimed at the second column: %+v", dr.m.card)
	}
	// Two moves land on statuses whose display name is the column's, and only
	// one of them lands on the status id the column actually maps.
	tr, found := dr.m.moveInto([]jira.Transition{
		{ID: "tr-a", Name: "Under way", To: jira.Status{ID: "10202", Name: "Under way"}},
		{ID: "tr-b", Name: "Start", To: jira.Status{ID: "10204", Name: "Building"}},
	}, 1)
	if !found {
		t.Fatal("no transition was found for a column one of them lands in")
	}
	if tr.ID != "tr-b" {
		t.Errorf("chose %q, want the move onto status 10204, which is the id the column maps", tr.ID)
	}
}

// The columns a board draws and the moves a workflow allows are two different
// things, and a column no move reaches has to say so rather than swallow the
// gesture.
func TestBoard_AColumnNoWorkflowMoveReachesIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
		{Name: "Unreachable", StatusIDs: []string{"10999"}},
	}}
	_, dr := stocked(t, cfg, []jira.Issue{
		{Key: "PROJ-1", Summary: "one", Status: jira.Status{ID: "10201", Name: "Triage"}},
	}, 100, 16)

	dr.key("m", "l")
	dr.send(movesMsg{gen: dr.m.gen, key: "PROJ-1", column: 1, moves: []jira.Transition{
		{ID: "tr-x", Name: "Somewhere else", To: jira.Status{ID: "10203", Name: "Shipped"}},
	}})

	if dr.m.card != nil {
		t.Error("the card is still in hand after a move nothing could make")
	}
	mustContain(t, dr.lastStatus().Text, "PROJ-1", "Triage", "Unreachable")
	if got := dr.lastStatus().Level; got != kernel.LevelWarn {
		t.Errorf("a refusal was reported at level %v, want a warning", got)
	}
}

// A transition insisting on a field cannot be made from a column drop, so the
// pane that fills a transition screen is handed the issue rather than a value
// being guessed for the field.
func TestBoard_ATransitionNeedingAScreenIsHandedToThePaneThatCanFillOne(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	d := testDeps(fake)
	dr := newDriver(t, d, 120, 20)

	// The done column's transition carries a required resolution on this site.
	dr.key("m", "l", "l", "enter")

	if n := countCalls(fake, "Transition"); n != 0 {
		t.Errorf("%d transitions were applied blind; the move needed a field this view cannot fill", n)
	}
	if len(dr.pushes) != 1 {
		t.Fatalf("%d views were pushed, want the transition pane", len(dr.pushes))
	}
	if got := dr.pushes[0].ID; got != issue.MoveViewID {
		t.Errorf("pushed %q, want %q", got, issue.MoveViewID)
	}
	if dr.m.card != nil {
		t.Error("the card is still in hand after the gesture was handed on")
	}
}

// Nothing is asked of the site until the gesture completes, and putting a card
// back is a gesture that completed by being cancelled.
func TestBoard_ACardPutBackAsksTheSiteForNothing(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	before := len(fake.Calls())

	dr.key("m", "l", "ctrl+g")

	if dr.m.card != nil {
		t.Error("the card is still in hand")
	}
	if got := fake.Calls()[before:]; len(got) != 0 {
		t.Errorf("the site was asked for %v after a card was put back", got)
	}
	if got := dr.column(0); !slices.Contains(got, "PROJ-3") {
		t.Errorf("the first column holds %v, want the card back where it came from", got)
	}
}

// Aiming a card back at the column it came from is not a move, and asking the
// site to transition an issue to the status it is already in is what would
// otherwise happen.
func TestBoard_DroppingACardWhereItCameFromAsksForNothing(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	before := len(fake.Calls())

	dr.key("m", "enter")

	if got := fake.Calls()[before:]; len(got) != 0 {
		t.Errorf("the site was asked for %v to move a card to where it already is", got)
	}
	mustContain(t, dr.lastStatus().Text, "already in")
}

func TestBoard_SaysWhatFailedAndWhatToDoAboutIt(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		err   error
		fails int
		said  string
	}{
		"a token that may not read boards": {
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "your token cannot see this project's boards"},
			said: "your token cannot see this project's boards",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/board"},
			said: "rate limited by Jira",
		},
		"a transport failure": {
			err:  &jira.TransportError{Op: "GET /board", Status: 502},
			said: "GET /board failed with HTTP 502",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			fake.FailNextN(4, tc.err)
			dr := newDriver(t, testDeps(fake), 100, 16)

			if dr.m.failure == nil {
				t.Fatal("the board reports no failure after every read was refused")
			}
			frame := dr.view()
			mustContain(t, frame, tc.said)
			mustContain(t, frame, kernel.DefaultGlobalKeys().Refresh.Help().Key)
			mustNotContain(t, frame, "Searching for the issues")
		})
	}
}

// A read the board no longer wants an answer to is dropped rather than drawn.
func TestBoard_AnAnswerToAQuestionThatHasMovedOnIsDropped(t *testing.T) {
	t.Parallel()
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
	}}
	_, dr := stocked(t, cfg, []jira.Issue{
		{Key: "PROJ-1", Summary: "one", Status: jira.Status{ID: "10201"}},
	}, 100, 16)

	stale := dr.m.gen - 1
	dr.send(issuesMsg{gen: stale, issues: []jira.Issue{
		{Key: "PROJ-9", Summary: "late", Status: jira.Status{ID: "10201"}},
	}})

	if got := dr.column(0); !slices.Equal(got, []string{"PROJ-1"}) {
		t.Errorf("the column holds %v; an answer to a question the board had moved on from was drawn", got)
	}
}

// Losing the keyboard is not being closed. A board parked while the palette is
// open still wants the answer it asked for; a board that has been discarded does
// not.
func TestBoard_KeepsItsReadOnABlurAndDropsItOnAClose(t *testing.T) {
	t.Parallel()
	fake := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))
	fake.Delay(50 * time.Millisecond)
	view, ok := New(testDeps(fake)).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	cmd := view.Init()
	if cmd == nil {
		t.Fatal("Init asked for nothing")
	}
	next, _ := view.Update(kernel.FocusMsg{Focused: false})
	view, _ = next.(*Model)
	if view.cancel == nil {
		t.Fatal("losing the keyboard let go of the read; a parked board still wants its answer")
	}
	view.Close()
	if view.cancel != nil {
		t.Error("a discarded board is still holding a read open")
	}
}

func TestBoard_AProjectWithNoBoardSaysSoRatherThanLookingBroken(t *testing.T) {
	t.Parallel()
	fake := jiratest.New(jiratest.WithProject("PROJ", jiratest.NoBoard))
	dr := newDriver(t, testDeps(fake), 100, 16)

	if dr.m.failure != nil {
		t.Fatalf("a project with no board was reported as a failure: %v", dr.m.failure)
	}
	mustContain(t, dr.view(), "No board draws on PROJ")
}

// A token that may not read boards is told so in the probe's own words, and the
// view says it rather than failing on every read behind it.
func TestBoard_ATokenThatMayNotReadBoardsIsGivenTheProbesOwnReason(t *testing.T) {
	t.Parallel()
	const reason = "you need the Browse Projects permission on PROJ"
	d := testDeps(newFake(9))
	d.Caps.Boards = jira.Capability{Reason: reason}
	dr := newDriver(t, d, 100, 16)

	mustContain(t, dr.view(), reason)
	if n := countCalls(d.Jira.(*jiratest.Fake), "Boards"); n != 0 {
		t.Errorf("the site was asked for boards %d times by a session that may not read them", n)
	}
}

// Only the columns and the rows that fit are built. A board of six thousand
// cards has to cost what a board of six costs per frame.
func TestBoard_OnlyTheColumnsAndRowsThatFitAreDrawn(t *testing.T) {
	t.Parallel()
	columns := make([]jira.Column, 0, 8)
	for i := range 8 {
		columns = append(columns, jira.Column{Name: "Column " + string(rune('A'+i)), StatusIDs: []string{strconv.Itoa(i)}})
	}
	issues := make([]jira.Issue, 0, 400)
	for i := range 400 {
		issues = append(issues, jira.Issue{
			Key: "PROJ-" + strconv.Itoa(i), Summary: "card",
			Status: jira.Status{ID: strconv.Itoa(i % 8)},
		})
	}
	_, dr := stocked(t, jira.BoardConfig{BoardID: 1, Name: "Wide", Columns: columns}, issues, 100, 20)

	if dr.m.lay.cols >= len(columns) {
		t.Fatalf("all %d columns were drawn into 100 cells", dr.m.lay.cols)
	}
	frame := dr.view()
	if strings.Contains(frame, "Column H") {
		t.Error("a column past the right-hand edge was drawn")
	}
	mustContain(t, frame, "8 columns", "shown")
	lines := strings.Split(frame, "\n")
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > 100 {
			t.Fatalf("line %d is %d cells wide, over the 100 the view was given", i, got)
		}
	}
	if len(lines) != 20 {
		t.Errorf("the board drew %d lines into a box of 20", len(lines))
	}
}

// The estimation field comes from the board configuration and is asked for by
// the id the site gave it, which is the only way a custom field can be read
// without one being written down here.
func TestBoard_AsksForTheBoardsOwnEstimationFieldAndNeverAWildcard(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)

	if !dr.m.plan.estimates {
		t.Fatal("this board estimates in a field and the plan says it does not")
	}
	if !strings.HasPrefix(dr.m.plan.estimate.ID, "customfield_") {
		t.Fatalf("the estimation field is %q, want the site's own custom field id", dr.m.plan.estimate.ID)
	}
	asked := dr.m.plan.projection().IDs
	if !slices.Contains(asked, dr.m.plan.estimate.ID) {
		t.Errorf("the projection asks for %v, without the field the board estimates in", asked)
	}
	for _, wildcard := range []string{jira.FieldsAll, jira.FieldsNavigable} {
		if slices.Contains(asked, wildcard) {
			t.Errorf("the projection asks for %s", wildcard)
		}
	}
}

// A refresh re-reads the cards; a refetch re-reads the board's shape too,
// because a column an administrator added is not a change to the cards.
func TestBoard_RefreshReadsTheCardsAndRefetchReadsTheBoardAgain(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	configs := countCalls(fake, "BoardConfig")

	dr.send(kernel.RefreshMsg{})
	if got := countCalls(fake, "BoardConfig"); got != configs {
		t.Errorf("a plain refresh read the board's columns again (%d then %d)", configs, got)
	}
	dr.send(kernel.RefreshMsg{Purge: true})
	if got := countCalls(fake, "BoardConfig"); got <= configs {
		t.Error("a refetch did not read the board's columns again")
	}
}

// A board is three questions, and an empty pane that cannot say which of them is
// outstanding is a pane that looks like a hang.
func TestBoard_SaysWhichOfItsThreeQuestionsIsOutstanding(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		step step
		said string
	}{
		"which boards there are": {step: stepBoards, said: "Asking which boards"},
		"what this board is":     {step: stepConfig, said: "columns"},
		"what is on it":          {step: stepIssues, said: "Searching for the issues"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := testDeps(nil)
			dr := newDriver(t, d, 100, 16)
			dr.m.loading, dr.m.step = true, tc.step
			dr.m.deps.Jira = newFake(0)
			dr.m.forget()
			mustContain(t, dr.view(), tc.said)
		})
	}
}
