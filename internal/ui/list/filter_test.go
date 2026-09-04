package list

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var (
	adaTerm = filter.Term{Facet: filter.FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"}
	grace   = filter.Term{Facet: filter.FacetAssignee, ID: "acct-grace", Label: "Grace Hopper"}
	triage  = filter.Term{Facet: filter.FacetStatus, ID: "10201", Label: "Triage"}
	shipped = filter.Term{Facet: filter.FacetStatus, ID: "10203", Label: "Shipped"}
)

// said reports whether the view put a sentence on the status line at any point.
// The last one is not always the answer: a project switch that lands on an
// empty default widens and says so afterwards.
func said(dr *driver, want string) bool {
	for _, msg := range dr.statuses {
		if strings.Contains(msg.Text, want) {
			return true
		}
	}
	return false
}

func TestList_FOpensThePickerOverWhatIsInForce(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})
	dr.key("f")

	if len(dr.pushes) != 1 {
		t.Fatalf("f pushed %d panes, want the picker", len(dr.pushes))
	}
	if got := dr.pushes[0].ID; got != filter.ViewID {
		t.Errorf("f pushed %q, want the picker", got)
	}
	picker, ok := dr.pushes[0].View.(interface{ LiveKeys() (kernel.KeySet, int) })
	if !ok {
		t.Fatal("the pushed view does not report its live keys")
	}
	if set, _ := picker.LiveKeys(); len(set.Acts) == 0 {
		t.Error("the picker was pushed advertising nothing")
	}
}

// The palette reaches the same gesture the key does, rather than a second
// implementation of it.
func TestList_ThePaletteOpensThePickerToo(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	dr.send(OpenFilterMsg{})

	if len(dr.pushes) != 1 || dr.pushes[0].ID != filter.ViewID {
		t.Fatalf("the palette pushed %+v, want the picker", dr.pushes)
	}
}

// Driven through the kernel rather than the model, because the palette entry is
// two messages and the second one only lands if the first put the list on top:
// f is not free in the issue pane, so this is the whole of how the picker is
// reached from anywhere else.
func TestList_ThePaletteCommandReachesThePickerThroughTheKernel(t *testing.T) {
	t.Parallel()

	m := startAll(t, testDeps(newFake(20)), 120, 30)
	m = send(t, m, kernel.RunCommandMsg{ID: "issues.filter-by"})

	got := frame(m)
	mustContain(t, got, "what to filter by", "assignee", "reporter", "priority")
	if !strings.Contains(got, "Filter") {
		t.Errorf("the header does not name the picker:\n%s", got)
	}
}

func TestList_AChosenValueBecomesTheSearchRatherThanAPassOverTheRows(t *testing.T) {
	t.Parallel()

	f := newFake(30)
	dr := openAll(t, testDeps(f), 120, 30)
	searches := countCalls(f, "Search")

	dr.send(filter.ChosenMsg{Term: shipped})

	if got, want := dr.m.jql, `project = "PROJ" AND status = "10203" ORDER BY updated DESC`; got != want {
		t.Errorf("the search is %q, want %q", got, want)
	}
	if countCalls(f, "Search") <= searches {
		t.Error("choosing a value asked the site nothing, so it cannot reach an issue that was not loaded")
	}
	if got := dr.m.title; got != "status Shipped in PROJ" {
		t.Errorf("the search is named %q", got)
	}
}

// The whole gesture, driven through the kernel the way a user drives it: f, a
// facet, a value, and the rows come back narrowed with the term named under
// them — while the picker is still open, since a toggle no longer closes it.
// Everything in between is a real keypress.
func TestList_TheWholeGestureFromFToANarrowedList(t *testing.T) {
	t.Parallel()

	m := startAll(t, testDeps(newFake(20)), 120, 30)
	m = send(t, m, keyPress("f"))
	mustContain(t, frame(m), "what to filter by")

	// Down to the statuses, then into them, then take the first.
	m = send(t, m, keyPress("j"))
	m = send(t, m, keyPress("j"))
	m = send(t, m, keyPress("enter"))
	mustContain(t, frame(m), "which status?", "Triage")

	m = send(t, m, keyPress("enter"))
	mustContain(t, frame(m), "which status?", "Triage")

	// esc twice: back to the facets, then closed — the picker's own gesture,
	// unchanged by multi-select.
	m = send(t, m, keyPress("esc"))
	m = send(t, m, keyPress("esc"))

	got := frame(m)
	mustContain(t, got, `status: Triage`, "status Triage in PROJ", "PROJ-")
	mustNotContain(t, got, "which status?", "The search failed.")
}

// The rows narrow on the first toggle, not on the close: the picker's own
// multi-select decision is that the view behind it follows live rather than
// being committed to at the end. Driven through the real kernel stack, with
// the picker still the pane on screen, so this is the search the list underneath
// it actually ran and not one a test injected directly.
func TestList_RowsNarrowAfterTheFirstToggleWithoutWaitingForTheClose(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	m := startAll(t, testDeps(f), 120, 30)
	searches := countCalls(f, "Search")
	m = send(t, m, keyPress("f"))
	m = send(t, m, keyPress("j"))
	m = send(t, m, keyPress("j"))
	m = send(t, m, keyPress("enter"))

	m = send(t, m, keyPress("enter"))

	got := frame(m)
	mustContain(t, got, "which status?", "Triage")
	if countCalls(f, "Search") <= searches {
		t.Fatal("the list has not asked the site again, so its rows have not narrowed")
	}
}

