package release

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestReleases_DrawsTheProjectsVersionsAndWhatStateEachIsIn(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(12)), 120, 20)
	frame := dr.view()

	mustContain(t, frame, "1.0", "2.0", "3.0", stateReleased, stateUnreleased)
	if got := len(dr.list().versions); got != 3 {
		t.Errorf("the list holds %d versions, want the three the project has", got)
	}
}

// The count is one request per version, so a list of them is not a list of
// requests. What the column says instead is that nobody has asked — never a
// zero, which would read as a version with nothing left open on it.
func TestReleases_TheOpenColumnSaysNobodyHasCountedRatherThanDrawingAZero(t *testing.T) {
	t.Parallel()

	fake := newFake(12)
	dr := listOf(t, testDeps(fake), 120, 20)

	if got := countCalls(fake, "UnresolvedCount"); got != 0 {
		t.Errorf("drawing the list cost %d unresolved counts; they are one request each", got)
	}
	for _, v := range dr.list().versions {
		if v.Unresolved != nil {
			t.Errorf("%s came back with a count nothing asked for", v.Name)
		}
		if got := openLabel(v); got != unknownOpen {
			t.Errorf("%s draws %q in the open column, want %q", v.Name, got, unknownOpen)
		}
	}
	mustContain(t, dr.view(), "open counts are read when a version is released")
}

// Five kinds of empty, and a reader cannot act on the difference unless the pane
// names it.
func TestReleases_EveryKindOfEmptySaysWhichItIs(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		deps  func() kernel.Deps
		after func(*driver)
		want  string
	}{
		"no site in this session": {
			deps: func() kernel.Deps {
				d := testDeps(nil)
				d.Jira = nil
				return d
			},
			want: "No Jira connection in this session yet.",
		},
		"no project to read versions of": {
			deps: func() kernel.Deps {
				d := testDeps(newFake(4))
				d.Project = ""
				return d
			},
			want: "Versions belong to a project, and this session is not scoped to one.",
		},
		"a read in flight": {
			deps: func() kernel.Deps { return testDeps(newFake(4)) },
			after: func(dr *driver) {
				m := dr.list()
				m.versions, m.loading, m.loaded, m.failure = nil, true, false, nil
				m.rebuildCells()
				m.sum = ""
			},
			want: "Reading the versions",
		},
		"a read that failed": {
			deps: func() kernel.Deps { return testDeps(newFake(4)) },
			after: func(dr *driver) {
				m := dr.list()
				m.versions, m.loading, m.failure, m.what = nil, false, errors.New("the host said no"), whatVersions
				m.rebuildCells()
				m.sum = ""
			},
			want: whatVersions,
		},
		"a project with no versions": {
			deps: func() kernel.Deps { return testDeps(newFake(4)) },
			after: func(dr *driver) {
				m := dr.list()
				m.versions, m.loaded = nil, true
				m.rebuildCells()
				m.sum = ""
			},
			want: "PROJ has no versions yet.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := listOf(t, tc.deps(), 100, 14)
			if tc.after != nil {
				tc.after(dr)
			}
			mustContain(t, dr.view(), tc.want)
		})
	}
}

// A refusal reaches the reader in the words the site used, on the status line
// and in the pane, because a status line is gone by the next keypress.
func TestReleases_ARefusedReadKeepsSayingSo(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a token that may not read them": {
			err:  &jira.CapabilityError{Capability: jira.CapBoards, Reason: "you need Browse Projects on PROJ"},
			want: "you need Browse Projects on PROJ",
		},
		"a site that is rate limiting": {
			err:  &jira.RateLimitError{Endpoint: "/rest/api/3/project/PROJ/version"},
			want: "rate limited by Jira",
		},
		"a transport that failed": {
			err:  &jira.TransportError{Op: "GET /rest/api/3/project/PROJ/version", Status: 502},
			want: "failed with HTTP 502",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(4)
			fake.FailNext(tc.err)
			dr := listOf(t, testDeps(fake), 100, 14)

			mustContain(t, dr.view(), whatVersions, tc.want)
			if got := dr.lastStatus().Text; !strings.Contains(got, tc.want) {
				t.Errorf("the status line says %q, want the site's own words %q", got, tc.want)
			}
			mustContain(t, dr.view(), retryHint)
		})
	}
}

