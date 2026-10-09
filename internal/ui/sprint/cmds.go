package sprint

import (
	"context"

	tea "charm.land/bubbletea/v2"

	appsprint "github.com/varijkapil13/saral/internal/app/sprint"
	"github.com/varijkapil13/saral/pkg/jira"
)

// op names what was asked of the site, so that an answer says which question it
// answers and a refusal can be worded after the thing that was refused.
type op uint8

const (
	opNone op = iota
	opRead
	opCreate
	opUpdate
	opStart
	opComplete
)

func (o op) word() string {
	switch o {
	case opRead:
		return "reading the sprints"
	case opCreate:
		return "creating the sprint"
	case opUpdate:
		return "saving the sprint"
	case opStart:
		return "starting the sprint"
	case opComplete:
		return "completing the sprint"
	case opNone:
	}
	return "asking the site"
}

// loadedMsg is the boards a project has and the sprints on them. more is the
// boards past the cap, which the head names rather than walks.
type loadedMsg struct {
	gen     int
	boards  []jira.Board
	more    int
	sprints []jira.Sprint
}

// wroteMsg is a sprint as the site has it after a write. The whole sprint comes
// back rather than the fields that were sent, so what is drawn afterwards is
// the site's answer and not this view's guess at it.
type wroteMsg struct {
	gen    int
	op     op
	sprint jira.Sprint
}

// failedMsg is a call that brought nothing back. The error travels whole so a
// refusal reaches the user in the words the site used, and so that a
// *jira.ValidationError can be put back on the fields it names.
type failedMsg struct {
	gen int
	op  op
	err error
}

// load reads the project's boards and then each board's sprints in the states
// asked for.
func load(ctx context.Context, r appsprint.Reader, project string, states []jira.SprintState, boardCap, sprintCap, gen int) tea.Cmd {
	return func() tea.Msg {
		l, err := appsprint.List(ctx, r, project, states, boardCap, sprintCap)
		if err != nil {
			return failedMsg{gen: gen, op: opRead, err: err}
		}
		return loadedMsg{gen: gen, boards: l.Boards, more: l.More, sprints: l.Sprints}
	}
}

func createSprint(ctx context.Context, w jira.SprintManager, in jira.SprintInput, gen int) tea.Cmd {
	return written(gen, opCreate, func() (jira.Sprint, error) { return appsprint.Create(ctx, w, in) })
}

func updateSprint(ctx context.Context, w jira.SprintManager, id int64, patch jira.SprintPatch, gen int) tea.Cmd {
	return written(gen, opUpdate, func() (jira.Sprint, error) { return appsprint.Update(ctx, w, id, patch) })
}

func startSprint(ctx context.Context, w jira.SprintManager, id int64, gen int) tea.Cmd {
	return written(gen, opStart, func() (jira.Sprint, error) { return appsprint.Start(ctx, w, id) })
}

func completeSprint(ctx context.Context, w jira.SprintManager, id int64, gen int) tea.Cmd {
	return written(gen, opComplete, func() (jira.Sprint, error) { return appsprint.Complete(ctx, w, id) })
}

func written(gen int, o op, call func() (jira.Sprint, error)) tea.Cmd {
	return func() tea.Msg {
		sp, err := call()
		if err != nil {
			return failedMsg{gen: gen, op: o, err: err}
		}
		return wroteMsg{gen: gen, op: o, sprint: sp}
	}
}
