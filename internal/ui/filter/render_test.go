package filter

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestPicker_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width, height int
		open          Facet
		typed         string
		terms         Terms
		golden        string
	}{
		"the facets": {
			width: 120, height: 20, golden: "facets_120x20.golden",
		},
		"the facets with terms already in force": {
			width: 120, height: 20, golden: "facets_terms_120x20.golden",
			terms: Terms{
				{Facet: FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"},
				{Facet: FacetStatus, ID: "10203", Label: "Shipped"},
			},
		},
		"the statuses of every workflow": {
			width: 120, height: 20, open: FacetStatus, golden: "statuses_120x20.golden",
		},
		"the accounts assignable here": {
			width: 120, height: 20, open: FacetAssignee, golden: "people_120x20.golden",
		},
		"a needle that narrows the accounts": {
			width: 120, height: 20, open: FacetAssignee, typed: "ada", golden: "people_typed_120x20.golden",
		},
		"a needle that matches nothing": {
			width: 120, height: 20, open: FacetPriority, typed: "zzz", golden: "nothing_120x20.golden",
		},
		"a narrow terminal": {
			width: 80, height: 20, open: FacetStatus, golden: "statuses_80x20.golden",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []Option{WithEditKey("e")}
			if len(tc.terms) > 0 {
				opts = append(opts, WithTerms(tc.terms))
			}
			dr := newDriver(t, testDeps(newFake(20)), tc.width, tc.height, opts...)
			if tc.open != FacetNone {
				dr.pick(tc.open)
			}
			if tc.typed != "" {
				dr.typeText(tc.typed)
			}
			golden(t, tc.golden, dr.view())
		})
	}
}

func TestPicker_RefusedFacetsGolden(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20, jiratest.WithCapabilities(jiratest.NoPeople)))
	d.Caps.People = jira.Capability{Reason: "needs the Browse users and groups permission"}
	dr := newDriver(t, d, 120, 20, WithEditKey("e"))

	golden(t, "facets_refused_120x20.golden", dr.view())
}

func TestPicker_FailureGolden(t *testing.T) {
	t.Parallel()

	f := newFake(20)
	f.FailNext(&jira.CapabilityError{Capability: jira.CapPeople, Reason: "needs the Browse users and groups permission"})
	dr := newDriver(t, testDeps(f), 120, 20, WithEditKey("e"))
	dr.pick(FacetAssignee)

	golden(t, "failed_120x20.golden", dr.view())
}

// Every row is exactly as wide as the pane, whatever is in it, or the selected
// row's highlight stops short of the edge.
func TestPicker_EveryRowFillsTheWidth(t *testing.T) {
	t.Parallel()

	for _, width := range []int{80, 100, 120, 200} {
		dr := newDriver(t, testDeps(newFake(20)), width, 20)
		dr.pick(FacetStatus)
		lines := strings.Split(dr.view(), "\n")[headHeight:]
		for i := range dr.m.shown {
			if got := ansi.StringWidth(lines[i]); got != width {
				t.Errorf("at %d columns a row is %d wide: %q", width, got, lines[i])
			}
		}
	}
}

// The pane keeps to the box it was given at every width, including one too
// narrow for a second column.
func TestPicker_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{40, 10}, {80, 20}, {120, 30}, {200, 60}} {
		dr := newDriver(t, testDeps(newFake(20)), size.w, size.h)
		dr.pick(FacetLabel)
		lines := strings.Split(dr.m.View(), "\n")
		if len(lines) != size.h {
			t.Errorf("at %dx%d the frame is %d lines", size.w, size.h, len(lines))
		}
		for _, line := range lines {
			if got := ansi.StringWidth(line); got > size.w {
				t.Errorf("at %dx%d a line is %d columns: %q", size.w, size.h, got, line)
			}
		}
	}
}

// The needle's line is memoized, and its placeholder names the facet, so going
// from one facet to another must not leave the last one's question on screen.
func TestPicker_TheNeedleAsksAboutTheFacetItIsOpenOn(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 120, 20)
	dr.pick(FacetStatus)
	mustContain(t, dr.view(), "which status?")

	dr.key("esc")
	dr.pick(FacetPriority)

	mustContain(t, dr.view(), "which priority?")
	mustNotContain(t, dr.view(), "which status?")
}

func TestPicker_RowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	dr := newDriver(t, testDeps(newFake(40)), 120, 30)
	dr.pick(FacetLabel)
	_ = dr.m.View()

	if got := testing.AllocsPerRun(200, func() { _ = dr.m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}
