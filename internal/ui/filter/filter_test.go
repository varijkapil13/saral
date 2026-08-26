package filter

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestPicker_OpensOnTheFacetsAndAsksTheSiteNothing(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := newDriver(t, testDeps(f), 120, 30)

	if dr.m.state != pickFacet {
		t.Errorf("the picker opened on state %d, want the facets", dr.m.state)
	}
	if got := len(f.Calls()); got != 0 {
		t.Errorf("opening the picker made %d calls, want none: %v", got, f.Calls())
	}
	mustContain(t, dr.view(), "assignee", "reporter", "status", "type", "priority", "label")
}

// The facets are read rather than searched, so nothing is typed there and the
// kernel keeps its own keys. Typing only starts once a facet is open.
func TestPicker_ClaimsTheKeyboardOnlyWhileTypingForAValue(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30)
	if dr.m.WantsRawKeys() {
		t.Error("the facets claim the keyboard, so esc could not close the picker")
	}

	dr.pick(FacetStatus)
	if !dr.m.WantsRawKeys() {
		t.Error("typing for a value does not claim the keyboard, so a q would quit the program")
	}

	dr.key("esc")
	if dr.m.WantsRawKeys() {
		t.Error("the picker still claims the keyboard after going back to the facets")
	}
	if dr.m.state != pickFacet {
		t.Error("esc did not go back to the facets")
	}
	if dr.pops != 0 {
		t.Errorf("esc from a value closed the picker %d times; it should only go back one state", dr.pops)
	}
}

func TestPicker_OffersTheAccountsAssignableInThisProject(t *testing.T) {
	t.Parallel()

	w := watching(newFake(20))
	dr := newDriver(t, testDeps(w), 120, 30)
	dr.pick(FacetAssignee)

	asked := w.asked()
	if len(asked) != 1 {
		t.Fatalf("opening the assignee facet made %d searches, want one: %+v", len(asked), asked)
	}
	if asked[0].Project != "PROJ" {
		t.Errorf("the assignee search was scoped to %q, want PROJ so the site drops its app accounts",
			asked[0].Project)
	}
	if asked[0].Match != "" {
		t.Errorf("the first search asked for %q, want the whole directory", asked[0].Match)
	}
	if got := dr.labels(); len(got) < 2 || got[0] != "unassigned" {
		t.Errorf("the assignee facet offers %v, want nobody first", got)
	}
}

// A reporter need not be assignable: an account that filed an issue and then
// lost the permission is still on those rows. So that search is the site-wide
// one, and what it drags in is badged and sunk rather than hidden.
func TestPicker_TheReporterSearchIsSiteWideAndBadgesWhatIsNotAPerson(t *testing.T) {
	t.Parallel()

	w := watching(newFake(20))
	dr := newDriver(t, testDeps(w), 120, 30)
	dr.pick(FacetReporter)

	asked := w.asked()
	if len(asked) != 1 || asked[0].Project != "" {
		t.Fatalf("the reporter search was %+v, want one search with no project on it", asked)
	}

	got := dr.labels()
	robot := slices.Index(got, "Nightly Runner")
	if robot < 0 {
		t.Fatalf("the app account is not offered at all: %v", got)
	}
	for _, person := range []string{"Ada Lovelace", "Grace Hopper", "Sam Tester"} {
		if at := slices.Index(got, person); at < 0 || at > robot {
			t.Errorf("%s is at %d and the app account at %d; the robots belong last", person, at, robot)
		}
	}
	mustContain(t, dr.view(), "app")
}

// An inactive account is still offered — it is on the rows it was assigned —
// and says so, because picking one and getting nothing back is worse.
func TestPicker_BadgesAnAccountThatIsNoLongerActive(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30)
	dr.pick(FacetReporter)

	at := slices.IndexFunc(dr.m.all, func(v value) bool { return v.term.Label == "Alan Turing" })
	if at < 0 {
		t.Fatal("the inactive account is not offered")
	}
	if got := dr.m.all[at].note; !strings.Contains(got, "inactive") {
		t.Errorf("the inactive account's row says %q, want it to say so", got)
	}
}

