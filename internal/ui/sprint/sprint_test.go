package sprint

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// scribe records the patches an update sent, which is the only way to hold the
// view to sending the fields that changed and no others.
type scribe struct {
	*jiratest.Fake
	mu      sync.Mutex
	patches []jira.SprintPatch
}

func scribbling(f *jiratest.Fake) *scribe { return &scribe{Fake: f} }

func (s *scribe) UpdateSprint(ctx context.Context, id int64, in jira.SprintPatch) (jira.Sprint, error) {
	s.mu.Lock()
	s.patches = append(s.patches, in)
	s.mu.Unlock()
	return s.Fake.UpdateSprint(ctx, id, in)
}

func (s *scribe) sent() []jira.SprintPatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]jira.SprintPatch(nil), s.patches...)
}

// The endpoint is the only thing that can narrow a board's sprints, and a board
// running for years has hundreds of closed ones. A read that names no state
// walks the lot.
func TestSprints_EveryReadNamesTheStatesItWants(t *testing.T) {
	t.Parallel()

	spy := watching(newFake())
	dr := newDriver(t, testDeps(spy), 120, 20)

	reads := spy.reads()
	if len(reads) == 0 {
		t.Fatal("the view asked for no sprints at all")
	}
	for i, states := range reads {
		if len(states) == 0 {
			t.Fatalf("read %d asked for every sprint the board has ever held", i)
		}
	}
	if got := reads[0]; len(got) != 2 || !hasState(got, jira.SprintActive) || !hasState(got, jira.SprintFuture) {
		t.Errorf("the first read asked for %v, want the running and the planned sprints and nothing else", got)
	}

	dr.key("o")
	reads = spy.reads()
	last := reads[len(reads)-1]
	if !hasState(last, jira.SprintClosed) {
		t.Errorf("after the toggle the read asked for %v, which does not include the closed sprints", last)
	}
	if !dr.m.showAll {
		t.Error("the toggle did not turn the closed sprints on")
	}
}

func hasState(states []jira.SprintState, want jira.SprintState) bool {
	for _, s := range states {
		if s == want {
			return true
		}
	}
	return false
}

// The toggle is the one thing this view advertises before the site has said
// anything, so it has to be answered in that state rather than merely bound.
func TestSprints_TheToggleAnswersBeforeThereIsAnythingToActOn(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(nil), 100, 14)
	set, _ := dr.m.LiveKeys()
	if len(set.Acts) == 0 {
		t.Fatal("a view with no connection advertises nothing at all")
	}
	before := dr.view()
	dr.key("o")
	after := dr.view()
	if before == after {
		t.Errorf("the key the footer advertises changed nothing on screen:\n%s", after)
	}
	mustContain(t, after, "closed")
}

func TestSprints_PutTheRunningSprintFirstThenThePlannedThenTheClosed(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake()), 120, 20)
	dr.key("o")

	want := []string{"Sprint 2", "Sprint 3", "Sprint 1"}
	got := dr.names()
	if len(got) != len(want) {
		t.Fatalf("the list holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the list is ordered %v, want %v", got, want)
		}
	}
}

// The port refuses to start a sprint with no dates, so the key is not offered
// where it would be refused and pressing it anyway says which date is missing.
func TestSprints_StartingNeedsBothDatesAndNamesTheOneThatIsMissing(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.April, 14, 0, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		start, end *time.Time
		says       string
	}{
		"neither date": {says: "has no dates yet"},
		"only a start": {start: &start, says: "has no end date yet"},
		"only an end":  {end: &end, says: "has no start date yet"},
		"both of them": {start: &start, end: &end, says: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake()
			dr := newDriver(t, testDeps(f), 120, 20)
			dr.onSprint("Sprint 3")
			dr.m.sprints[dr.m.cursor].Start, dr.m.sprints[dr.m.cursor].End = tc.start, tc.end
			dr.key("s")

			if tc.says == "" {
				if dr.m.state != confirming {
					t.Fatalf("a sprint with both dates did not reach the confirm; it is in state %d", dr.m.state)
				}
				return
			}
			if dr.m.state == confirming {
				t.Fatal("a sprint that cannot start reached the confirm")
			}
			if got := dr.lastStatus().Text; !strings.Contains(got, tc.says) {
				t.Errorf("the refusal says %q, want it to name the missing date: %q", got, tc.says)
			}
			if n := countCalls(f, "StartSprint"); n != 0 {
				t.Errorf("the site was asked to start it %d times", n)
			}
		})
	}
}

