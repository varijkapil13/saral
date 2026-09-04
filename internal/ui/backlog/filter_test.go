package backlog

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/widget/filterbar"
)

// f opens the same person/status/label picker the issue list and the board
// use, and is a different key from space, which picks a row.
func TestBacklog_FOpensThePersonStatusLabelPicker(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	dr.key("f")
	if got := dr.pushes; len(got) != 1 || got[0].ID != filter.ViewID {
		t.Fatalf("f pushed %+v, want one push of %q", got, filter.ViewID)
	}
}

// The whole reason this exists: choosing a term narrows what the backlog
// shows, without asking the site again, and choosing the same term again
// restores it — the same behaviour board.terms already has, applied here.
func TestBacklog_ChoosingATermNarrowsLocallyAndChoosingItAgainRestoresIt(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	before := rowsShown(dr)
	if before == 0 {
		t.Fatal("the backlog answered with no rows before any term was chosen")
	}
	term, ok := firstAssignee(dr)
	if !ok {
		t.Fatal("no generated issue carries an assignee, so this case proves nothing")
	}

	dr.send(filter.ChosenMsg{Term: term})
	if got := rowsShown(dr); got == 0 || got >= before {
		t.Fatalf("choosing %s narrowed to %d rows, want fewer than the %d before", term.Label, got, before)
	}
	if dr.m.filteredOut == 0 {
		t.Error("filteredOut is 0 after a term hid at least one issue")
	}
	for g := range dr.m.groups {
		for _, at := range dr.m.groups[g].issues {
			iss := &dr.m.issues[at]
			if iss.Assignee == nil || iss.Assignee.AccountID != term.ID {
				t.Errorf("%s is drawn and is not assigned to %s", iss.Key, term.Label)
			}
		}
	}

	dr.send(filter.ChosenMsg{Term: term})
	if got := rowsShown(dr); got != before {
		t.Errorf("choosing %s again left %d rows, want the original %d", term.Label, got, before)
	}
	if dr.m.filteredOut != 0 {
		t.Errorf("filteredOut is %d after the only term was toggled back off", dr.m.filteredOut)
	}
}

// ctrl+g clears every term in force in one stroke, the same key the issue
// list and the board answer it with.
func TestBacklog_CtrlGClearsATermInForce(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, testDeps(seeded(t)), 120, 24)
	before := rowsShown(dr)
	term, ok := firstAssignee(dr)
	if !ok {
		t.Fatal("no generated issue carries an assignee, so this case proves nothing")
	}
	dr.send(filter.ChosenMsg{Term: term})
	if got := rowsShown(dr); got >= before {
		t.Fatalf("choosing a term left %d rows, want fewer than the %d before", got, before)
	}

	dr.key("ctrl+g")
	if len(dr.m.terms) != 0 {
		t.Errorf("ctrl+g left %v in force", dr.m.terms)
	}
	if got := rowsShown(dr); got != before {
		t.Errorf("ctrl+g left %d rows, want the original %d back", got, before)
	}
	if strings.Contains(dr.view(), "clears everything") {
		t.Error("the bar is still drawn after ctrl+g cleared the only term")
	}
}

// Clicking a chip's × drops the whole facet, and clicking a value inside one
// drops just that value — both through the widget the issue list and the
// board use.
func TestBacklog_ClickingTheBarDropsAFacetOrAValue(t *testing.T) {
	t.Parallel()
	d := testDeps(seeded(t))
	dr := newDriver(t, d, 120, 24)
	assignee, ok := firstAssignee(dr)
	if !ok {
		t.Fatal("no generated issue carries an assignee, so this case proves nothing")
	}
	lbl := firstLabel(dr, t)
	label := filter.Term{Facet: filter.FacetLabel, ID: lbl, Label: lbl}
	dr.send(filter.ChosenMsg{Term: assignee})
	dr.send(filter.ChosenMsg{Term: label})

	pressOn(t, d, dr, filterbar.FacetZone(filter.FacetLabel))
	if got := dr.m.terms; len(got) != 1 || got[0].Facet != filter.FacetAssignee {
		t.Fatalf("clicking the label chip's x left %v, want only the assignee term", got)
	}

	pressOn(t, d, dr, filterbar.ValueZone(assignee))
	if len(dr.m.terms) != 0 {
		t.Errorf("clicking the assignee value left %v, want none", dr.m.terms)
	}
}

// Goldens for the bar itself: one facet and two, at the widths docs/FILTERS.md
// asks for.
func TestBacklogRender_FilterBarGolden(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		width, height int
		golden        string
		twoTerms      bool
	}{
		"one facet at 80":  {width: 80, height: 20, golden: "backlog_term_80x20.golden"},
		"one facet at 120": {width: 120, height: 24, golden: "backlog_term_120x24.golden"},
		"two facets at 120": {
			width: 120, height: 24, golden: "backlog_two_terms_120x24.golden", twoTerms: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dr := newDriver(t, testDeps(seeded(t)), tc.width, tc.height)
			term, ok := firstAssignee(dr)
			if !ok {
				t.Fatal("no generated issue carries an assignee, so this case proves nothing")
			}
			dr.send(filter.ChosenMsg{Term: term})
			if tc.twoTerms {
				lbl := firstLabel(dr, t)
				dr.send(filter.ChosenMsg{Term: filter.Term{Facet: filter.FacetLabel, ID: lbl, Label: lbl}})
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

func rowsShown(dr *driver) int {
	n := 0
	for g := range dr.m.groups {
		n += len(dr.m.groups[g].issues)
	}
	return n
}

func firstAssignee(dr *driver) (filter.Term, bool) {
	for i := range dr.m.issues {
		if a := dr.m.issues[i].Assignee; a != nil && a.AccountID != "" {
			return filter.Term{Facet: filter.FacetAssignee, ID: a.AccountID, Label: a.DisplayName}, true
		}
	}
	return filter.Term{}, false
}

func firstLabel(dr *driver, t *testing.T) string {
	t.Helper()
	for i := range dr.m.issues {
		if len(dr.m.issues[i].Labels) > 0 {
			return dr.m.issues[i].Labels[0]
		}
	}
	t.Fatal("no generated issue carries a label, so this case proves nothing")
	return ""
}
