package cloud

import "github.com/varijkapil13/saral/pkg/jira"

// What this adapter can be asked for.
//
// These belong in the built package and not in a test: a package that claims to
// adapt something and does not should fail `go build`, not a suite somebody has
// to run.
//
// The list is what the adapter satisfies today. A packet landing the rest of an
// area adds its role here in the same PR; jira.Client joins the list when the
// last of the port's methods arrives.
var (
	_ jira.Prober         = (*Client)(nil)
	_ jira.Identifier     = (*Client)(nil)
	_ jira.Searcher       = (*Client)(nil)
	_ jira.FieldCatalogue = (*Client)(nil)
	_ jira.SchemaReader   = (*Client)(nil)
	_ jira.IssueWriter    = (*Client)(nil)
	_ jira.Mover          = (*Client)(nil)
	_ jira.CommentReader  = (*Client)(nil)
	_ jira.Commenter      = (*Client)(nil)

	// The composite a session is built with.
	_ jira.SessionClient = (*Client)(nil)
)
