package filterbar

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var (
	ada  = filter.Term{Facet: filter.FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"}
	ben  = filter.Term{Facet: filter.FacetAssignee, ID: "acct-ben", Label: "Ben Adams"}
	bug  = filter.Term{Facet: filter.FacetType, ID: "10001", Label: "Bug"}
	prog = filter.Term{Facet: filter.FacetStatus, ID: "10203", Label: "In Progress"}
)

func plainTheme() *kernel.Theme {
	return kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs())
}

func TestBar_DrawsNothingWithNoTermsInForce(t *testing.T) {
	t.Parallel()

	bar := New(widget.Zoner{})
	if got := bar.Render(nil, 120, plainTheme(), "ctrl+g", 1); got != "" {
		t.Errorf("an empty term set drew %q, want nothing", got)
	}
}

// One chip per facet, not one per value: the grouping filter.Terms already
// promises.
func TestBar_GroupsTheValuesOfOneFacetIntoOneChip(t *testing.T) {
	t.Parallel()

	bar := New(widget.Zoner{})
	got := ansi.Strip(bar.Render(filter.Terms{ada, ben, prog}, 120, plainTheme(), "ctrl+g", 1))

	if strings.Count(got, "assignee:") != 1 {
		t.Errorf("the assignee facet is named more than once: %q", got)
	}
	if !strings.Contains(got, "Ada Lovelace, Ben Adams") {
		t.Errorf("the two assignees are not joined into one chip: %q", got)
	}
	if !strings.Contains(got, "status: In Progress") {
		t.Errorf("the status chip is missing: %q", got)
	}
	if !strings.Contains(got, "ctrl+g clears everything") {
		t.Errorf("the clear-everything hint is missing: %q", got)
	}
}

func TestBar_ClickOnAValueNamesJustThatValue(t *testing.T) {
	t.Parallel()

	bar, z, mgr := markedBar(t)
	line := bar.Render(filter.Terms{ada, ben, prog}, 120, plainTheme(), "ctrl+g", 1)
	msg := pressOn(t, mgr, z, line, ValueZone(ben))

	facet, value, ok := bar.Click(msg, filter.Terms{ada, ben, prog})
	if !ok {
		t.Fatal("the click resolved to nothing")
	}
	if facet != filter.FacetNone {
		t.Errorf("a click on a value resolved to dropping the facet %q too", facet.Label())
	}
	if value != ben {
		t.Errorf("the click named %+v, want Ben Adams", value)
	}
}

func TestBar_ClickOnTheCrossDropsTheWholeFacet(t *testing.T) {
	t.Parallel()

	bar, z, mgr := markedBar(t)
	terms := filter.Terms{ada, ben, prog}
	line := bar.Render(terms, 120, plainTheme(), "ctrl+g", 1)
	msg := pressOn(t, mgr, z, line, FacetZone(filter.FacetAssignee))

	facet, _, ok := bar.Click(msg, terms)
	if !ok || facet != filter.FacetAssignee {
		t.Errorf("the × resolved to %q, ok=%v, want the assignee facet", facet.Label(), ok)
	}
}

func TestBar_ClickOffAnyZoneResolvesToNothing(t *testing.T) {
	t.Parallel()

	bar, _, _ := markedBar(t)
	terms := filter.Terms{ada}
	_ = bar.Render(terms, 120, plainTheme(), "ctrl+g", 1)

	_, _, ok := bar.Click(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}, terms)
	if ok {
		t.Error("a click on nothing scanned resolved to a drop")
	}
}

func TestBar_RenderIsMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	bar := New(widget.Zoner{})
	terms := filter.Terms{ada, ben, prog}
	theme := plainTheme()
	_ = bar.Render(terms, 120, theme, "ctrl+g", 1)

	if got := testing.AllocsPerRun(200, func() { _ = bar.Render(terms, 120, theme, "ctrl+g", 1) }); got != 0 {
		t.Errorf("a memoized bar allocates %.1f times, want none", got)
	}
}

func TestBar_ATermChangeInvalidatesTheMemo(t *testing.T) {
	t.Parallel()

	bar := New(widget.Zoner{})
	theme := plainTheme()
	first := bar.Render(filter.Terms{ada}, 120, theme, "ctrl+g", 1)
	second := bar.Render(filter.Terms{ada, bug}, 120, theme, "ctrl+g", 2)

	if first == second {
		t.Error("adding a term left the line unchanged")
	}
}

// A chip that does not fit falls back to an ellipsis rather than overflowing
// the width it was given.
func TestBar_NeverOverflowsTheWidthItWasGiven(t *testing.T) {
	t.Parallel()

	bar := New(widget.Zoner{})
	terms := filter.Terms{
		ada, ben,
		{Facet: filter.FacetReporter, ID: "acct-x", Label: strings.Repeat("a very long name ", 4)},
		bug, prog,
	}
	for i, w := range []int{80, 100, 120, 200} {
		got := bar.Render(terms, w, plainTheme(), "ctrl+g", i+1)
		for _, line := range strings.Split(got, "\n") {
			if n := ansi.StringWidth(line); n > w {
				t.Errorf("at width %d a line measures %d: %q", w, n, line)
			}
		}
	}
}
