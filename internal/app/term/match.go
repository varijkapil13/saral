package term

import (
	"slices"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Match reports whether an issue passes every facet in force: AND across
// facets, OR within one facet's own values, the same semantics Clause compiles
// to JQL for a search. A view whose issues are already whole in memory
// evaluates it rather than sending it.
func (t Terms) Match(iss *jira.Issue) bool {
	if len(t) == 0 {
		return true
	}
	byFacet := make(map[Facet][]Term, len(t))
	for _, one := range t {
		byFacet[one.Facet] = append(byFacet[one.Facet], one)
	}
	for facet, want := range byFacet {
		matched := false
		for _, one := range want {
			if facet.holds(iss, one.ID) {
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

// holds reads the same field of an issue that field names on the JQL side, an
// empty id meaning the field itself is empty — "unassigned" is a value like any
// other, the way the picker already treats it.
func (f Facet) holds(iss *jira.Issue, id string) bool {
	switch f {
	case FacetAssignee:
		if id == "" {
			return iss.Assignee == nil || iss.Assignee.AccountID == ""
		}
		return iss.Assignee != nil && iss.Assignee.AccountID == id
	case FacetReporter:
		if id == "" {
			return iss.Reporter == nil || iss.Reporter.AccountID == ""
		}
		return iss.Reporter != nil && iss.Reporter.AccountID == id
	case FacetStatus:
		return iss.Status.ID == id
	case FacetType:
		return iss.Type.ID == id
	case FacetPriority:
		if id == "" {
			return iss.Priority == nil
		}
		return iss.Priority != nil && iss.Priority.ID == id
	case FacetLabel:
		return slices.Contains(iss.Labels, id)
	case FacetNone:
	}
	return false
}
