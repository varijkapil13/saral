package move

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// twoProjects is a site with work in both projects, so that the target
// suggestions have something to offer.
func twoProjects(t *testing.T) (*jiratest.Fake, []jira.Issue) {
	t.Helper()
	f := newFake(6, jiratest.WithIssues(jiratest.GenFor("OTHER", 4)))
	return f, seeded(t, f, "PROJ-1", "PROJ-2", "PROJ-3")
}

func TestMove_OffersTheProjectsBehindRecentIssuesAndNotTheOneTheIssuesAreIn(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))

	if !slices.Contains(dr.m.found, "OTHER") {
		t.Errorf("the target suggestions do not offer OTHER: %v", dr.m.found)
	}
	if slices.Contains(dr.m.found, "PROJ") {
		t.Errorf("the target suggestions offer PROJ, which is where the issues already are: %v", dr.m.found)
	}
	mustContain(t, dr.view(), "Move 3 issues out of PROJ", "OTHER")
}

func TestMove_ATypedProjectKeyIsCheckedAgainstTheSiteBeforeAnythingElseIsAsked(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))

	dr.typeKey("nosuch")
	if dr.m.step != stepTarget && dr.m.step != stepTyping {
		t.Fatalf("a key the site does not know moved the wizard on to step %d", dr.m.step)
	}
	if dr.m.failure == nil {
		t.Fatal("a key the site does not know left no failure behind")
	}
	mustContain(t, dr.view(), "NOSUCH")

	dr.key("esc")
	dr.typeKey("other")
	if dr.m.step != stepType {
		t.Fatalf("a real key left the wizard on step %d rather than the issue type", dr.m.step)
	}
	if dr.m.target != "OTHER" {
		t.Errorf("the target is %q; a project key is upper case on Jira", dr.m.target)
	}
}

// The workflow a remap lands on is the chosen issue type's, and the two types
// here reach a status of the same name under different ids — which is what a
// team-managed project mints on a real site, and the reason an answer about
// statuses carries ids at all.
func TestMove_RemapsAStatusByIdAndNeverByItsDisplayName(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.typeKey("OTHER")

	source := dr.m.issues[0].Status
	elsewhere := jira.Status{ID: source.ID + "-elsewhere", Name: source.Name, Category: source.Category}
	dr.send(vocabularyMsg{gen: dr.m.gen, project: "OTHER", types: []jira.IssueTypeStatuses{
		{Type: jira.IssueType{ID: "t1", Name: "One"}, Statuses: []jira.Status{source}},
		{Type: jira.IssueType{ID: "t2", Name: "Two"}, Statuses: []jira.Status{elsewhere}},
	}})
	dr.m.cursor = 1
	dr.key("enter")

	at, ok := sourceByName(dr.m.remaps, source.Name)
	if !ok {
		t.Fatalf("no row for %q: %v", source.Name, names(dr.m.remaps))
	}
	to, found := dr.m.landing(at)
	if !found {
		t.Fatal("the row has nowhere to land")
	}
	if to.ID != elsewhere.ID {
		t.Errorf("%q was mapped to %s; the chosen type's workflow reaches it under %s, so the target came "+
			"from somewhere other than that workflow", source.Name, to.ID, elsewhere.ID)
	}
	in := dr.m.request()
	if !slices.Contains(in.StatusMap, jira.StatusMapping{FromStatusID: source.ID, ToStatusID: elsewhere.ID}) {
		t.Errorf("the request maps %v; %s -> %s is missing", in.StatusMap, source.ID, elsewhere.ID)
	}
}

// Every source status is mapped, not only the ones that move: naming one entry
// on this endpoint stops the rest being resolved from the workflow.
func TestMove_MapsEverySourceStatusAndNotOnlyTheOnesThatChange(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.walkTo("OTHER")

	in := dr.m.request()
	if len(in.StatusMap) != len(dr.m.remaps) {
		t.Errorf("%d of the %d source statuses reached the request: %v", len(in.StatusMap), len(dr.m.remaps), in.StatusMap)
	}
	for i := range dr.m.remaps {
		from := dr.m.remaps[i].from.ID
		if !slices.ContainsFunc(in.StatusMap, func(mp jira.StatusMapping) bool { return mp.FromStatusID == from }) {
			t.Errorf("%s is not in the map: %v", from, in.StatusMap)
		}
	}
}

