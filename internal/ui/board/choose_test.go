package board

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	appboard "github.com/varijkapil13/saral/internal/app/board"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// shelved is a second status the fake's last column maps, beside the one the
// fake itself has there, reached by a move with no screen.
var shelved = jira.Status{ID: "10299", Name: "Shelved", Category: jira.CategoryDone}

const shelve = "tr-shelve"

// twoInDone is the fake with a last column mapping two statuses. The fake's own
// move there needs a screen; the added one does not.
type twoInDone struct {
	*jiratest.Fake
	mu     sync.Mutex
	posted []string
	moved  map[string]jira.Status
	fail   error
	// without are the issues offered no move to shelved.
	without map[string]bool
}

func newTwoInDone(issues int) *twoInDone {
	return &twoInDone{Fake: newFake(issues), moved: map[string]jira.Status{}, without: map[string]bool{}}
}

func (w *twoInDone) BoardConfig(ctx context.Context, boardID int64) (jira.BoardConfig, error) {
	cfg, err := w.Fake.BoardConfig(ctx, boardID)
	if err != nil {
		return cfg, err
	}
	last := &cfg.Columns[len(cfg.Columns)-1]
	last.StatusIDs = append(slices.Clone(last.StatusIDs), shelved.ID)
	return cfg, nil
}

func (w *twoInDone) Transitions(ctx context.Context, key string) ([]jira.Transition, error) {
	list, err := w.Fake.Transitions(ctx, key)
	if err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.without[key] || w.moved[key].ID == shelved.ID {
		return list, nil
	}
	return append(list, jira.Transition{ID: shelve, Name: "Shelve", To: shelved}), nil
}

func (w *twoInDone) Transition(ctx context.Context, key, id string, in jira.IssuePatch) error {
	if id != shelve {
		return w.Fake.Transition(ctx, key, id, in)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail != nil {
		return w.fail
	}
	w.posted = append(w.posted, key)
	w.moved[key] = shelved
	return nil
}

func (w *twoInDone) IssueFields(ctx context.Context, key string, fields []string) (jira.Issue, error) {
	iss, err := w.Fake.IssueFields(ctx, key, fields)
	w.mu.Lock()
	defer w.mu.Unlock()
	if st, ok := w.moved[key]; ok {
		iss.Status = st
	}
	return iss, err
}

func (w *twoInDone) shelved() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.posted)
}

