package plan

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// answer is what the kernel hands a view: the command's own reply with the
// envelope the kernel addresses it by taken off.
func answer(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if reply, addressed := msg.(kernel.ReplyMsg); addressed {
		return reply.Msg
	}
	return msg
}

func TestPlans_ASiteThatAnswersPutsItsOwnPlansOnScreen(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, testDeps(f), 120, 20, WithDefined(defined()))

	if dr.m.source != fromSite {
		t.Fatalf("the plans on screen came from the profile, and the site answered")
	}
	if len(dr.m.plans) == 0 {
		t.Fatal("the site answered and nothing is on screen")
	}
	for _, row := range dr.m.plans {
		if row.plan.Local {
			t.Errorf("%q is marked local and the site answered for it", row.plan.Name)
		}
	}
	mustContain(t, dr.view(), "the site's plans")
	mustNotContain(t, dr.view(), "Q3 delivery")
}

// The whole design turns on this: the refusal is an answer with a reason, in the
// same view, and not an error screen.
func TestPlans_ARefusalPutsTheProfilesPlansOnScreenWithTheReason(t *testing.T) {
	t.Parallel()

	const said = "the Plans API needs Administer Jira"
	f := newFake(5)
	f.FailNext(&jira.CapabilityError{Capability: jira.CapPlans, Reason: said})
	dr := newDriver(t, testDeps(f), 120, 20, WithDefined(defined()))

	if dr.m.source != fromProfile {
		t.Fatal("a refusal left the view waiting for the site's plans")
	}
	if got := dr.names(); len(got) != 2 || got[0] != "Q3 delivery" {
		t.Errorf("the plans on screen are %v, want the two the profile defines", got)
	}
	frame := dr.view()
	mustContain(t, frame, said, "plans defined in this profile", "Q3 delivery")
	if dr.m.failure != nil {
		t.Errorf("the refusal was kept as a failure as well: %v", dr.m.failure)
	}
	if got := dr.lastStatus(); got.Level != kernel.LevelWarn || !strings.Contains(got.Text, said) {
		t.Errorf("the status line said %+v, want the site's own words as a warning", got)
	}
}

// A probe that has already answered saves the round trip: the profile's plans
// are the first frame rather than what is drawn after a refusal comes back.
func TestPlans_AProbeThatAlreadySaidNoAsksNothingOfTheSite(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, refusedDeps(f), 120, 20, WithDefined(defined()))

	if n := countCalls(f, "Plans"); n != 0 {
		t.Errorf("the view asked for the plans %d times over a capability the probe had already refused", n)
	}
	mustContain(t, dr.view(), "the Plans API needs Administer Jira", "Q3 delivery")
}

// Only the refusal that names CapPlans has a fallback. Everything else is a
// read to try again, and the pane keeps saying so after the status line has
// gone.
func TestPlans_EveryOtherFailureStaysAFailureWithItsOwnReason(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		says string
	}{
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			says: "",
		},
		"a transport failure": {
			err:  &jira.TransportError{Op: "GET /rest/api/3/plans/plan", Err: errors.New("no such host")},
			says: "no such host",
		},
		"a refusal about something else": {
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "boards need the Browse Projects permission"},
			says: "boards need the Browse Projects permission",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(5)
			f.FailNext(tc.err)
			dr := newDriver(t, testDeps(f), 120, 20, WithDefined(defined()))

			if dr.m.source == fromProfile {
				t.Fatalf("%v was treated as the plans permission being absent", tc.err)
			}
			if dr.m.failure == nil {
				t.Fatal("the failure was not kept, so the pane stops saying it")
			}
			frame := dr.view()
			mustContain(t, frame, "The plans could not be read.", retryHint)
			if tc.says != "" {
				mustContain(t, frame, tc.says)
			}
			if got := dr.lastStatus(); got.Level != kernel.LevelError {
				t.Errorf("the status line was %+v, want the failure reported as one", got)
			}
		})
	}
}

// A rate limit says how long to wait, and the pane is where that survives the
// next keypress.
func TestPlans_ARateLimitSaysHowLongInThePane(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	f.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
	dr := newDriver(t, testDeps(f), 120, 20)

	reason, _ := jira.Reason(dr.m.failure)
	if reason == "" {
		t.Fatal("the rate limit carried no words of its own")
	}
	mustContain(t, dr.view(), reason)
}