func sourceByName(rows []remap, name string) (int, bool) {
	for i := range rows {
		if rows[i].from.Name == name {
			return i, true
		}
	}
	return 0, false
}

func names(rows []remap) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].from.Name)
	}
	return out
}

func TestMove_TheConfirmScreenNamesTheWholeMappingAndSubmitsNothingByItself(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.walkTo("OTHER")

	frame := dr.view()
	mustContain(t, frame, "into OTHER as Story", "watchers", "not emailed", "no undo",
		"PROJ-1", "PROJ-2", "PROJ-3")
	for i := range dr.m.remaps {
		to, _ := dr.m.landing(i)
		mustContain(t, frame, dr.m.remaps[i].from.Name+" -> "+to.Name)
	}
	if n := countCalls(f, "BulkMove"); n != 0 {
		t.Errorf("reaching the confirm screen submitted %d moves; nothing may go before the answer", n)
	}
}

func TestMove_NothingSubmitsFromAStepBeforeTheConfirmScreen(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.typeKey("OTHER")

	for _, at := range []struct {
		name string
		step step
	}{{"the issue type", stepType}, {"the status remap", stepStatus}} {
		if dr.m.step != at.step {
			dr.key("enter")
		}
		dr.key("y")
		if n := countCalls(f, "BulkMove"); n != 0 {
			t.Fatalf("y on %s submitted a move", at.name)
		}
		if dr.m.step == stepRunning {
			t.Fatalf("y on %s started the move", at.name)
		}
	}
}

func TestMove_TogglesWhoIsEmailedOnTheConfirmScreenAndCarriesItIntoTheRequest(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.walkTo("OTHER")

	if dr.m.request().Notify {
		t.Error("a bulk move emails the watchers by default; a move of a thousand issues must not")
	}
	dr.key("n")
	mustContain(t, dr.view(), "watchers -> emailed")
	if !dr.m.request().Notify {
		t.Error("n did not reach the request")
	}
}

func TestMove_SubmitsAndFollowsTheTaskOnTheQueueItWasGiven(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.key("y")

	if !strings.Contains(dr.m.ref.URL, "/bulk/queue/") {
		t.Errorf("the wizard is following %q; a bulk move is polled on its own queue", dr.m.ref.URL)
	}
	if dr.m.step != stepDone {
		t.Fatalf("the move ended on step %d rather than done:\n%s", dr.m.step, dr.view())
	}
	if dr.m.state != jira.TaskComplete {
		t.Errorf("the task ended %q", dr.m.state)
	}
	if got := dr.lastStatus(); got.Text != "3 issues moved to OTHER" {
		t.Errorf("the status line says %q", got.Text)
	}
	mustContain(t, dr.view(), "3 issues moved to OTHER")
}

// CANCEL_REQUESTED is the trap: it is not a stopped task, and a poller with no
// case for it reports a move still running as finished.
func TestMove_ATaskAskedToCancelIsStillRunningAndIsStillFollowed(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.running()

	// The fake walks a task enqueued, running, complete, so this one state is
	// delivered by hand. It is the state a real cancel sits in for as long as it
	// takes to stop.
	before := countCalls(f, "Task")
	cmd := dr.once(taskMsg{gen: dr.m.gen, status: jira.TaskStatus{State: jira.TaskCancelRequested, Progress: 60}})

	if dr.m.step != stepRunning {
		t.Fatalf("CANCEL_REQUESTED left the wizard on step %d; the task has not stopped", dr.m.step)
	}
	mustContain(t, dr.view(), "still running")
	if cmd == nil {
		t.Fatal("a task that has not stopped was not asked about again")
	}
	if _, ok := answer(cmd).(taskMsg); !ok {
		t.Errorf("the follow-up asked for %T rather than the task", answer(cmd))
	}
	if got := countCalls(f, "Task"); got <= before {
		t.Errorf("the queue was asked %d times and then %d", before, got)
	}
}