// An answer to a question that has since changed is dropped rather than drawn.
func TestReleases_AnAnswerToAQuestionAlreadyChangedIsDropped(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 100, 14)
	held := dr.list().versions

	dr.send(versionsMsg{gen: dr.list().gen - 1, versions: []jira.Version{{ID: "gone", Name: "9.9"}}})
	if got := dr.list().versions; len(got) != len(held) || got[0].ID != held[0].ID {
		t.Errorf("a stale answer replaced the rows: %+v", got)
	}
	mustNotContain(t, dr.view(), "9.9")
}

// The editor is where every letter belongs to the text, so the view has to claim
// the keys — and only there, or j and k stop moving the cursor.
func TestReleases_ClaimsTheKeysOnlyWhileAVersionIsBeingTyped(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 100, 20)
	if dr.list().WantsRawKeys() {
		t.Error("the list claims raw keys while it is only being read")
	}
	dr.key("n")
	if !dr.list().WantsRawKeys() {
		t.Error("the editor does not claim raw keys, so a name loses its digits and q quits")
	}
	dr.key("esc")
	if dr.list().WantsRawKeys() {
		t.Error("the list still claims raw keys after the editor was closed")
	}
}

func TestReleases_RefusesToThrowAwayAVersionBeingTyped(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 100, 20)
	if _, blocked := dr.list().BlocksClose(); blocked {
		t.Error("a list nobody is typing into refuses to be closed")
	}
	dr.key("n")
	if _, blocked := dr.list().BlocksClose(); blocked {
		t.Error("an editor with nothing typed into it refuses to be closed")
	}
	dr.typeText("4.0")
	reason, blocked := dr.list().BlocksClose()
	if !blocked {
		t.Fatal("a typed version can be thrown away without a word")
	}
	mustContain(t, reason, "ctrl+s")
}

func TestReleases_CreatingAVersionSendsTheProjectAndTheTypedValues(t *testing.T) {
	t.Parallel()

	fake := newFake(4)
	watch := watching(fake)
	dr := listOf(t, testDeps(watch), 100, 24)

	dr.key("n")
	dr.typeText("4.0")
	dr.key("tab")
	dr.typeText("the next one")
	dr.key("tab", "tab")
	dr.typeText("2026-06-01")
	dr.key("ctrl+s")

	sent := watch.saved()
	if len(sent) != 1 {
		t.Fatalf("the site was sent %d saves, want one: %+v", len(sent), sent)
	}
	in := sent[0]
	switch {
	case in.ID != "":
		t.Errorf("a create carried the id %q; an empty id is what creates", in.ID)
	case in.ProjectKey != "PROJ":
		t.Errorf("a create carried project %q, want PROJ", in.ProjectKey)
	case in.Name != "4.0":
		t.Errorf("the name went as %q", in.Name)
	case in.Description != "the next one":
		t.Errorf("the description went as %q", in.Description)
	case in.ReleaseDate.String() != "2026-06-01":
		t.Errorf("the release date went as %q", in.ReleaseDate.String())
	}
	mustContain(t, dr.lastStatus().Text, "4.0 created")
	if dr.list().mode == editing {
		t.Error("the editor is still open over a version the site has stored")
	}
	mustContain(t, dr.view(), "4.0", "the next one")
	if got := dr.list().selectedID(); got == "" || got == oneOh {
		t.Errorf("the cursor is on %q; a create leaves it on what was created", got)
	}
}

// A date this program cannot read is refused here, without a round trip and
// without turning into a day somewhere east of the reader.
// The editor is walked with tab and shift+tab, which are the two strokes a
// field list can spare while every letter belongs to the text.
func TestReleases_TheEditorWalksBothWaysAndWrapsRound(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 100, 24)
	dr.key("n")
	if got := dr.list().form.at; got != fieldName {
		t.Fatalf("the editor opened on field %d", got)
	}
	dr.key("shift+tab")
	if got := dr.list().form.at; got != fieldCount-1 {
		t.Errorf("shift+tab from the first field went to %d, want the last", got)
	}
	dr.key("tab")
	if got := dr.list().form.at; got != fieldName {
		t.Errorf("tab from the last field went to %d, want the first", got)
	}
	dr.typeText("4.0")
	dr.key("tab")
	dr.typeText("a note")
	dr.key("shift+tab")
	if got := dr.list().form.input.Value(); got != "4.0" {
		t.Errorf("walking back and forth left %q in the name", got)
	}
}

