package timeline

import (
	"slices"

	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/pkg/jira"
)

// MatchesTerms reports whether an issue passes every facet in force: AND across
// facets, OR within one facet's own values. It is evaluated locally because a
// chart's own read is already whole in memory.
func MatchesTerms(iss *jira.Issue, terms appterm.Terms) bool {
	if len(terms) == 0 {
		return true
	}
	byFacet := make(map[appterm.Facet][]appterm.Term, len(terms))
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
func matchesFacet(iss *jira.Issue, facet appterm.Facet, id string) bool {
	switch facet {
	case appterm.FacetAssignee:
		if id == "" {
			return iss.Assignee == nil || iss.Assignee.AccountID == ""
		}
		return iss.Assignee != nil && iss.Assignee.AccountID == id
	case appterm.FacetReporter:
		if id == "" {
			return iss.Reporter == nil || iss.Reporter.AccountID == ""
		}
		return iss.Reporter != nil && iss.Reporter.AccountID == id
	case appterm.FacetStatus:
		return iss.Status.ID == id
	case appterm.FacetType:
		return iss.Type.ID == id
	case appterm.FacetPriority:
		if id == "" {
			return iss.Priority == nil
		}
		return iss.Priority != nil && iss.Priority.ID == id
	case appterm.FacetLabel:
		return slices.Contains(iss.Labels, id)
	case appterm.FacetNone:
	}
	return false
}

// CompareRanges orders ranges the way a reader scans a chart: earliest start
// first, and everything with no date after everything with one.
func CompareRanges(a, b Range) int {
	switch {
	case a.OK() != b.OK():
		if a.OK() {
			return -1
		}
		return 1
	case a.Start.Before(b.Start):
		return -1
	case b.Start.Before(a.Start):
		return 1
	default:
		return 0
	}
}
