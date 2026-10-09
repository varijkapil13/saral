package sprint

import (
	"context"
	"errors"
	"strconv"

	tea "charm.land/bubbletea/v2"

	appsprint "github.com/varijkapil13/saral/internal/app/sprint"
	"github.com/varijkapil13/saral/pkg/jira"
)

func destWords(d appsprint.Destination) string {
	switch d.Kind {
	case appsprint.DestNext:
		return named(d.Sprint) + ", the next planned sprint"
	case appsprint.DestNew:
		return "a new sprint, " + d.Name
	case appsprint.DestBacklog:
	}
	return "the backlog"
}

// completedMsg is a sprint closed after its open issues moved somewhere other
// than the backlog.
type completedMsg struct {
	gen  int
	done appsprint.Completion
}

// completeFailedMsg is a completion that stopped part way. Whatever it did
// before it stopped travels with it, so the list can show a sprint it created
// and the status line can say how many issues moved.
type completeFailedMsg struct {
	gen int
	err *completionError
}

// completionError says which step a completion stopped at and what had already
// happened, in one sentence, because a half-finished completion is the one
// failure a reader has to act on differently depending on how far it got.
type completionError struct {
	*appsprint.CompletionError
}

func (e *completionError) Error() string {
	name := named(e.Sprint)
	cause := e.Err
	if errors.Is(cause, appsprint.ErrTooManyIssues) {
		cause = &jira.ValidationError{Messages: []string{
			"more than " + strconv.Itoa(appsprint.IssueCap) + " issues are in it, more than one completion will move; " +
				"send them to the backlog, or move them from the backlog view first",
		}}
	}
	switch e.Stage {
	case appsprint.StageRead:
		return "nothing was moved and " + name + " is still running: its issues could not be read: " + cause.Error()
	case appsprint.StageCreate:
		return "nothing was moved and " + name + " is still running: the new sprint could not be created: " + cause.Error()
	case appsprint.StageMove:
		return "moved " + strconv.Itoa(e.Moved) + " of " + strconv.Itoa(e.Total) + " open issues into " +
			named(e.Target) + "; " + name + " is still running and was not closed: " + cause.Error()
	case appsprint.StageClose:
	}
	return "all " + strconv.Itoa(e.Total) + " open issues moved into " + named(e.Target) + ", but " +
		name + " was not closed: " + cause.Error()
}

func (e *completionError) Unwrap() error { return e.CompletionError }

func completeInto(ctx context.Context, w appsprint.Finisher, sp jira.Sprint, d appsprint.Destination, gen int) tea.Cmd {
	return func() tea.Msg {
		done, err := appsprint.CompleteInto(ctx, w, sp, d)
		var stopped *appsprint.CompletionError
		if errors.As(err, &stopped) {
			return completeFailedMsg{gen: gen, err: &completionError{stopped}}
		}
		if err != nil {
			return failedMsg{gen: gen, op: opComplete, err: err}
		}
		return completedMsg{gen: gen, done: done}
	}
}
