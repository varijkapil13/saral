package list

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
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
// them. Everything in between is a real keypress.
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

	got := frame(m)
	mustContain(t, got, `status "Triage"`, "status Triage in PROJ", "PROJ-")
	mustNotContain(t, got, "which status?", "The search failed.")
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
	mustNotContain(t, dr.view(), "status \"Shipped\"")
}

// A filter you cannot see is one you cannot escape, so every term is on screen
// with the way off it.
func TestList_TheTermsInForceAreNamedUnderTheRows(t *testing.T) {
	t.Parallel()

	dr := openAll(t, testDeps(newFake(20)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})
	dr.send(filter.ChosenMsg{Term: adaTerm})

	mustContain(t, dr.view(), `assignee "Ada Lovelace"`, `status "Shipped"`, "click one to drop it")
}

// With no pointer the line names the key that changes them instead, because
// clicking a chip is the only other way off one.
func TestList_TheTermsLineNamesAKeyWhenThereIsNoPointer(t *testing.T) {
	t.Parallel()

	d := plainDeps(newFake(20))
	d.Zones = nil
	dr := openAll(t, d, 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})

	mustContain(t, dr.view(), "f changes them")
	mustNotContain(t, dr.view(), "click one to drop it")
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
	mustNotContain(t, dr.view(), `status "Shipped"`)
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

	mustContain(t, dr.view(), `status "Shipped"`, `only rows matching "PROJ-2"`)
}

func TestList_TermsGolden(t *testing.T) {
	t.Parallel()

	alan := filter.Term{Facet: filter.FacetAssignee, ID: "acct-alan", Label: "Alan Turing"}
	dr := openAll(t, testDeps(newFake(12)), 120, 30)
	dr.send(filter.ChosenMsg{Term: shipped})
	dr.send(filter.ChosenMsg{Term: alan})

	golden(t, "list_two_terms_120x30.golden", dr.view())
}