func TestReleases_ADateItCannotReadIsRefusedWithoutAskingTheSite(t *testing.T) {
	t.Parallel()

	fake := newFake(4)
	dr := listOf(t, testDeps(fake), 100, 24)
	before := countCalls(fake, "SaveVersion")

	dr.key("n")
	dr.typeText("4.0")
	dr.key("tab", "tab")
	dr.typeText("next tuesday")
	dr.key("ctrl+s")

	if got := countCalls(fake, "SaveVersion"); got != before {
		t.Error("a date this program cannot read was sent to the site anyway")
	}
	mustContain(t, dr.view(), "starts has to be a date like "+exampleDay)
	if dr.list().mode != editing {
		t.Error("the editor closed over a refusal, so the typed version is gone")
	}
}

// Archiving flips one flag, and the endpoint empties every field it is not
// given — so the whole version goes back or archiving 2.0 also forgets its name,
// its description and when it was meant to ship.
func TestReleases_ArchivingKeepsEverythingElseAboutTheVersion(t *testing.T) {
	t.Parallel()

	fake := newFake(4)
	watch := watching(fake)
	dr := listOf(t, testDeps(watch), 100, 20)
	dr.moveTo(twoOh)
	before, _ := dr.list().byID(twoOh)

	dr.key("A")

	sent := watch.saved()
	if len(sent) != 1 {
		t.Fatalf("archiving sent %d saves, want one", len(sent))
	}
	in := sent[0]
	switch {
	case in.ID != twoOh:
		t.Errorf("the save named %q, want the version under the cursor", in.ID)
	case in.Archived == nil || !*in.Archived:
		t.Errorf("the save carried Archived %v, want it set", in.Archived)
	case in.Name != before.Name:
		t.Errorf("archiving renamed the version to %q", in.Name)
	case in.Description != before.Description:
		t.Errorf("archiving emptied the description; it went as %q", in.Description)
	case in.StartDate != before.StartDate:
		t.Errorf("archiving moved the start date to %q", in.StartDate.String())
	case in.ReleaseDate != before.ReleaseDate:
		t.Errorf("archiving moved the release date to %q", in.ReleaseDate.String())
	}
	after, _ := dr.list().byID(twoOh)
	if !after.Archived {
		t.Error("the row still says the version is not archived")
	}
	if got := versionState(after, dr.list().today()); got != stateArchived {
		t.Errorf("the row is in state %q after being archived", got)
	}
}

func TestReleases_ArchivingAgainUnarchives(t *testing.T) {
	t.Parallel()

	fake := newFake(4)
	watch := watching(fake)
	dr := listOf(t, testDeps(watch), 100, 20)
	dr.moveTo(twoOh)

	dr.key("A")
	dr.key("A")

	sent := watch.saved()
	if len(sent) != 2 {
		t.Fatalf("two presses sent %d saves", len(sent))
	}
	if sent[1].Archived == nil || *sent[1].Archived {
		t.Errorf("the second press sent Archived %v, want it cleared", sent[1].Archived)
	}
}

// Releasing reads what is open first and hands the number to the flow. The list
// itself never releases anything.
func TestReleases_ReleasingReadsWhatIsOpenAndPushesTheDecision(t *testing.T) {
	t.Parallel()

	fake := newFake(0, jiratest.WithIssues(openOn(twoOh, 3)))
	dr := listOf(t, testDeps(fake), 100, 20)
	dr.moveTo(twoOh)
	dr.key("enter")

	if got := countCalls(fake, "UnresolvedCount"); got != 1 {
		t.Errorf("releasing cost %d counts, want exactly one", got)
	}
	if got := countCalls(fake, "ReleaseVersion"); got != 0 {
		t.Errorf("the list released the version itself %d times; the flow is what releases", got)
	}
	push, pushed := dr.pushed()
	if !pushed {
		t.Fatal("releasing pushed nothing, so there is no confirm to answer")
	}
	if push.ID != FlowViewID {
		t.Errorf("releasing pushed %q, want the release flow", push.ID)
	}
	flow, ok := push.View.(*Flow)
	if !ok {
		t.Fatalf("the push carried a %T", push.View)
	}
	if flow.open != 3 {
		t.Errorf("the flow was handed %d open issues, want the 3 the fake holds", flow.open)
	}
	if v, _ := dr.list().byID(twoOh); v.Unresolved == nil || *v.Unresolved != 3 {
		t.Errorf("the row did not keep the count that was read: %v", v.Unresolved)
	}
}

