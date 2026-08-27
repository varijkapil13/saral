package sprint

import (
	"context"

	tea "charm.land/bubbletea/v2"

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

// reader is the pair of reads one paint needs. They are one role because they
// are one question — which sprints are there — and splitting them into two
// commands would need a context each, so the first one's would be released
// while the second still wanted it.
type reader interface {
	jira.BoardReader
	jira.SprintReader
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
//
// The states are never omitted: a board with years of history has hundreds of
// closed sprints, and the endpoint is the only thing that can narrow them.
func load(ctx context.Context, r reader, project string, states []jira.SprintState, boardCap, sprintCap, gen int) tea.Cmd {
	return func() tea.Msg {
		boards, err := r.Boards(ctx, project)
		if err != nil {
			return failedMsg{gen: gen, op: opRead, err: err}
		}
		more := 0
		if len(boards) > boardCap {
			more, boards = len(boards)-boardCap, boards[:boardCap]
		}
		out := make([]jira.Sprint, 0, len(boards)*8)
		for i := range boards {
			held, err := walkSprints(ctx, r, boards[i].ID, states, sprintCap)
			if err != nil {
				return failedMsg{gen: gen, op: opRead, err: err}
			}
			out = append(out, held...)
		}
		return loadedMsg{gen: gen, boards: boards, more: more, sprints: out}
	}
}

// walkSprints reads as many of a board's sprints as the view will offer. The
// walk is bounded because the closed ones go back to the board's first day, and
// a truncated list is drawn as truncated rather than as the whole of it.
func walkSprints(ctx context.Context, r jira.SprintReader, boardID int64, states []jira.SprintState, limit int) ([]jira.Sprint, error) {
	page, err := r.Sprints(ctx, boardID, states...)
	if err != nil {
		return nil, err
	}
	out := append([]jira.Sprint(nil), page.Items...)
	for page.HasMore() && len(out) < limit {
		page, err = page.Next(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func createSprint(ctx context.Context, w jira.SprintManager, in jira.SprintInput, gen int) tea.Cmd {
	return func() tea.Msg {
		sp, err := w.CreateSprint(ctx, in)
		if err != nil {
			return failedMsg{gen: gen, op: opCreate, err: err}
		}
		return wroteMsg{gen: gen, op: opCreate, sprint: sp}
	}
}

// updateSprint sends the fields the patch names and no others: the endpoint
// underneath is a full replace, which is why every field of the patch is a
// pointer and why one this view did not touch is left nil.
func updateSprint(ctx context.Context, w jira.SprintManager, id int64, patch jira.SprintPatch, gen int) tea.Cmd {
	return func() tea.Msg {
		sp, err := w.UpdateSprint(ctx, id, patch)
		if err != nil {
			return failedMsg{gen: gen, op: opUpdate, err: err}
		}
		return wroteMsg{gen: gen, op: opUpdate, sprint: sp}
	}
}

// startSprint moves a future sprint to active. The port refuses a sprint with
// no dates without a round trip, so the refusal arrives as a
// *jira.ValidationError naming the date that is missing.
func startSprint(ctx context.Context, w jira.SprintManager, id int64, gen int) tea.Cmd {
	return func() tea.Msg {
		sp, err := w.StartSprint(ctx, id)
		if err != nil {
			return failedMsg{gen: gen, op: opStart, err: err}
		}
		return wroteMsg{gen: gen, op: opStart, sprint: sp}
	}
}

func completeSprint(ctx context.Context, w jira.SprintManager, id int64, gen int) tea.Cmd {
	return func() tea.Msg {
		sp, err := w.CompleteSprint(ctx, id)
		if err != nil {
			return failedMsg{gen: gen, op: opComplete, err: err}
		}
		return wroteMsg{gen: gen, op: opComplete, sprint: sp}
	}
}
