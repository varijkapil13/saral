package board

import (
	"errors"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var ada = jira.User{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true}

// refusals are the three ways a write comes back refused that every write path
// here has to survive.
func refusals() map[string]error {
	return map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Schedule Issues in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "PUT /issue/rank", Err: errors.New("connection reset")},
	}
}

func TestRank_KMovesTheCardAboveTheOneBeforeItAndTellsTheSite(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	before := dr.column(0)
	dr.moveTo(0, 2)

	dr.key("K")

	want := slices.Clone(before)
	want[1], want[2] = want[2], want[1]
	if got := dr.column(0); !slices.Equal(got, want) {
		t.Errorf("the first column reads %v after K, want %v", got, want)
	}
	if got := dr.m.selectedKey(); got != before[2] {
		t.Errorf("the cursor is on %s, want it to stay on the card it moved, %s", got, before[2])
	}
	if got := countCalls(fake, "RankIssues"); got != 1 {
		t.Errorf("K made %d rank calls, want 1", got)
	}
	mustContain(t, dr.lastStatus().Text, before[2]+" now sits above "+before[1])
	if dr.m.rank != nil {
		t.Error("a rank the site accepted is still marked as in flight")
	}
}

func TestRank_EveryDirectionLandsWhereItSays(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		row  int
		key  string
		want func([]string) []string
	}{
		"J moves it below the next one": {row: 0, key: "J", want: func(c []string) []string {
			return append([]string{c[1], c[0]}, c[2:]...)
		}},
		"{ puts it first": {row: 2, key: "{", want: func(c []string) []string {
			return append([]string{c[2], c[0], c[1]}, c[3:]...)
		}},
		"} puts it last": {row: 0, key: "}", want: func(c []string) []string {
			return append(slices.Clone(c[1:]), c[0])
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			dr := newDriver(t, testDeps(fake), 120, 20)
			before := dr.column(0)
			dr.moveTo(0, tc.row)
			dr.key(tc.key)
			if got, want := dr.column(0), tc.want(before); !slices.Equal(got, want) {
				t.Errorf("the column reads %v, want %v", got, want)
			}
			if got := countCalls(fake, "RankIssues"); got != 1 {
				t.Errorf("%s made %d rank calls, want 1", tc.key, got)
			}
		})
	}
}

// The site's own order is what a re-read comes back in, so a rank that landed
// is still where it was put when the board is read again.
func TestRank_TheSiteAgreesOnceTheBoardIsReadAgain(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.moveTo(0, 2)
	dr.key("{")
	moved := dr.column(0)
	dr.send(kernel.RefreshMsg{})
	if got := dr.column(0); !slices.Equal(got, moved) {
		t.Errorf("a re-read of the board draws %v, want the ranked order %v", got, moved)
	}
}

func TestRank_ARefusalPutsTheCardBack(t *testing.T) {
	t.Parallel()
	errs := refusals()
	errs["a 207 that refused the card"] = &jira.PartialRankError{
		Failed: []jira.RankFailure{{Key: "PROJ-6", Reason: "You cannot rank this issue."}},
	}
	for name, err := range errs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			dr := newDriver(t, testDeps(fake), 120, 20)
			before := dr.column(0)
			dr.moveTo(0, 2)
			fake.FailNext(err)
			dr.key("K")

			if got := dr.column(0); !slices.Equal(got, before) {
				t.Errorf("the column reads %v after a refused rank, want it put back to %v", got, before)
			}
			if got := dr.m.selectedKey(); got != before[2] {
				t.Errorf("the cursor is on %s, want it on %s where it was put back", got, before[2])
			}
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
			if dr.m.rank != nil || dr.m.stale {
				t.Error("a refused rank left the board ranking or badged stale")
			}
		})
	}
}