func TestPlans_OpeningAPlanReadsTheReleasesOfItsProjects(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, refusedDeps(f), 120, 30, WithDefined(defined()))
	dr.key("enter")

	if n := countCalls(f, "Versions"); n != 1 {
		t.Fatalf("opening one plan read the versions %d times, want once", n)
	}
	frame := dr.view()
	mustContain(t, frame, "source", "project PROJ", "filter 10023", "search", "releases", "1.0", "released")
	mustContain(t, frame, "Target start")

	dr.key("enter")
	if strings.Contains(dr.view(), "project PROJ") {
		t.Error("closing the plan left its sources on screen")
	}
	dr.key("enter")
	if n := countCalls(f, "Versions"); n != 1 {
		t.Errorf("reopening a plan read the versions again (%d reads); what was read is still held", n)
	}
}

// A plan the site answered with names each project by a numeric id. Nothing in
// the port turns one into a key, so the row says that rather than printing the
// number as though it were a project, and no version read is attempted.
func TestPlans_ASitePlanSaysWhyItsProjectsCannotBeNamed(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, testDeps(f), 120, 30)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{
		ID: "42", Name: "Delivery", Status: "Active",
		Sources: []jira.PlanSource{{Type: jira.PlanSourceProject, Value: "10432"}},
	}}})
	before := countCalls(f, "Versions")
	dr.key("enter")

	frame := dr.view()
	mustContain(t, frame, "project id 10432", "cannot resolve an id to a project key")
	mustNotContain(t, frame, "project 10432")
	if got := countCalls(f, "Versions") - before; got != 0 {
		t.Errorf("the view made %d version reads for a project it only has an id for", got)
	}
}

// A board source cannot be searched or read either, and it must not be dropped:
// a source left out turns a plan into a narrower plan that nothing explains.
func TestPlans_ASourceThisViewCannotUseIsStillDrawn(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(5)), 120, 30)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{
		ID: "42", Name: "Delivery",
		Sources: []jira.PlanSource{
			{Type: jira.PlanSourceBoard, Value: "17"},
			{Type: jira.PlanSourceType("custom"), Value: "9"},
		},
	}}})
	dr.key("enter")

	mustContain(t, dr.view(), "board 17", "custom 9")
}

func TestPlans_AnAnswerToAQuestionAlreadyChangedIsDropped(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(5)), 120, 20)
	stale := dr.m.gen - 1

	dr.send(plansMsg{gen: stale, plans: []jira.Plan{{ID: "9", Name: "Stale plan"}}})
	mustNotContain(t, dr.view(), "Stale plan")

	dr.send(failedMsg{gen: stale, err: &jira.CapabilityError{Capability: jira.CapPlans, Reason: "nope"}})
	if dr.m.source == fromProfile {
		t.Error("a refusal to a question already changed switched the view over anyway")
	}
}

func TestPlans_KeepsItsReadOnABlurAndDropsItOnAClose(t *testing.T) {
	t.Parallel()

	f := newFake(5)

	kept, reading := unrunRead(t, testDeps(f))
	if _, more := kept.Update(kernel.FocusMsg{}); more != nil {
		t.Fatal("losing the keyboard asked for more work")
	}
	if _, gaveUp := answer(reading).(failedMsg); gaveUp {
		t.Error("the view gave up its read when it merely lost the keyboard")
	}

	dropped, alsoReading := unrunRead(t, testDeps(f))
	closer, ok := dropped.(kernel.Closer)
	if !ok {
		t.Fatal("the view does not implement kernel.Closer, so nothing stops its read")
	}
	closer.Close()

	failed, ok := answer(alsoReading).(failedMsg)
	if !ok {
		t.Fatalf("the read came back as %T, want the failure a cancelled context produces", answer(alsoReading))
	}
	if !errors.Is(failed.err, context.Canceled) {
		t.Errorf("err = %v, want the context's own error", failed.err)
	}
}

// unrunRead builds a view that has asked the site for its plans and hands back
// the command carrying that read rather than running it, so a test can decide
// what happens to it first.
func unrunRead(t *testing.T, d kernel.Deps) (kernel.View, tea.Cmd) {
	t.Helper()
	view, ok := New(d).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 20})
	cmd := next.Init()
	if cmd == nil {
		t.Fatal("the view asked the site for nothing")
	}
	return next, cmd
}

