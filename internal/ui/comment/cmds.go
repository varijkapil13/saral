package comment

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// loadedMsg carries the first page of a thread, replacing whatever was held.
type loadedMsg struct {
	gen  int
	page jira.Page[jira.Comment]
}

// pagedMsg carries the page after the one already in hand.
type pagedMsg struct {
	gen  int
	page jira.Page[jira.Comment]
}

// savedMsg carries a comment the site stored. edited says which of the two
// writes produced it, because only one of them moves the cursor.
type savedMsg struct {
	gen     int
	comment jira.Comment
	edited  bool
}

// deletedMsg carries the id of a comment the site no longer has.
type deletedMsg struct {
	gen int
	id  string
}

// failedMsg is any request that produced no answer. The error travels whole so
// that the status line can use the wording the error itself carries — a refusal
// reaches the user as the sentence Jira wrote.
type failedMsg struct {
	gen int
	err error
}

func load(ctx context.Context, client jira.Client, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		page, err := client.Comments(ctx, key)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return loadedMsg{gen: gen, page: page}
	}
}

func more(ctx context.Context, page jira.Page[jira.Comment], gen int) tea.Cmd {
	return func() tea.Msg {
		next, err := page.Next(ctx)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return pagedMsg{gen: gen, page: next}
	}
}

func add(ctx context.Context, client jira.Client, key string, body adf.Doc, gen int) tea.Cmd {
	return func() tea.Msg {
		stored, err := client.AddComment(ctx, key, body)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return savedMsg{gen: gen, comment: stored}
	}
}

func edit(ctx context.Context, client jira.Client, key, id string, body adf.Doc, gen int) tea.Cmd {
	return func() tea.Msg {
		stored, err := client.EditComment(ctx, key, id, body)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return savedMsg{gen: gen, comment: stored, edited: true}
	}
}

func remove(ctx context.Context, client jira.Client, key, id string, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := client.DeleteComment(ctx, key, id); err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return deletedMsg{gen: gen, id: id}
	}
}

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that closing the view cuts the request short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}