// Jira's matching is undocumented and neither substring nor fuzzy, and a
// request per keystroke is slower than typing the JQL this replaces. So the
// site is asked once, and a keystroke ranks what came back — for as long as
// what came back is the whole directory.
func TestPicker_TypingCostsNoRoundTripWhileTheWholeDirectoryIsHeld(t *testing.T) {
	t.Parallel()

	w := watching(newFake(20))
	dr := newDriver(t, testDeps(w), 120, 30)
	dr.pick(FacetAssignee)
	if !dr.m.complete {
		t.Fatal("the fake answered fewer accounts than it was allowed and the picker did not notice")
	}

	dr.typeText("grace")

	if got := len(w.asked()); got != 1 {
		t.Errorf("typing five letters made %d searches, want the one that opened the facet", got)
	}
	if got := dr.labels(); len(got) == 0 || got[0] != "Grace Hopper" {
		t.Errorf("typing \"grace\" offers %v, want Grace Hopper first", got)
	}
}

// A site with more accounts than one search may return is the case the port
// documents: a longer needle can match what a shorter one did not, so the site
// is asked again — once per needle, and only once what is held has run thin.
func TestPicker_AsksTheSiteAgainOnlyWhenWhatIsHeldRunsThin(t *testing.T) {
	t.Parallel()

	w := watching(newFake(20, jiratest.WithPeople(crowd(peopleLimit+10))))
	dr := newDriver(t, testDeps(w), 120, 30)
	dr.pick(FacetAssignee)
	if dr.m.complete {
		t.Fatal("a directory bigger than the limit was taken for the whole of it")
	}
	opened := len(w.asked())

	// One letter still leaves a screenful of candidates, so it is answered from
	// what is held.
	dr.typeText("p")
	if got := len(w.asked()); got != opened {
		t.Errorf("a needle that still matches plenty made %d searches, want %d", got, opened)
	}

	// A needle nothing held answers is what the site is for.
	dr.typeText("erson 57")
	asked := w.asked()
	if len(asked) <= opened {
		t.Fatalf("a needle with no local answer made no search: %+v", asked)
	}
	last := asked[len(asked)-1]
	if last.Match != "person 57" {
		t.Errorf("the search asked for %q, want the whole needle", last.Match)
	}

	// The same needle again is the same question, and is not asked twice.
	before := len(w.asked())
	dr.send(keyPress("backspace"))
	dr.typeText("7")
	if got := len(w.asked()); got != before {
		t.Errorf("retyping the same needle made %d searches, want %d", got, before)
	}
}

// A needle the site never answered is not one it has been asked. Without that,
// one transport failure makes those keystrokes unanswerable for as long as the
// facet is open.
func TestPicker_AsksAgainForANeedleTheSiteRefused(t *testing.T) {
	t.Parallel()

	f := newFake(20, jiratest.WithPeople(crowd(peopleLimit+10)))
	w := watching(f)
	dr := newDriver(t, testDeps(w), 120, 30)
	dr.pick(FacetAssignee)

	f.FailNext(&jira.TransportError{Op: "searching the accounts", Err: errors.New("connection refused")})
	dr.typeText("z")
	if dr.m.failure == nil {
		t.Fatal("the search did not fail at all, so there is nothing to ask again")
	}
	asked := len(w.asked())

	dr.send(keyPress("backspace"))
	dr.typeText("z")

	if got := len(w.asked()); got <= asked {
		t.Errorf("retyping a needle the site refused made %d searches, want more than %d", got, asked)
	}
}