// The queue names what it could not move by numeric issue id, so the ids are
// what the wizard keeps and the keys it submitted are what it draws: an id is
// not something anybody can search for, open or hand to somebody else.
func TestMove_AFailedTaskDrawsTheKeysBehindTheIdsTheQueueReports(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	f.FailNextTask()
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.key("y")

	if dr.m.state != jira.TaskFailed {
		t.Fatalf("the task ended %q rather than failed", dr.m.state)
	}
	if len(dr.m.failed) != len(iss) {
		t.Fatalf("a failed move of %d issues left the wizard holding %v", len(iss), dr.m.failed)
	}
	frame := dr.view()
	for i := range iss {
		if iss[i].ID == "" {
			t.Fatalf("%s came out of the fake with no id, so this asserts nothing", iss[i].Key)
		}
		mustContain(t, frame, iss[i].Key, "did not move")
	}
	// A raw id drawn beside the keys reads as a key of a project nobody has.
	for _, id := range dr.m.failed {
		mustNotContain(t, frame, id)
	}
}

// An id the wizard cannot place is a real answer and not a broken one: a subtask
// travels with its parent and was never on the list this view submitted.
func TestMove_AFailureItCannotPlaceIsDrawnAsAnIdAndSaidToBeOne(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.running()

	const subtask = "90017"
	dr.send(taskMsg{gen: dr.m.gen, status: jira.TaskStatus{
		State: jira.TaskFailed, Progress: 50, Failed: []string{subtask},
	}})

	mustContain(t, dr.view(), "issue id "+subtask, "did not move")
}

// A complete task with issues in Failed is a legal partial outcome, and
// reporting it as a success is how a move that left issues behind reads as one
// that did not.
func TestMove_APartialOutcomeIsReportedRatherThanReadAsASuccess(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.running()

	left := iss[2]
	dr.send(taskMsg{gen: dr.m.gen, status: jira.TaskStatus{
		State: jira.TaskComplete, Progress: 100, Failed: []string{left.ID},
	}})

	if dr.m.step != stepDone {
		t.Fatalf("a complete task left the wizard on step %d", dr.m.step)
	}
	got := dr.lastStatus()
	if got.Level != kernel.LevelWarn {
		t.Errorf("a move that left an issue behind reported at level %v", got.Level)
	}
	mustContain(t, got.Text, "2 issues moved to OTHER", "1 issue did not")
	mustContain(t, dr.view(), left.Key, "did not move")
}

func TestMove_AsksAboutATaskLessOftenTheLongerItRuns(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.key("y")

	waits := w.asked()
	if len(waits) < 3 {
		t.Fatalf("the queue was asked %d times, which is too few to say anything about the backoff", len(waits))
	}
	if waits[0] != 0 {
		t.Errorf("the first question waited %s; a small move is over before a fixed delay elapses", waits[0])
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] <= waits[i-1] {
			t.Errorf("question %d waited %s after %s, so the wait is not backing off: %v", i, waits[i], waits[i-1], waits)
			break
		}
	}
}

func TestMove_ARateLimitIsAPauseAndNotTheEndOfTheMove(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.running()

	before := len(w.asked())
	cmd := dr.once(failedMsg{gen: dr.m.gen, at: stepRunning, err: &jira.RateLimitError{RetryAfter: 3 * time.Second}})

	if dr.m.step != stepRunning {
		t.Fatalf("a rate limit ended the move: step %d", dr.m.step)
	}
	if dr.m.failure != nil {
		t.Error("a rate limit was kept as a failure")
	}
	if cmd == nil {
		t.Fatal("a rate limit stopped the poll instead of pausing it")
	}
	_ = answer(cmd)
	waits := w.asked()
	if len(waits) <= before {
		t.Fatalf("the queue was asked %d times and then %d", before, len(waits))
	}
	if got := waits[before]; got != 3*time.Second {
		t.Errorf("the pause was %s; Jira asked for 3s", got)
	}
	mustContain(t, dr.view(), "pause")
}

// A poll that stops being able to reach the site has not stopped the move: the
// queue has it, and saying otherwise is the one sentence a user cannot check.
func TestMove_APollThatCannotReachTheSiteSaysTheMoveIsStillJirasToFinish(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	w := &immediate{}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss), withWaiter(w.wait))
	dr.walkTo("OTHER")
	dr.running()
	ref := dr.m.ref.ID

	dr.send(failedMsg{gen: dr.m.gen, at: stepRunning, err: &jira.TransportError{
		Op: "read the bulk queue", Err: errors.New("dial tcp: connection refused"),
	}})

	frame := dr.view()
	mustContain(t, frame, "stopped being able to follow it", ref, "still Jira's to finish")
	mustNotContain(t, frame, "moved to OTHER.")
}