func TestChoose_ALabelNamesTheStatusAndOnlyAddsWhatTellsTwoApart(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		in   []jira.Transition
		want []string
	}{
		"moves named for their status": {
			in: []jira.Transition{
				{ID: "1", Name: "Done", To: jira.Status{ID: "a", Name: "Done"}},
				{ID: "2", Name: "won't do", To: jira.Status{ID: "b", Name: "Won't Do"}},
			},
			want: []string{"Done", "Won't Do"},
		},
		"a move named otherwise": {
			in: []jira.Transition{
				{ID: "1", Name: "Close", To: jira.Status{ID: "a", Name: "Closed"}},
				{ID: "2", Name: "Declined", To: jira.Status{ID: "b", Name: "Declined"}},
			},
			want: []string{"Closed (Close)", "Declined"},
		},
		"two statuses sharing a name": {
			in: []jira.Transition{
				{ID: "1", Name: "Finish", To: jira.Status{ID: "a", Name: "Closed"}},
				{ID: "2", Name: "Closed", To: jira.Status{ID: "b", Name: "Closed"}},
			},
			want: []string{"Closed (Finish)", "Closed (Closed)"},
		},
		"two moves alike in every word": {
			in: []jira.Transition{
				{ID: "31", Name: "Closed", To: jira.Status{ID: "a", Name: "Closed"}},
				{ID: "41", Name: "Closed", To: jira.Status{ID: "b", Name: "Closed"}},
			},
			want: []string{"Closed (Closed) [31]", "Closed (Closed) [41]"},
		},
		"a status with no name": {
			in:   []jira.Transition{{ID: "1", Name: "Park", To: jira.Status{ID: "a"}}, {ID: "2", To: jira.Status{ID: "b"}}},
			want: []string{"Park", "b"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := choiceLabels(tc.in); !slices.Equal(got, tc.want) {
				t.Errorf("labels %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChoose_ASetIsAskedByStatusAlone(t *testing.T) {
	t.Parallel()
	got := statusLabels([]jira.Transition{
		{ID: "1", Name: "Finish", To: jira.Status{ID: "a", Name: "Closed"}},
		{ID: "2", Name: "Close", To: jira.Status{ID: "b", Name: "Closed"}},
		{ID: "3", Name: "Park", To: jira.Status{ID: "c", Name: "Parked"}},
		{ID: "4", To: jira.Status{ID: "d"}},
	})
	if want := []string{"Closed [a]", "Closed [b]", "Parked", "d"}; !slices.Equal(got, want) {
		t.Errorf("labels %q, want %q", got, want)
	}
}

// Every way a single card is landed reaches the same question when the column
// it lands in maps two statuses the workflow can reach, and nothing is sent
// until it is answered.
func TestChoose_EveryWayOfLandingACardAsksWhichStatus(t *testing.T) {
	t.Parallel()
	for name, gesture := range map[string]func(kernel.Deps, *driver){
		"m, aim and enter": func(_ kernel.Deps, dr *driver) { dr.key("t", "l", "l", "enter") },
		"the palette":      func(_ kernel.Deps, dr *driver) { dr.send(MoveIssueMsg{}); dr.key("l", "l", "enter") },
		"L":                func(_ kernel.Deps, dr *driver) { dr.moveTo(1, 0); dr.key("L") },
		"a click on the column": func(d kernel.Deps, dr *driver) {
			dr.key("t")
			pressOn(dr.t, d, dr, colZone(2))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newTwoInDone(9)
			d := testDeps(w)
			dr := newDriver(t, d, 120, 20)
			key := dr.m.selectedKey()
			if name == "L" {
				key = dr.column(1)[0]
			}

			gesture(d, dr)

			if !dr.m.choosing() {
				t.Fatalf("the board is not asking which status; card %+v", dr.m.card)
			}
			if dr.m.card.key != key {
				t.Errorf("the card in hand is %s, want %s", dr.m.card.key, key)
			}
			if got := len(dr.m.card.choices); got != 2 {
				t.Errorf("%d options offered, want the two statuses the column maps", got)
			}
			if n := countCalls(w.Fake, "Transition") + len(w.shelved()); n != 0 {
				t.Errorf("%d moves were sent before a status was chosen", n)
			}
			if slices.Contains(dr.column(2), key) {
				t.Errorf("%s is drawn in the column before it was moved", key)
			}
			if _, state := dr.m.LiveKeys(); state != int(keysChoosing) {
				t.Errorf("the keys are for state %d, want the question's own", state)
			}
		})
	}
}

func TestChoose_TheQuestionGolden(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newTwoInDone(9)), 120, 20)
	dr.key("t", "l", "l", "enter", "right")
	golden(t, "choosing_120x20.golden", dr.view())
}

func TestChoose_AStatusWithNoScreenIsMovedTo(t *testing.T) {
	t.Parallel()
	w := newTwoInDone(9)
	dr := newDriver(t, testDeps(w), 120, 20)
	key := dr.m.selectedKey()

	dr.key("t", "l", "l", "enter", "down", "up", "j", "enter")

	if got := w.shelved(); !slices.Equal(got, []string{key}) {
		t.Fatalf("shelved %v, want %s", got, key)
	}
	if n := countCalls(w.Fake, "Transition"); n != 0 {
		t.Errorf("the other status was moved to as well (%d)", n)
	}
	if dr.m.card != nil || dr.m.moving {
		t.Error("the card is still in hand after it landed")
	}
	if got := dr.column(2); !slices.Contains(got, key) {
		t.Errorf("the last column holds %v, want %s", got, key)
	}
	if got := dr.m.byKey(key).Status.ID; got != shelved.ID {
		t.Errorf("%s is in status %s, want the one chosen", key, got)
	}
	mustContain(t, dr.lastStatus().Text, key+" moved")
}

func TestChoose_AStatusNeedingAScreenIsHandedToTheIssuePane(t *testing.T) {
	t.Parallel()
	w := newTwoInDone(9)
	dr := newDriver(t, testDeps(w), 120, 20)

	dr.key("t", "l", "l", "enter", "enter")

	if n := countCalls(w.Fake, "Transition") + len(w.shelved()); n != 0 {
		t.Errorf("%d moves were sent blind", n)
	}
	if len(dr.pushes) != 1 || dr.pushes[0].ID != issue.ViewID {
		t.Fatalf("pushed %+v, want the issue pane", dr.pushes)
	}
	if dr.m.card != nil {
		t.Error("the card is still in hand after it was handed on")
	}
}

func TestChoose_AClickOnAnOptionTakesIt(t *testing.T) {
	t.Parallel()
	w := newTwoInDone(9)
	d := testDeps(w)
	dr := newDriver(t, d, 120, 20)
	key := dr.m.selectedKey()
	dr.key("t", "l", "l", "enter")

	pressOn(t, d, dr, choiceZone(1))

	if got := w.shelved(); !slices.Equal(got, []string{key}) {
		t.Errorf("shelved %v, want %s", got, key)
	}
}

func TestChoose_CancellingPutsTheCardBackAndSendsNothing(t *testing.T) {
	t.Parallel()
	for _, stroke := range []string{"esc", "ctrl+g"} {
		t.Run(stroke, func(t *testing.T) {
			t.Parallel()
			w := newTwoInDone(9)
			dr := newDriver(t, testDeps(w), 120, 20)
			key := dr.m.selectedKey()
			dr.key("t", "l", "l", "enter")
			if stroke == "esc" && !dr.m.WantsBack() {
				t.Fatal("the question does not claim esc, so the kernel keeps it")
			}

			dr.key(stroke)

			if dr.m.card != nil {
				t.Fatal("the card is still in hand")
			}
			if n := countCalls(w.Fake, "Transition") + len(w.shelved()); n != 0 {
				t.Errorf("%d moves were sent", n)
			}
			if got := dr.column(0); !slices.Contains(got, key) {
				t.Errorf("the first column holds %v, want %s back", got, key)
			}
			if got := dr.m.selectedKey(); got != key {
				t.Errorf("the cursor is on %s, want it back on %s", got, key)
			}
		})
	}
}

func TestChoose_ARefusedMoveAfterTheChoicePutsTheCardBack(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Transition Issues in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "POST /transitions", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newTwoInDone(9)
			w.fail = err
			dr := newDriver(t, testDeps(w), 120, 20)
			key := dr.m.selectedKey()

			dr.key("t", "l", "l", "enter", "right", "enter")

			if dr.m.card != nil || dr.m.moving {
				t.Error("the card is still in hand after the site refused the move")
			}
			if got := dr.column(0); !slices.Contains(got, key) {
				t.Errorf("the first column holds %v, want %s put back", got, key)
			}
			reason, _ := jira.Reason(err)
			mustContain(t, dr.lastStatus().Text, reason)
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
		})
	}
}

