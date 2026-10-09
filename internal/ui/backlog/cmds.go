package backlog

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	appboard "github.com/varijkapil13/saral/internal/app/board"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
	"github.com/varijkapil13/saral/pkg/jira"
)

// loadedMsg carries everything one read of a board answered with. No boards is
// an answer rather than a failure, and so is a site with no sprint field: both
// arrive here with the rest of it empty.
type loadedMsg struct {
	gen     int
	boards  []jira.Board
	boardAt int
	config  jira.BoardConfig
	sprints []jira.Sprint
	field   jira.FieldRef
	page    jira.Page[jira.Issue]
	missing []string
	// fields is the ids the page was read with, which a single issue read back
	// after a create asks for too.
	fields []string
	// noSprints is the site's own sentence for a board that has none — a Kanban
	// board answers the sprint read with a 400 — and "" for a board that has
	// sprints, or none open. It is not a failure: the backlog is still read.
	noSprints string
}

// pagedMsg carries the page after the one already in hand.
type pagedMsg struct {
	gen    int
	page   jira.Page[jira.Issue]
	stored error
}

// movedMsg is one chunk of a move the site accepted.
type movedMsg struct {
	gen   int
	at    int
	moved int
}

// moveFailedMsg is the chunk a move stopped on. The chunks before it moved, and
// at is which one refused, so the view can say how much of the selection is
// still where it was.
type moveFailedMsg struct {
	gen int
	at  int
	err error
}

// failedMsg is a read that brought nothing back. The error travels whole so
// that a refusal reaches the user in the words the site used.
type failedMsg struct {
	gen int
	err error
}

// revalidatedMsg is one row re-read after the issue pane reported a landed
// write elsewhere. It carries its own generation, separate from a move's or a
// walk's, because none of the three should cancel either of the others.
type revalidatedMsg struct {
	gen   int
	key   string
	issue jira.Issue
	err   error
}

// revalidate re-reads one issue by the fields it was last drawn with, after a
// write against it landed somewhere other than this backlog.
func revalidate(ctx context.Context, reader jira.IssueReader, key string, fields []string, gen int) tea.Cmd {
	return func() tea.Msg {
		iss, err := appboard.Reread(ctx, reader, key, fields)
		return revalidatedMsg{gen: gen, key: key, issue: iss, err: err}
	}
}

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	if cancel == nil {
		return cmd
	}
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}

// read is one whole load of a backlog; appboard.LoadBacklog says what it asks
// and why.
func read(ctx context.Context, s site, search *appquery.Search, project string, at int, wantID int64, roomy bool, gen int) tea.Cmd {
	return func() tea.Msg {
		got, err := appboard.LoadBacklog(ctx, s, search, appboard.BacklogQuery{
			Project: project, At: at, WantID: wantID, PageSize: pageSize, SprintLimit: sprintLimit,
			Projection: func(sprint jira.FieldRef, config jira.BoardConfig) appquery.Projection {
				return projectionOf(sprint, config, roomy)
			},
		})
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return loadedMsg{
			gen: gen, boards: got.Boards, boardAt: got.BoardAt, config: got.Config, sprints: got.Sprints,
			field: got.Field, page: got.Page, missing: got.Missing, fields: got.Fields, noSprints: got.NoSprints,
		}
	}
}

// projectionOf is what one read of a board's backlog asks for. The rank field is
// named by the board configuration, by id, so it is added to the projection
// rather than looked up by a name. Reporter and labels join it for the same
// reason appboard.Plan.Projection widens it: term.FacetReporter and FacetLabel
// match against this read's own issues, and ListProjection alone leaves both
// fields unread. The project is what an issue created from a section is made in,
// and a roomy card adds the fields it draws beyond a row.
func projectionOf(sprint jira.FieldRef, config jira.BoardConfig, roomy bool) appquery.Projection {
	projection := appquery.ListProjection().With(sprint.ID, "reporter", "labels", "project")
	if roomy {
		for _, id := range card.RoomyFields {
			if !slices.Contains(projection.IDs, id) {
				projection = projection.With(id)
			}
		}
	}
	if config.RankFieldID != "" {
		projection = projection.With(config.RankFieldID)
	}
	if est := appboard.EstimateOf(config); est.ID != "" {
		projection = projection.With(est.ID)
	}
	return projection
}

func nextPage(ctx context.Context, page jira.Page[jira.Issue], gen int, put func() error) tea.Cmd {
	return func() tea.Msg {
		next, stored, err := appboard.NextPage(ctx, page, put)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return pagedMsg{gen: gen, page: next, stored: stored}
	}
}

// moveInto moves one chunk. A sprint id of zero is the backlog, which is its own
// endpoint rather than a sprint with no number.
func moveInto(ctx context.Context, mgr jira.SprintManager, sprintID int64, keys []string, at, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := appboard.MoveInto(ctx, mgr, sprintID, keys); err != nil {
			return moveFailedMsg{gen: gen, at: at, err: err}
		}
		return movedMsg{gen: gen, at: at, moved: len(keys)}
	}
}

func stored(put func() error) tea.Cmd {
	if put == nil {
		return nil
	}
	return func() tea.Msg {
		if err := put(); err != nil {
			return kernel.Warn("this backlog could not be stored for next time: " + err.Error())()
		}
		return nil
	}
}
