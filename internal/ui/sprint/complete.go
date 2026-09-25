package sprint

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// dest is where a completion sends the issues still open in the sprint.
type dest uint8

const (
	destBacklog dest = iota
	destNext
	destNew
)

// destination is one answer to where the open issues go. sprint is the planned
// sprint for destNext; name is what destNew will call the sprint it creates.
type destination struct {
	kind   dest
	sprint jira.Sprint
	name   string
}

func (d destination) words() string {
	switch d.kind {
	case destNext:
		return named(d.sprint) + ", the next planned sprint"
	case destNew:
		return "a new sprint, " + d.name
	case destBacklog:
	}
	return "the backlog"
}

// destinations are the three places the web UI offers. The next planned sprint
// is the first one on the same board in the list's own order, and is left out
// when the board has none.
func (m *Model) destinations(sp jira.Sprint) []destination {
	out := []destination{{kind: destBacklog}}
	for i := range m.sprints {
		if next := m.sprints[i]; next.BoardID == sp.BoardID && next.State == jira.SprintFuture {
			out = append(out, destination{kind: destNext, sprint: next})
			break
		}
	}
	return append(out, destination{kind: destNew, name: m.successorName(sp)})
}

// successorName counts on from a trailing number, the way a board names its
// sprints, and past any name already on the board. A name with no number gets
// one.
func (m *Model) successorName(sp jira.Sprint) string {
	base := strings.TrimSpace(sp.Name)
	stem := strings.TrimRightFunc(base, unicode.IsDigit)
	n, err := strconv.Atoi(base[len(stem):])
	if err != nil {
		stem, n = strings.TrimSpace(base)+" ", 1
		if base == "" {
			stem = "Sprint "
		}
	}
	taken := make(map[string]bool, len(m.sprints))
	for i := range m.sprints {
		if m.sprints[i].BoardID == sp.BoardID {
			taken[strings.TrimSpace(m.sprints[i].Name)] = true
		}
	}
	for {
		n++
		if name := stem + strconv.Itoa(n); !taken[name] {
			return name
		}
	}
}

// finisher is what a completion into another sprint needs: the read of what is
// open, a sprint to create, the move and the close.
type finisher interface {
	issueReader
	jira.SprintManager
}

type stage uint8

const (
	stageRead stage = iota
	stageCreate
	stageMove
	stageClose
)

// completedMsg is a sprint closed after its open issues moved somewhere other
// than the backlog.
type completedMsg struct {
	gen     int
	sprint  jira.Sprint
	target  jira.Sprint
	created bool
	moved   int
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
	stage   stage
	sprint  jira.Sprint
	target  jira.Sprint
	created bool
	moved   int
	total   int
	err     error
}

func (e *completionError) Error() string {
	name := named(e.sprint)
	switch e.stage {
	case stageRead:
		return "nothing was moved and " + name + " is still running: its issues could not be read: " + e.err.Error()
	case stageCreate:
		return "nothing was moved and " + name + " is still running: the new sprint could not be created: " + e.err.Error()
	case stageMove:
		return "moved " + strconv.Itoa(e.moved) + " of " + strconv.Itoa(e.total) + " open issues into " +
			named(e.target) + "; " + name + " is still running and was not closed: " + e.err.Error()
	case stageClose:
	}
	return "all " + strconv.Itoa(e.total) + " open issues moved into " + named(e.target) + ", but " +
		name + " was not closed: " + e.err.Error()
}

func (e *completionError) Unwrap() error { return e.err }

// completeInto moves a sprint's open issues into another sprint, creating it
// first when asked to, and closes the sprint only once every one of them has
// moved. The close endpoint has no way to name a destination: it sends what is
// open to the backlog, so anything going elsewhere has to go before it runs.
//
// The open issues are read again here rather than taken from the progress on
// screen, because that is as old as the frame it was drawn in.
func completeInto(ctx context.Context, w finisher, sp jira.Sprint, d destination, gen int) tea.Cmd {
	return func() tea.Msg {
		fail := func(e completionError) tea.Msg {
			e.sprint = sp
			return completeFailedMsg{gen: gen, err: &e}
		}
		p, err := readProgress(ctx, w, sp.BoardID, sp.ID)
		if err == nil && p.capped {
			err = &jira.ValidationError{Messages: []string{
				"more than " + strconv.Itoa(issueCap) + " issues are in it, more than one completion will move; " +
					"send them to the backlog, or move them from the backlog view first",
			}}
		}
		if err != nil {
			return fail(completionError{stage: stageRead, err: err})
		}
		target, created := d.sprint, false
		if d.kind == destNew {
			target, err = w.CreateSprint(ctx, jira.SprintInput{BoardID: sp.BoardID, Name: d.name})
			if err != nil {
				return fail(completionError{stage: stageCreate, err: err})
			}
			created = true
		}
		total := len(p.open)
		if total > 0 {
			if err := w.MoveToSprint(ctx, target.ID, p.open); err != nil {
				moved := 0
				var partial *jira.PartialMoveError
				if errors.As(err, &partial) {
					moved = len(partial.Moved)
				}
				return fail(completionError{
					stage: stageMove, target: target, created: created, moved: moved, total: total, err: err,
				})
			}
		}
		closed, err := w.CompleteSprint(ctx, sp.ID)
		if err != nil {
			return fail(completionError{
				stage: stageClose, target: target, created: created, moved: total, total: total, err: err,
			})
		}
		return completedMsg{gen: gen, sprint: closed, target: target, created: created, moved: total}
	}
}