// Both moves are irreversible, so the confirm is the only route to them: the
// key that asks for one asks the question and nothing else.
func TestSprints_NeitherMoveReachesTheSiteWithoutTheConfirm(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		sprint string
		key    string
		call   string
	}{
		"starting a planned sprint": {sprint: "Sprint 3", key: "s", call: "StartSprint"},
		"completing a running one":  {sprint: "Sprint 2", key: "c", call: "CompleteSprint"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake()
			dr := newDriver(t, testDeps(f), 120, 20)
			dr.onSprint(tc.sprint)
			if tc.sprint == "Sprint 3" {
				at, to := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC)
				dr.m.sprints[dr.m.cursor].Start, dr.m.sprints[dr.m.cursor].End = &at, &to
			}

			dr.key(tc.key)
			if dr.m.state != confirming {
				t.Fatalf("%s did not ask first", tc.key)
			}
			if n := countCalls(f, tc.call); n != 0 {
				t.Fatalf("%s reached the site %d times before anybody answered", tc.call, n)
			}

			dr.key("esc")
			if dr.m.state != browsing {
				t.Fatal("esc left the confirm up")
			}
			if n := countCalls(f, tc.call); n != 0 {
				t.Fatalf("%s ran after the confirm was refused", tc.call)
			}

			dr.key(tc.key)
			dr.key("y")
			if n := countCalls(f, tc.call); n != 1 {
				t.Errorf("%s ran %d times after a y, want once", tc.call, n)
			}
		})
	}
}

// What completing a sprint does to the issues left open in it is the fact the
// question turns on, and the count is not available through the port — so the
// confirm says both, rather than implying a number nothing can get.
func TestSprints_TheCompleteConfirmSaysWhatHappensToTheIssuesLeftOpen(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake()), 100, 16)
	dr.onSprint("Sprint 2")
	dr.key("c")

	frame := dr.view()
	mustContain(t, frame,
		"Complete Sprint 2?",
		"cannot be undone",
		"leaves the sprint",
		"backlog",
		"cannot say how many",
		"y goes ahead",
		"esc leaves it alone",
	)
}

// future to active to closed is the whole of the state machine, so the move that
// does not exist from a state is refused with the state named in the site's own
// word for it.
func TestSprints_AMoveThatIsNotInTheStateMachineIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		state jira.SprintState
		key   string
		says  string
	}{
		"completing a planned sprint":                        {state: jira.SprintFuture, key: "c", says: "only a running sprint can be completed"},
		"completing a closed one":                            {state: jira.SprintClosed, key: "c", says: "only a running sprint can be completed"},
		"starting a running one":                             {state: jira.SprintActive, key: "s", says: "only a planned sprint can be started"},
		"starting a closed one":                              {state: jira.SprintClosed, key: "s", says: "only a planned sprint can be started"},
		"starting one in a state this build has no word for": {state: "on hold", key: "s", says: "on hold"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake()
			dr := newDriver(t, testDeps(f), 120, 20)
			dr.onSprint("Sprint 3")
			dr.m.sprints[dr.m.cursor].State = tc.state
			dr.key(tc.key)

			if dr.m.state == confirming {
				t.Fatalf("a %q sprint reached the confirm for %q", tc.state, tc.key)
			}
			if got := dr.lastStatus().Text; !strings.Contains(got, tc.says) {
				t.Errorf("the refusal says %q, want %q in it", got, tc.says)
			}
			if n := countCalls(f, "StartSprint") + countCalls(f, "CompleteSprint"); n != 0 {
				t.Errorf("the site was asked anyway, %d times", n)
			}
		})
	}
}

