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
	m.terms = m.terms.Toggle(term)
	m.place()
	if m.more && len(m.terms) > 0 {
		return kernel.Warn("this board has more cards than are loaded, so the filter only sees the ones on screen")
	}
	return nil
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
