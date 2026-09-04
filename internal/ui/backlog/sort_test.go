package backlog

import (
	"slices"
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

// groupKeys is the keys of one section in the order it is drawn.
func groupKeys(dr *driver, g int) []string {
	out := make([]string, 0, len(dr.m.groups[g].issues))
	for _, at := range dr.m.groups[g].issues {
		out = append(out, dr.m.issues[at].Key)
	}
	return out
}

// isOrderedBy asserts every section the backlog drew is ordered by f, without
// assuming which order the fake happened to generate — it walks the section
// with the same compare function the view itself sorted it with.
func isOrderedBy(t *testing.T, dr *driver, f sortField, desc bool) {
	t.Helper()
	for g := range dr.m.groups {
		issues := dr.m.groups[g].issues
		for i := 1; i < len(issues); i++ {
			a, b := &dr.m.issues[issues[i-1]], &dr.m.issues[issues[i]]
			c := f.compare(a, b)
			if desc {
				c = -c
			}
			if c > 0 {
				t.Fatalf("section %d: %s sorts after %s under %s (desc=%v)", g, a.Key, b.Key, f.label, desc)
			}
		}
	}
}

// TestSort_ChoosingAFieldReordersEveryGroup checks the field is applied inside
// each section rather than across the whole backlog, which is what keeps a
// sprint's own rows a section of their own.
func TestSort_ChoosingAFieldReordersEveryGroup(t *testing.T) {
	ownSortCache(t)

	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	last := len(dr.m.groups) - 1
	before := groupKeys(dr, last)
	if len(before) < 2 {
		t.Fatal("the backlog section needs at least two issues to prove an order changed")
	}

	dr.key("s", "l", "enter") // key -> summary
	f, ok := sortFieldByID("summary")
	if !ok {
		t.Fatal("summary is not a field this view knows")
	}
	isOrderedBy(t, dr, f, false)
	if dr.m.mode == sorting {
		t.Error("choosing a field left the picker open")
	}
}

// TestSort_ChoosingTheFieldAlreadyChosenTogglesDirection mirrors
// internal/ui/list's own gesture: docs/FILTERS.md asks for one gesture that
// reaches a field and its direction both.
func TestSort_ChoosingTheFieldAlreadyChosenTogglesDirection(t *testing.T) {
	ownSortCache(t)

	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	dr.key("s", "enter") // key, ascending
	if dr.m.sort.desc {
		t.Fatal("key did not open ascending")
	}
	f, _ := sortFieldByID("key")
	isOrderedBy(t, dr, f, false)

	dr.key("s", "enter") // key again: toggles
	if !dr.m.sort.desc {
		t.Fatal("choosing the same field again did not flip the direction")
	}
	isOrderedBy(t, dr, f, true)
}

// TestSort_TakesOverFromTheBoardsOwnRank is the reason a sort exists here at
// all: jiratest.Scrum gives the board a rank field, so choosing nothing keeps
// reading rank order, and choosing a field must stop deferring to it.
func TestSort_TakesOverFromTheBoardsOwnRank(t *testing.T) {
	ownSortCache(t)

	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	if dr.m.config.RankFieldID == "" {
		t.Fatal("this fixture's board has no rank field, so this test proves nothing")
	}
	if got := dr.m.ordering(); !strings.Contains(got, "Rank") {
		t.Fatalf("ordering() before any sort is %q, want it to name the board's rank", got)
	}

	dr.key("s", "l", "l", "l", "l", "enter") // key,summary,status,type,priority
	if got := dr.m.ordering(); !strings.Contains(got, "Sorted") {
		t.Errorf("ordering() is %q once a field is chosen, want it to say so rather than naming rank", got)
	}
	f, _ := sortFieldByID("priority")
	isOrderedBy(t, dr, f, false)
}

// TestSort_KeepsAFilterInForce checks a sort and a local filter are two
// independent axes, the way internal/ui/list's own combination is.
func TestSort_KeepsAFilterInForce(t *testing.T) {
	ownSortCache(t)

	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	term, ok := firstAssignee(dr)
	if !ok {
		t.Fatal("no generated issue carries an assignee, so this case proves nothing")
	}
	dr.send(filter.ChosenMsg{Term: term})
	if dr.m.filteredOut == 0 {
		t.Fatal("the term never actually narrowed anything")
	}
	narrowed := dr.m.filteredOut

	dr.key("s", "l", "enter") // summary
	if dr.m.filteredOut != narrowed {
		t.Errorf("choosing a sort changed how many the filter hid: %d, want %d", dr.m.filteredOut, narrowed)
	}
	for g := range dr.m.groups {
		for _, at := range dr.m.groups[g].issues {
			iss := &dr.m.issues[at]
			if iss.Assignee == nil || iss.Assignee.AccountID != term.ID {
				t.Errorf("%s is drawn under the sort and is not assigned to %s", iss.Key, term.Label)
			}
		}
	}
	f, _ := sortFieldByID("summary")
	isOrderedBy(t, dr, f, false)
}

// TestSort_SurvivesARestart is the reason config.UIState is where it is: this
// machine's own idea of how a view likes to look at things.
func TestSort_SurvivesARestart(t *testing.T) {
	ownSortCache(t)

	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	dr.key("s", "l", "l", "l", "enter") // key,summary,status,type

	spec, kept := config.LoadUIState().Sort(ViewID)
	if !kept || spec.Field != "type" || spec.Desc {
		t.Fatalf("the choice on disk is %+v (kept=%v), want {type false}", spec, kept)
	}

	reopened, ok := New(testDeps(seeded(t))).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	if reopened.sort.field != "type" {
		t.Fatalf("a freshly built view opened with sort %+v, want type", reopened.sort)
	}
}

// TestSort_CancellingLeavesTheOrderAsItWas checks esc is a true cancel, not a
// clear.
func TestSort_CancellingLeavesTheOrderAsItWas(t *testing.T) {
	ownSortCache(t)

	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	dr.key("s", "l", "enter") // summary
	before := groupKeys(dr, len(dr.m.groups)-1)

	dr.key("s", "l", "l") // move around without choosing
	dr.key("esc")

	if dr.m.mode == sorting {
		t.Error("esc left the picker open")
	}
	if dr.m.sort.field != "summary" {
		t.Errorf("cancelling changed the order to %+v", dr.m.sort)
	}
	if got := groupKeys(dr, len(dr.m.groups)-1); !slices.Equal(got, before) {
		t.Errorf("cancelling still reordered a section: %v vs %v", got, before)
	}
}

// TestSort_HeaderNamesTheChoiceAcrossGlyphTiers is the golden
// docs/ROADMAP.md asks for: the header names what is in force, in words a
// Nerd Font is not needed to read.
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
			d := testDeps(seeded(t))
			d.Theme = kernel.NewTheme(kernel.ThemeNoColor, true, tier.glyphs)
			dr := newDriver(t, d, 100, 24)
			dr.key("s", "l", "l", "l", "l", "l", "l", "l", "enter") // ...updated
			dr.key("s", "enter")                                    // toggle to descending

			golden(t, "sort_header_"+tier.name+"_100x24.golden", firstLine(dr.view()))
		})
	}
}

// TestSort_ClickingTheHeaderLabelReopensThePicker is docs/UX.md principle 3:
// every action reachable by key is reachable by mouse too.
func TestSort_ClickingTheHeaderLabelReopensThePicker(t *testing.T) {
	ownSortCache(t)

	d := testDeps(seeded(t))
	dr := newDriver(t, d, 120, 24)
	dr.key("s", "l", "enter") // summary
	if dr.m.mode == sorting {
		t.Fatal("choosing a field left the picker open")
	}

	pressOn(t, d, dr, sortZone)

	if dr.m.mode != sorting {
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