// The endpoint under UpdateSprint is a full replace, so a field the patch does
// not name is a field that is nulled. Only what changed may be sent.
func TestSprints_AnUpdateSendsTheFieldsThatChangedAndNoOthers(t *testing.T) {
	t.Parallel()

	spy := scribbling(newFake())
	dr := newDriver(t, testDeps(spy), 120, 20)
	dr.onSprint("Sprint 2")
	dr.key("e")
	dr.setField(fieldGoal, "make it fast")
	dr.key("ctrl+s")

	sent := spy.sent()
	if len(sent) != 1 {
		t.Fatalf("the site took %d patches, want one", len(sent))
	}
	patch := sent[0]
	if patch.Goal == nil || *patch.Goal != "make it fast" {
		t.Errorf("the patch carried goal %v, want the typed one", patch.Goal)
	}
	for name, ptr := range map[string]bool{
		"name":  patch.Name != nil,
		"start": patch.Start != nil,
		"end":   patch.End != nil,
	} {
		if ptr {
			t.Errorf("the patch named %s, which nobody changed; the endpoint replaces what it is sent", name)
		}
	}
	if dr.m.state != browsing {
		t.Error("the form stayed open after the site answered")
	}
}

// A date that has been set cannot be unset through the patch — a nil field
// means leave it alone and there is no way to say "none" — so emptying one is
// refused here rather than sent as something else.
func TestSprints_ADateThatIsSetCannotBeClearedFromTheForm(t *testing.T) {
	t.Parallel()

	spy := scribbling(newFake())
	dr := newDriver(t, testDeps(spy), 100, 18)
	dr.onSprint("Sprint 2")
	dr.key("e")
	dr.setField(fieldStart, "")
	dr.key("ctrl+s")

	if n := len(spy.sent()); n != 0 {
		t.Fatalf("the site took %d patches, want none", n)
	}
	mustContain(t, dr.view(), "cannot be cleared")
	if dr.m.state != filling {
		t.Error("the form closed over a value it refused to send")
	}
}

// A closed sprint takes only its name and its goal. The dates are not offered
// rather than offered and refused, and the reason is on screen.
func TestSprints_AClosedSprintTakesOnlyItsNameAndItsGoal(t *testing.T) {
	t.Parallel()

	spy := scribbling(newFake())
	dr := newDriver(t, testDeps(spy), 100, 20)
	dr.key("o")
	dr.onSprint("Sprint 1")
	dr.key("e")

	mustContain(t, dr.view(), "only its name and its goal")

	// tab walks the fields it can take, and the dates are not among them.
	seen := map[field]bool{}
	for range 6 {
		seen[dr.m.form.at] = true
		dr.key("tab")
	}
	if seen[fieldStart] || seen[fieldEnd] {
		t.Error("tab put the cursor in a date field of a closed sprint")
	}

	dr.m.form.at = fieldStart
	dr.typeText("2026-04-01")
	if got := dr.m.form.value(fieldStart); got != writeDate(dr.m.form.sprint.Start, time.UTC) {
		t.Errorf("typing changed a closed sprint's start date to %q", got)
	}

	dr.setField(fieldName, "Sprint one, renamed")
	dr.key("ctrl+s")
	sent := spy.sent()
	if len(sent) != 1 {
		t.Fatalf("the site took %d patches, want the rename", len(sent))
	}
	if sent[0].Start != nil || sent[0].End != nil {
		t.Error("the rename of a closed sprint carried a date")
	}
}

func TestSprints_AnEditThatChangedNothingIsNotSent(t *testing.T) {
	t.Parallel()

	spy := scribbling(newFake())
	dr := newDriver(t, testDeps(spy), 100, 18)
	dr.onSprint("Sprint 2")
	dr.key("e")
	dr.key("ctrl+s")

	if n := len(spy.sent()); n != 0 {
		t.Fatalf("the site took %d patches for an edit that changed nothing", n)
	}
	mustContain(t, dr.view(), "nothing on this screen has changed")
}

// The port validates locally, so a refusal about a value arrives without a
// round trip and belongs on the field it names.
func TestSprints_ARefusalAboutAValueIsDrawnOnTheFieldItNames(t *testing.T) {
	t.Parallel()

	f := newFake()
	dr := newDriver(t, testDeps(f), 100, 20)
	dr.key("n")
	dr.setField(fieldName, "Sprint 9")
	f.FailNext(&jira.ValidationError{
		Fields:   []jira.FieldError{{Field: "name", Message: "a sprint of that name is already on this board"}},
		Messages: []string{"nothing was created"},
	})
	dr.key("ctrl+s")

	if dr.m.state != filling {
		t.Fatalf("the form closed over a refusal; it is in state %d", dr.m.state)
	}
	if got := dr.m.form.problems[fieldName]; !strings.Contains(got, "already on this board") {
		t.Errorf("the name field says %q, want the site's own words", got)
	}
	mustContain(t, dr.view(), "already on this board", "nothing was created")
}

