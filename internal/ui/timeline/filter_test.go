package timeline

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/widget/filterbar"
)

// f opens the same person/status/label picker the issue list, the board and
// the backlog use.
func TestTimeline_FOpensThePersonStatusLabelPicker(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(24)), 140, 24)
	dr.key("f")
	if got := dr.pushes; len(got) != 1 || got[0].ID != filter.ViewID {
		t.Fatalf("f pushed %+v, want one push of %q", got, filter.ViewID)
	}
}

// The whole reason this exists: choosing a term narrows what the chart draws,
// without asking the site again, and choosing the same term again restores it
// — the same behaviour board.terms and backlog.terms already have.
func TestTimeline_ChoosingATermNarrowsLocallyAndChoosingItAgainRestoresIt(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(24)), 140, 24)
	before := len(dr.m.rows)
	if before == 0 {
		t.Fatal("the chart answered with no bars before any term was chosen")
	}
	term, ok := firstAssignee(dr)
	if !ok {
		t.Fatal("no generated issue carries an assignee, so this case proves nothing")
	}

	dr.send(filter.ChosenMsg{Term: term})
	if got := len(dr.m.rows); got == 0 || got >= before {
		t.Fatalf("choosing %s narrowed to %d bars, want fewer than the %d before", term.Label, got, before)
	}
	if dr.m.filteredOut == 0 {
		t.Error("filteredOut is 0 after a term hid at least one issue")
	}
	for _, r := range dr.m.rows {
		iss := &dr.m.issues[r.at]
		if iss.Assignee == nil || iss.Assignee.AccountID != term.ID {
			t.Errorf("%s is drawn and is not assigned to %s", iss.Key, term.Label)
		}
	}

	dr.send(filter.ChosenMsg{Term: term})
	if got := len(dr.m.rows); got != before {
		t.Errorf("choosing %s again left %d bars, want the original %d", term.Label, got, before)
	}
	if dr.m.filteredOut != 0 {
		t.Errorf("filteredOut is %d after the only term was toggled back off", dr.m.filteredOut)
	}
}

// ctrl+g clears every term in force in one stroke, the same key the issue
// list, the board and the backlog answer it with.
func TestTimeline_CtrlGClearsATermInForce(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(newFake(24)), 140, 24)
	before := len(dr.m.rows)
	term, ok := firstAssignee(dr)
	if !ok {
		t.Fatal("no generated issue carries an assignee, so this case proves nothing")
	}
	dr.send(filter.ChosenMsg{Term: term})
	if got := len(dr.m.rows); got >= before {
		t.Fatalf("choosing a term left %d bars, want fewer than the %d before", got, before)
	}

	dr.key("ctrl+g")
	if len(dr.m.terms) != 0 {
		t.Errorf("ctrl+g left %v in force", dr.m.terms)
	}
	if got := len(dr.m.rows); got != before {
		t.Errorf("ctrl+g left %d bars, want the original %d back", got, before)
	}
	if strings.Contains(dr.view(), "clears everything") {
		t.Error("the bar is still drawn after ctrl+g cleared the only term")
	}
}

// Clicking a chip's × drops the whole facet, and clicking a value inside one
// drops just that value — both through the widget the other three views use.
func TestTimeline_ClickingTheBarDropsAFacetOrAValue(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(24))
	dr := newDriver(t, d, 140, 24)
	assignee, ok := firstAssignee(dr)
	if !ok {
		t.Fatal("no generated issue carries an assignee, so this case proves nothing")
	}
	typ := firstType(dr)
	dr.send(filter.ChosenMsg{Term: assignee})
	dr.send(filter.ChosenMsg{Term: typ})

	pressOn(t, d, dr, filterbar.FacetZone(filter.FacetType))
	if got := dr.m.terms; len(got) != 1 || got[0].Facet != filter.FacetAssignee {
		t.Fatalf("clicking the type chip's x left %v, want only the assignee term", got)
	}

	pressOn(t, d, dr, filterbar.ValueZone(assignee))
	if len(dr.m.terms) != 0 {
		t.Errorf("clicking the assignee value left %v, want none", dr.m.terms)
	}
}

// Goldens for the bar itself: one facet and two, at the widths docs/FILTERS.md
// asks for.
func TestTimelineRender_FilterBarGolden(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		width, height int
		golden        string
		twoTerms      bool
	}{
		"one facet at 80":   {width: 80, height: 20, golden: "chart_term_80x20.golden"},
		"one facet at 140":  {width: 140, height: 24, golden: "chart_term_140x24.golden"},
		"two facets at 140": {width: 140, height: 24, golden: "chart_two_terms_140x24.golden", twoTerms: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := newDriver(t, testDeps(newFake(24)), tc.width, tc.height)
			term, ok := firstAssignee(dr)
			if !ok {
				t.Fatal("no generated issue carries an assignee, so this case proves nothing")
			}
			dr.send(filter.ChosenMsg{Term: term})
			if tc.twoTerms {
				dr.send(filter.ChosenMsg{Term: firstType(dr)})
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

func firstAssignee(dr *driver) (filter.Term, bool) {
	for i := range dr.m.issues {
		if a := dr.m.issues[i].Assignee; a != nil && a.AccountID != "" {
			return filter.Term{Facet: filter.FacetAssignee, ID: a.AccountID, Label: a.DisplayName}, true
		}
	}
	return filter.Term{}, false
}

func firstType(dr *driver) filter.Term {
	return filter.Term{Facet: filter.FacetType, ID: dr.m.issues[0].Type.ID, Label: dr.m.issues[0].Type.Name}
}