// crowd is a directory too big for one search to hand back whole.
func crowd(n int) []jira.User {
	out := make([]jira.User, 0, n)
	for i := range n {
		out = append(out, jira.User{
			AccountID:   "acct-" + string(rune('a'+i%26)) + itoa(i),
			DisplayName: "Person " + itoa(i),
			Active:      true,
			TimeZone:    time.UTC,
			Kind:        jira.AccountPerson,
		})
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// An app account is assigned work like a person, so it can be what a search is
// filtered by — and the assignable search drops it, so reopening the picker
// would offer no way to take that filter off. The ids in force are drawn back
// by id for exactly that.
func TestPicker_DrawsBackAnAccountInForceThatTheSearchDoesNotAnswerWith(t *testing.T) {
	t.Parallel()

	robot := Term{Facet: FacetAssignee, ID: "acct:nightly-bot", Label: "Nightly Runner"}
	f := newFake(20)
	dr := newDriver(t, testDeps(f), 120, 30, WithTerms(Terms{robot}))
	dr.pick(FacetAssignee)

	if got := countCalls(f, "People"); got != 1 {
		t.Fatalf("the ids in force were resolved %d times, want once", got)
	}
	at := -1
	for i, v := range dr.m.all {
		if v.term.ID == robot.ID {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("the account in force is not on offer: %v", dr.labels())
	}
	if !dr.m.terms.Has(dr.m.all[at].term) {
		t.Error("the account in force is on offer but not marked as in force")
	}
	mustContain(t, dr.view(), "Nightly Runner")
}

// An id the search already answered with costs no second read: the whole point
// of the bulk read here is the ids it did not.
func TestPicker_DoesNotResolveAnAccountTheSearchAlreadyAnsweredWith(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := newDriver(t, testDeps(f), 120, 30, WithTerms(Terms{
		{Facet: FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"},
	}))
	dr.pick(FacetAssignee)

	if got := countCalls(f, "People"); got != 0 {
		t.Errorf("an account the search returned was read again %d times", got)
	}
}

// The empty id is the field being empty rather than an account, so it is never
// sent to a read that resolves account ids.
func TestPicker_DoesNotResolveTheUnassignedTerm(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	dr := newDriver(t, testDeps(f), 120, 30, WithTerms(Terms{{Facet: FacetAssignee, Label: "unassigned"}}))
	dr.pick(FacetAssignee)

	if got := countCalls(f, "People"); got != 0 {
		t.Errorf("the empty assignee was looked up as an account %d times", got)
	}
}

func TestPicker_StatusesAreTheUnionOfEveryWorkflowAndSayWhichTypesTheyCameFrom(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30)
	dr.pick(FacetStatus)

	byID := make(map[string]value, len(dr.m.all))
	for _, v := range dr.m.all {
		if _, dup := byID[v.term.ID]; dup {
			t.Errorf("status %q is offered twice", v.term.ID)
		}
		byID[v.term.ID] = v
	}
	// The fake mints a project-scoped status reusing another's display name,
	// which is what a team-managed project does. Both have to be on offer, and
	// the row has to say which workflow each belongs to.
	first, second := byID["10202"], byID["10204"]
	if first.term.Label != second.term.Label {
		t.Fatalf("the two ids that share a name are %q and %q", first.term.Label, second.term.Label)
	}
	if first.note == second.note || first.note == "" || second.note == "" {
		t.Errorf("both rows read %q / %q, so nothing on screen tells them apart", first.note, second.note)
	}
}

func TestPicker_PrioritiesKeepTheSitesOwnOrder(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30)
	dr.pick(FacetPriority)

	if got, want := dr.labels(), []string{"Urgent", "Normal", "Whenever"}; !slices.Equal(got, want) {
		t.Errorf("the priorities are offered as %v, want the site's own ranking order %v", got, want)
	}
}

// The endpoint takes no query and ignores one sent anyway, so the labels are
// walked and then narrowed here.
func TestPicker_LabelsAreWalkedAndNarrowedLocally(t *testing.T) {
	t.Parallel()

	f := newFake(40)
	dr := newDriver(t, testDeps(f), 120, 30)
	dr.pick(FacetLabel)

	if len(dr.m.all) == 0 {
		t.Fatal("no labels were offered at all")
	}
	walked := countCalls(f, "Labels")
	dr.typeText("fro")

	if got := countCalls(f, "Labels"); got != walked {
		t.Errorf("typing over the labels asked the site again (%d calls, was %d)", got, walked)
	}
	if got := dr.labels(); len(got) == 0 || got[0] != "frontend" {
		t.Errorf("typing \"fro\" offers %v, want frontend first", got)
	}
}

// A label is whatever anybody typed, and two of the fake's are not ASCII. The
// column is measured in grapheme clusters, so one of them must not shift every
// column to its right.
func TestPicker_ALabelThatIsNotASCIIKeepsTheColumnsWhereTheyAre(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(40)), 120, 30)
	dr.pick(FacetLabel)
	dr.typeText("検")

	if got := dr.labels(); len(got) == 0 || got[0] != "検索" {
		t.Fatalf("typing a CJK rune offers %v, want the label that starts with it", got)
	}
	lines := strings.Split(dr.view(), "\n")[headHeight:]
	for i, at := range dr.m.shown {
		if got := ansi.StringWidth(lines[i]); got != 120 {
			t.Errorf("the row for %q is %d columns wide, want 120: %q",
				dr.m.all[at].term.Label, got, lines[i])
		}
	}
}

