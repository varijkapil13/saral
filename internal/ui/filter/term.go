package filter

import (
	"slices"
	"strings"
)

// Facet is one of the things a search can be narrowed by. The word on screen is
// this program's; the values behind it are always the site's, resolved at run
// time, and the JQL field a facet writes is the query language's own name for
// it rather than anything about this instance.
type Facet uint8

// The facets a picker offers.
const (
	FacetNone Facet = iota
	FacetAssignee
	FacetReporter
	FacetStatus
	FacetType
	FacetPriority
	FacetLabel
)

// Facets is the order the picker offers them in, and the order a composed
// clause writes them in. It is fixed rather than the order they were chosen in,
// so that two ways of arriving at one filter produce one query — which is also
// the cache key the rows are stored under.
//
// Version, component and sprint are absent and are not an oversight: none of
// the three can be read through jira.SessionClient, so offering one would mean
// a facet with nowhere to get its values from.
var Facets = []Facet{FacetAssignee, FacetReporter, FacetStatus, FacetType, FacetPriority, FacetLabel}

// Label is the word for the facet on screen.
func (f Facet) Label() string {
	switch f {
	case FacetAssignee:
		return "assignee"
	case FacetReporter:
		return "reporter"
	case FacetStatus:
		return "status"
	case FacetType:
		return "type"
	case FacetPriority:
		return "priority"
	case FacetLabel:
		return "label"
	case FacetNone:
		return ""
	}
	return ""
}

// plural is the word for a count of the facet's values. It is spelt out rather
// than made by adding an s, because three of the six do not take one.
func (f Facet) plural() string {
	switch f {
	case FacetAssignee, FacetReporter:
		return "accounts"
	case FacetStatus:
		return "statuses"
	case FacetType:
		return "types"
	case FacetPriority:
		return "priorities"
	case FacetLabel:
		return "labels"
	case FacetNone:
		return ""
	}
	return ""
}

// field is the JQL field the facet narrows.
func (f Facet) field() string {
	switch f {
	case FacetAssignee:
		return "assignee"
	case FacetReporter:
		return "reporter"
	case FacetStatus:
		return "status"
	case FacetType:
		return "issuetype"
	case FacetPriority:
		return "priority"
	case FacetLabel:
		return "labels"
	case FacetNone:
		return ""
	}
	return ""
}

// people reports whether the facet's values are accounts, which is the half of
// the vocabulary that needs a permission of its own and cannot be ranked by the
// site.
func (f Facet) people() bool { return f == FacetAssignee || f == FacetReporter }

// Term is one value a search is narrowed by, held by the id the site gave it.
//
// A display name is never the identity. It is localised, it is not unique on
// one site — a team-managed project mints statuses that reuse the stock names —
// and one account on the measured site answered to two different names on two
// endpoints within a minute.
type Term struct {
	Facet Facet
	// ID is the site's own id for the value: an account id, a status id, an
	// issue type id, a priority id, or the label itself. Empty means the field
	// is empty, which is how "unassigned" is a value like any other.
	ID string
	// Label is what to draw. Nothing is ever matched on it.
	Label string
}

// Same reports whether two terms name the same value. Only the facet and the id
// count: a term stored from a list row and one chosen in the picker carry the
// same id under two spellings of the same name.
func (t Term) Same(other Term) bool { return t.Facet == other.Facet && t.ID == other.ID }

// Terms is what is in force, grouped by facet in the order Facets declares and
// then by when each value was chosen. Two facets narrow together; two values of
// one facet widen it.
//
// The grouping is what keeps one filter one thing: the clause, the words on
// screen and the chips under the rows are all this order, so two routes to the
// same filter agree about what it is called and ask the site one question.
type Terms []Term

// Has reports whether a value is already in force.
func (t Terms) Has(want Term) bool {
	for _, got := range t {
		if got.Same(want) {
			return true
		}
	}
	return false
}

// Count is how many values of one facet are in force, which is what the picker
// puts beside the facet's name.
func (t Terms) Count(f Facet) int {
	n := 0
	for _, got := range t {
		if got.Facet == f {
			n++
		}
	}
	return n
}

// Toggle adds a value or takes it off again, and answers with a new slice so
// that nothing holding the old one sees it move. Choosing a value already in
// force is how it comes off — the same gesture the mouse makes on a cell it has
// already narrowed by.
func (t Terms) Toggle(term Term) Terms {
	if term.Facet == FacetNone {
		return t
	}
	out := make(Terms, 0, len(t)+1)
	removed := false
	for _, got := range t {
		if got.Same(term) {
			removed = true
			continue
		}
		out = append(out, got)
	}
	if removed {
		return out
	}
	out = append(out, term)
	slices.SortStableFunc(out, func(a, b Term) int { return facetOrder(a.Facet) - facetOrder(b.Facet) })
	return out
}

// Without drops every value of one facet, and answers with a new slice so that
// nothing holding the old one sees it move — the whole-clause counterpart to
// Toggle's one value at a time, which is what a click on a chip's × asks for.
func (t Terms) Without(f Facet) Terms {
	out := make(Terms, 0, len(t))
	for _, term := range t {
		if term.Facet != f {
			out = append(out, term)
		}
	}
	return out
}

func facetOrder(f Facet) int {
	if at := slices.Index(Facets, f); at >= 0 {
		return at
	}
	return len(Facets)
}

// Clause is the JQL predicate the terms compose to, and the empty string when
// there are none. Values of one facet are joined with IN, and two facets with
// AND: picking two people means either of them, and picking a person and a
// status means both.
func (t Terms) Clause() string {
	var b strings.Builder
	for _, f := range Facets {
		ids, empty := t.valuesOf(f)
		if len(ids) == 0 && !empty {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(" AND ")
		}
		writeClause(&b, f.field(), ids, empty)
	}
	return b.String()
}

// valuesOf splits one facet's values into the ids and whether the field being
// empty is one of them. IS EMPTY cannot go inside an IN list, so the two halves
// are built apart and joined with OR.
func (t Terms) valuesOf(f Facet) (ids []string, empty bool) {
	for _, got := range t {
		switch {
		case got.Facet != f:
		case got.ID == "":
			empty = true
		default:
			ids = append(ids, got.ID)
		}
	}
	return ids, empty
}

func writeClause(b *strings.Builder, field string, ids []string, empty bool) {
	if len(ids) == 0 {
		b.WriteString(field)
		b.WriteString(" IS EMPTY")
		return
	}
	if empty {
		b.WriteByte('(')
	}
	b.WriteString(field)
	if len(ids) == 1 {
		b.WriteString(" = ")
		b.WriteString(quote(ids[0]))
	} else {
		b.WriteString(" IN (")
		for i, id := range ids {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quote(id))
		}
		b.WriteByte(')')
	}
	if empty {
		b.WriteString(" OR ")
		b.WriteString(field)
		b.WriteString(" IS EMPTY)")
	}
}

// Words says what is in force in the site's own words, for the line that names
// the search on screen.
func (t Terms) Words() string {
	var b strings.Builder
	for _, f := range Facets {
		names := t.namesOf(f)
		if len(names) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(" and ")
		}
		b.WriteString(f.Label())
		b.WriteByte(' ')
		b.WriteString(strings.Join(names, " or "))
	}
	return b.String()
}

func (t Terms) namesOf(f Facet) []string {
	var out []string
	for _, got := range t {
		if got.Facet == f {
			out = append(out, got.Label)
		}
	}
	return out
}

// quote spells a value as a JQL string literal. The quotes are not decoration:
// three of the eleven account ids on the measured site carry a colon, a label
// is whatever anybody typed, and both go into a clause the site parses.
func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}