func TestList_TermsCompose(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		terms []filter.Term
		want  string
	}{
		"two values of one facet are either of them": {
			terms: []filter.Term{adaTerm, grace},
			want:  `project = "PROJ" AND assignee IN ("acct-ada", "acct-grace") ORDER BY updated DESC`,
		},
		"two facets are both": {
			terms: []filter.Term{adaTerm, shipped},
			want:  `project = "PROJ" AND assignee = "acct-ada" AND status = "10203" ORDER BY updated DESC`,
		},
		"the same value twice comes off again": {
			terms: []filter.Term{shipped, triage, shipped},
			want:  `project = "PROJ" AND status = "10201" ORDER BY updated DESC`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dr := openAll(t, testDeps(newFake(30)), 120, 30)
			for _, term := range tc.terms {
				dr.send(filter.ChosenMsg{Term: term})
			}
			if got := dr.m.jql; got != tc.want {
				t.Errorf("the search is %q, want %q", got, tc.want)
			}
		})
	}
}

// a is the no-terms state rather than a second way to clear a filter, so
// dropping the last term lands exactly on the search it runs.
func TestList_DroppingEveryTermLandsOnTheSearchAIsBoundTo(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})
	dr.send(filter.ChosenMsg{Term: adaTerm})

	dr.key("a")

	if len(dr.m.terms) != 0 {
		t.Errorf("a left %+v in force", dr.m.terms)
	}
	if got := dr.m.jql; got != allUpdated {
		t.Errorf("a ran %q, want %q", got, allUpdated)
	}
	mustNotContain(t, dr.view(), "status: Shipped")
}

// A filter you cannot see is one you cannot escape, so every term is on screen
// with the way off it.
func TestList_TheTermsInForceAreNamedUnderTheRows(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})
	dr.send(filter.ChosenMsg{Term: adaTerm})

	mustContain(t, dr.view(), `assignee: Ada Lovelace`, `status: Shipped`, "ctrl+g clears everything")
}

// The clear-everything hint names the key rather than the pointer, so it reads
// the same whether or not there is one to click with.
func TestList_TheTermsLineNamesTheClearKeyRegardlessOfThePointer(t *testing.T) {
	t.Parallel()

	d := plainDeps(newFake(20))
	d.Zones = nil
	dr := openAll(t, d, 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})

	mustContain(t, dr.view(), "ctrl+g clears everything")
}

// A status and an issue type are minted per project, so the ids in force name
// values the new project has never heard of.
func TestList_AProjectSwitchTakesTheTermsWithIt(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})

	dr.send(kernel.ProjectMsg{Project: "OTHER"})

	if len(dr.m.terms) != 0 {
		t.Errorf("%+v survived a project switch", dr.m.terms)
	}
	if strings.Contains(dr.m.jql, "PROJ\"") {
		t.Errorf("the search still names the project that was left: %q", dr.m.jql)
	}
	if !said(dr, "the filters were about PROJ") {
		t.Errorf("nothing said the filters came off with the project: %v", dr.statuses)
	}
}

// A search the user runs some other way is not the term set any more, so what
// is on screen must stop claiming it is.
func TestList_AnEditedSearchDropsTheTerms(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})

	dr.send(QueryMsg{JQL: allJQL, Title: "All issues"})

	if len(dr.m.terms) != 0 {
		t.Errorf("%+v survived a search run from somewhere else", dr.m.terms)
	}
	mustNotContain(t, dr.view(), `status: Shipped`)
}

// The rows and the terms are two different things being left out, and both are
// named under the list.
func TestList_ATermAndAKeptFilterAreBothOnScreen(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(30)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})
	dr.key("/")
	dr.typeText("PROJ-2")
	dr.key("enter")

	mustContain(t, dr.view(), `status: Shipped`, `only rows matching "PROJ-2"`)
}

func TestList_TermsGolden(t *testing.T) {
	t.Parallel()

	alan := filter.Term{Facet: filter.FacetAssignee, ID: "acct-alan", Label: "Alan Turing"}
	dr := openAll(t, testDeps(newFake(12)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})
	dr.send(filter.ChosenMsg{Term: alan})

	golden(t, "list_two_terms_120x30.golden", dr.view())
}

