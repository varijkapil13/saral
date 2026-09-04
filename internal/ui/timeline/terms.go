package timeline

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// OpenFilterMsg opens the picker over the chart on screen. It is exported so
// the palette reaches the same gesture f does.
type OpenFilterMsg struct{}

// ClearFilterMsg drops every term the filter picker put in force. It is
// exported so the palette reaches the gesture ctrl+g does rather than a second
// implementation of it.
type ClearFilterMsg struct{}

// openFilterPicker pushes the same picker the issue list uses, over this
// chart, armed with whatever is already in force.
func (m *Model) openFilterPicker() tea.Cmd {
	keys := defaultKeys()
	return kernel.Push(filter.ViewID, "Filter",
		filter.New(m.deps, filter.WithTerms(m.terms), filter.WithEditKey(keys.FilterBy.Help().Key)))
}

// applyFilterTerm puts a value in force or takes it off again, and rebuilds
// the chart's rows from what is already loaded rather than asking the site
// again — the same locally-applied narrowing board.terms and backlog.terms
// use, and for the same reason: this chart's own read is already whole in
// memory.
func (m *Model) applyFilterTerm(term filter.Term) tea.Cmd {
	return m.setTerms(m.terms.Toggle(term))
}

// clearFilter drops every term in force. Named separately from setTerms(nil)
// because it is the one ctrl+g reaches, and a no-op on an already-empty chart
// is not worth a rebuild.
func (m *Model) clearFilter() tea.Cmd {
	if len(m.terms) == 0 {
		return nil
	}
	return m.setTerms(nil)
}

func (m *Model) setTerms(next filter.Terms) tea.Cmd {
	m.terms, m.termsGen = next, m.termsGen+1
	m.take(m.res, m.issues, false)
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

// matchesTerms reports whether an issue passes every facet currently in
// force: AND across facets, OR within one facet's own values — the same
// semantics board.matchesTerms evaluates locally for the same reason: what is
// held is already whole in memory.
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