// A 207 that names the one card sent among the ranked is the rank landing.
func TestRank_APartialAnswerThatRankedTheCardIsASuccess(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	before := dr.column(0)
	dr.moveTo(0, 1)
	fake.FailNext(&jira.PartialRankError{Ranked: []string{before[1]}})
	dr.key("K")
	if got := dr.column(0); got[0] != before[1] {
		t.Errorf("the column reads %v, want %s first", got, before[1])
	}
	if got := dr.lastStatus().Level; got == kernel.LevelError {
		t.Errorf("a rank the site accepted was reported as a failure: %s", dr.lastStatus().Text)
	}
}

// Two steps taken before the site answers the first go to the site one after
// the other, and the second names where the card is on screen by then.
func TestRank_StepsTakenWhileOneIsOutAreSentInOrder(t *testing.T) {
	t.Parallel()
	fake := newFake(12)
	dr := newDriver(t, testDeps(fake), 120, 20)
	before := dr.column(0)
	dr.moveTo(0, 3)

	first := dr.m.reorder(rankUp)
	if second := dr.m.reorder(rankUp); second != nil {
		t.Fatal("a second step was sent while the first was still out")
	}
	dr.run(first)

	if got := countCalls(fake, "RankIssues"); got != 2 {
		t.Fatalf("two steps made %d rank calls, want 2", got)
	}
	want := []string{before[0], before[3], before[1], before[2]}
	if got := dr.column(0)[:4]; !slices.Equal(got, want) {
		t.Errorf("the column reads %v, want %v", got, want)
	}
	dr.send(kernel.RefreshMsg{})
	if got := dr.column(0)[:4]; !slices.Equal(got, want) {
		t.Errorf("the site holds %v after both steps, want %v", got, want)
	}
}

// A second step refused puts the card back where the first, accepted, left it.
func TestRank_ARefusedSecondStepKeepsTheFirst(t *testing.T) {
	t.Parallel()
	fake := newFake(12)
	dr := newDriver(t, testDeps(fake), 120, 20)
	before := dr.column(0)
	dr.moveTo(0, 3)

	first := dr.m.reorder(rankUp)
	_ = dr.m.reorder(rankUp)
	msg := first()
	reply, ok := msg.(kernel.ReplyMsg)
	if !ok {
		t.Fatalf("the rank answered %T", msg)
	}
	fake.FailNext(&jira.CapabilityError{Reason: "you need Schedule Issues in this project"})
	dr.send(reply.Msg)

	want := []string{before[0], before[1], before[3], before[2]}
	if got := dr.column(0)[:4]; !slices.Equal(got, want) {
		t.Errorf("the column reads %v, want the first step kept: %v", got, want)
	}
}

func TestRank_ABoardThatDoesNotRankSaysSoAndAsksNothing(t *testing.T) {
	t.Parallel()
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Type: jira.BoardKanban, Columns: []jira.Column{
		{Name: "Waiting", StatusIDs: []string{"10201"}},
	}}
	issues := []jira.Issue{
		{Key: "PROJ-1", Status: jira.Status{ID: "10201"}},
		{Key: "PROJ-2", Status: jira.Status{ID: "10201"}},
	}
	_, dr := stocked(t, cfg, issues, 100, 16)
	dr.m.deps.Jira = newFake(1)
	dr.moveTo(0, 1)
	dr.key("K")
	mustContain(t, dr.lastStatus().Text, "ordered by its filter")
	if got := dr.column(0); !slices.Equal(got, []string{"PROJ-1", "PROJ-2"}) {
		t.Errorf("the column reads %v after a refused rank", got)
	}
}

func TestRank_TheEdgesSaySoRatherThanAskingTheSite(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.moveTo(0, 0)
	dr.key("K")
	mustContain(t, dr.lastStatus().Text, "already first")
	dr.moveTo(0, dr.m.columnLen(0)-1)
	dr.key("J")
	mustContain(t, dr.lastStatus().Text, "already last")
	if got := countCalls(fake, "RankIssues"); got != 0 {
		t.Errorf("a card at the edge made %d rank calls", got)
	}
}

