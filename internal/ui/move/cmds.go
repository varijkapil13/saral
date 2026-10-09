package move

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	appmove "github.com/varijkapil13/saral/internal/app/move"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

type candidatesMsg struct {
	gen  int
	keys []string
}

// vocabularyMsg carries the target project's issue types and the statuses each
// one's workflow reaches.
type vocabularyMsg struct {
	gen     int
	project string
	types   []jira.IssueTypeStatuses
}

// schemaMsg is the target's create screen, or why it could not be read. A
// refusal is carried here rather than as a failedMsg because the wizard goes on
// without it.
type schemaMsg struct {
	gen    int
	schema jira.Schema
	err    error
}

// submittedMsg carries the task the queue took the move under. The ref is
// carried whole, because the endpoint to poll is part of it and cannot be built
// from the id.
type submittedMsg struct {
	gen int
	ref jira.TaskRef
}

// taskMsg is one answer from the queue. paused is set instead of status when the
// queue asked for a pause.
type taskMsg struct {
	gen    int
	status jira.TaskStatus
	paused time.Duration
}

// failedMsg is a read or a write that brought nothing back. The error travels
// whole so that a refusal reaches the user in the words the site used, and at
// says which step was asking.
type failedMsg struct {
	gen int
	at  step
	err error
}

func candidates(ctx context.Context, client appquery.SearchClient, moving []jira.Issue, gen int) tea.Cmd {
	return func() tea.Msg {
		keys, err := appmove.Candidates(ctx, client, moving)
		if err != nil {
			return failedMsg{gen: gen, at: stepTarget, err: err}
		}
		return candidatesMsg{gen: gen, keys: keys}
	}
}

func vocabulary(ctx context.Context, vocab jira.FilterVocabulary, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		types, err := appmove.Vocabulary(ctx, vocab, project)
		if err != nil {
			return failedMsg{gen: gen, at: stepTarget, err: err}
		}
		return vocabularyMsg{gen: gen, project: project, types: types}
	}
}

func schemaOf(ctx context.Context, reader jira.SchemaReader, project, typeID string, gen int) tea.Cmd {
	return func() tea.Msg {
		schema, err := appmove.Schema(ctx, reader, project, typeID)
		return schemaMsg{gen: gen, schema: schema, err: err}
	}
}

func submit(ctx context.Context, mover jira.Relocator, in jira.MoveRequest, gen int) tea.Cmd {
	return func() tea.Msg {
		ref, err := appmove.Submit(ctx, mover, in)
		if err != nil {
			return failedMsg{gen: gen, at: stepConfirm, err: err}
		}
		return submittedMsg{gen: gen, ref: ref}
	}
}

// poll drains one answer from the task being followed.
func poll(ctx context.Context, task *appmove.Task, gen int) tea.Cmd {
	return func() tea.Msg {
		got, err := task.Next(ctx)
		if err != nil {
			return failedMsg{gen: gen, at: stepRunning, err: err}
		}
		return taskMsg{gen: gen, status: got.Status, paused: got.Paused}
	}
}