// A read that came back refused says so where the refusal stays put: a status
// line is gone by the next keypress and the pane is not.
func TestSprints_EveryFailureKeepsItsReasonInThePane(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		says string
	}{
		"a permission the token has not": {
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "needs the Browse Projects permission"},
			says: "Browse Projects",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			says: "30s",
		},
		"a transport failure": {
			err:  &jira.TransportError{Op: "GET /rest/agile/1.0/board", Err: errors.New("dial tcp: connection refused")},
			says: "connection refused",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake()
			f.FailNext(tc.err)
			dr := newDriver(t, testDeps(f), 100, 16)

			if dr.lastStatus().Level != kernel.LevelError {
				t.Errorf("the status line reported level %d, want a failure", dr.lastStatus().Level)
			}
			mustContain(t, dr.view(), tc.says)
			mustContain(t, dr.view(), "refused")

			// And it is still there after a keystroke, which is what clears the
			// status line.
			dr.key("j")
			mustContain(t, dr.view(), tc.says)
		})
	}
}

// Losing the keyboard is not being closed: the kernel blurs a view it is
// covering as well as one it is switching away from, and nothing about losing
// the keys means nobody wants the answer.
func TestSprints_KeepsItsReadOnABlurAndDrawsTheAnswerWhenItLands(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake()), 100, 16)
	held := loadedMsg{
		gen:     dr.m.gen,
		boards:  []jira.Board{{ID: 7, Name: "OPS board"}},
		sprints: []jira.Sprint{{ID: 71, BoardID: 7, Name: "OPS Sprint 1", State: jira.SprintActive}},
	}
	dr.send(kernel.FocusMsg{Focused: false})
	dr.send(held)

	mustContain(t, dr.view(), "OPS Sprint 1")
}

// An answer to a question that has since changed is dropped rather than drawn.
func TestSprints_AnAnswerToAQuestionThatHasChangedIsDropped(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake()), 100, 16)
	stale := loadedMsg{
		gen:     dr.m.gen - 1,
		boards:  []jira.Board{{ID: 9, Name: "stale board"}},
		sprints: []jira.Sprint{{ID: 91, BoardID: 9, Name: "a sprint nobody asked for", State: jira.SprintActive}},
	}
	dr.send(stale)

	mustNotContain(t, dr.view(), "a sprint nobody asked for", "stale board")
}

func TestSprints_ARefusalOnAWriteLeavesTheListAlone(t *testing.T) {
	t.Parallel()

	f := newFake()
	dr := newDriver(t, testDeps(f), 100, 16)
	before := dr.names()
	dr.onSprint("Sprint 2")
	f.FailNext(&jira.RateLimitError{RetryAfter: 5 * time.Second})
	dr.key("c")
	dr.key("y")

	if got := dr.names(); len(got) != len(before) {
		t.Errorf("the list is %v after a refused write, want %v", got, before)
	}
	if dr.m.inflight != opNone {
		t.Error("the view still thinks a write is in flight")
	}
	mustContain(t, dr.view(), "5s")
}

// The form holds several fields' worth of typing, and the kernel asks before it
// throws the view away.
func TestSprints_AFormWithSomethingInItRefusesToBeThrownAway(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake()), 100, 18)
	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Fatal("the list refuses to close and is holding nothing")
	}
	dr.key("n")
	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Fatal("an empty form refuses to close")
	}
	dr.setField(fieldName, "Sprint 9")
	reason, blocked := dr.m.BlocksClose()
	if !blocked {
		t.Fatal("a filled-in form went with the program")
	}
	mustContain(t, reason, "ctrl+s", "esc")

	dr.key("esc")
	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Error("the form was discarded and still refuses to close")
	}
}