// The last card loaded is not the last card while more are still to come.
func TestRank_BottomWaitsForTheWholeColumn(t *testing.T) {
	t.Parallel()
	fake := newFake(300)
	dr := heldDriver(t, testDeps(fake), 120, 20)
	if !dr.m.more {
		t.Fatal("the board read every card in one page; this test needs more than one")
	}
	dr.key("}")
	mustContain(t, dr.lastStatus().Text, "still loading")
	if got := countCalls(fake, "RankIssues"); got != 0 {
		t.Errorf("} made %d rank calls on a board part way through its read", got)
	}
}

func TestRank_ADragWithinAColumnRanksTheCardWhereItWasDropped(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	d := testDeps(fake)
	dr := newDriver(t, d, 120, 20)
	before := dr.column(0)

	pressOn(t, d, dr, cardZone(before[0]))
	to := zoneOf(t, d, dr, cardZone(before[2]))
	dr.send(tea.MouseMotionMsg{X: to.StartX, Y: to.StartY, Button: tea.MouseLeft})
	if dr.m.card != nil {
		t.Fatal("a drag inside its own column picked the card up for a column move")
	}
	dr.send(tea.MouseReleaseMsg{X: to.StartX, Y: to.StartY, Button: tea.MouseLeft})

	want := append([]string{before[1], before[2], before[0]}, before[3:]...)
	if got := dr.column(0); !slices.Equal(got, want) {
		t.Errorf("the column reads %v after the drag, want %v", got, want)
	}
	if got := countCalls(fake, "RankIssues"); got != 1 {
		t.Errorf("the drag made %d rank calls, want 1", got)
	}
}

func TestShift_LLandsTheCardInTheNextColumnInOneStroke(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	key := dr.column(0)[0]
	dr.moveTo(0, 0)

	dr.key("L")

	if !slices.Contains(dr.column(1), key) {
		t.Errorf("the second column holds %v after L, want %s in it", dr.column(1), key)
	}
	if got := countCalls(fake, "Transition"); got != 1 {
		t.Errorf("L made %d transitions, want 1", got)
	}
	if dr.m.card != nil || dr.m.moving {
		t.Error("L left a card in hand")
	}
}

func TestShift_HOnTheFirstColumnSaysSo(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.moveTo(0, 0)
	dr.key("H")
	mustContain(t, dr.lastStatus().Text, "already in the first column")
	if got := countCalls(fake, "Transitions"); got != 0 {
		t.Errorf("H at the edge asked for %d transition lists", got)
	}
}

func TestShift_ARefusalPutsTheCardBack(t *testing.T) {
	t.Parallel()
	for name, err := range refusals() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			dr := newDriver(t, testDeps(fake), 120, 20)
			key := dr.column(0)[0]
			dr.moveTo(0, 0)
			fake.FailNext(err)
			dr.key("L")
			if !slices.Contains(dr.column(0), key) || dr.m.card != nil {
				t.Errorf("a refused L left %s out of its column", key)
			}
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
		})
	}
}

func TestMine_OToggleIsAnAssigneeTermTheBarNames(t *testing.T) {
	t.Parallel()
	fake := newFake(12, jiratest.WithMe(ada))
	dr := newDriver(t, testDeps(fake), 120, 20)

	dr.key("o")
	want := filter.Term{Facet: filter.FacetAssignee, ID: ada.AccountID, Label: ada.DisplayName}
	if !dr.m.terms.Has(want) || len(dr.m.terms) != 1 {
		t.Fatalf("o put %v in force, want only %v", dr.m.terms, want)
	}
	for c := range dr.m.cols {
		for r := range dr.m.cols[c] {
			if iss := dr.m.issueAt(c, r); iss.Assignee == nil || iss.Assignee.AccountID != ada.AccountID {
				t.Errorf("%s is on the board under only-mine", iss.Key)
			}
		}
	}
	mustContain(t, dr.view(), "Ada Lovelace")

	dr.key("o")
	if len(dr.m.terms) != 0 {
		t.Errorf("a second o left %v in force", dr.m.terms)
	}
	if got := countCalls(fake, "Me"); got != 1 {
		t.Errorf("two toggles asked who this is %d times, want once", got)
	}
}

