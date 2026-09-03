package board

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pageSize is how many cards one request asks for. A column is virtualized, so
// the number that matters is that it is several screens' worth.
const pageSize = 100

// step is which of the three reads the board is waiting on. A board is three
// questions — which boards, what this one looks like, what is on it — and an
// empty pane that cannot say which of them is outstanding is a pane that looks
// like a hang.
type step uint8

const (
	stepIdle step = iota
	stepBoards
	stepConfig
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
	issues  []jira.Issue
	more    bool
	missing []string
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
	gen  int
	key  string
	to   string
	from string
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

// cards fills the board, through the read that applies the board's own saved
// filter and column mapping at the site. Nothing here composes a query: the
// filter behind a board is JQL only the site can run, and a board rebuilt out of
// its statuses is a different board.
//
// It asks for the narrow field set a card draws plus the board's own estimation
// field, never for a wildcard, and it carries the board's sub-query and
// whichever of the board's own quick filters are toggled on, which are the two
// parts of a board the endpoint leaves to the caller.
func cards(ctx context.Context, reader jira.BoardReader, search *app.Search, p plan, quickFilters []string, gen int) tea.Cmd {
	return func() tea.Msg {
		wanted, err := search.Resolve(ctx, p.projection())
		if err != nil {
			return failedMsg{gen: gen, step: stepIssues, err: err}
		}
		page, err := reader.BoardIssues(ctx, p.boardID, jira.BoardQuery{
			Fields:       wanted.IDs,
			SubQuery:     p.subQuery,
			QuickFilters: quickFilters,
			MaxResults:   pageSize,
		})
		if err != nil {
			return failedMsg{gen: gen, step: stepIssues, err: err}
		}
		return issuesMsg{gen: gen, issues: page.Items, more: page.HasMore(), missing: wanted.Missing}
	}
}

// moves reads what the held issue can do right now, so that the column it is
// dropped on is reached by a transition this token may actually make.
func moves(ctx context.Context, mover jira.Mover, key string, column, gen int) tea.Cmd {
	return func() tea.Msg {
		found, err := mover.Transitions(ctx, key)
		if err != nil {
			return failedMsg{gen: gen, step: stepIssues, err: err}
		}
		return movesMsg{gen: gen, key: key, column: column, moves: found}
	}
}

// apply moves an issue by transition id. A status is not writable on Jira, so a
// column change is a workflow move and never a field set — and the transition is
// named by the id the site gave it, never by the status it lands on.
func apply(ctx context.Context, mover jira.Mover, key, transitionID, to, from string, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := mover.Transition(ctx, key, transitionID, jira.IssuePatch{}); err != nil {
			return failedMsg{gen: gen, step: stepIssues, err: err}
		}
		return movedMsg{gen: gen, key: key, to: to, from: from}
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