// A capability that comes back is worth a read; one that goes away puts the
// profile's plans up with the new reason and asks for nothing.
func TestPlans_AFreshProbeChangesWhichPlansAreOnScreen(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, refusedDeps(f), 120, 20, WithDefined(defined()))
	if dr.m.source != fromProfile {
		t.Fatal("the probe refused the plans and the view went to the site anyway")
	}

	granted := fullCaps()
	dr.send(kernel.CapabilitiesMsg{Caps: granted})
	if dr.m.source != fromSite {
		t.Fatal("the capability came back and the view kept the profile's plans")
	}
	if n := countCalls(f, "Plans"); n != 1 {
		t.Errorf("the view read the plans %d times after the capability came back, want once", n)
	}

	revoked := fullCaps()
	revoked.Plans = jira.Capability{Reason: "your token has been made read-only"}
	dr.send(kernel.CapabilitiesMsg{Caps: revoked})
	if dr.m.source != fromProfile {
		t.Fatal("the capability went away and the view kept the site's plans")
	}
	mustContain(t, dr.view(), "your token has been made read-only")
	if n := countCalls(f, "Plans"); n != 1 {
		t.Errorf("a capability going away cost %d more reads", n-1)
	}
}

// The stand-in plans are the session's project and its saved queries, so both
// of them moving moves the list. It is only a stand-in: a profile that defines
// its own plans keeps them.
func TestPlans_TheStandInPlansFollowTheSessionUntilTheProfileDefinesItsOwn(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, refusedDeps(f), 120, 20)
	if got := dr.names(); len(got) != 1 || got[0] != "PROJ" {
		t.Fatalf("the plans on screen are %v, want the session's project", got)
	}

	saved, err := app.NewSavedQueries(app.SavedQuery{Name: "Mine", JQL: "assignee = currentUser()"})
	if err != nil {
		t.Fatal(err)
	}
	dr.send(kernel.SavedQueriesMsg{Queries: saved})
	dr.send(kernel.ProjectMsg{Project: "OPS"})
	if got := dr.names(); len(got) != 2 || got[0] != "OPS" || got[1] != "Mine" {
		t.Errorf("the plans on screen are %v, want the new project and the saved query", got)
	}

	own := newDriver(t, refusedDeps(f), 120, 20, WithDefined(defined()))
	own.send(kernel.ProjectMsg{Project: "OPS"})
	if got := own.names(); len(got) != 2 || got[0] != "Q3 delivery" {
		t.Errorf("a profile's own plans changed with the session: %v", got)
	}
}

// A refresh over plans that came from a file is invisible unless it says
// something, and a refresh nobody can see is indistinguishable from one that
// never ran.
func TestPlans_ARefreshOverTheProfilesPlansSaysWhatItDid(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, refusedDeps(f), 120, 20, WithDefined(defined()))
	dr.send(kernel.RefreshMsg{})

	if n := countCalls(f, "Plans"); n != 0 {
		t.Errorf("a refresh asked the site for plans it may not read (%d calls)", n)
	}
	if got := dr.lastStatus().Text; !strings.Contains(got, "defined in this profile") {
		t.Errorf("the refresh said %q, which does not say where the plans came from", got)
	}
}

func TestPlans_ARefreshOverTheSitesPlansReadsThemAgain(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, testDeps(f), 120, 20)
	dr.send(kernel.RefreshMsg{Purge: true})

	if n := countCalls(f, "Plans"); n != 2 {
		t.Errorf("the plans were read %d times over an open and a refresh, want twice", n)
	}
}

// A read that fails for one of a plan's projects fails the whole expansion: a
// plan drawn from two projects and answered for by one is a shorter list that
// nothing on screen explains.
func TestPlans_AProjectThatRefusesFailsTheWholeExpansion(t *testing.T) {
	t.Parallel()

	f := newFake(5, jiratest.WithProject("OPS", jiratest.Kanban))
	dr := newDriver(t, refusedDeps(f), 120, 30, WithDefined([]Defined{
		{Name: "Both", Projects: []string{"PROJ", "OPS"}},
	}))
	f.FailNext(&jira.CapabilityError{Capability: jira.CapBoards, Reason: "you may not browse OPS"})
	dr.key("enter")

	mustContain(t, dr.view(), "you may not browse OPS")
	mustNotContain(t, dr.view(), "1.0")
}

func TestPlans_ASessionWithNoConnectionStillDrawsTheProfilesPlans(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(nil), 120, 20, WithDefined(defined()))

	mustContain(t, dr.view(), "there is no Jira connection in this session", "Q3 delivery")
	dr.key("enter")
	mustContain(t, dr.view(), "project PROJ")
}

