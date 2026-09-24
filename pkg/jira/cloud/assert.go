package cloud

import "github.com/varijkapil13/saral/pkg/jira"

// What this adapter can be asked for.
//
// These belong in the built package and not in a test: a package that claims to
// adapt something and does not should fail `go build`, not a suite somebody has
// to run.
//
// The adapter implements the whole port, and says so first; the roles below it
// are what the rest of the tree holds it as.
var (
	_ jira.Client = (*Client)(nil)

	_ jira.Prober         = (*Client)(nil)
	_ jira.Identifier     = (*Client)(nil)
	_ jira.Searcher       = (*Client)(nil)
	_ jira.FieldCatalogue = (*Client)(nil)
	_ jira.SchemaReader   = (*Client)(nil)
	_ jira.IssueWriter    = (*Client)(nil)
	_ jira.Mover          = (*Client)(nil)
	_ jira.CommentReader  = (*Client)(nil)
	_ jira.Commenter      = (*Client)(nil)
	_ jira.PeopleFinder   = (*Client)(nil)

	_ jira.FilterVocabulary = (*Client)(nil)

	// The composite a session is built with.
	_ jira.SessionClient = (*Client)(nil)
)
