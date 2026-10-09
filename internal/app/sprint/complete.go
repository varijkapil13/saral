package sprint

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Dest is where a completion sends the issues still open in the sprint.
type Dest uint8

// The three places the web UI offers.
const (
	DestBacklog Dest = iota
	DestNext
	DestNew
)

// Destination is one answer to where the open issues go. Sprint is the planned
// sprint for DestNext; Name is what DestNew will call the sprint it creates.
type Destination struct {
	Kind   Dest
	Sprint jira.Sprint
	Name   string
}

// Destinations are where sp's open issues can go, given the sprints on the
// list. The next planned sprint is the first future one on the same board in
// the list's own order, and is left out when the board has none.
func Destinations(sprints []jira.Sprint, sp jira.Sprint) []Destination {
	out := []Destination{{Kind: DestBacklog}}
	for i := range sprints {
		if next := sprints[i]; next.BoardID == sp.BoardID && next.State == jira.SprintFuture {
			out = append(out, Destination{Kind: DestNext, Sprint: next})
			break
		}
	}
	return append(out, Destination{Kind: DestNew, Name: SuccessorName(sprints, sp)})
}

// SuccessorName counts on from a trailing number, the way a board names its
// sprints, and past any name already on sp's board. A name with no number gets
// one.
func SuccessorName(sprints []jira.Sprint, sp jira.Sprint) string {
	base := strings.TrimSpace(sp.Name)
	stem := strings.TrimRightFunc(base, unicode.IsDigit)
	n, err := strconv.Atoi(base[len(stem):])
	if err != nil {
		stem, n = strings.TrimSpace(base)+" ", 1
		if base == "" {
			stem = "Sprint "
		}
	}
	taken := make(map[string]bool, len(sprints))
	for i := range sprints {
		if sprints[i].BoardID == sp.BoardID {
			taken[strings.TrimSpace(sprints[i].Name)] = true
		}
	}
	for {
		n++
		if name := stem + strconv.Itoa(n); !taken[name] {
			return name
		}
	}
}

// Finisher is what a completion into another sprint needs: the read of what is
// open, a sprint to create, the move and the close.
type Finisher interface {
	IssueReader
	jira.SprintManager
}

// Stage is the step of a completion.
type Stage uint8

// The steps in the order a completion takes them.
const (
	StageRead Stage = iota
	StageCreate
	StageMove
	StageClose
)

// ErrTooManyIssues is a sprint holding more than IssueCap issues, which is more
// than one completion will move anywhere but the backlog.
var ErrTooManyIssues = errors.New("more issues are in the sprint than one completion will move")

// Completion is a sprint closed after its open issues moved into Target.
type Completion struct {
	Sprint  jira.Sprint
	Target  jira.Sprint
	Created bool
	Moved   int
}

// CompletionError is a completion that stopped part way, with what it had
// already done: the stage it stopped at, a sprint it created, and how many of
// the open issues moved.
type CompletionError struct {
	Stage   Stage
	Sprint  jira.Sprint
	Target  jira.Sprint
	Created bool
	Moved   int
	Total   int
	Err     error
}

func (e *CompletionError) Error() string { return e.Err.Error() }

func (e *CompletionError) Unwrap() error { return e.Err }

// CompleteInto closes sp with its open issues sent to d. Into the backlog that
// is the close alone. Anywhere else the open issues are moved first, into a
// sprint created for them when d asks for one, and the sprint is closed only
// once every one of them has moved: the close endpoint has no way to name a
// destination and sends what is open to the backlog.
//
// The open issues are read again here rather than taken from a progress read,
// because that is as old as whatever drew it. A failure is a *CompletionError.
func CompleteInto(ctx context.Context, w Finisher, sp jira.Sprint, d Destination) (Completion, error) {
	if d.Kind == DestBacklog {
		closed, err := w.CompleteSprint(ctx, sp.ID)
		if err != nil {
			return Completion{}, &CompletionError{Stage: StageClose, Sprint: sp, Err: err}
		}
		return Completion{Sprint: closed}, nil
	}
	fail := func(e CompletionError) (Completion, error) {
		e.Sprint = sp
		return Completion{}, &e
	}
	p, err := ReadProgress(ctx, w, sp.BoardID, sp.ID)
	if err == nil && p.Capped {
		err = ErrTooManyIssues
	}
	if err != nil {
		return fail(CompletionError{Stage: StageRead, Err: err})
	}
	target, created := d.Sprint, false
	if d.Kind == DestNew {
		target, err = w.CreateSprint(ctx, jira.SprintInput{BoardID: sp.BoardID, Name: d.Name})
		if err != nil {
			return fail(CompletionError{Stage: StageCreate, Err: err})
		}
		created = true
	}
	total := len(p.Open)
	if total > 0 {
		if err := w.MoveToSprint(ctx, target.ID, p.Open); err != nil {
			moved := 0
			var partial *jira.PartialMoveError
			if errors.As(err, &partial) {
				moved = len(partial.Moved)
			}
			return fail(CompletionError{
				Stage: StageMove, Target: target, Created: created, Moved: moved, Total: total, Err: err,
			})
		}
	}
	closed, err := w.CompleteSprint(ctx, sp.ID)
	if err != nil {
		return fail(CompletionError{
			Stage: StageClose, Target: target, Created: created, Moved: total, Total: total, Err: err,
		})
	}
	return Completion{Sprint: closed, Target: target, Created: created, Moved: total}, nil
}
