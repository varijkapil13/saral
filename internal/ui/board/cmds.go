package board

import (
	"context"

	tea "charm.land/bubbletea/v2"

	appboard "github.com/varijkapil13/saral/internal/app/board"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pageSize is how many cards one request asks for. A column is virtualized, so
// the number that matters is that it is several screens' worth.
const pageSize = 100

const sprintLimit = 50

type step = appboard.Step

const (
	stepIdle    = appboard.StepIdle
	stepBoards  = appboard.StepBoards
	stepConfig  = appboard.StepConfig
	stepSprints = appboard.StepSprints
	stepIssues  = appboard.StepIssues
)

// boardsMsg carries the boards that draw on this project. An empty list is an
// answer: a project with no board is ordinary.
type boardsMsg struct {
	gen    int
	boards []jira.Board
}

// configMsg carries one board's real shape: its columns, whether it estimates
// and whether it ranks.
type configMsg struct {
	gen int
	cfg jira.BoardConfig
}

// issuesMsg carries the cards, and what this site had no field for.
type issuesMsg struct {
	gen     int
	page    jira.Page[jira.Issue]
	missing []string
	// first is the page that answers the read; every page after it is appended
	// to what the first drew, so a board longer than one page fills in behind
	// an instant first paint rather than stopping at it.
	first     bool
	fields    []string
	sprints   []jira.Sprint
	sprint    jira.Sprint
	noSprints bool
	stored    error
}

// moreFailedMsg is a page past the first that did not arrive. The board keeps
// what it has and says so: the cards on screen are real, and the count keeps
// its plus so nothing claims they are all of them.
type moreFailedMsg struct {
	gen int
	err error
}

// movesMsg carries the transitions available on one issue at the moment it was
// picked up, together with the column it is aimed at. The list is per issue and
// per token and expires, so it is read when the drop happens and never kept.
type movesMsg struct {
	gen    int
	key    string
	column int
	moves  []jira.Transition
}

// movedMsg is a transition that landed.
type movedMsg struct {
	gen    int
	key    string
	to     string
	from   string
	status jira.Status
}

type moveFailedMsg struct {
	gen int
	key string
	err error
}

type rereadMsg struct {
	gen   int
	key   string
	issue jira.Issue
	err   error
}

// revalidatedMsg is one card re-read after the issue pane reported a landed
// write elsewhere. It carries its own generation, separate from a move's,
// because the two happen for unrelated reasons and neither should cancel the
// other.
type revalidatedMsg struct {
	gen   int
	key   string
	issue jira.Issue
	err   error
}

// failedMsg is any read or write that brought nothing back. The error travels
// whole so that a refusal reaches the user in the site's own words, and the step
// travels with it so that the pane can say which question went unanswered.
type failedMsg struct {
	gen  int
	step step
	err  error
}

func boards(ctx context.Context, reader jira.BoardReader, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		found, err := appboard.ListBoards(ctx, reader, project)
		if err != nil {
			return failedMsg{gen: gen, step: stepBoards, err: err}
		}
		return boardsMsg{gen: gen, boards: found}
	}
}

func config(ctx context.Context, reader jira.BoardReader, boardID int64, gen int) tea.Cmd {
	return func() tea.Msg {
		cfg, err := appboard.Config(ctx, reader, boardID)
		if err != nil {
			return failedMsg{gen: gen, step: stepConfig, err: err}
		}
		return configMsg{gen: gen, cfg: cfg}
	}
}

type cardsQuery struct {
	plan         appboard.Plan
	quickFilters []string
	probe        bool
	sprints      []jira.Sprint
	noSprints    bool
	sprint       int64
	look         card.Look
}

// cards fills the board with the narrow field set a card draws plus the
// board's own estimation field; appboard.ReadCards says what it asks.
func cards(ctx context.Context, reader appboard.Site, search *appquery.Search, q cardsQuery, gen int) tea.Cmd {
	return func() tea.Msg {
		got, failed, err := appboard.ReadCards(ctx, reader, search, appboard.CardsQuery{
			Plan: q.plan, Projection: projectionFor(q.plan, q.look), QuickFilters: q.quickFilters,
			PageSize: pageSize, SprintLimit: sprintLimit,
			Probe: q.probe, Sprints: q.sprints, NoSprints: q.noSprints, Sprint: q.sprint,
		})
		if err != nil {
			return failedMsg{gen: gen, step: failed, err: err}
		}
		return issuesMsg{
			gen: gen, page: got.Page, missing: got.Missing, first: true, fields: got.Fields,
			sprints: got.Sprints, sprint: got.Sprint, noSprints: got.NoSprints,
		}
	}
}

// moreCards writes the page in hand before reading the next, so a walk's pages
// reach the cache in order.
func moreCards(ctx context.Context, page jira.Page[jira.Issue], gen int, put func() error) tea.Cmd {
	return func() tea.Msg {
		next, stored, err := appboard.NextPage(ctx, page, put)
		if err != nil {
			return moreFailedMsg{gen: gen, err: err}
		}
		return issuesMsg{gen: gen, page: next, stored: stored}
	}
}

// moves reads what the held issue can do right now, so that the column it is
// dropped on is reached by a transition this token may actually make.
func moves(ctx context.Context, mover jira.Mover, key string, column, gen int) tea.Cmd {
	return func() tea.Msg {
		found, err := appboard.MovesOf(ctx, mover, key)
		if err != nil {
			return moveFailedMsg{gen: gen, key: key, err: err}
		}
		return movesMsg{gen: gen, key: key, column: column, moves: found}
	}
}

func apply(ctx context.Context, mover jira.Mover, key string, tr jira.Transition, to, from string, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := appboard.Apply(ctx, mover, key, tr.ID); err != nil {
			return moveFailedMsg{gen: gen, key: key, err: err}
		}
		return movedMsg{gen: gen, key: key, to: to, from: from, status: tr.To}
	}
}

func reread(ctx context.Context, reader jira.IssueReader, key string, fields []string, gen int) tea.Cmd {
	return func() tea.Msg {
		iss, err := appboard.Reread(ctx, reader, key, fields)
		return rereadMsg{gen: gen, key: key, issue: iss, err: err}
	}
}

// revalidate re-reads one card by the fields it was last drawn with, after a
// write against it landed somewhere other than this board.
func revalidate(ctx context.Context, reader jira.IssueReader, key string, fields []string, gen int) tea.Cmd {
	return func() tea.Msg {
		iss, err := appboard.Reread(ctx, reader, key, fields)
		return revalidatedMsg{gen: gen, key: key, issue: iss, err: err}
	}
}

func stored(put func() error) tea.Cmd {
	if put == nil {
		return nil
	}
	return func() tea.Msg {
		if err := put(); err != nil {
			return kernel.Warn(storeFailed + err.Error())()
		}
		return nil
	}
}

const storeFailed = "this board could not be stored for next time: "

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}
