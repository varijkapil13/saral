package board

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
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
func (m *Model) applyFilterTerm(term filter.Term) tea.Cmd {
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

// termsMemoryKey is where this view keeps the terms in force, under its own
// ViewID.
const termsMemoryKey = "terms"

// recallTerms is what the last session on this profile left this board
// narrowed by, and whether it ever narrowed one at all.
func (m *Model) recallTerms() (filter.Terms, bool) {
	enc, ok := kernel.Recall(m.deps, ViewID, termsMemoryKey)
	if !ok {
		return nil, false
	}
	return filter.DecodeTerms(enc)
}

// rememberTerms keeps the terms now in force, so the next session opens on
// the same narrowing. An empty encoding clears whatever was kept before.
func (m *Model) rememberTerms() { kernel.Keep(m.deps, ViewID, termsMemoryKey, m.terms.Encode()) }

func (m *Model) setTerms(next filter.Terms) tea.Cmd {
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
	if facet != filter.FacetNone {
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

// matchesTerms reports whether an issue passes every facet currently in
// force: AND across facets, OR within one facet's own values, the same
// semantics Terms.Clause() compiles to JQL for a search. Evaluated here
// instead of sent, because a board's own contents are already whole in
// memory — see applyFilterTerm.
func matchesTerms(iss *jira.Issue, terms filter.Terms) bool {
	if len(terms) == 0 {
		return true
	}
	byFacet := make(map[filter.Facet][]filter.Term, len(terms))
	for _, t := range terms {
		byFacet[t.Facet] = append(byFacet[t.Facet], t)
	}
	for facet, want := range byFacet {
		matched := false
		for _, t := range want {
			if matchesFacet(iss, facet, t.ID) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// matchesFacet reads the same field of an issue that Facet.field names on the
// JQL side, an empty id meaning the field itself is empty — "unassigned" is a
// value like any other, the way the picker already treats it.
func matchesFacet(iss *jira.Issue, facet filter.Facet, id string) bool {
	switch facet {
	case filter.FacetAssignee:
		if id == "" {
			return iss.Assignee == nil || iss.Assignee.AccountID == ""
		}
		return iss.Assignee != nil && iss.Assignee.AccountID == id
	case filter.FacetReporter:
		if id == "" {
			return iss.Reporter == nil || iss.Reporter.AccountID == ""
		}
		return iss.Reporter != nil && iss.Reporter.AccountID == id
	case filter.FacetStatus:
		return iss.Status.ID == id
	case filter.FacetType:
		return iss.Type.ID == id
	case filter.FacetPriority:
		if id == "" {
			return iss.Priority == nil
		}
		return iss.Priority != nil && iss.Priority.ID == id
	case filter.FacetLabel:
		return slices.Contains(iss.Labels, id)
	case filter.FacetNone:
	}
	return false
}
