package board

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func siteIssue(t *testing.T, f *jiratest.Fake, key string) jira.Issue {
	t.Helper()
	iss, err := f.Issue(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

func (d *driver) typeText(text string) {
	d.t.Helper()
	for _, r := range text {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// pickFirst picks the first n cards of a column with space.
func (d *driver) pickFirst(col, n int) []string {
	d.t.Helper()
	d.moveTo(col, 0)
	keys := slices.Clone(d.column(col)[:n])
	for range n {
		d.key("space")
	}
	return keys
}

func parkSteps(msg tea.Msg) bool {
	_, step := msg.(bulkStepMsg)
	return step
}

func TestPick_SpaceVAndXPickAndLetGo(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(9)), 120, 20)
	keys := dr.pickFirst(0, 2)
	if len(dr.m.picked) != 2 || !dr.m.picked[keys[0]] || !dr.m.picked[keys[1]] {
		t.Fatalf("space twice picked %v, want %v", dr.m.picked, keys)
	}
	if got := dr.m.selectedKey(); got != dr.column(0)[2] {
		t.Errorf("space left the cursor on %s, want it moved on to the next card", got)
	}
	dr.key("x")
	if len(dr.m.picked) != 0 {
		t.Errorf("x left %d picked", len(dr.m.picked))
	}
	dr.key("v")
	if len(dr.m.picked) != dr.m.columnLen(dr.m.curCol) {
		t.Errorf("v picked %d of a column of %d", len(dr.m.picked), dr.m.columnLen(dr.m.curCol))
	}
	dr.key("v")
	if len(dr.m.picked) != 0 {
		t.Errorf("v on a column already picked left %d picked, want it let go", len(dr.m.picked))
	}
	dr.pickFirst(0, 1)
	if !dr.m.WantsBack() {
		t.Fatal("a board with a card picked does not claim esc")
	}
	dr.key("esc")
	if len(dr.m.picked) != 0 {
		t.Errorf("esc left %d picked", len(dr.m.picked))
	}
}

func TestPick_Golden(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		keys   []string
		golden string
	}{
		"two cards picked":            {golden: "picked_120x20.golden"},
		"asking who they go to":       {keys: []string{"@"}, golden: "picked_assign_120x20.golden"},
		"waiting for the go-ahead":    {keys: []string{"+", "u", "r", "g", "e", "n", "t", "enter"}, golden: "picked_confirm_120x20.golden"},
		"the picked set aimed onward": {keys: []string{"m", "l"}, golden: "picked_held_120x20.golden"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := newDriver(t, testDeps(newFake(9)), 120, 20)
			dr.pickFirst(0, 2)
			dr.key(tc.keys...)
			golden(t, tc.golden, dr.view())
		})
	}
}

func TestBulk_AssignFindsThePersonAndChangesEveryPickedCard(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	keys := dr.pickFirst(0, 2)
	dr.key("@")
	dr.typeText("grace")
	if b := dr.m.bulk; b == nil || len(b.found) == 0 || b.found[0].AccountID != grace.AccountID {
		t.Fatalf("typing a name found %+v, want Grace first", dr.m.bulk)
	}
	dr.key("enter")
	if countCalls(fake, "UpdateIssue") != 0 {
		t.Fatal("choosing the person changed a card before the go-ahead")
	}
	mustContain(t, dr.view(), "assign 2 cards to Grace Hopper?")
	dr.key("enter")

	for _, key := range keys {
		if got := siteIssue(t, fake, key); got.Assignee == nil || got.Assignee.AccountID != grace.AccountID {
			t.Errorf("%s is assigned to %+v on the site, want Grace", key, got.Assignee)
		}
		if got := dr.m.byKey(key); got.Assignee == nil || got.Assignee.AccountID != grace.AccountID {
			t.Errorf("%s is drawn assigned to %+v, want Grace", key, got.Assignee)
		}
	}
	mustContain(t, dr.lastStatus().Text, "assigned 2 cards to Grace Hopper")
	if len(dr.m.picked) != 0 || dr.m.bulk != nil {
		t.Errorf("a finished run left %d picked and bulk %+v", len(dr.m.picked), dr.m.bulk)
	}
}

// Nothing typed offers nobody, so a set can be unassigned from the same prompt.
func TestBulk_AssignToNobodyUnassigns(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	keys := dr.pickFirst(0, 2)
	dr.key("@", "enter", "enter")
	for _, key := range keys {
		if got := siteIssue(t, fake, key); got.Assignee != nil {
			t.Errorf("%s is still assigned to %s", key, got.Assignee.DisplayName)
		}
	}
	mustContain(t, dr.lastStatus().Text, "unassigned 2 cards")
}