func TestPicker_ChoosingAValueClosesAndNamesTheTerm(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30)
	dr.pick(FacetPriority)
	dr.key("enter")

	term, chose := dr.chosen()
	if !chose {
		t.Fatal("choosing a value named no term")
	}
	if term.Facet != FacetPriority || term.ID != "10401" || term.Label != "Urgent" {
		t.Errorf("the term is %+v, want the priority 10401", term)
	}
	if dr.pops != 1 {
		t.Errorf("choosing a value closed the picker %d times, want once", dr.pops)
	}
}

// The picker never applies a term itself: it is pushed over the view being
// filtered and holds no pointer to it.
func TestPicker_ChoosingAValueAlreadyInForceNamesItAgainSoItComesOff(t *testing.T) {
	t.Parallel()

	urgent := Term{Facet: FacetPriority, ID: "10401", Label: "Urgent"}
	dr := newDriver(t, testDeps(newFake(20)), 120, 30, WithTerms(Terms{urgent}))
	dr.pick(FacetPriority)

	mustContain(t, dr.view(), "Urgent")
	if !dr.m.terms.Has(urgent) {
		t.Fatal("the picker was opened over a term it does not hold")
	}
	dr.key("enter")

	term, chose := dr.chosen()
	if !chose || term.ID != "10401" {
		t.Errorf("choosing a value in force named %+v, want the same term back so the list toggles it off", term)
	}
}

// The facet list says how many of each facet's values are in force, which is
// the only account of a compound filter the picker itself can give.
func TestPicker_TheFacetsSayWhatIsAlreadyInForce(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30, WithTerms(Terms{
		{Facet: FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"},
		{Facet: FacetAssignee, ID: "acct-grace", Label: "Grace Hopper"},
	}))

	mustContain(t, dr.view(), "2 in force", "assignee Ada Lovelace or Grace Hopper")
}

// CapPeople is site-wide and an ordinary token can lack it. The facet stays on
// the list and says why in the site's own words, because a facet that
// disappears is one nobody can find out about.
func TestPicker_SaysWhyItCannotOfferPeopleWhenTheTokenMayNotLookThemUp(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20, jiratest.WithCapabilities(jiratest.NoPeople)))
	d.Caps.People = jira.Capability{Reason: "needs the Browse users and groups permission"}
	dr := newDriver(t, d, 120, 30, WithEditKey("e"))

	mustContain(t, dr.view(), "needs the Browse users and groups permission")

	dr.pick(FacetAssignee)

	if dr.m.state != pickFacet {
		t.Error("a refused facet opened anyway")
	}
	got := dr.lastStatus().Text
	mustContain(t, got, "needs the Browse users and groups permission", "e edits the search by hand")
}

// Statuses and types are per project and there is no site-wide answer to give.
func TestPicker_SaysWhyItCannotOfferStatusesWithNoProject(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	d.Project = ""
	dr := newDriver(t, d, 120, 30)

	dr.pick(FacetStatus)

	if dr.m.state != pickFacet {
		t.Error("a facet with nowhere to read its values from opened anyway")
	}
	mustContain(t, dr.lastStatus().Text, "per project")
}

