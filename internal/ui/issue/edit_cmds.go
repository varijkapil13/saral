package issue

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// editLoadedMsg carries the issue re-read with the fields the editor changes.
type editLoadedMsg struct {
	gen   int
	issue jira.Issue
}

// editSchemaMsg carries the create screen, which is where the values a picker
// may offer come from. Nothing about them is written down here: a site decides
// its own priorities and calls them whatever its language calls them.
type editSchemaMsg struct {
	gen    int
	schema jira.Schema
}

// editFailedMsg is anything the editor asked for and did not get. The error
// travels whole so the wording the user sees is the error's own.
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

// editSavedMsg is a write that landed.
type editSavedMsg struct{ gen int }

// movesLoadedMsg carries the transitions available on one issue at the moment
// it was asked.
type movesLoadedMsg struct {
	gen   int
	moves []jira.Transition
}

// moveDoneMsg is a transition that landed.
type moveDoneMsg struct{ gen int }

func loadForEdit(ctx context.Context, search *app.Search, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		res, err := search.Run(ctx, app.Request{JQL: byKey(key), Projection: editProjection(), MaxResults: 1})
		if err != nil {
			return editFailedMsg{gen: gen, err: err}
		}
		if len(res.Page.Items) == 0 {
			return editFailedMsg{gen: gen, err: &jira.NotFoundError{Kind: "issue", ID: key}}
		}
		return editLoadedMsg{gen: gen, issue: res.Page.Items[0]}
	}
}

func loadEditSchema(ctx context.Context, client jira.Client, project, issueTypeID string, gen int) tea.Cmd {
	return func() tea.Msg {
		schema, err := client.CreateMeta(ctx, project, issueTypeID)
		if err != nil {
			return editFailedMsg{gen: gen, err: err}
		}
		return editSchemaMsg{gen: gen, schema: schema}
	}
}

func saveEdit(ctx context.Context, client jira.Client, key string, patch jira.IssuePatch, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := client.UpdateIssue(ctx, key, patch); err != nil {
			return editFailedMsg{gen: gen, err: err}
		}
		return editSavedMsg{gen: gen}
	}
}

// loadMoves reads the transitions this issue can make right now. They are never
// cached: which ones exist depends on the status the issue is in at the moment
// of asking, and on conditions the workflow evaluates against this issue.
func loadMoves(ctx context.Context, client jira.Client, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		moves, err := client.Transitions(ctx, key)
		if err != nil {
			return editFailedMsg{gen: gen, err: err}
		}
		return movesLoadedMsg{gen: gen, moves: moves}
	}
}

func applyMove(ctx context.Context, client jira.Client, key, transitionID string, patch jira.IssuePatch, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := client.Transition(ctx, key, transitionID, patch); err != nil {
			return editFailedMsg{gen: gen, err: err}
		}
		return moveDoneMsg{gen: gen}
	}
}
