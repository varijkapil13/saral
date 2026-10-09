package board

import (
	tea "charm.land/bubbletea/v2"

	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/filterbar"
)

// applyFilterTerm puts a value in force or takes it off again, and re-places
// the cards from what is already loaded rather than asking the site again.
//
// BoardQuery's own doc says why: what a board holds is the board's to define,
// and a caller that wants a subset of it filters the rows it was given rather
// than asking the site a question whose answer nothing can compare against the
// board on screen. A person, a status, a type, a priority or a label is
// exactly that kind of subset — this program's own idea of a narrower board,
// not one the site's board draws too — so it is applied here and never sent.
func (m *Model) applyFilterTerm(term appterm.Term) tea.Cmd {
	return m.setTerms(m.terms.Toggle(term))
}

// clearFilter drops every term FilterBy put in force. Named separately from
// setTerms(nil) because it is the one ctrl+g reaches, and a no-op on an
// already-empty board is not worth a place().
func (m *Model) clearFilter() tea.Cmd {
	if len(m.terms) == 0 {
		return nil
	}
	return m.setTerms(nil)
}

func (m *Model) recallTerms() (appterm.Terms, bool) { return filterbar.Recall(m.deps, ViewID) }

func (m *Model) rememberTerms() { filterbar.Keep(m.deps, ViewID, m.terms) }

func (m *Model) setTerms(next appterm.Terms) tea.Cmd {
	m.terms = next
	m.rememberTerms()
	m.place()
	if m.more && len(m.terms) > 0 {
		return kernel.Warn("this board has more cards than are loaded, so the filter only sees the ones on screen")
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

// openFilterPicker pushes the same picker the issue list uses, over this
// board, armed with whatever is already in force.
func (m *Model) openFilterPicker() tea.Cmd {
	keys := defaultKeys()
	return kernel.Push(filter.ViewID, "Filter",
		filter.New(m.deps, filter.WithTerms(m.terms), filter.WithEditKey(keys.FilterBy.Help().Key)))
}
