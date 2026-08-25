package jira

import (
	"context"

	"github.com/varijkapil13/saral/pkg/adf"
)

// The roles below are what a holder asks of an adapter; Client is what an
// adapter grows into. A caller takes the role its own job is named after.
//
// Each role restates a signature Client already carries. Keep them identical: no
// type can hold two methods of one name, so a role that drifts from the port
// makes the assertions in pkg/jira/jiratest stop compiling, and nothing else
// catches it.

// Prober reports what a site, a token and a project can do. The kernel holds one
// to run the capability probe behind the first frame, and onboarding holds one
// to show a token's real permissions before anything is written to disk.
type Prober interface {
	Capabilities(ctx context.Context, projectKey string) (Capabilities, error)
}

// Identifier reports who a token belongs to. It is the first thing asked of a
// credential that has just been typed in — an answer is proof the three fields
// go together — and the account it names is the one a create screen defaults to.
type Identifier interface {
	Me(ctx context.Context) (User, error)
}

// Searcher runs JQL. Query.Fields must be an explicit narrow list.
type Searcher interface {
	Search(ctx context.Context, q Query) (Page[Issue], error)
}

// FieldCatalogue reads the site's field definitions. It is how a field written
// down by name becomes the ID to ask this site for, and it is the only way:
// custom field IDs differ per site, so a name is resolved at runtime or the
// field is not asked for at all.
type FieldCatalogue interface {
	Fields(ctx context.Context) ([]Field, error)
}

// SchemaReader reports what a project and issue type require. A create or edit
// screen is built from the answer rather than from a fixed list of fields,
// because which fields exist, which are required and which are on the screen at
// all are site configuration.
type SchemaReader interface {
	CreateMeta(ctx context.Context, projectKey, issueTypeID string) (Schema, error)
}

// IssueWriter creates issues and applies sparse patches to them. It cannot move
// one through the workflow: that is Mover, because a transition is a separate
// permission, has its own screen, and can be refused on an issue the same token
// may otherwise edit.
type IssueWriter interface {
	CreateIssue(ctx context.Context, in IssueInput) (Issue, error)
	UpdateIssue(ctx context.Context, key string, in IssuePatch) error
}

// Mover lists the workflow moves available on one issue right now and applies
// one of them. The list is per issue and per token and expires: it is asked for
// when the mover opens, never cached across issues.
type Mover interface {
	Transitions(ctx context.Context, key string) ([]Transition, error)
	Transition(ctx context.Context, key, transitionID string, in IssuePatch) error
}

// CommentReader reads an issue's thread. It is separate from Commenter because
// the detail pane shows a thread it has no business writing to, and asking only
// for the read makes that visible in the signature.
type CommentReader interface {
	Comments(ctx context.Context, key string) (Page[Comment], error)
}

// Commenter reads and writes an issue's thread.
type Commenter interface {
	CommentReader
	AddComment(ctx context.Context, key string, body adf.Doc) (Comment, error)
	EditComment(ctx context.Context, key, id string, body adf.Doc) (Comment, error)
	DeleteComment(ctx context.Context, key, id string) error
}

// PeopleFinder looks accounts up: by typing, and by the ids a saved filter
// holds. A filter that names a person stores the account id and nothing else,
// because a display name is neither unique nor stable, so both halves are one
// role — whatever can offer a picker must also be able to draw the choice back.
//
// Holding this role is not permission to use it: Browse users and groups is
// site-wide, and a token without it gets a *CapabilityError naming CapPeople
// rather than an empty list.
type PeopleFinder interface {
	FindPeople(ctx context.Context, q PeopleQuery) ([]User, error)
	People(ctx context.Context, accountIDs []string) ([]User, error)
}

// FilterVocabulary is the site's own words for the things a filter narrows by,
// resolved at runtime because every one of them is site configuration. Nothing
// here may be written down in the source: statuses are minted per project, a
// priority is a row an administrator can rename, and labels are whatever anyone
// has typed.
type FilterVocabulary interface {
	IssueTypeStatuses(ctx context.Context, projectKey string) ([]IssueTypeStatuses, error)
	Priorities(ctx context.Context) ([]Priority, error)
	Labels(ctx context.Context) (Page[string], error)
}

// SessionClient is every role a view in this build actually calls, and nothing
// else. It is what a session is built with.
//
// It exists so an adapter can be wired in once it serves the views that exist,
// rather than once it serves the whole port — which is the difference between a
// binary that talks to Jira and one that cannot. A batch that lands the adapter
// method its own view needs widens this in the same PR. Widening is additive:
// nothing that compiled stops compiling, and the diff says which capability the
// batch added.
type SessionClient interface {
	Prober
	Identifier
	Searcher
	FieldCatalogue
	SchemaReader
	IssueWriter
	Mover
	Commenter
	PeopleFinder
	FilterVocabulary
}
