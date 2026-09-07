package issue

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// editFailedMsg is anything the transition picker or the $EDITOR handoff asked
// for and did not get. The error travels whole so the wording the user sees is
// the error's own.
type editFailedMsg struct {
	gen int
	err error
}

// editedMsg is what came back from the user's editor.
type editedMsg struct {
	gen int
	// doc is the reconciled document, and nil when nothing was applied.
	doc *adf.Doc
	// cleared is an author who emptied the file on purpose, which is a change
	// to make rather than a handoff to abandon.
	cleared bool
	note    string
	err     error
}

// movesLoadedMsg carries the transitions available on one issue at the moment
// it was asked.
type movesLoadedMsg struct {
	gen   int
	moves []jira.Transition
}

// moveDoneMsg is a transition that landed.
type moveDoneMsg struct{ gen int }

// loadMoves reads the transitions this issue can make right now. They are never
// cached: which ones exist depends on the status the issue is in at the moment
// of asking, and on conditions the workflow evaluates against this issue.
func loadMoves(ctx context.Context, client jira.Mover, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		moves, err := client.Transitions(ctx, key)
		if err != nil {
			return editFailedMsg{gen: gen, err: err}
		}
		return movesLoadedMsg{gen: gen, moves: moves}
	}
}

func applyMove(ctx context.Context, client jira.Mover, key, transitionID string, patch jira.IssuePatch, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := client.Transition(ctx, key, transitionID, patch); err != nil {
			return editFailedMsg{gen: gen, err: err}
		}
		return moveDoneMsg{gen: gen}
	}
}