func TestChoose_ACardWithOneMoveIntoTheColumnIsNotAsked(t *testing.T) {
	t.Parallel()
	w := newTwoInDone(9)
	dr := newDriver(t, testDeps(w), 120, 20)
	w.without[dr.m.selectedKey()] = true

	dr.key("t", "l", "l", "enter")

	if dr.m.choosing() {
		t.Error("a card with one move into the column was asked which")
	}
	if len(dr.pushes) != 1 {
		t.Errorf("pushed %d views, want the one move taken straight to the issue pane", len(dr.pushes))
	}
}

// --- a picked set ---------------------------------------------------------------

func TestChooseSet_ThePickedCardsAllLandInTheStatusChosen(t *testing.T) {
	t.Parallel()
	w := newTwoInDone(9)
	dr := newDriver(t, testDeps(w), 120, 20)
	keys := dr.pickFirst(0, 2)

	dr.key("t", "l", "l", "enter")

	b := dr.m.bulk
	if b == nil || b.stage != stageAskStatus || len(b.choices) != 2 {
		t.Fatalf("the set is not being asked which status: %+v", b)
	}
	if n := countCalls(w.Fake, "Transitions"); n != 1 {
		t.Errorf("the question read %d cards' moves, want the first card's only", n)
	}
	golden(t, "choosing_set_120x20.golden", dr.view())

	dr.key("right", "enter")
	mustContain(t, dr.view(), "move 2 cards to Done as Shelved?")
	if len(w.shelved()) != 0 {
		t.Fatal("a card moved before the go-ahead")
	}
	dr.key("enter")

	if got := w.shelved(); !slices.Equal(got, keys) {
		t.Errorf("shelved %v, want %v", got, keys)
	}
	if n := countCalls(w.Fake, "Transition"); n != 0 {
		t.Errorf("%d cards took the other status", n)
	}
	mustContain(t, dr.lastStatus().Text, "moved 2 cards to Done as Shelved")
}