func TestMove_SaysWhyItCannotMoveAnythingWhenTheTokenMayNot(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	d := testDeps(f)
	d.Caps = noMoveCaps()
	dr := newDriver(t, d, 100, 20, WithIssues(iss))

	mustContain(t, dr.view(), "You need the Bulk Change permission to move issues between projects")
	if n := countCalls(f, "Search"); n != 0 {
		t.Errorf("a session that may not move issues asked the site %d times anyway", n)
	}
	dr.typeKey("OTHER")
	if dr.m.step == stepType {
		t.Error("a session that may not move issues was allowed past the first step")
	}
	if got := dr.lastStatus().Text; !strings.Contains(got, "Bulk Change") {
		t.Errorf("the refusal reached the status line as %q rather than in the probe's own words", got)
	}
}

func TestMove_TheCapabilityAnswerArrivingLaterIsTakenAndDrawn(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	d := testDeps(f)
	d.Caps = noMoveCaps()
	dr := newDriver(t, d, 100, 20, WithIssues(iss))
	mustContain(t, dr.view(), "Bulk Change")

	dr.send(kernel.CapabilitiesMsg{Caps: fullCaps()})
	mustNotContain(t, dr.view(), "Bulk Change")
	dr.typeKey("OTHER")
	if dr.m.step != stepType {
		t.Errorf("the wizard is on step %d after the capability arrived", dr.m.step)
	}
}

func TestMove_RefusesMoreIssuesThanOneMoveTakesRatherThanSendingTheFirstThousand(t *testing.T) {
	t.Parallel()
	f := newFake(2, jiratest.WithIssues(jiratest.GenFor("OTHER", 2)))
	iss := make([]jira.Issue, 0, maxKeys+1)
	for i := range maxKeys + 1 {
		iss = append(iss, jira.Issue{
			Key:     "PROJ-" + strconv.Itoa(i+1),
			Project: jira.ProjectRef{Key: "PROJ"},
			Status:  jira.Status{ID: "10201", Name: "Triage", Category: jira.CategoryToDo},
		})
	}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.walkTo("OTHER")
	dr.key("y")

	if n := countCalls(f, "BulkMove"); n != 0 {
		t.Fatalf("a selection of %d issues was submitted anyway", len(iss))
	}
	mustContain(t, dr.view(), "1000 issues in one move", "1001")
}

func TestMove_TheSiteSayingNoIsDrawnInItsOwnWordsAndKeptOnScreen(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a capability refusal": {
			err:  &jira.CapabilityError{Capability: jira.CapBulkMove, Reason: "Bulk Change is not granted to you"},
			want: "Bulk Change is not granted to you",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/issuetype"},
			want: "rate limited by Jira",
		},
		"a transport failure": {
			err:  &jira.TransportError{Op: "read the issue types", Err: errors.New("dial tcp: no route to host")},
			want: "no route to host",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f, iss := twoProjects(t)
			dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
			f.FailNext(tc.err)
			dr.typeKey("OTHER")

			mustContain(t, dr.view(), tc.want)
			if got := dr.lastStatus(); !strings.Contains(got.Text, tc.want) {
				t.Errorf("the status line says %q rather than the site's own words", got.Text)
			}
			// A status line is gone by the next keypress; the pane is not.
			dr.key("j")
			mustContain(t, dr.view(), tc.want)
		})
	}
}

func TestMove_KeepsItsReadOnABlurAndDropsItOnAClose(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))

	dr.m.step = stepTyping
	dr.m.input.SetValue("OTHER")
	cmd := dr.m.lookUp("OTHER")
	if cmd == nil {
		t.Fatal("looking a project key up returned no command")
	}
	dr.send(kernel.FocusMsg{Focused: false})
	if msg := answer(cmd); !isVocabulary(msg) {
		t.Errorf("a blur gave up the read: %T", msg)
	}

	cmd = dr.m.lookUp("OTHER")
	dr.m.Close()
	msg := answer(cmd)
	failed, ok := msg.(failedMsg)
	if !ok {
		t.Fatalf("a closed wizard still answered with %T", msg)
	}
	if !errors.Is(failed.err, context.Canceled) {
		t.Errorf("the read came back with %v rather than a cancellation", failed.err)
	}
}

