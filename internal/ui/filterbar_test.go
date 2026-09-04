package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// filterSweepTerm is what the sweep below puts in force. Any facet does.
var filterSweepTerm = filter.Term{Facet: filter.FacetStatus, ID: "1", Label: "Triage"}

// TestFilterBar_EveryViewThatFiltersDrawsIt walks the registry rather than
// naming views: kernel.ViewSpec.Filters is how a view declares it handles
// filter.ChosenMsg and draws internal/ui/widget/filterbar's bar, the way
// RunsQueries already declares a narrower thing about a view the kernel may
// not otherwise ask — the kernel imports neither internal/ui/filter nor the
// widget, so a view has to say so itself. Building the check here rather than
// in one view's own package is what lets the walk see every view at once:
// internal/ui is the one package every view is registered into, the same
// reason TestDestinations_NameEveryViewThatClaimedADigit and
// TestLiveKeys_EveryViewWhoseKeysMoveReportsThem live here too.
//
// board once held a filter.Terms and rendered none of it for a whole packet —
// this sweep is what makes that specific silence fail the build the next time
// it happens to some other view, rather than only to the one this packet
// happened to be reviewing by hand.
func TestFilterBar_EveryViewThatFiltersDrawsIt(t *testing.T) {
	sweepEnv(t)
	specs := kernel.Views()
	if len(specs) == 0 {
		t.Fatal("no view registered, so this sweep is checking nothing")
	}

	filtering := 0
	for _, spec := range specs {
		if !spec.Filters {
			continue
		}
		filtering++
		after := filterSweepFrame(t, spec, filter.ChosenMsg{Term: filterSweepTerm})
		if !strings.Contains(after, "clears everything") {
			t.Errorf("%s declares Filters and does not draw the filter bar "+
				"(internal/ui/widget/filterbar) once a %s filter is in force:\n%s",
				spec.ID, filterSweepTerm.Facet.Label(), after)
		}
	}
	if filtering == 0 {
		t.Fatal("no view in this build declares kernel.ViewSpec.Filters, so this sweep is checking nothing")
	}
}

// filterSweepFrame builds one view fresh, gives it a size and applies msgs in
// order, then reports what it draws. It never calls Init: applying a term is
// meant to work before a view has read anything from a site, the way it does
// for a session that filters before the first answer lands.
func filterSweepFrame(t *testing.T, spec kernel.ViewSpec, msgs ...tea.Msg) string {
	t.Helper()
	view := spec.New(depsFor(t))
	view, _ = view.Update(kernel.SizeMsg{Width: 100, Height: 30})
	view, _ = view.Update(kernel.FocusMsg{Focused: true})
	for _, msg := range msgs {
		view, _ = view.Update(msg)
	}
	return ansi.Strip(view.View())
}
