package search

import (
	"strconv"
	"strings"
)

// CannedSearch is the JQL half of a search the list offers by name: the
// predicate, which is empty for every issue there is, and the order to read the
// answer in. The two are kept apart because a search with no predicate has no
// AND to hang an ORDER BY off, and "every issue in this project" is exactly
// that search.
type CannedSearch struct {
	Where string
	Order string
}

// The searches every session offers. Nothing here names a project, a status or
// a field: the project is whatever the session is scoped to and goes in at run
// time.
var (
	EveryIssue       = CannedSearch{Order: "ORDER BY updated DESC"}
	MyIssues         = CannedSearch{Where: "assignee = currentUser()", Order: "ORDER BY updated DESC"}
	ReportedIssues   = CannedSearch{Where: "reporter = currentUser()", Order: "ORDER BY created DESC"}
	UnassignedIssues = CannedSearch{Where: "assignee IS EMPTY", Order: "ORDER BY created DESC"}
)

// At composes the search for the project the session is on.
func (s CannedSearch) At(project string) string {
	return strings.TrimSpace(Scoped(project, s.Where) + " " + s.Order)
}

// Scoped narrows a clause to the session's project when there is one. An empty
// clause is every issue in that project, so there is nothing to put an AND
// between. The key is whatever the session was opened against; nothing about it
// is written down.
func Scoped(project, clause string) string {
	p, c := strings.TrimSpace(project), strings.TrimSpace(clause)
	switch {
	case p == "":
		return c
	case c == "":
		return "project = " + listQuote(p)
	}
	return "project = " + listQuote(p) + " AND " + c
}

func listQuote(s string) string {
	return strconv.Quote(strings.ReplaceAll(s, `"`, ""))
}

// ProbeJQL asks whether anything anywhere on this site is assigned to the
// account the credential belongs to. It is the unscoped form of the search a
// session opens on, so currentUser() is written down once rather than twice.
func ProbeJQL() string { return MyIssues.At("") }

// SlotFromKey reads a stroke as a saved-query slot, false for anything that is
// not one of 1..MaxSavedSlot.
func SlotFromKey(stroke string) (int, bool) {
	slot, err := strconv.Atoi(stroke)
	if err != nil || slot < 1 || slot > MaxSavedSlot {
		return 0, false
	}
	return slot, true
}
