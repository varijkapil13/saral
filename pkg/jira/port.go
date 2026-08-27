// Package jira is the port every part of Saral talks to instead of talking to
// Jira. It defines what Jira can do in domain terms; the adapters under
// pkg/jira/cloud, pkg/jira/jiratest and, later, pkg/jira/dc define how.
//
// The interface deliberately does not mirror the REST API. Where the wire shape
// is dangerous it is not exposed at all: a sprint is started with StartSprint
// rather than a PUT, because that PUT nulls every field the caller omits, and a
// version is released through ReleaseVersion, which cannot skip the unresolved
// issue count. Where the wire shape is site-specific — field IDs, statuses,
// board columns, permissions — the port returns what was resolved at runtime
// rather than letting a caller assume.
//
// Everything in this package is safe for concurrent use by definition: every
// method takes a context and must honour its cancellation, because views cancel
// their in-flight work when they close.
//
// Client is what an adapter grows into, not what a caller asks for. Callers take
// one of the role interfaces in roles.go — Prober, Searcher, Commenter and the
// rest — each named for a job, so that a holder's signature says which part of
// Jira it needs and an adapter is usable for that job before it is complete.
package jira

import (
	"context"
	"io"

	"github.com/varijkapil13/saral/pkg/adf"
)

// Client is the Jira port. Implementations are pkg/jira/cloud for a real site
// and pkg/jira/jiratest for tests.
//
// The signature is frozen: adding a method is a small reviewed change, but
// changing one breaks every packet at once.
type Client interface {
	// Capabilities reports what this site and token can do in one project.
	//
	// The project matters: boards belong to a project, and Jira scopes Move,
	// Delete and Create as project permissions, so the same token answers
	// differently in two projects on one site. An empty projectKey probes only
	// what is site-wide and leaves the per-project capabilities unavailable
	// with a reason saying so.
	Capabilities(ctx context.Context, projectKey string) (Capabilities, error)

	// Search runs a JQL query. Query.Fields must be an explicit narrow list.
	Search(ctx context.Context, q Query) (Page[Issue], error)
	// Issue fetches one issue by key.
	Issue(ctx context.Context, key string) (Issue, error)
	// CreateIssue creates an issue and returns it as stored.
	CreateIssue(ctx context.Context, in IssueInput) (Issue, error)
	// UpdateIssue applies a sparse patch. It cannot change status or project.
	UpdateIssue(ctx context.Context, key string, in IssuePatch) error
	// Transitions lists the workflow moves available on this issue right now.
	Transitions(ctx context.Context, key string) ([]Transition, error)
	// Transition moves an issue, optionally filling the transition screen.
	Transition(ctx context.Context, key, transitionID string, in IssuePatch) error

	// Comments lists an issue's comments, oldest first.
	Comments(ctx context.Context, key string) (Page[Comment], error)
	// AddComment adds a comment.
	AddComment(ctx context.Context, key string, body adf.Doc) (Comment, error)
	// EditComment replaces a comment's body.
	EditComment(ctx context.Context, key, id string, body adf.Doc) (Comment, error)
	// DeleteComment removes a comment.
	DeleteComment(ctx context.Context, key, id string) error

	// Attachments lists an issue's attachments.
	Attachments(ctx context.Context, key string) ([]Attachment, error)
	// Upload attaches files to an issue.
	Upload(ctx context.Context, key string, files []FileRef) ([]Attachment, error)
	// Download streams an attachment into w, starting at opt.From.
	Download(ctx context.Context, id string, w io.Writer, opt DownloadOptions) error
	// DeleteAttachment removes an attachment.
	DeleteAttachment(ctx context.Context, id string) error

	// Versions lists a project's versions.
	Versions(ctx context.Context, projectKey string) ([]Version, error)
	// SaveVersion creates a version, or updates it when VersionInput.ID is set.
	SaveVersion(ctx context.Context, v VersionInput) (Version, error)
	// UnresolvedCount reports how many issues on a version are still open.
	UnresolvedCount(ctx context.Context, versionID string) (int, error)
	// ReleaseVersion releases a version, handling its unresolved issues the way
	// ReleaseInput says to.
	ReleaseVersion(ctx context.Context, id string, in ReleaseInput) (Version, error)

	// Boards lists the boards that draw on a project.
	Boards(ctx context.Context, projectKey string) ([]Board, error)
	// BoardConfig reads a board's columns, estimation field and rank field.
	BoardConfig(ctx context.Context, boardID int64) (BoardConfig, error)
	// Sprints lists a board's sprints, narrowed to the states named. Passing no
	// state lists them all, which on a board with years of history is a walk
	// nothing on a first-paint path should be doing.
	Sprints(ctx context.Context, boardID int64, states ...SprintState) (Page[Sprint], error)
	// Sprint fetches one sprint by id, including its dates.
	//
	// It exists because an issue's sprint value carries an id and a name and no
	// dates: a timeline drawing a bar from sprint dates starts from that id and
	// has no board to reach it through, and walking every board of the project
	// to find one sprint is the alternative.
	Sprint(ctx context.Context, id int64) (Sprint, error)
	// CreateSprint creates a future sprint.
	CreateSprint(ctx context.Context, in SprintInput) (Sprint, error)
	// UpdateSprint changes only the fields the patch names.
	UpdateSprint(ctx context.Context, id int64, in SprintPatch) (Sprint, error)
	// StartSprint moves a future sprint to active. Both dates must be set.
	StartSprint(ctx context.Context, id int64) (Sprint, error)
	// CompleteSprint closes an active sprint.
	CompleteSprint(ctx context.Context, id int64) (Sprint, error)
	// MoveToSprint moves issues into a sprint.
	MoveToSprint(ctx context.Context, sprintID int64, keys []string) error
	// MoveToBacklog moves issues out of whatever sprint they are in.
	MoveToBacklog(ctx context.Context, keys []string) error

	// Fields returns the site's field catalogue, which is how a custom field is
	// resolved from a name to an ID.
	Fields(ctx context.Context) ([]Field, error)
	// CreateMeta reports what a project and issue type require to create an
	// issue.
	CreateMeta(ctx context.Context, projectKey, issueTypeID string) (Schema, error)
	// BulkMove submits an asynchronous cross-project move and returns the task
	// to poll. It is the only way to change an issue's project.
	BulkMove(ctx context.Context, in MoveRequest) (TaskRef, error)
	// Task reports on a long-running task.
	Task(ctx context.Context, ref TaskRef) (TaskStatus, error)
	// Plans lists Advanced Roadmaps plans, which need Administer Jira.
	Plans(ctx context.Context) ([]Plan, error)
	// Me returns the authenticated account, including its timezone.
	Me(ctx context.Context) (User, error)

	// FindPeople searches the site's accounts. The site decides what matches and
	// in what order; see PeopleQuery, whose documentation is the contract.
	FindPeople(ctx context.Context, q PeopleQuery) ([]User, error)
	// People resolves account ids to accounts, which is how an id written into a
	// saved filter becomes a name to draw. An id this site does not know is
	// absent from the answer rather than a blank row in it, so the result is
	// keyed by AccountID and never by position.
	People(ctx context.Context, accountIDs []string) ([]User, error)
	// IssueTypeStatuses lists each of a project's issue types with the statuses
	// its workflow can put an issue in.
	IssueTypeStatuses(ctx context.Context, projectKey string) ([]IssueTypeStatuses, error)
	// Priorities lists the site's priorities, in the site's own order — which is
	// the order they rank in, and is not alphabetical.
	Priorities(ctx context.Context) ([]Priority, error)
	// Labels lists every label in use on the site. It pages, because a busy site
	// has thousands, and it cannot be narrowed: the endpoint takes no query and
	// ignores one sent anyway, so a caller filters what it walked.
	Labels(ctx context.Context) (Page[string], error)
}