// A session with no site at all still draws, and says why every facet is
// refused rather than offering six rows that do nothing.
func TestPicker_SaysSoWithNoConnectionAtAll(t *testing.T) {
	t.Parallel()

	d := testDeps(nil)
	d.Jira = nil
	dr := newDriver(t, d, 120, 30)

	mustContain(t, dr.view(), "no Jira connection")
}

func TestPicker_KeepsTheRefusalInThePaneWhenTheSiteSaysNo(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"a permission it does not have": {
			err:  &jira.CapabilityError{Capability: jira.CapPeople, Reason: "needs Browse users and groups"},
			want: "needs Browse users and groups",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second},
			want: "30s",
		},
		"the transport": {
			err:  &jira.TransportError{Op: "reading the priorities", Err: errors.New("connection refused")},
			want: "connection refused",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFake(20)
			f.FailNext(tc.err)
			dr := newDriver(t, testDeps(f), 120, 30)
			dr.pick(FacetPriority)

			mustContain(t, dr.view(), "The site would not say.", tc.want)
			if got := dr.lastStatus().Level; got != kernel.LevelError {
				t.Errorf("the status line reported level %d, want an error", got)
			}
			// The status line goes on the next keypress; the pane has to keep it.
			dr.key("j")
			mustContain(t, dr.view(), tc.want)
		})
	}
}

// A refusal is not the end of the picker: the way back is still there, and it
// is what the footer names, because there is nothing to choose.
func TestPicker_ARefusedFacetStillOffersTheWayBack(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	f.FailNext(&jira.RateLimitError{RetryAfter: time.Minute})
	dr := newDriver(t, testDeps(f), 120, 30)
	dr.pick(FacetPriority)

	set, _ := dr.m.LiveKeys()
	if len(set.Acts) != 1 || set.Acts[0].Help().Key != "esc" {
		t.Errorf("a refused facet advertises %v, want the way back and nothing else", set.Acts)
	}
	dr.key("esc")
	if dr.m.state != pickFacet {
		t.Error("esc did not go back to the facets after a refusal")
	}
}

// An answer to a question the user has already changed is dropped rather than
// drawn: the picker moved to another facet before the first one landed.
func TestPicker_DropsAnAnswerToAFacetThatIsNoLongerOpen(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30)
	dr.pick(FacetPriority)
	stale := dr.m.gen

	dr.key("esc")
	dr.pick(FacetLabel)
	labels := len(dr.m.all)

	dr.send(vocabularyMsg{gen: stale, facet: FacetPriority, values: priorityValues([]jira.Priority{
		{ID: "10401", Name: "Urgent"},
	})})

	if got := len(dr.m.all); got != labels {
		t.Errorf("a late answer about priorities replaced the labels: %d rows, was %d", got, labels)
	}
}

// The picker is virtualized: only the rows on screen are built, whatever the
// site holds.
func TestPicker_DrawsOnlyTheRowsThatFit(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20, jiratest.WithPeople(crowd(peopleLimit)))), 120, 12)
	dr.pick(FacetReporter)

	lines := strings.Split(dr.m.View(), "\n")
	if got, want := len(lines), 12; got != want {
		t.Fatalf("the frame is %d lines, want the %d it was given", got, want)
	}
	if len(dr.m.shown) <= dr.m.rowsHeight() {
		t.Fatalf("only %d rows are on offer, so nothing was virtualized", len(dr.m.shown))
	}
}

func TestPicker_TheEmptyStatesSayWhichKindOfEmptyTheyAre(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 30, WithEditKey("e"))
	dr.pick(FacetPriority)
	dr.typeText("nothing like this")

	mustContain(t, dr.view(), "No priority here matches", "e on the list edits the search by hand.")
	mustNotContain(t, dr.view(), "The site would not say.")
}

