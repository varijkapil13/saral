package board

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/pkg/jira"
)

// MatchesTerms reports whether an issue passes every facet in force: AND across
// facets, OR within one facet's own values, the same semantics Terms.Clause()
// compiles to JQL for a search. A board or backlog evaluates it rather than
// sending it, because what a board holds is the board's to define and its
// contents are already whole in memory: a person, a status, a type, a priority
// or a label is this program's own idea of a narrower board, not one the site's
// board draws too.
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

// MineToggled is the terms with the assignee facet set to exactly this account,
// labelled label, or with it taken off when that is already all it holds.
func MineToggled(terms appterm.Terms, me jira.User, label string) appterm.Terms {
	mine := appterm.Term{Facet: appterm.FacetAssignee, ID: me.AccountID, Label: label}
	if terms.Has(mine) && terms.Count(appterm.FacetAssignee) == 1 {
		return terms.Without(appterm.FacetAssignee)
	}
	return terms.Without(appterm.FacetAssignee).Toggle(mine)
}

// MatchesNeedle reports whether an issue's key or summary holds needle, in any
// case.
func MatchesNeedle(iss *jira.Issue, needle string) bool {
	return iss != nil && (ContainsFold(iss.Key, needle) || ContainsFold(iss.Summary, needle))
}

// ContainsFold is a case-insensitive strings.Contains that allocates nothing,
// which a search run on every keystroke over every card has to be.
func ContainsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return true
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return false
}

// SprintsOn is the ids of the sprints an issue's sprint field names. The value
// arrives in one of two shapes and the field's own type is neither: a read that
// sent no schema decodes the array as options, and a read that did finds the
// field declared as an array of json, which nothing has a slot for, so the
// bytes are kept as text.
func SprintsOn(iss *jira.Issue, field jira.FieldRef) []int64 {
	if field.ID == "" {
		return nil
	}
	if options, ok := iss.Fields.Options(field); ok {
		out := make([]int64, 0, len(options))
		for _, option := range options {
			if id, err := strconv.ParseInt(strings.TrimSpace(option.ID), 10, 64); err == nil {
				out = append(out, id)
			}
		}
		return out
	}
	text, ok := iss.Fields.Text(field)
	if !ok {
		return nil
	}
	return sprintIDsIn(text)
}

// sprintIDsIn reads the ids out of the json shape of a sprint value.
func sprintIDsIn(text string) []int64 {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") {
		return nil
	}
	var wire []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wire); err != nil {
		return nil
	}
	out := make([]int64, 0, len(wire))
	for _, one := range wire {
		if one.ID != 0 {
			out = append(out, one.ID)
		}
	}
	return out
}

// EstimateOf is the field a board estimates in, or none.
func EstimateOf(cfg jira.BoardConfig) jira.FieldRef {
	if !cfg.Estimates() {
		return jira.FieldRef{}
	}
	return cfg.Estimation.Field
}

// DoneStatuses is the statuses of a board's last column that maps any, which is
// what the board calls done.
func DoneStatuses(cfg jira.BoardConfig) map[string]bool {
	for c := len(cfg.Columns) - 1; c >= 0; c-- {
		ids := cfg.Columns[c].StatusIDs
		if len(ids) == 0 {
			continue
		}
		out := make(map[string]bool, len(ids))
		for _, id := range ids {
			out[strings.TrimSpace(id)] = true
		}
		return out
	}
	return nil
}