func TestSprints_ClaimsTheKeysOnlyWhileTypingOrAnswering(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake()), 100, 18)
	if dr.m.WantsRawKeys() {
		t.Error("the list swallows q and esc")
	}
	dr.key("n")
	if !dr.m.WantsRawKeys() {
		t.Error("the form does not take the keys, so a sprint name loses its digits")
	}
	dr.key("esc")
	dr.onSprint("Sprint 2")
	dr.key("c")
	if !dr.m.WantsRawKeys() {
		t.Error("the confirm does not take the keys, so esc never reaches it in a root view")
	}
}

// A project switch is a different question, so what was read for the last one
// goes rather than being drawn under a head that names the new one.
func TestSprints_AProjectSwitchDropsWhatWasReadForTheLastOne(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(jiratest.WithProject("OPS", jiratest.Kanban))), 100, 16)
	mustContain(t, dr.view(), "Sprint 2")

	dr.send(kernel.ProjectMsg{Project: "OPS"})
	mustNotContain(t, dr.view(), "Sprint 2")
	mustContain(t, dr.view(), "OPS board")
}

// An answer is delivered to the view that asked for it, wherever the stack has
// got to by the time it lands, so every read this view issues carries its own
// address and two instances never share one.
func TestSprints_AsksUnderItsOwnAddress(t *testing.T) {
	t.Parallel()

	first, ok := New(testDeps(newFake())).(*Model)
	if !ok {
		t.Fatal("New no longer builds a *Model")
	}
	second, _ := New(testDeps(newFake())).(*Model)
	if first.Addr() == 0 {
		t.Error("the view answers to the zero address, which the kernel resolves to nothing")
	}
	if first.Addr() == second.Addr() {
		t.Error("two views share an address, so an answer to one is drawn into the other")
	}

	msg := first.load()()
	reply, addressed := msg.(kernel.ReplyMsg)
	if !addressed {
		t.Fatalf("a read came back as %T rather than as this view's own answer", msg)
	}
	if len(reply.To) != 1 || reply.To[0] != first.Addr() {
		t.Errorf("the read is addressed to %v, want the view that issued it", reply.To)
	}
}

// Nothing pushes this view: it holds a footer slot, so the kernel keeps it and
// brings it back on its digit rather than discarding it. A Close on it would be
// a method nobody calls, and cancelling a read on a switch away is what made
// opening the palette over a loading pane cancel the load.
func TestSprints_IsARootTheKernelKeepsRatherThanDiscards(t *testing.T) {
	t.Parallel()

	if _, closes := New(testDeps(newFake())).(kernel.Closer); closes {
		t.Error("the sprints view implements kernel.Closer and nothing would ever call it")
	}
}

// The dates are checked here rather than left to the port. The real adapter
// refuses a sprint that ends before it starts without a round trip, and
// pkg/jira/jiratest's fake does not — so a test that leaned on the port for
// this would be a test of nothing. Nothing reaches the site until the form is
// consistent on its own.
func TestSprints_ASprintThatEndsBeforeItStartsNeverReachesTheSite(t *testing.T) {
	t.Parallel()

	f := newFake()
	dr := newDriver(t, testDeps(f), 100, 20)
	dr.key("n")
	dr.setField(fieldName, "Sprint 9")
	dr.setField(fieldStart, "2026-04-01")
	dr.setField(fieldEnd, "2026-03-01")
	dr.key("ctrl+s")

	if n := countCalls(f, "CreateSprint"); n != 0 {
		t.Errorf("the site was asked to create it %d times", n)
	}
	mustContain(t, dr.view(), "cannot end before it starts")

	dr.setField(fieldEnd, "2026-04-14")
	dr.key("ctrl+s")
	if n := countCalls(f, "CreateSprint"); n != 1 {
		t.Errorf("the site took the corrected sprint %d times, want once", n)
	}
}

// A date that is not a date is said to be one, in the shape a reader can copy.
func TestSprints_ADateThatIsNotOneSaysWhatShapeItShouldBe(t *testing.T) {
	t.Parallel()

	f := newFake()
	dr := newDriver(t, testDeps(f), 100, 20)
	dr.key("n")
	dr.setField(fieldName, "Sprint 9")
	dr.setField(fieldStart, "next tuesday")
	dr.key("ctrl+s")

	if n := countCalls(f, "CreateSprint"); n != 0 {
		t.Errorf("the site was asked with a start date of %q, %d times", "next tuesday", n)
	}
	mustContain(t, dr.view(), dateShape)
}