func isVocabulary(msg tea.Msg) bool {
	_, ok := msg.(vocabularyMsg)
	return ok
}

func TestMove_AnAnswerToAQuestionAlreadyMovedPastIsDropped(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))
	dr.typeKey("OTHER")
	types := len(dr.m.vocab)

	dr.send(vocabularyMsg{gen: dr.m.gen - 1, project: "OTHER", types: nil})
	if len(dr.m.vocab) != types {
		t.Error("an answer to a question already moved past replaced what is on screen")
	}
	dr.send(vocabularyMsg{gen: dr.m.gen, project: "SOMEWHEREELSE", types: nil})
	if len(dr.m.vocab) != types {
		t.Error("an answer about another project replaced what is on screen")
	}
}

func TestMove_TakesRawKeysOnlyWhileAProjectKeyIsBeingTyped(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 20, WithIssues(iss))

	if dr.m.WantsRawKeys() {
		t.Error("the wizard claims every key before anything is being typed into it")
	}
	dr.key("i")
	if !dr.m.WantsRawKeys() {
		t.Error("a project key cannot be typed without the raw keys: q would quit and every digit would be eaten")
	}
	dr.typeText("OTH")
	if got := dr.m.input.Value(); got != "OTH" {
		t.Errorf("the field holds %q", got)
	}
	dr.key("esc")
	if dr.m.WantsRawKeys() || dr.m.step != stepTarget {
		t.Errorf("esc left the wizard on step %d still taking typing", dr.m.step)
	}
}

func TestMove_WalksBackAStepAtATimeAndKeepsWhatWasChosen(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.walkTo("OTHER")

	dr.key("shift+tab")
	if dr.m.step != stepStatus {
		t.Fatalf("shift+tab from the confirm screen landed on step %d", dr.m.step)
	}
	dr.key("shift+tab")
	if dr.m.step != stepType || dr.m.cursor != dr.m.typeAt {
		t.Fatalf("shift+tab landed on step %d with the cursor at %d rather than on the type that was chosen (%d)",
			dr.m.step, dr.m.cursor, dr.m.typeAt)
	}
	dr.key("shift+tab")
	if dr.m.step != stepTarget {
		t.Fatalf("shift+tab landed on step %d rather than the project", dr.m.step)
	}
	dr.key("shift+tab")
	if dr.m.step != stepTarget {
		t.Error("shift+tab from the first step went somewhere")
	}
}

// A subtask type in the target needs a parent over there, and a bulk move
// carries no way to name one. Saying so beats a 400 after the queue has taken it.
func TestMove_WillNotMoveIssuesOntoASubtaskType(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.typeKey("OTHER")

	at := slices.IndexFunc(dr.m.vocab, func(v jira.IssueTypeStatuses) bool { return v.Type.Subtask })
	if at < 0 {
		t.Fatal("the fake offers no subtask type")
	}
	dr.m.cursor = at
	dr.key("enter")

	if dr.m.step != stepType {
		t.Fatalf("a subtask type was accepted: step %d", dr.m.step)
	}
	mustContain(t, dr.view(), "subtask type", "cannot give these issues a parent")
	if got := dr.lastStatus().Text; !strings.Contains(got, "travel with them anyway") {
		t.Errorf("the refusal reads %q and does not say what does happen to subtasks", got)
	}
}

