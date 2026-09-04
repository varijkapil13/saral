package list

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// ownSortCache points this test's saved sort at a directory of its own.
// t.Setenv rules out t.Parallel, which is why nothing in this file runs in
// parallel.
func ownSortCache(t *testing.T) {
	t.Helper()
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
}

// TestSort_ChoosingAFieldReRunsTheSearch is the gate docs/ROADMAP.md asks for:
// the JQL changed, not just the order rows happened to land in on screen.
func TestSort_ChoosingAFieldReRunsTheSearch(t *testing.T) {
	ownSortCache(t)

	dr := openAll(t, testDeps(newFake(12)), 120, 24)
	before := dr.m.jql

	dr.key("s", "l", "l", "enter") // key -> summary -> status
	after := dr.m.jql

	if after == before {
		t.Fatalf("choosing a sort left the JQL unchanged: %q", after)
	}
	if !strings.Contains(after, "ORDER BY status ASC") {
		t.Errorf("jql is %q, want an ORDER BY status ASC", after)
	}
	if !strings.HasPrefix(after, `project = "PROJ"`) {
		t.Errorf("jql is %q, the search's own WHERE clause should survive", after)
	}
	if dr.m.sorting {
		t.Error("choosing a field left the picker open")
	}
}

// TestSort_ChoosingTheFieldAlreadyChosenTogglesDirection is the other half of
// docs/FILTERS.md's "each toggling ascending and descending": one gesture
// reaches both a field and its direction.
func TestSort_ChoosingTheFieldAlreadyChosenTogglesDirection(t *testing.T) {
	ownSortCache(t)

	dr := openAll(t, testDeps(newFake(12)), 120, 24)
	dr.key("s", "l", "l", "enter") // status, ascending
	if dr.m.sort.desc {
		t.Fatal("status did not open ascending")
	}

	dr.key("s", "enter") // status again: the cursor opens back on it
	if !dr.m.sort.desc {
		t.Fatal("choosing the same field again did not flip the direction")
	}
	if !strings.Contains(dr.m.jql, "ORDER BY status DESC") {
		t.Errorf("jql is %q, want ORDER BY status DESC", dr.m.jql)
	}

	dr.key("s", "enter") // and back to ascending
	if dr.m.sort.desc {
		t.Fatal("a third choice did not flip the direction back")
	}
}

// TestSort_KeepsAFilterInForce checks the other direction too: a search
// narrowed by a term keeps the term when a sort is chosen over it.
func TestSort_KeepsAFilterInForce(t *testing.T) {
	ownSortCache(t)

	dr := openAll(t, testDeps(newFake(12)), 120, 24)
	dr.send(filter.ChosenMsg{Term: triage})
	if len(dr.m.terms) == 0 {
		t.Fatal("the term never took")
	}
	narrowedJQL := dr.m.jql

	dr.key("s", "enter") // key, ascending
	if len(dr.m.terms) != 1 {
		t.Fatalf("choosing a sort dropped the terms: %v", dr.m.terms)
	}
	if !strings.Contains(dr.m.jql, "ORDER BY key ASC") {
		t.Errorf("jql is %q, want ORDER BY key ASC", dr.m.jql)
	}
	if !strings.Contains(dr.m.jql, "status") {
		t.Errorf("jql is %q, the term's own clause should still be there", dr.m.jql)
	}
	if dr.m.jql == narrowedJQL {
		t.Error("the order never actually changed")
	}
}

// TestSort_ASortInForceKeepsAFilterAppliedAfterIt is the mirror: choosing a
// term after a sort has already been picked must not lose the order.
func TestSort_ASortInForceKeepsAFilterAppliedAfterIt(t *testing.T) {
	ownSortCache(t)

	dr := openAll(t, testDeps(newFake(12)), 120, 24)
	dr.key("s", "l", "enter") // summary, ascending
	dr.send(filter.ChosenMsg{Term: shipped})

	if !strings.Contains(dr.m.jql, "ORDER BY summary ASC") {
		t.Errorf("jql is %q, the sort chosen earlier should survive a new term", dr.m.jql)
	}
}