// A person chosen in the picker is a query run at the site, driven the way a
// user drives it: f, down to the reporters, into them, and take the first.
// Nothing narrows the rows already loaded, so the count under the search is the
// site's answer and not a pass over what was on screen.
func TestList_AReporterChosenInThePickerComesBackAsThatPersonsIssues(t *testing.T) {
	t.Parallel()

	const issues = 30
	want := matchingKeys(jiratest.Gen(issues), func(iss *jira.Issue) bool {
		return iss.Reporter != nil && iss.Reporter.DisplayName == "Ada Lovelace"
	})
	if len(want) == 0 || len(want) == issues {
		t.Fatalf("Ada reported %d of %d issues, which would prove nothing", len(want), issues)
	}

	m := startAll(t, testDeps(newFake(issues)), 120, 40)
	m = send(t, m, keyPress("f"))
	m = send(t, m, keyPress("j"))
	m = send(t, m, keyPress("enter"))
	mustContain(t, frame(m), "which reporter?", "Ada Lovelace")

	m = send(t, m, keyPress("enter"))
	mustContain(t, frame(m), "which reporter?", "Ada Lovelace")

	m = send(t, m, keyPress("esc"))
	m = send(t, m, keyPress("esc"))

	got := frame(m)
	mustContain(t, got, `reporter: Ada Lovelace`, strconv.Itoa(len(want))+" issues")
	mustNotContain(t, got, "which reporter?", "The search failed.")
}

// What a term is worth is the rows it brings back, not the string it composes.
// Every facet the picker offers is chosen here and the answer checked issue by
// issue against the fixture — including the IN list two values of one facet
// compose to, and the OR that lets nobody sit beside a named person.
func TestList_EveryFacetNarrowsToTheIssuesThatMatchIt(t *testing.T) {
	t.Parallel()

	const issues = 30
	nobody := filter.Term{Facet: filter.FacetAssignee, Label: unassigned}
	for name, tc := range map[string]struct {
		terms []filter.Term
		match func(iss *jira.Issue) bool
	}{
		"an assignee": {
			terms: []filter.Term{adaTerm},
			match: func(iss *jira.Issue) bool {
				return iss.Assignee != nil && iss.Assignee.AccountID == "acct-ada"
			},
		},
		"a reporter": {
			terms: []filter.Term{{Facet: filter.FacetReporter, ID: "acct-grace", Label: "Grace Hopper"}},
			match: func(iss *jira.Issue) bool {
				return iss.Reporter != nil && iss.Reporter.AccountID == "acct-grace"
			},
		},
		"a status": {
			terms: []filter.Term{shipped},
			match: func(iss *jira.Issue) bool { return iss.Status.ID == "10203" },
		},
		"a type": {
			terms: []filter.Term{{Facet: filter.FacetType, ID: "10302", Label: "Defect"}},
			match: func(iss *jira.Issue) bool { return iss.Type.ID == "10302" },
		},
		"a priority": {
			terms: []filter.Term{{Facet: filter.FacetPriority, ID: "10402", Label: "Normal"}},
			match: func(iss *jira.Issue) bool { return iss.Priority != nil && iss.Priority.ID == "10402" },
		},
		"a label": {
			terms: []filter.Term{{Facet: filter.FacetLabel, ID: "infra", Label: "infra"}},
			match: func(iss *jira.Issue) bool { return slices.Contains(iss.Labels, "infra") },
		},
		"two values of one facet, which widen it": {
			terms: []filter.Term{triage, shipped},
			match: func(iss *jira.Issue) bool {
				return iss.Status.ID == "10201" || iss.Status.ID == "10203"
			},
		},
		"two facets, which narrow together": {
			terms: []filter.Term{shipped, {Facet: filter.FacetPriority, ID: "10403", Label: "Whenever"}},
			match: func(iss *jira.Issue) bool {
				return iss.Status.ID == "10203" && iss.Priority != nil && iss.Priority.ID == "10403"
			},
		},
		"nobody, or a named person": {
			terms: []filter.Term{nobody, adaTerm},
			match: func(iss *jira.Issue) bool {
				return iss.Assignee == nil || iss.Assignee.AccountID == "acct-ada"
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := matchingKeys(jiratest.Gen(issues), tc.match)
			if len(want) == 0 || len(want) == issues {
				t.Fatalf("the term matches %d of %d issues, so it would pass against a site that filtered nothing",
					len(want), issues)
			}

			dr := openAll(t, testDeps(newFake(issues)), 120, 40)
			for _, term := range tc.terms {
				dr.send(filter.ChosenMsg{Term: term})
			}

			mustNotContain(t, dr.view(), "The search failed.")
			if got := loadedKeys(dr.m); !slices.Equal(got, want) {
				t.Errorf("%s selected %v, want %v (the search was %q)", name, got, want, dr.m.jql)
			}
		})
	}
}

// matchingKeys is the keys of the fixture issues a predicate keeps, sorted so
// that it can be compared with an answer that came back in another order.
func matchingKeys(all []jira.Issue, match func(iss *jira.Issue) bool) []string {
	out := make([]string, 0, len(all))
	for i := range all {
		if match(&all[i]) {
			out = append(out, all[i].Key)
		}
	}
	slices.Sort(out)
	return out
}

func loadedKeys(m *Model) []string {
	out := make([]string, 0, len(m.issues))
	for i := range m.issues {
		out = append(out, m.issues[i].Key)
	}
	slices.Sort(out)
	return out
}