// Only mine replaces whoever else the assignee facet names, and leaves the
// other facets alone.
func TestMine_ReplacesOtherAssigneesAndKeepsOtherFacets(t *testing.T) {
	t.Parallel()
	grace := filter.Term{Facet: filter.FacetAssignee, ID: "acct-grace", Label: "Grace Hopper"}
	bug := filter.Term{Facet: filter.FacetType, ID: "10004", Label: "Bug"}
	got := mineToggled(filter.Terms{grace, bug}, ada)
	mine := filter.Term{Facet: filter.FacetAssignee, ID: ada.AccountID}
	if !got.Has(mine) || got.Has(grace) || !got.Has(bug) || len(got) != 2 {
		t.Errorf("only mine over %v gave %v", filter.Terms{grace, bug}, got)
	}
}

func TestMine_ASiteThatWillNotSayWhoThisIsIsReported(t *testing.T) {
	t.Parallel()
	for name, err := range refusals() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(6, jiratest.WithMe(ada))
			dr := newDriver(t, testDeps(fake), 120, 20)
			fake.FailNext(err)
			dr.key("o")
			if len(dr.m.terms) != 0 {
				t.Errorf("a refused Me put %v in force", dr.m.terms)
			}
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
			dr.key("o")
			if len(dr.m.terms) != 1 {
				t.Error("o did not ask again after a refusal")
			}
		})
	}
}

func typeInto(dr *driver, s string) {
	for _, r := range s {
		dr.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestFind_TypingMovesTheCursorAndNWalksTheMatches(t *testing.T) {
	t.Parallel()
	fake := newFake(24)
	d := testDeps(fake)
	dr := newDriver(t, d, 120, 20)

	dr.key("/")
	if !dr.m.WantsRawKeys() {
		t.Fatal("the search prompt does not take the keyboard, so the kernel would spend its digits")
	}
	typeInto(dr, "proj-1")
	if got := dr.m.selectedKey(); !containsFold(got, "proj-1") {
		t.Fatalf("typing proj-1 left the cursor on %s", got)
	}
	golden(t, "find_120x20.golden", dr.view())
	dr.key("enter")
	if dr.m.finding || dr.m.WantsRawKeys() {
		t.Fatal("enter left the prompt open")
	}

	seen := map[string]bool{dr.m.selectedKey(): true}
	for range 20 {
		dr.key("n")
		key := dr.m.selectedKey()
		if !containsFold(key, "proj-1") {
			t.Fatalf("n landed on %s, which does not match", key)
		}
		seen[key] = true
	}
	var want int
	for i := range dr.m.issues {
		if matchesNeedle(&dr.m.issues[i], "proj-1") {
			want++
		}
	}
	if len(seen) != want {
		t.Errorf("n visited %d cards, want every one of the %d that match", len(seen), want)
	}
	at := dr.m.selectedKey()
	dr.key("n", "N")
	if got := dr.m.selectedKey(); got != at {
		t.Errorf("n then N landed on %s, want back on %s", got, at)
	}
}

func TestFind_EscGoesBackAndANeedleNothingMatchesSaysSo(t *testing.T) {
	t.Parallel()
	fake := newFake(12)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.moveTo(1, 1)
	start := dr.m.selectedKey()

	dr.key("/")
	typeInto(dr, "qqqq")
	mustContain(t, dr.view(), "no card matches")
	if got := dr.m.selectedKey(); got != start {
		t.Errorf("a search that matched nothing moved the cursor to %s", got)
	}
	dr.key("esc")
	if dr.m.finding || dr.m.needle != "" {
		t.Error("esc left a search in force")
	}
	if got := dr.m.selectedKey(); got != start {
		t.Errorf("esc left the cursor on %s, want back on %s", got, start)
	}
	dr.key("n")
	mustContain(t, dr.lastStatus().Text, "nothing is being searched for")
}

func TestContainsFold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		s, sub string
		want   bool
	}{
		{"PROJ-12", "proj-1", true},
		{"Fix the Search index", "SEARCH", true},
		{"Déjà vu", "DÉJÀ", true},
		{"short", "longer than it", false},
		{"anything", "", true},
		{"PROJ-2", "proj-3", false},
	} {
		if got := containsFold(tc.s, tc.sub); got != tc.want {
			t.Errorf("containsFold(%q, %q) = %v, want %v", tc.s, tc.sub, got, tc.want)
		}
	}
}