// The versions the open issues could move to are the ones that are somewhere to
// put open work: not this one, not already released, not archived.
func TestReleases_TheFlowIsOnlyOfferedVersionsWorthMovingWorkTo(t *testing.T) {
	t.Parallel()

	fake := newFake(0, jiratest.WithIssues(openOn(twoOh, 2)))
	dr := listOf(t, testDeps(fake), 100, 20)
	dr.moveTo(twoOh)
	dr.key("enter")

	push, _ := dr.pushed()
	flow, ok := push.View.(*Flow)
	if !ok {
		t.Fatalf("the push carried a %T", push.View)
	}
	if len(flow.targets) != 1 || flow.targets[0].ID != threeOh {
		t.Errorf("the flow was offered %+v; 1.0 is released and 2.0 is the one being shipped", flow.targets)
	}
}

func TestReleases_WillNotOfferToReleaseSomethingItCannot(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		id   string
		prep func(*driver)
		want string
	}{
		"a version already released": {
			id:   oneOh,
			want: "already been released",
		},
		"a version that is archived": {
			id: twoOh,
			prep: func(dr *driver) {
				dr.moveTo(twoOh)
				dr.key("A")
			},
			want: "is archived",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fake := newFake(8)
			dr := listOf(t, testDeps(fake), 100, 20)
			if tc.prep != nil {
				tc.prep(dr)
			}
			dr.moveTo(tc.id)
			dr.key("enter")

			if got := countCalls(fake, "UnresolvedCount"); got != 0 {
				t.Errorf("it counted what is open on a version it cannot release (%d times)", got)
			}
			if _, pushed := dr.pushed(); pushed {
				t.Error("it opened a release screen for a version it cannot release")
			}
			mustContain(t, dr.lastStatus().Text, tc.want)
		})
	}
}

// The flow's answer patches the row rather than costing a refetch, because a
// refetch would throw the reader's place away for one row already in hand.
func TestReleases_TheFlowsAnswerPatchesTheRowItShipped(t *testing.T) {
	t.Parallel()

	fake := newFake(8)
	dr := listOf(t, testDeps(fake), 100, 20)
	reads := countCalls(fake, "Versions")
	dr.moveTo(threeOh)

	shipped, _ := dr.list().byID(twoOh)
	shipped.Released = true
	shipped.ReleaseDate = jira.Date{Year: 2026, Month: 3, Day: 5}
	dr.send(releasedMsg{version: shipped, asked: 0})

	if got := countCalls(fake, "Versions"); got != reads {
		t.Errorf("a released version cost %d more reads of the whole list", got-reads)
	}
	if v, _ := dr.list().byID(twoOh); !v.Released {
		t.Error("the row still says 2.0 has not been released")
	}
	if got := dr.list().selectedID(); got != twoOh {
		t.Errorf("the cursor is on %q; a release moves it onto what was released", got)
	}
}

// A refetch keeps the reader's place, by version rather than by row number.
func TestReleases_ARefetchKeepsTheReaderOnTheSameVersion(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(8)), 100, 20)
	dr.moveTo(threeOh)

	dr.send(kernel.RefreshMsg{})

	if got := dr.list().selectedID(); got != threeOh {
		t.Errorf("the cursor moved to %q over a refetch", got)
	}
}