func TestBulk_TheGoAheadCannotBeSkipped(t *testing.T) {
	t.Parallel()
	for name, keys := range map[string][]string{
		"esc at the question":             {"+", "a", "esc"},
		"esc at the confirmation":         {"+", "a", "enter", "esc"},
		"another key at the confirmation": {"+", "a", "enter", "j", "l", "m"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			dr := newDriver(t, testDeps(fake), 120, 20)
			dr.pickFirst(0, 2)
			dr.key(keys...)
			if got := countCalls(fake, "UpdateIssue"); got != 0 {
				t.Errorf("%v changed %d cards without the go-ahead", keys, got)
			}
		})
	}
}

func TestBulk_MoveTakesEachCardThroughItsOwnTransition(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	keys := dr.pickFirst(0, 3)
	dr.key("m", "l", "enter")
	if countCalls(fake, "Transition") != 0 {
		t.Fatal("landing the set moved cards before the go-ahead")
	}
	mustContain(t, dr.view(), "move 3 cards to "+dr.m.plan.columns[1].name+"?")
	dr.key("enter")
	for _, key := range keys {
		if !slices.Contains(dr.column(1), key) {
			t.Errorf("%s is not drawn in the second column: %v", key, dr.column(1))
		}
		got := siteIssue(t, fake, key)
		if at, _ := dr.m.plan.columnOf(got.Status.ID); at != 1 {
			t.Errorf("%s is in %s on the site, want the second column", key, got.Status.Name)
		}
	}
	if got := countCalls(fake, "Transitions"); got != 3 {
		t.Errorf("the move read the transitions %d times, want once per card", got)
	}
}

// Done needs a resolution on the fake's workflow, which a bulk move cannot
// fill, so every card says why it did not move and stays picked.
func TestBulk_AMoveThatNeedsAScreenIsReportedPerCard(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	keys := dr.pickFirst(0, 2)
	dr.key("m", "l", "l", "enter", "enter")
	got := dr.lastStatus()
	if got.Level != kernel.LevelError {
		t.Errorf("the report is at level %v: %q", got.Level, got.Text)
	}
	mustContain(t, got.Text, keys[0]+", "+keys[1]+" did not change", "needs a field filled in")
	if len(dr.m.picked) != 2 {
		t.Errorf("%d cards are still picked, want both, so the same gesture tries them again", len(dr.m.picked))
	}
}

func TestBulk_APartialFailureSaysWhichChangedAndKeepsTheRestPicked(t *testing.T) {
	t.Parallel()
	for name, err := range refusals() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			dr := newDriver(t, testDeps(fake), 120, 20)
			keys := dr.pickFirst(0, 3)
			dr.key("+")
			dr.typeText("urgent")
			dr.key("enter")
			fake.FailNext(nil)
			fake.FailNext(err)
			dr.key("enter")

			for i, key := range keys {
				labelled := slices.Contains(siteIssue(t, fake, key).Labels, "urgent")
				if labelled == (i == 1) {
					t.Errorf("%s labelled = %v on the site", key, labelled)
				}
			}
			got := dr.lastStatus()
			if got.Level != kernel.LevelError {
				t.Errorf("the report is at level %v: %q", got.Level, got.Text)
			}
			reason, _ := jira.Reason(err)
			mustContain(t, got.Text, "added the label urgent to 2 cards of the 3", keys[1]+" did not change: "+reason)
			if len(dr.m.picked) != 1 || !dr.m.picked[keys[1]] {
				t.Errorf("picked after the run is %v, want only %s", dr.m.picked, keys[1])
			}
		})
	}
}

func TestBulk_ALabelWithASpaceIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.pickFirst(0, 1)
	dr.key("+")
	dr.typeText("two words")
	dr.key("enter")
	mustContain(t, dr.lastStatus().Text, "cannot contain a space")
	if dr.m.bulk == nil || dr.m.bulk.stage != stageAskLabel {
		t.Error("a refused label left the question")
	}
}

func TestBulk_ThePersonSearchFailingIsSaidAndChoosesNobody(t *testing.T) {
	t.Parallel()
	for name, err := range refusals() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(9)
			dr := newDriver(t, testDeps(fake), 120, 20)
			dr.pickFirst(0, 1)
			dr.key("@")
			fake.FailNext(err)
			dr.typeText("g")
			if got := dr.lastStatus(); got.Level != kernel.LevelError {
				t.Errorf("the failed search was said at level %v", got.Level)
			}
			reason, _ := jira.Reason(err)
			mustContain(t, dr.view(), reason[:min(len(reason), 30)])
			dr.key("enter")
			if dr.m.bulk.stage != stageAskPerson {
				t.Error("enter went on to confirm with nobody found")
			}
		})
	}
}

