package board

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pageSize is how many cards one request asks for. A column is virtualized, so
// the number that matters is that it is several screens' worth.
const pageSize = 100

const sprintLimit = 50

type site interface {
	jira.BoardReader
	jira.SprintReader
	jira.SprintIssueReader
}

// step is which of the three reads the board is waiting on. A board is three
// questions — which boards, what this one looks like, what is on it — and an
// empty pane that cannot say which of them is outstanding is a pane that looks
// like a hang.
type step uint8

const (
	stepIdle step = iota
	stepBoards
	stepConfig
	stepSprints
	stepIssues
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
		found, err := reader.Boards(ctx, project)
		if err != nil {
			return failedMsg{gen: gen, step: stepBoards, err: err}
		}
		return boardsMsg{gen: gen, boards: found}
	}
}

func config(ctx context.Context, reader jira.BoardReader, boardID int64, gen int) tea.Cmd {
	return func() tea.Msg {
		cfg, err := reader.BoardConfig(ctx, boardID)
		if err != nil {
			return failedMsg{gen: gen, step: stepConfig, err: err}
		}
		return configMsg{gen: gen, cfg: cfg}
	}
}

type cardsQuery struct {
	plan         plan
	quickFilters []string
	probe        bool
	sprints      []jira.Sprint
	noSprints    bool
	sprint       int64
}

// cards fills the board, through the read that applies the board's own saved
// filter and column mapping at the site. Nothing here composes a query: the
// filter behind a board is JQL only the site can run, and a board rebuilt out of
// its statuses is a different board.
//
// A board that runs sprints shows its active sprint and nothing else, so its
// cards are that sprint's; whether it runs them is the sprint read's answer and
// never the board's type, which docs/API-NOTES.md says nothing may branch on.
//
// It asks for the narrow field set a card draws plus the board's own estimation
// field, never for a wildcard, and it carries the board's sub-query and
// whichever of the board's own quick filters are toggled on, which are the two
// parts of a board the endpoint leaves to the caller.
func cards(ctx context.Context, reader site, search *app.Search, q cardsQuery, gen int) tea.Cmd {
	return func() tea.Msg {
		wanted, err := search.Resolve(ctx, q.plan.projection())
		if err != nil {
			return failedMsg{gen: gen, step: stepIssues, err: err}
		}
		out := issuesMsg{
			gen: gen, missing: wanted.Missing, first: true, fields: wanted.IDs,
			sprints: q.sprints, noSprints: q.noSprints,
		}
		if q.probe {
			out.sprints, out.noSprints, err = activeSprints(ctx, reader, q.plan.boardID)
			if err != nil {
				return failedMsg{gen: gen, step: stepSprints, err: err}
			}
		}
		query := jira.BoardQuery{
			Fields:       wanted.IDs,
			SubQuery:     q.plan.subQuery,
			QuickFilters: q.quickFilters,
			MaxResults:   pageSize,
		}
		var page jira.Page[jira.Issue]
		switch {
		case out.noSprints:
			page, err = reader.BoardIssues(ctx, q.plan.boardID, query)
		case len(out.sprints) == 0:
			return out
		default:
			out.sprint = pickSprint(out.sprints, q.sprint)
			page, err = reader.SprintIssues(ctx, q.plan.boardID, out.sprint.ID, query)
		}
		if err != nil {
			return failedMsg{gen: gen, step: stepIssues, err: err}
		}
		out.page = page
		return out
	}
}

// activeSprints reads a 400 as a board that runs no sprints, the way
// backlog.openSprints does.
func activeSprints(ctx context.Context, r jira.SprintReader, boardID int64) (sprints []jira.Sprint, noSprints bool, err error) {
	page, err := r.Sprints(ctx, boardID, jira.SprintActive)
	var invalid *jira.ValidationError
	if errors.As(err, &invalid) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	all, err := jira.Collect(ctx, page, sprintLimit)
	if err != nil {
		return nil, false, err
	}
	out := make([]jira.Sprint, 0, len(all))
	for _, sp := range all {
		if sp.State == jira.SprintActive {
			out = append(out, sp)
		}
	}
	return out, false, nil
}

func pickSprint(sprints []jira.Sprint, want int64) jira.Sprint {
	for _, sp := range sprints {
		if sp.ID == want {
			return sp
		}
	}
	return sprints[0]
}

// moreCards writes the page in hand before reading the next, so a walk's pages
// reach the cache in order.
func moreCards(ctx context.Context, page jira.Page[jira.Issue], gen int, put func() error) tea.Cmd {
	return func() tea.Msg {
		var stored error
		if put != nil {
			stored = put()
		}
		next, err := page.Next(ctx)
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
		found, err := mover.Transitions(ctx, key)
		if err != nil {
			return moveFailedMsg{gen: gen, key: key, err: err}
		}
		return movesMsg{gen: gen, key: key, column: column, moves: found}
	}
}

// apply moves an issue by transition id. A status is not writable on Jira, so a
// column change is a workflow move and never a field set — and the transition is
// named by the id the site gave it, never by the status it lands on.
func apply(ctx context.Context, mover jira.Mover, key string, tr jira.Transition, to, from string, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := mover.Transition(ctx, key, tr.ID, jira.IssuePatch{}); err != nil {
			return moveFailedMsg{gen: gen, key: key, err: err}
		}
		return movedMsg{gen: gen, key: key, to: to, from: from, status: tr.To}
	}
}

// reread does not search: the index trails a write.
func reread(ctx context.Context, reader jira.IssueReader, key string, fields []string, gen int) tea.Cmd {
	return func() tea.Msg {
		iss, err := reader.IssueFields(ctx, key, fields)
		return rereadMsg{gen: gen, key: key, issue: iss, err: err}
	}
}

// revalidate re-reads one card by the fields it was last drawn with, after a
// write against it landed somewhere other than this board.
func revalidate(ctx context.Context, reader jira.IssueReader, key string, fields []string, gen int) tea.Cmd {
	return func() tea.Msg {
		iss, err := reader.IssueFields(ctx, key, fields)
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

// needsScreen reports whether a transition insists on a field this view cannot
// fill. Only a required field counts: a screen of optional ones is a move that
// can be made without answering any of them.
func needsScreen(tr jira.Transition) bool {
	for i := range tr.Fields {
		if tr.Fields[i].Required {
			return true
		}
	}
	return false
}