// Switching project throws the versions away: a version belongs to one project,
// so leaving the rows up would be showing somebody another project's releases.
func TestReleases_SwitchingProjectReadsTheNewOnesVersions(t *testing.T) {
	t.Parallel()

	fake := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithProject("OPS", jiratest.Kanban),
	)
	dr := listOf(t, testDeps(fake), 100, 20)

	dr.send(kernel.ProjectMsg{Project: "OPS"})

	for _, v := range dr.list().versions {
		if !strings.HasPrefix(v.ID, "ver-OPS") {
			t.Errorf("PROJ's version %q survived the switch to OPS", v.ID)
		}
	}
	mustContain(t, dr.view(), "OPS")
}

// Only the rows that fit are built, so a project with four hundred versions
// costs a frame what one with four costs.
func TestReleases_OnlyTheRowsThatFitAreRendered(t *testing.T) {
	t.Parallel()

	dr := listOf(t, testDeps(newFake(4)), 100, 12)
	stock(dr, manyVersions(400))

	m := dr.list()
	m.rows.reset()
	_ = m.View()
	if got, ceiling := len(m.rows.rows), m.rowsHeight()+2*overscan+1; got > ceiling {
		t.Errorf("drawing a window of %d rows rendered %d of them, over the ceiling of %d",
			m.rowsHeight(), got, ceiling)
	}
}

func TestReleases_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 20}, {100, 28}, {120, 30}, {200, 60}} {
		dr := listOf(t, testDeps(newFake(8)), size.w, size.h)
		for _, state := range []struct {
			name string
			do   func()
		}{
			{"browsing", func() {}},
			{"editing", func() { dr.key("n") }},
			{"typing a long name", func() { dr.typeText(strings.Repeat("long-", 40)) }},
		} {
			state.do()
			frame := dr.view()
			lines := strings.Split(frame, "\n")
			if len(lines) != size.h {
				t.Errorf("%dx%d %s: %d lines, want %d", size.w, size.h, state.name, len(lines), size.h)
			}
			for i, line := range lines {
				if got := len([]rune(line)); got > size.w {
					t.Errorf("%dx%d %s: line %d is %d columns wide", size.w, size.h, state.name, i, got)
				}
			}
		}
	}
}

func TestReleases_EveryRowFillsTheWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 100, 132, 200} {
		dr := listOf(t, testDeps(newFake(8)), width, 20)
		m := dr.list()
		for i := range m.versions {
			row := ansi.Strip(m.row(i, i == 0))
			if got := len([]rune(row)); got != width {
				t.Errorf("at width %d row %d is %d columns wide, so a selected row's highlight stops short",
					width, i, got)
			}
		}
	}
}

// With the mouse off nothing is marked, so terminal text selection has nothing
// to pick up and no click can land.
func TestReleases_WithTheMouseOffTheFrameCarriesNoMarker(t *testing.T) {
	t.Parallel()

	off := zone.New()
	t.Cleanup(off.Close)
	off.SetEnabled(false)

	d := plainDeps(newFake(8))
	d.Zones = off
	dr := listOf(t, d, 120, 20)

	frame := dr.m.View()
	if strings.ContainsRune(frame, '\x1b') {
		t.Errorf("an escape survived a frame drawn with the mouse off:\n%q", frame)
	}
	if !strings.Contains(frame, "2.0") {
		t.Fatalf("the versions did not draw at all:\n%q", frame)
	}
}

// stock puts versions on the list as the message a read would have produced.
func stock(dr *driver, versions []jira.Version) {
	dr.t.Helper()
	dr.send(versionsMsg{gen: dr.list().gen, versions: versions})
}

func manyVersions(n int) []jira.Version {
	out := make([]jira.Version, 0, n)
	for i := range n {
		out = append(out, jira.Version{
			ID:          "ver-" + strconv.Itoa(i),
			Name:        "1." + strconv.Itoa(i),
			Description: "release number " + strconv.Itoa(i),
			Released:    i%3 == 0,
		})
	}
	return out
}

