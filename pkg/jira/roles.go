package jira

import (
	"context"
	"io"

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
	Attacher
	Releaser
	BoardReader
	SprintManager
	Relocator
	PlanReader
}

// AttachmentReader lists an issue's attachments and streams one out. It is
// separate from Attacher because the preview pane shows files it has no
// business replacing, and because attachments can be disabled site-wide: a
// token that may read one may still not add one.
type AttachmentReader interface {
	Attachments(ctx context.Context, key string) ([]Attachment, error)
	Download(ctx context.Context, id string, w io.Writer, opt DownloadOptions) error
}

// Attacher reads, adds and removes an issue's attachments.
type Attacher interface {
	AttachmentReader
	Upload(ctx context.Context, key string, files []FileRef) ([]Attachment, error)
	DeleteAttachment(ctx context.Context, id string) error
}

// VersionReader reads a project's versions and how many issues are still open
// on one. The count is its own call and its own method because it is the fact a
// release decision turns on, and asking for it on a list of forty versions is
// forty requests — so a list is read without it and a release screen asks.
type VersionReader interface {
	Versions(ctx context.Context, projectKey string) ([]Version, error)
	UnresolvedCount(ctx context.Context, versionID string) (int, error)
}

// Releaser reads, writes and releases versions. ReleaseVersion is the only way
// to ship one: the raw PUT will release a version over the top of its open
// issues without saying so, and ReleaseInput cannot be built without deciding
// what happens to them.
type Releaser interface {
	VersionReader
	SaveVersion(ctx context.Context, v VersionInput) (Version, error)
	ReleaseVersion(ctx context.Context, id string, in ReleaseInput) (Version, error)
}

// BoardReader reads the boards on a project, the configuration of one, and what
// one is showing. Nothing about a board may be assumed: its columns, whether it
// estimates, whether it ranks and which issues it holds are all answers, and
// none of them can be worked out from the others — the filter behind a board is
// JQL only the site can run, so BoardIssues and BoardBacklog are the only route
// to a board's contents.
type BoardReader interface {
	Boards(ctx context.Context, projectKey string) ([]Board, error)
	BoardConfig(ctx context.Context, boardID int64) (BoardConfig, error)
	BoardIssues(ctx context.Context, boardID int64, q BoardQuery) (Page[Issue], error)
	BoardBacklog(ctx context.Context, boardID int64, q BoardQuery) (Page[Issue], error)
}

// SprintReader lists a board's sprints. A backlog view holds only this: it
// draws which sprint an issue sits in without being able to end one.
type SprintReader interface {
	Sprints(ctx context.Context, boardID int64, states ...SprintState) (Page[Sprint], error)
	Sprint(ctx context.Context, id int64) (Sprint, error)
}

// SprintManager runs a board's sprints and moves issues in and out of them.
//
// The state machine is the port's, not the caller's: future to active to closed
// and no other move, which is why there is a method per transition instead of a
// state to set. Nothing here can null a field it was not given.
type SprintManager interface {
	SprintReader
	CreateSprint(ctx context.Context, in SprintInput) (Sprint, error)
	UpdateSprint(ctx context.Context, id int64, in SprintPatch) (Sprint, error)
	StartSprint(ctx context.Context, id int64) (Sprint, error)
	CompleteSprint(ctx context.Context, id int64) (Sprint, error)
	MoveToSprint(ctx context.Context, sprintID int64, keys []string) error
	MoveToBacklog(ctx context.Context, keys []string) error
}

// TaskWatcher reports on a long-running Jira task. It is its own role because
// the thing that polls a task is not always the thing that started one, and a
// TaskRef carries the endpoint to poll with it.
type TaskWatcher interface {
	Task(ctx context.Context, ref TaskRef) (TaskStatus, error)
}

// Relocator moves issues to another project and follows the task that does it.
// The two halves are one role: BulkMove returns nothing but a TaskRef, so a
// holder that cannot poll has submitted a change it cannot report on.
type Relocator interface {
	TaskWatcher
	BulkMove(ctx context.Context, in MoveRequest) (TaskRef, error)
}

// PlanReader lists Advanced Roadmaps plans. Holding it is not permission to use
// it: every Plans endpoint is gated on Administer Jira, and the per-plan rights
// the web UI grants do not reach the API — so an ordinary token gets a
// *CapabilityError naming CapPlans, and the plan view draws what config defines
// locally instead.
type PlanReader interface {
	Plans(ctx context.Context) ([]Plan, error)
}

// Every role is a subset of Client, and this is what says so.
//
// The assertions in pkg/jira/jiratest point the other way: they prove the fake
// carries at least what a role asks for. Neither of them notices a method
// dropped from Client, because the fake still has it and the role still wants
// it — so Client can shed a method and nothing stops compiling. Converting a
// nil Client to each role closes that direction: a signature that drifts, or a
// method deleted from the port, fails the build here.
var (
	_ Prober           = Client(nil)
	_ Identifier       = Client(nil)
	_ Searcher         = Client(nil)
	_ FieldCatalogue   = Client(nil)
	_ SchemaReader     = Client(nil)
	_ IssueWriter      = Client(nil)
	_ Mover            = Client(nil)
	_ CommentReader    = Client(nil)
	_ Commenter        = Client(nil)
	_ PeopleFinder     = Client(nil)
	_ FilterVocabulary = Client(nil)
	_ SessionClient    = Client(nil)

	_ AttachmentReader = Client(nil)
	_ Attacher         = Client(nil)
	_ VersionReader    = Client(nil)
	_ Releaser         = Client(nil)
	_ BoardReader      = Client(nil)
	_ SprintReader     = Client(nil)
	_ SprintManager    = Client(nil)
	_ TaskWatcher      = Client(nil)
	_ Relocator        = Client(nil)
	_ PlanReader       = Client(nil)
)
