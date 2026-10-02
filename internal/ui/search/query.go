package search

import (
	"github.com/varijkapil13/saral/pkg/jira"
)

// Scope is what a search covers.
type Scope uint8

// The two scopes.
const (
	ScopeSite Scope = iota
	ScopeProject
)

const orderByUpdated = " ORDER BY updated DESC"

func compose(tq jira.TextQuery, scope Scope, project string) (string, bool) {
	if tq.Empty() {
		return "", false
	}
	clause := tq.Clause(jira.TextAll)
	if scope == ScopeProject && project != "" {
		return "project = " + jira.QuoteJQL(project) + " AND " + clause + orderByUpdated, true
	}
	return clause + orderByUpdated, true
}

func quote(glyphsASCII bool, s string) string {
	if glyphsASCII {
		return `"` + s + `"`
	}
	return "“" + s + "”"
}