// The two views owe the kernel different interfaces, and which is which is not a
// detail: a root nothing pushes must not implement Closer, because a Close
// nothing calls is a read cancelled nowhere; a pushed screen must, because a pop
// throws it away while its write is still out.
func TestReleases_TheLifecycleInterfacesEachViewOwes(t *testing.T) {
	t.Parallel()

	list := New(testDeps(newFake(4)))
	if _, ok := list.(kernel.Closer); ok {
		t.Error("the versions list implements kernel.Closer and nothing pushes it, so nothing would call it")
	}
	for name, is := range map[string]bool{
		"KeyReporter": func() bool { _, ok := list.(kernel.KeyReporter); return ok }(),
		"KeyCapturer": func() bool { _, ok := list.(kernel.KeyCapturer); return ok }(),
		"Blocker":     func() bool { _, ok := list.(kernel.Blocker); return ok }(),
		"Addressed":   func() bool { _, ok := list.(kernel.Addressed); return ok }(),
	} {
		if !is {
			t.Errorf("the versions list does not implement kernel.%s", name)
		}
	}

	flow := NewFlow(testDeps(newFake(4)), twoOhVersion(), 3, nil)
	if _, ok := flow.(kernel.Closer); !ok {
		t.Error("the release screen is pushed and does not implement kernel.Closer, so a pop leaves " +
			"its write running for an answer nothing can draw")
	}
	if _, ok := flow.(kernel.KeyCapturer); ok {
		t.Error("the release screen claims raw keys and nothing on it is typed into")
	}
	first, ok := flow.(kernel.Addressed)
	if !ok {
		t.Fatal("the release screen does not implement kernel.Addressed")
	}
	second, _ := NewFlow(testDeps(newFake(4)), twoOhVersion(), 3, nil).(kernel.Addressed)
	switch {
	case first.Addr() == 0:
		t.Error("the release screen answers to the zero address, which the kernel resolves to nothing")
	case second != nil && first.Addr() == second.Addr():
		t.Error("two release screens share an address, so an answer to one is drawn into the other")
	}
	if listed, _ := list.(kernel.Addressed); listed != nil && listed.Addr() == first.Addr() {
		t.Error("the list and the screen it pushes share an address")
	}
}

// What the registries hold after this package's init, which is what the wiring
// step and the palette read. A bad registration is recorded rather than raised,
// so it is only ever found by asking.
func TestReleases_RegistersItselfCleanly(t *testing.T) {
	t.Parallel()

	if errs := kernel.RegistrationErrors(); len(errs) > 0 {
		t.Fatalf("this package registered %d bad thing(s): %v", len(errs), errs)
	}
	spec, ok := kernel.LookupView(ViewID)
	if !ok {
		t.Fatalf("no view is registered as %q", ViewID)
	}
	switch {
	case spec.Slot != 5:
		t.Errorf("the versions list took slot %d; docs/UX.md allocates 5 to releases", spec.Slot)
	case spec.Requires != "":
		t.Errorf("the view requires the capability %q, and there is none for versions", spec.Requires)
	case spec.RunsQueries:
		t.Error("the view claims to run saved queries, which are JQL and not versions")
	}
	if _, ok := kernel.LookupView(FlowViewID); ok {
		t.Errorf("%q is registered as a view the registry can build, and it cannot be built "+
			"without a version and a count", FlowViewID)
	}
	if kernel.KeysFor(FlowViewID).IsZero() {
		t.Errorf("%q registered no keys, so its footer is a row of globals", FlowViewID)
	}

	want := map[string]string{
		"releases.open":    kernel.SlotGesture(spec.Slot),
		"releases.new":     "n",
		"releases.edit":    "e",
		"releases.archive": "A",
		"releases.release": "enter",
	}
	shown := map[string]bool{}
	for _, b := range kernel.KeysFor(ViewID).Acts {
		shown[b.Help().Key] = true
	}
	for id, key := range want {
		cmd, found := kernel.LookupCommand(id)
		if !found {
			t.Errorf("no command %q is registered", id)
			continue
		}
		if len(cmd.Keys) != 1 || cmd.Keys[0] != key {
			t.Errorf("command %q teaches %v, want %q", id, cmd.Keys, key)
			continue
		}
		if key != kernel.SlotGesture(spec.Slot) && !shown[key] {
			t.Errorf("command %q teaches %q, which the resting footer does not show", id, key)
		}
	}
	for _, cmd := range kernel.Commands() {
		if _, named := want[cmd.ID]; !named && strings.HasPrefix(cmd.ID, "releases.") {
			t.Errorf("command %q is registered and this test does not name it, so the wiring step "+
				"has one more keyOwners row to add than it was told about", cmd.ID)
		}
	}
}
