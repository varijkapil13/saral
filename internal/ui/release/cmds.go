package release

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// versionsMsg carries a project's versions as one read answered them.
//
// Every version in it has a nil Unresolved, which says nobody has counted what
// is still open on it rather than that nothing is. The count is a request per
// version, so a list of forty is not forty requests: it is read when a release
// screen needs one.
type versionsMsg struct {
	gen      int
	versions []jira.Version
}

// savedMsg is the version the site stored, which is not always the version that
// was sent: a name is trimmed and a date can come back adjusted.
type savedMsg struct {
	gen     int
	version jira.Version
	created bool
}

// countedMsg is how many issues are still open on one version. It travels with
// the id because two counts can be in flight for two rows and only the id says
// which row an answer belongs to.
type countedMsg struct {
	gen  int
	id   string
	open int
}

// releasedMsg is a version the site has released, carrying the decision it was
// released under and the count that decision was made against.
//
// Both halves are needed to read the answer. The port returns the version with
// Unresolved holding what the release left open on it, and a move or a strip
// that did not reach every issue comes back released with a non-zero count —
// which is a partly finished sweep and has to be said out loud rather than
// reported as a release.
type releasedMsg struct {
	gen     int
	version jira.Version
	policy  jira.UnresolvedPolicy
	asked   int
}

// failedMsg is a call that brought nothing back. what names the thing that was
// being asked for, in the words the pane uses, so a refusal says which of the
// four reads or writes it refused.
type failedMsg struct {
	gen  int
	what string
	err  error
}

// The four things this package asks the site for, named for the sentence the
// pane builds out of them.
const (
	whatVersions = "The versions could not be read."
	whatSave     = "The version was not saved."
	whatCount    = "What is open on this version could not be counted."
	whatRelease  = "The version was not released."
)

// loadVersions reads a project's versions. It takes the reader rather than the
// session, because listing versions is all it does.
func loadVersions(ctx context.Context, reader jira.VersionReader, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		versions, err := reader.Versions(ctx, project)
		if err != nil {
			return failedMsg{gen: gen, what: whatVersions, err: err}
		}
		return versionsMsg{gen: gen, versions: versions}
	}
}

// saveVersion creates a version or updates one. created says which, because the
// answer looks the same either way and the sentence the status line says does
// not.
func saveVersion(ctx context.Context, writer jira.Releaser, in jira.VersionInput, created bool, gen int) tea.Cmd {
	return func() tea.Msg {
		version, err := writer.SaveVersion(ctx, in)
		if err != nil {
			return failedMsg{gen: gen, what: whatSave, err: err}
		}
		return savedMsg{gen: gen, version: version, created: created}
	}
}

// countOpen reads how many issues are still open on one version. It is its own
// call because it is its own request: no version read reports the number, and
// the release decision is the only thing that needs it.
func countOpen(ctx context.Context, reader jira.VersionReader, id string, gen int) tea.Cmd {
	return func() tea.Msg {
		open, err := reader.UnresolvedCount(ctx, id)
		if err != nil {
			return failedMsg{gen: gen, what: whatCount, err: err}
		}
		return countedMsg{gen: gen, id: id, open: open}
	}
}

// releaseOne ships a version. asked is the count the reader decided against, so
// that an answer leaving issues behind can be reported against the number that
// was on the confirm rather than against nothing.
func releaseOne(ctx context.Context, writer jira.Releaser, id string, in jira.ReleaseInput, asked, gen int) tea.Cmd {
	return func() tea.Msg {
		version, err := writer.ReleaseVersion(ctx, id, in)
		if err != nil {
			return failedMsg{gen: gen, what: whatRelease, err: err}
		}
		return releasedMsg{gen: gen, version: version, policy: in.Unresolved, asked: asked}
	}
}

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}

// updateOf is the input that changes one thing about a version and nothing
// else.
//
// Every field has to be sent. The endpoint empties a name, a description or a
// date it is not given, so an input built to archive a version and nothing more
// still carries the four values the version already had — otherwise archiving
// 2.0 also forgets when it was meant to ship.
func updateOf(v jira.Version) jira.VersionInput {
	return jira.VersionInput{
		ID:          v.ID,
		Name:        v.Name,
		Description: v.Description,
		StartDate:   v.StartDate,
		ReleaseDate: v.ReleaseDate,
	}
}