func TestPlans_TheRowsFollowTheCursorThroughAnAnswer(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(5)), 120, 20)
	plans := []jira.Plan{{ID: "1", Name: "First"}, {ID: "2", Name: "Second"}, {ID: "3", Name: "Third"}}
	dr.send(plansMsg{gen: dr.m.gen, plans: plans})
	dr.key("j", "j")
	if got := dr.m.plans[dr.m.planUnderCursor()].plan.Name; got != "Third" {
		t.Fatalf("the cursor is on %q, want the third plan", got)
	}

	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{plans[2], plans[0]}})
	if got := dr.m.plans[dr.m.planUnderCursor()].plan.Name; got != "Third" {
		t.Errorf("an answer that reordered the plans left the cursor on %q", got)
	}
}

// Only the visible window is built, whatever the profile defines.
func TestPlans_OnlyTheRowsThatFitAreRendered(t *testing.T) {
	t.Parallel()

	many := make([]Defined, 0, 500)
	for i := range 500 {
		many = append(many, Defined{Name: "plan-" + strconv.Itoa(i), Projects: []string{"PROJ"}})
	}
	dr := newDriver(t, refusedDeps(newFake(5)), 120, 12, WithDefined(many))

	frame := strings.Split(dr.view(), "\n")
	if len(frame) != 12 {
		t.Fatalf("the frame is %d lines at a height of 12", len(frame))
	}
	if strings.Contains(dr.view(), "plan-40") {
		t.Error("a row far below the window was drawn")
	}
}

func TestPlans_TheClauseAPlanRendersTo(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in      Defined
		jql     string
		problem string
	}{
		"one project": {
			in:  Defined{Projects: []string{"ENG"}},
			jql: `project = "ENG"`,
		},
		"two projects and a filter": {
			in:  Defined{Projects: []string{"ENG", "OPS"}, Filters: []string{"10023"}},
			jql: `(project IN ("ENG", "OPS") OR filter IN (10023))`,
		},
		"a narrowing over the sources": {
			in:  Defined{Projects: []string{"ENG"}, JQL: "labels = roadmap"},
			jql: `project = "ENG" AND (labels = roadmap)`,
		},
		"JQL and nothing else": {
			in:  Defined{JQL: "resolution IS EMPTY"},
			jql: "resolution IS EMPTY",
		},
		"a quote in a project key": {
			in:  Defined{Projects: []string{`we"ird`}},
			jql: `project = "we\"ird"`,
		},
		"a filter named rather than numbered": {
			in:      Defined{Filters: []string{"my filter"}},
			problem: "numeric id",
		},
		"nothing at all": {
			in:      Defined{Name: "empty"},
			problem: "names no project",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jql, problem := tc.in.clause()
			if jql != tc.jql {
				t.Errorf("jql = %q, want %q", jql, tc.jql)
			}
			switch {
			case tc.problem == "" && problem != "":
				t.Errorf("problem = %q, want none", problem)
			case tc.problem != "" && !strings.Contains(problem, tc.problem):
				t.Errorf("problem = %q, want it to mention %q", problem, tc.problem)
			}
		})
	}
}

// A plan the profile got wrong says so on its own row and is not silently
// dropped, and it never reaches a search.
func TestPlans_APlanThatCannotBecomeASearchSaysSo(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, refusedDeps(newFake(5)), 120, 20, WithDefined([]Defined{
		{Name: "Broken", Filters: []string{"the roadmap one"}},
	}))
	dr.key("enter")

	frame := dr.view()
	mustContain(t, frame, "Broken", "problem", "numeric id")
	if got := dr.m.plans[0].jql; got != "" {
		t.Errorf("the plan rendered to %q, and it cannot be turned into a search at all", got)
	}
}

// The whole project-source path here exists because the site answers with a
// numeric project id, and everything above the port is tested against the fake:
// a fake that answered with a key instead would leave the one shape this view has
// to explain on screen as the one no test ever meets.
func TestPlans_TheFakeAnswersAProjectSourceTheWayTheSiteDoes(t *testing.T) {
	t.Parallel()

	plans, err := newFake(1).Plans(t.Context())
	if err != nil {
		t.Fatalf("Plans: %v", err)
	}
	sources := 0
	for _, plan := range plans {
		for _, source := range plan.Sources {
			if source.Type != jira.PlanSourceProject {
				continue
			}
			sources++
			if !digits(source.Value) {
				t.Errorf("jiratest answers a project source with %q, which is a project key; "+
					"pkg/jira/cloud maps this field from a numeric project id, and no port method "+
					"turns an id into a key, so a view tested only against this meets a shape no "+
					"site sends", source.Value)
			}
		}
	}
	if sources == 0 {
		t.Fatal("the fake answered with no project sources at all, so this checked nothing")
	}
}
