package search

import (
	"regexp"
	"strings"
)

// SortField is one field JQL can order by that this client can name without
// guessing: a keyword of the query language itself, never a customfield id or
// anything read off a site. JQLName is the language's own spelling, which is
// not always the label this program draws (issuetype, not type).
type SortField struct {
	ID      string
	Label   string
	JQLName string
	// Desc is the direction a field opens in the first time it is chosen: the
	// three date fields read newest first, everything else alphabetically.
	Desc bool
}

// SortFields is the picker's whole offer, in the order docs/FILTERS.md lists
// them.
var SortFields = []SortField{
	{ID: "key", Label: "key", JQLName: "key"},
	{ID: "summary", Label: "summary", JQLName: "summary"},
	{ID: "status", Label: "status", JQLName: "status"},
	{ID: "type", Label: "type", JQLName: "issuetype"},
	{ID: "priority", Label: "priority", JQLName: "priority"},
	{ID: "assignee", Label: "assignee", JQLName: "assignee"},
	{ID: "created", Label: "created", JQLName: "created", Desc: true},
	{ID: "updated", Label: "updated", JQLName: "updated", Desc: true},
	{ID: "due", Label: "due", JQLName: "duedate", Desc: true},
}

// SortFieldByID finds a field of SortFields by its id.
func SortFieldByID(id string) (SortField, bool) {
	for _, f := range SortFields {
		if f.ID == id {
			return f, true
		}
	}
	return SortField{}, false
}

// SortFieldIndex is where id sits in SortFields, or 0 where it is not there.
func SortFieldIndex(id string) int {
	for i, f := range SortFields {
		if f.ID == id {
			return i
		}
	}
	return 0
}

// SortChoice is the order a search is asked to run in, over and above whatever
// it would otherwise carry. A zero value is no choice at all, which leaves a
// search reading in the order it always named for itself.
type SortChoice struct {
	Field string
	Desc  bool
}

// Chosen reports whether any order was chosen.
func (c SortChoice) Chosen() bool { return c.Field != "" }

// Clause is the ORDER BY this choice asks for, and false where it names a
// field a hand-edited file or an older build put there and this one does not.
func (c SortChoice) Clause() (string, bool) {
	f, ok := SortFieldByID(c.Field)
	if !ok {
		return "", false
	}
	dir := "ASC"
	if c.Desc {
		dir = "DESC"
	}
	return "ORDER BY " + f.JQLName + " " + dir, true
}

// Next is the choice after picking fieldID. Choosing the field already in
// force toggles its direction, which is how one gesture reaches both halves of
// docs/FILTERS.md's "each toggling ascending and descending"; any other field
// opens in its own direction.
func (c SortChoice) Next(fieldID string) SortChoice {
	f, ok := SortFieldByID(fieldID)
	if !ok {
		return c
	}
	if c.Field == f.ID {
		return SortChoice{Field: f.ID, Desc: !c.Desc}
	}
	return SortChoice{Field: f.ID, Desc: f.Desc}
}

// listOrderBy finds the ORDER BY a composed query ends in. JQL puts exactly
// one at the end of a query, never inside a clause's own text, so matching to
// the end of the string is enough to take the whole thing off.
var listOrderBy = regexp.MustCompile(`(?i)\s+order\s+by\s+.*$`)

// ApplySort replaces whatever order a query already carries with the one
// chosen, leaving everything before it untouched. A jql with no choice made
// for it is returned as it arrived, which is what leaves a search's own ORDER
// BY the answer until something asks for another one.
func ApplySort(jql string, c SortChoice) string {
	clause, ok := c.Clause()
	if !ok {
		return jql
	}
	base := strings.TrimSpace(listOrderBy.ReplaceAllString(jql, ""))
	if base == "" {
		return clause
	}
	return base + " " + clause
}