// An answer that lands while the reader is looking at the list must not move
// the highlight. shown holds indices into all, so an answer that appends to all
// and sorts it leaves those row numbers pointing at other values — and enter
// filters by whatever the cursor is on, so a highlight that slid one row runs a
// search nobody asked for and says nothing about it.
func TestPicker_AnAnswerThatLandsLateLeavesTheCursorOnTheSameValue(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		facet Facet
		// first is what the late answer puts at the top of the list, so that the
		// test fails if the answer was dropped instead of drawn.
		first string
		late  func(m *Model) tea.Msg
	}{
		"an account search": {
			facet: FacetReporter,
			first: "Aaa Aardvark",
			late: func(m *Model) tea.Msg {
				return peopleMsg{gen: m.gen, facet: FacetReporter, needle: "aa", people: []jira.User{{
					AccountID: "acct-aardvark", DisplayName: "Aaa Aardvark",
					Active: true, TimeZone: time.UTC, Kind: jira.AccountPerson,
				}}}
			},
		},
		"a vocabulary read": {
			facet: FacetLabel,
			first: "aardvark",
			late: func(m *Model) tea.Msg {
				labels := []string{"aardvark"}
				for i := range m.all {
					labels = append(labels, m.all[i].term.ID)
				}
				return vocabularyMsg{gen: m.gen, facet: FacetLabel, values: labelValues(labels)}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dr := newDriver(t, testDeps(newFake(40)), 120, 30)
			dr.pick(tc.facet)
			dr.key("down")

			sel := dr.m.selected()
			if sel == nil {
				t.Fatalf("the %s facet offered no row to stand on", tc.facet.Label())
			}
			was := sel.term

			dr.send(tc.late(dr.m))

			if got := dr.labels(); len(got) == 0 || got[0] != tc.first {
				t.Fatalf("the answer never reached the list: it offers %v", got)
			}
			now := dr.m.selected()
			if now == nil || now.term != was {
				t.Errorf("the highlight moved from %q to %q; enter would filter by the wrong value",
					was.Label, now.term.Label)
			}
		})
	}
}

// The picker is pushed over the list and the palette is pushed over the picker,
// so a vocabulary read given up on a blur is one nothing asks for again. A
// picker the kernel has thrown away lets it go.
func TestPicker_KeepsItsReadOnABlurAndDropsItOnAClose(t *testing.T) {
	t.Parallel()

	f := newFake(20)

	kept := openOnAFacet(t, testDeps(f))
	reading := kept.cmd
	if _, more := kept.view.Update(kernel.FocusMsg{}); more != nil {
		t.Fatal("losing the keyboard asked for more work")
	}
	if _, gaveUp := answer(reading).(failedMsg); gaveUp {
		t.Error("the picker gave up its read when it merely lost the keyboard")
	}

	dropped := openOnAFacet(t, testDeps(f))
	closer, ok := dropped.view.(kernel.Closer)
	if !ok {
		t.Fatal("the picker does not implement kernel.Closer, so nothing stops its read")
	}
	closer.Close()

	failed, ok := answer(dropped.cmd).(failedMsg)
	if !ok {
		t.Fatalf("the read came back as %T, want the failure a cancelled context produces", answer(dropped.cmd))
	}
	if !errors.Is(failed.err, context.Canceled) {
		t.Errorf("err = %v, want the context's own error", failed.err)
	}
}

// openOnAFacet drives the picker to the state where it has asked the site for a
// vocabulary, and hands back the command carrying that read rather than running
// it, so a test can decide what happens to it first.
type facetOpen struct {
	view kernel.View
	cmd  tea.Cmd
}

func openOnAFacet(t *testing.T, d kernel.Deps) facetOpen {
	t.Helper()

	view := New(d)
	view, _ = view.Update(kernel.SizeMsg{Width: 120, Height: 30})
	view, _ = view.Update(kernel.FocusMsg{Focused: true})
	view, cmd := view.Update(keyPress("enter"))
	if cmd == nil {
		t.Fatal("choosing a facet asked the site for nothing")
	}
	return facetOpen{view: view, cmd: cmd}
}

// answer is what the kernel hands a view: the command's own reply with the
// envelope the kernel addresses it by taken off.
func answer(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if reply, addressed := msg.(kernel.ReplyMsg); addressed {
		return reply.Msg
	}
	return msg
}