func TestSprintLine_DaysLeftAndProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		end  time.Time
		want string
	}{
		{now.Add(4 * 24 * time.Hour), "4 days left"},
		{now.Add(20 * time.Hour), "1 day left"},
		{now.Add(2 * time.Hour), "ends today"},
		{now.Add(-24 * time.Hour), "ended yesterday"},
		{now.Add(-72 * time.Hour), "ended 3 days ago"},
	} {
		m := &Model{sprint: jira.Sprint{End: &tc.end}}
		if got := m.daysLeft(now); got != tc.want {
			t.Errorf("a sprint ending %s reads %q, want %q", tc.end, got, tc.want)
		}
	}

	points := jira.FieldRef{ID: "customfield_13401", Name: "Story Points"}
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Type: jira.BoardScrum, Columns: []jira.Column{
		{Name: "To Do", StatusIDs: []string{"1"}}, {Name: "Done", StatusIDs: []string{"3"}}, {Name: "Unused"},
	}}
	card := func(key, status string, pts float64) jira.Issue {
		iss := jira.Issue{Key: key, Status: jira.Status{ID: status}}
		if pts > 0 {
			iss.Fields = iss.Fields.With(points, jira.FieldValue{Kind: jira.KindNumber, Number: pts})
		}
		return iss
	}
	issues := []jira.Issue{card("PROJ-1", "1", 3), card("PROJ-2", "3", 5), card("PROJ-3", "3", 0), card("PROJ-4", "9", 8)}

	_, counted := stocked(t, cfg, issues, 100, 16)
	if done, total, unit := counted.m.sprintDone(); done != 2 || total != 3 || unit != "issues" {
		t.Errorf("a board that does not estimate counts %v of %v %s, want 2 of 3 issues", done, total, unit)
	}
	estimated := cfg
	estimated.Estimation = &jira.Estimation{Type: jira.EstimationField, Field: points}
	_, pointed := stocked(t, estimated, issues, 100, 16)
	if done, total, unit := pointed.m.sprintDone(); done != 5 || total != 8 || unit != "Story Points" {
		t.Errorf("an estimating board counts %v of %v %s, want 5 of 8 Story Points", done, total, unit)
	}
}

func TestSprintLine_OnlyARunningSprintHasOne(t *testing.T) {
	t.Parallel()
	fake := newFake(6)
	dr := newDriver(t, testDeps(fake), 120, 20)
	mustContain(t, dr.view(), "Goal: Make it usable", "days left", "done")

	kanban := jiratest.New(jiratest.WithProject("PROJ", jiratest.Kanban), jiratest.WithIssues(jiratest.Gen(6)))
	plain := newDriver(t, testDeps(kanban), 120, 20)
	mustNotContain(t, plain.view(), "Goal:", "days left")
}

func (d *driver) moveTo(col, row int) {
	d.t.Helper()
	d.m.moveTo(col, row)
	if d.m.curCol != col || d.m.curRow != row {
		d.t.Fatalf("the cursor could not be put on column %d row %d", col, row)
	}
}