// Naming one mandatory field on this endpoint stops every other one being kept
// from the source, so a half-answered group may not be submitted.
func TestMove_WillNotSubmitAHalfAnsweredGroupOfMandatoryFields(t *testing.T) {
	t.Parallel()
	f, iss := twoProjects(t)
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.typeKey("OTHER")
	dr.key("enter")

	ref := func(id, name string) jira.FieldRef { return jira.FieldRef{ID: id, Name: name} }
	dr.send(schemaMsg{gen: dr.m.gen, schema: jira.Schema{Fields: []jira.FieldMeta{
		{Field: ref("customfield_1", "Erfassungsart"), Name: "Erfassungsart", Required: true,
			AllowedValues: []jira.Option{{ID: "1", Label: "Eins"}}},
		{Field: ref("customfield_2", "Kostenstelle"), Name: "Kostenstelle", Required: true},
	}}})
	dr.key("enter")
	if dr.m.step != stepFields {
		t.Fatalf("a target insisting on two fields left the wizard on step %d", dr.m.step)
	}

	// Both fields keep what the source holds, so the group is whole and no value
	// is sent at all.
	if in := dr.m.request(); in.Fields.Len() != 0 {
		t.Errorf("a group left alone sent %d values; every mandatory field was meant to be kept", in.Fields.Len())
	}
	dr.key("enter")
	if dr.m.step != stepConfirm {
		t.Fatalf("a group left alone was refused: step %d", dr.m.step)
	}

	dr.key("shift+tab")
	dr.key("right")
	if dr.m.fields[0].retains() {
		t.Fatal("the right key set no value")
	}
	dr.key("enter")
	if dr.m.step == stepConfirm {
		t.Fatal("a half-answered group of mandatory fields reached the confirm screen")
	}
	mustContain(t, dr.view(), "Kostenstelle", "stops it being kept from the source")

	dr.key("left")
	if !dr.m.fields[0].retains() {
		t.Fatal("a value cannot be put back to being kept from the source")
	}
	dr.key("enter")
	if dr.m.step != stepConfirm {
		t.Errorf("putting the value back left the wizard on step %d", dr.m.step)
	}
}

func TestMove_MandatoryFieldsAreWhateverTheTargetSaysAtRuntime(t *testing.T) {
	t.Parallel()
	ref := func(id, name string) jira.FieldRef { return jira.FieldRef{ID: id, Name: name} }
	schema := jira.Schema{
		Fields: []jira.FieldMeta{
			{Field: ref("summary", "Summary"), Name: "Summary", Required: true},
			{Field: ref("project", "Project"), Name: "Project", Required: true},
			{Field: ref("issuetype", "Issue Type"), Name: "Issue Type", Required: true},
			{Field: ref("status", "Status"), Name: "Status", Required: true},
			{Field: ref("customfield_1", "Erfassungsart"), Name: "Erfassungsart", Required: true,
				AllowedValues: []jira.Option{{ID: "1", Label: "Eins"}, {ID: "2", Label: "Zwei"}}},
			{Field: ref("customfield_2", "Kostenstelle"), Name: "Kostenstelle", Required: true, HasDefault: true},
			{Field: ref("customfield_3", "Notiz"), Name: "Notiz"},
		},
	}
	got := mandatory(schema)
	if len(got) != 2 {
		t.Fatalf("the target insists on %d fields a move has to reckon with, want 2: %v", len(got), fieldNames(got))
	}
	if got[0].meta.Field.ID != "customfield_1" || got[1].meta.Field.ID != "customfield_2" {
		t.Errorf("the fields left over are %v", fieldNames(got))
	}
	for i := range got {
		if !got[i].retains() {
			t.Errorf("%s starts out being written rather than kept from the source", got[i].meta.Field.ID)
		}
	}
	if written(got) {
		t.Error("a group nobody has touched reports that something is being written")
	}
}

func fieldNames(fields []pending) []string {
	out := make([]string, 0, len(fields))
	for i := range fields {
		out = append(out, fields[i].meta.Field.ID)
	}
	return out
}

func TestMove_DrawsOnlyTheRowsThatFit(t *testing.T) {
	t.Parallel()
	f := newFake(2, jiratest.WithIssues(jiratest.GenFor("OTHER", 2)))
	iss := make([]jira.Issue, 0, 400)
	for i := range 400 {
		iss = append(iss, jira.Issue{
			Key: "PROJ-" + strconv.Itoa(i+1), Summary: "row " + strconv.Itoa(i+1),
			Project: jira.ProjectRef{Key: "PROJ"},
			Status:  jira.Status{ID: "10201", Name: "Triage", Category: jira.CategoryToDo},
		})
	}
	dr := newDriver(t, testDeps(f), 100, 24, WithIssues(iss))
	dr.walkTo("OTHER")

	frame := dr.view()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Errorf("the frame is %d lines in a box 24 tall", got)
	}
	mustContain(t, frame, "PROJ-1")
	mustNotContain(t, frame, "PROJ-399")
}