func TestChooseSet_ACardWithNoMoveToTheStatusIsReported(t *testing.T) {
	t.Parallel()
	w := newTwoInDone(9)
	dr := newDriver(t, testDeps(w), 120, 20)
	keys := dr.pickFirst(0, 2)
	w.without[keys[1]] = true

	dr.key("t", "l", "l", "enter", "right", "enter", "enter")

	if got := w.shelved(); !slices.Equal(got, keys[:1]) {
		t.Errorf("shelved %v, want %v", got, keys[:1])
	}
	mustContain(t, dr.lastStatus().Text, keys[1], appboard.ErrNoMoveTo.Error())
	if !dr.m.picked[keys[1]] {
		t.Error("the card that did not move was let go, so the gesture cannot try it again")
	}
}

func TestChooseSet_ACancelSendsNothing(t *testing.T) {
	t.Parallel()
	for _, stroke := range []string{"esc", "ctrl+g"} {
		t.Run(stroke, func(t *testing.T) {
			t.Parallel()
			w := newTwoInDone(9)
			dr := newDriver(t, testDeps(w), 120, 20)
			keys := dr.pickFirst(0, 2)
			dr.key("t", "l", "l", "enter", stroke)
			if dr.m.bulk != nil {
				t.Fatal("the question is still up")
			}
			if n := countCalls(w.Fake, "Transition") + len(w.shelved()); n != 0 {
				t.Errorf("%d moves were sent", n)
			}
			if len(dr.m.picked) != len(keys) {
				t.Errorf("%d picked after the cancel, want the %d still picked", len(dr.m.picked), len(keys))
			}
		})
	}
}

func TestChooseSet_AClickOnAnOptionTakesIt(t *testing.T) {
	t.Parallel()
	w := newTwoInDone(9)
	d := testDeps(w)
	dr := newDriver(t, d, 120, 20)
	dr.pickFirst(0, 2)
	dr.key("t", "l", "l", "enter")

	pressOn(t, d, dr, choiceZone(1))

	if b := dr.m.bulk; b == nil || b.stage != stageConfirm || b.status != shelved.ID {
		t.Errorf("a click on the second option left %+v, want the go-ahead for it", b)
	}
}

func TestChooseSet_ARefusedReadOfTheMovesAsksNothing(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"a 403":               &jira.CapabilityError{Reason: "you need Transition Issues in this project"},
		"a rate limit":        &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"a transport failure": &jira.TransportError{Op: "GET /transitions", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newTwoInDone(9)
			dr := newDriver(t, testDeps(w), 120, 20)
			dr.pickFirst(0, 2)
			dr.key("t", "l", "l")
			w.FailNext(err)
			dr.key("enter")

			if dr.m.bulk != nil {
				t.Errorf("the set is still being asked about: %+v", dr.m.bulk)
			}
			if n := countCalls(w.Fake, "Transition") + len(w.shelved()); n != 0 {
				t.Errorf("%d moves were sent", n)
			}
			reason, _ := jira.Reason(err)
			mustContain(t, dr.lastStatus().Text, reason)
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the refusal was reported at level %v", got)
			}
		})
	}
}

func TestChooseSet_AColumnOfOneStatusGoesStraightToTheGoAhead(t *testing.T) {
	t.Parallel()
	fake := newFake(9)
	dr := newDriver(t, testDeps(fake), 120, 20)
	dr.pickFirst(0, 2)
	before := countCalls(fake, "Transitions")

	dr.key("t", "l", "enter")

	if b := dr.m.bulk; b == nil || b.stage != stageConfirm {
		t.Fatalf("the set is at %+v, want the go-ahead", b)
	}
	if n := countCalls(fake, "Transitions"); n != before {
		t.Errorf("a column of one status read %d cards' moves before the go-ahead", n-before)
	}
}