// TestSort_SurvivesARestart is the reason config.UIState is where it is:
// this machine's own idea of how a view likes to look at things.
func TestSort_SurvivesARestart(t *testing.T) {
	ownSortCache(t)

	dr := openAll(t, testDeps(newFake(12)), 120, 24)
	dr.key("s", "l", "l", "l", "l", "l", "enter") // key,summary,status,type,priority,assignee

	spec, kept := config.LoadUIState().Sort(ViewID)
	if !kept || spec.Field != "assignee" || spec.Desc {
		t.Fatalf("the choice on disk is %+v (kept=%v), want {assignee false}", spec, kept)
	}

	reopened, ok := New(testDeps(newFake(12))).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if reopened.sort.field != "assignee" {
		t.Fatalf("a freshly built view opened with sort %+v, want assignee", reopened.sort)
	}
	if !strings.Contains(reopened.jql, "ORDER BY assignee ASC") {
		t.Errorf("the reopened view's own default search is %q, want it sorted by assignee", reopened.jql)
	}
}

// TestSort_CancellingLeavesTheOrderAsItWas checks esc is a true cancel, not a
// clear: it must not run a query, and any order already in force stays.
func TestSort_CancellingLeavesTheOrderAsItWas(t *testing.T) {
	ownSortCache(t)

	dr := openAll(t, testDeps(newFake(12)), 120, 24)
	dr.key("s", "l", "enter") // summary
	jqlBefore := dr.m.jql

	dr.key("s", "l", "l", "l") // move the cursor around without choosing
	dr.key("esc")

	if dr.m.sorting {
		t.Error("esc left the picker open")
	}
	if dr.m.sort.field != "summary" {
		t.Errorf("cancelling changed the order to %+v", dr.m.sort)
	}
	if dr.m.jql != jqlBefore {
		t.Errorf("cancelling still re-ran the search: %q vs %q", dr.m.jql, jqlBefore)
	}
}

// TestSort_HeaderNamesTheChoiceAcrossGlyphTiers is the golden docs/ROADMAP.md
// asks for: the header names what is in force, in words a Nerd Font is not
// needed to read.
func TestSort_HeaderNamesTheChoiceAcrossGlyphTiers(t *testing.T) {
	for _, tier := range []struct {
		name   string
		glyphs kernel.Glyphs
	}{
		{"nerd", kernel.NerdGlyphs()},
		{"ascii", kernel.ASCIIGlyphs()},
	} {
		t.Run(tier.name, func(t *testing.T) {
			ownSortCache(t)
			d := testDeps(newFake(12))
			d.Theme = kernel.NewTheme(kernel.ThemeNoColor, true, tier.glyphs)
			dr := openAll(t, d, 100, 24)
			dr.key("s", "l", "l", "l", "l", "l", "l", "l", "enter") // ...updated
			dr.key("s", "enter")                                   // toggle to descending

			golden(t, "sort_header_"+tier.name+"_100x24.golden", firstLine(dr.view()))
		})
	}
}

// TestSort_ClickingTheHeaderLabelReopensThePicker is docs/UX.md principle 3:
// every action reachable by key is reachable by mouse too, the same way a
// click on the title reopens the search prompt.
func TestSort_ClickingTheHeaderLabelReopensThePicker(t *testing.T) {
	ownSortCache(t)

	d := testDeps(newFake(12))
	dr := openAll(t, d, 120, 24)
	dr.key("s", "l", "enter") // summary
	if dr.m.sorting {
		t.Fatal("choosing a field left the picker open")
	}

	pressOn(t, d, dr, sortZone)

	if !dr.m.sorting {
		t.Error("clicking the sort label did not reopen the picker")
	}
	if dr.m.sortCursor != sortFieldIndex("summary") {
		t.Errorf("the picker opened on cursor %d, want it on the field already chosen", dr.m.sortCursor)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
