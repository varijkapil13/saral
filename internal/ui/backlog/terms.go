package backlog

import (
	tea "charm.land/bubbletea/v2"

	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/filterbar"
)

// OpenFilterMsg opens the picker over the backlog on screen. It is exported so
// the palette reaches the same gesture f does.
type OpenFilterMsg struct{}

// ClearFilterMsg drops every term the filter picker put in force. It is
// exported so the palette reaches the gesture ctrl+g does rather than a second
// implementation of it.
type ClearFilterMsg struct{}

// openFilterPicker pushes the same picker the issue list uses, over this
// backlog, armed with whatever is already in force.
func (m *Model) openFilterPicker() tea.Cmd {
	keys := defaultKeys()
	return kernel.Push(filter.ViewID, "Filter",
		filter.New(m.deps, filter.WithTerms(m.terms), filter.WithEditKey(keys.FilterBy.Help().Key)))
}

// applyFilterTerm puts a value in force or takes it off again, and regroups
// from what is already loaded rather than asking the site again: a backlog's
// own read is already whole in memory, the way a board's is.
func (m *Model) applyFilterTerm(term appterm.Term) tea.Cmd {
	return m.setTerms(m.terms.Toggle(term))
}

// clearFilter drops every term in force. Named separately from setTerms(nil)
// because it is the one ctrl+g reaches, and a no-op on an already-empty
// backlog is not worth a regroup.
func (m *Model) clearFilter() tea.Cmd {
	if len(m.terms) == 0 {
		return nil
	}
	return m.setTerms(nil)
}

func (m *Model) recallTerms() (appterm.Terms, bool) { return filterbar.Recall(m.deps, ViewID) }

func (m *Model) rememberTerms() { filterbar.Keep(m.deps, ViewID, m.terms) }

func (m *Model) setTerms(next appterm.Terms) tea.Cmd {
	m.terms, m.termsGen = next, m.termsGen+1
	m.rememberTerms()
	m.regroup()
	if m.page.HasMore() && len(m.terms) > 0 {
		return kernel.Warn("this backlog has more issues than are loaded, so the filter only sees the ones on screen")
	}
	return nil
}

// clickTerm resolves a click on the filter bar: one on a value's name drops
// just that value, and one on a facet's × drops the whole clause, both through
// the same Toggle the keyboard uses so the two cannot disagree.
func (m *Model) clickTerm(msg tea.MouseClickMsg) (tea.Cmd, bool) {
	if len(m.terms) == 0 {
		return nil, false
	}
	facet, value, ok := m.bar.Click(msg, m.terms)
	if !ok {
		return nil, false
	}
	if facet != appterm.FacetNone {
		return m.setTerms(m.terms.Without(facet)), true
	}
	return m.setTerms(m.terms.Toggle(value)), true
}