func TestBulk_AssignIsRefusedWithoutThePeopleCapability(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(9))
	d.Caps.People = jira.Capability{Reason: "this token may not browse users"}
	dr := newDriver(t, d, 120, 20)
	dr.key("@")
	if dr.m.bulk != nil {
		t.Fatal("@ opened the person question without the capability to answer it")
	}
	mustContain(t, dr.lastStatus().Text, "this token may not browse users")
}

// A run in progress shows how far it is, refuses every other key, stops on
// ctrl+g after the card in flight and keeps what it did not send picked.
func TestBulk_ARunShowsProgressAndStopsWhenAsked(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	keys := dr.pickFirst(0, 3)
	dr.park = parkSteps
	dr.key("+")
	dr.typeText("urgent")
	dr.key("enter", "enter")
	if len(dr.parked) != 1 {
		t.Fatalf("%d steps are in flight, want one at a time", len(dr.parked))
	}
	golden(t, "bulk_running_120x20.golden", dr.view())
	at := dr.m.selectedKey()
	dr.key("j")
	if dr.m.selectedKey() != at {
		t.Error("the cursor moved while a run was going")
	}
	dr.key("ctrl+g")
	dr.unpark()
	if len(dr.parked) != 0 {
		t.Fatalf("a run asked to stop sent another card")
	}
	if dr.m.bulk != nil {
		t.Fatal("the run did not end")
	}
	mustContain(t, dr.lastStatus().Text, "added the label urgent to 1 card of the 3", "stopped with 2 cards not sent")
	for _, key := range keys[1:] {
		if !dr.m.picked[key] {
			t.Errorf("%s was not sent and is no longer picked", key)
		}
	}
}

// Leaving mid-run is held up: the kernel is told why, and a view on top is
// asked, which stops after the card in flight and then lets the leave go on.
func TestBulk_LeavingWhileARunIsGoingWaitsForTheCardInFlight(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.pickFirst(0, 3)
	dr.park = parkSteps
	dr.key("+")
	dr.typeText("urgent")
	dr.key("enter", "enter")

	reason, blocked := dr.m.BlocksClose()
	if !blocked || !strings.Contains(reason, "0 of 3 cards have been sent") {
		t.Fatalf("BlocksClose = %q, %v mid-run", reason, blocked)
	}
	dr.run(dr.m.AskClose())
	if dr.proceeds != 0 {
		t.Fatal("the leave went ahead with a card still in flight")
	}
	dr.unpark()
	if dr.proceeds != 1 {
		t.Errorf("the leave was replayed %d times once the card in flight answered, want once", dr.proceeds)
	}
	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Error("the board still blocks a close after the run ended")
	}
}

func TestBulk_NothingIsBlockedWhenNothingRuns(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(9)), 120, 20)
	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Error("an idle board blocks a close")
	}
	dr.run(dr.m.AskClose())
	if dr.proceeds != 1 {
		t.Errorf("an idle board asked to close replied %d proceeds, want one", dr.proceeds)
	}
}

// The assignee is saved against the one each card was read with, so somebody
// else's change in between is refused rather than overwritten.
func TestBulk_AssignRefusesACardSomebodyElseReassignedMeanwhile(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	keys := dr.pickFirst(0, 2)
	other := ada.AccountID
	if got := dr.m.byKey(keys[0]).Assignee; got != nil && got.AccountID == ada.AccountID {
		other = grace.AccountID
	}
	if err := fake.UpdateIssue(context.Background(), keys[0], jira.IssuePatch{Assignee: &other}); err != nil {
		t.Fatal(err)
	}
	dr.key("@")
	dr.typeText("grace")
	dr.key("enter", "enter")
	if got := siteIssue(t, fake, keys[0]).Assignee; got == nil || got.AccountID != other {
		t.Errorf("%s was reassigned over the change made meanwhile: %+v", keys[0], got)
	}
	got := dr.lastStatus()
	if got.Level != kernel.LevelError || !strings.Contains(got.Text, keys[0]+" did not change") {
		t.Errorf("the report is %+v, want %s named as refused", got, keys[0])
	}
	if !dr.m.picked[keys[0]] || dr.m.picked[keys[1]] {
		t.Errorf("picked after the run is %v, want only %s", dr.m.picked, keys[0])
	}
}
