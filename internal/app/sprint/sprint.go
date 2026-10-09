// Package sprint is a board's sprints: reading them by state, the writes the
// port allows on one, completing one into a destination, and how much of a
// running one is done.
//
// The lifecycle belongs to the port — future to active to closed and nothing
// else — so there is one call per move and no state to set.
package sprint

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Reader is the pair of reads one listing needs.
type Reader interface {
	jira.BoardReader
	jira.SprintReader
}

// Listing is the boards a project has and the sprints on them. More is the
// boards past the cap, which a caller names rather than walks.
type Listing struct {
	Boards  []jira.Board
	More    int
	Sprints []jira.Sprint
}

// List reads the project's boards and then each board's sprints in the states
// asked for, at most boardCap boards and sprintCap sprints a board.
//
// The states are never omitted: a board with years of history has hundreds of
// closed sprints, and the endpoint is the only thing that can narrow them.
func List(ctx context.Context, r Reader, project string, states []jira.SprintState, boardCap, sprintCap int) (Listing, error) {
	boards, err := r.Boards(ctx, project)
	if err != nil {
		return Listing{}, err
	}
	more := 0
	if len(boards) > boardCap {
		more, boards = len(boards)-boardCap, boards[:boardCap]
	}
	out := make([]jira.Sprint, 0, len(boards)*8)
	for i := range boards {
		held, err := walkSprints(ctx, r, boards[i].ID, states, sprintCap)
		// A board with no sprints — a Kanban board — answers this read with a
		// 400 and its own sentence. That is the board answering, not refusing:
		// it contributes nothing and the listing goes on. Anything else the site
		// says is still a refusal to read, and docs/API-NOTES.md says why the
		// board's type is not what decides this.
		var invalid *jira.ValidationError
		if errors.As(err, &invalid) {
			continue
		}
		if err != nil {
			return Listing{}, err
		}
		out = append(out, held...)
	}
	return Listing{Boards: boards, More: more, Sprints: out}, nil
}

// walkSprints is bounded because the closed ones go back to the board's first
// day, and a truncated list is drawn as truncated rather than as the whole of it.
func walkSprints(ctx context.Context, r jira.SprintReader, boardID int64, states []jira.SprintState, limit int) ([]jira.Sprint, error) {
	page, err := r.Sprints(ctx, boardID, states...)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(page.Items)
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

// Create plans a sprint on a board.
func Create(ctx context.Context, w jira.SprintManager, in jira.SprintInput) (jira.Sprint, error) {
	return w.CreateSprint(ctx, in)
}

// Update sends the fields the patch names and no others: the endpoint
// underneath is a full replace, which is why every field of the patch is a
// pointer and why one nobody touched is left nil.
func Update(ctx context.Context, w jira.SprintManager, id int64, patch jira.SprintPatch) (jira.Sprint, error) {
	return w.UpdateSprint(ctx, id, patch)
}

// Start moves a future sprint to active. The port refuses a sprint with no
// dates without a round trip, as a *jira.ValidationError naming the date.
func Start(ctx context.Context, w jira.SprintManager, id int64) (jira.Sprint, error) {
	return w.StartSprint(ctx, id)
}

// Complete closes a running sprint, sending what is open in it to the backlog.
func Complete(ctx context.Context, w jira.SprintManager, id int64) (jira.Sprint, error) {
	return w.CompleteSprint(ctx, id)
}

// States is what a listing is narrowed to. The closed ones are asked for only
// when they are wanted, because they are the ones there are hundreds of.
func States(closed bool) []jira.SprintState {
	if closed {
		return []jira.SprintState{jira.SprintActive, jira.SprintFuture, jira.SprintClosed}
	}
	return []jira.SprintState{jira.SprintActive, jira.SprintFuture}
}

// Running is the sprints that are active, in the order given.
func Running(sprints []jira.Sprint) []jira.Sprint {
	out := make([]jira.Sprint, 0, 2)
	for i := range sprints {
		if sprints[i].State == jira.SprintActive {
			out = append(out, sprints[i])
		}
	}
	return out
}

// Rank orders the states.
type Rank int

// The states in the order a list shows them, and a state no constant covers.
const (
	RankActive Rank = iota
	RankFuture
	RankClosed
	RankOther
)

// RankOf orders the states without switching exhaustively on them: the type is
// an open string and a site can report a value none of the three constants
// covers.
func RankOf(s jira.SprintState) Rank {
	switch s {
	case jira.SprintActive:
		return RankActive
	case jira.SprintFuture:
		return RankFuture
	case jira.SprintClosed:
		return RankClosed
	}
	return RankOther
}

// Sort puts the sprint a team is in first, then the ones it is going to be in,
// then the ones it has finished, newest first. It sorts by state and by date
// and never by name: a name is whatever anybody typed. It sorts in place and
// returns its argument.
func Sort(in []jira.Sprint) []jira.Sprint {
	slices.SortStableFunc(in, func(a, b jira.Sprint) int {
		if r := RankOf(a.State) - RankOf(b.State); r != 0 {
			return int(r)
		}
		if RankOf(a.State) == RankClosed {
			return compareTimes(b.End, a.End)
		}
		return compareTimes(a.Start, b.Start)
	})
	return in
}

// DropClosed removes the closed sprints in place and returns what is left.
func DropClosed(in []jira.Sprint) []jira.Sprint {
	return slices.DeleteFunc(in, func(sp jira.Sprint) bool { return RankOf(sp.State) == RankClosed })
}

// Put replaces a sprint by id or adds it, and keeps the list in order.
func Put(in []jira.Sprint, sp jira.Sprint) []jira.Sprint {
	at := slices.IndexFunc(in, func(held jira.Sprint) bool { return held.ID == sp.ID })
	if at < 0 {
		in = append(in, sp)
	} else {
		in[at] = sp
	}
	return Sort(in)
}

// compareTimes sorts a sprint with no date after one that has one: a date that
// is not set is not a date at the beginning of time.
func compareTimes(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	case a.Before(*b):
		return -1
	case b.Before(*a):
		return 1
	}
	return 0
}

// Block is why a lifecycle move cannot be made, worked out before anything is
// asked of the site. The port refuses the same things.
type Block uint8

// The reasons a move is refused.
const (
	Clear Block = iota
	// NotPlanned is a start asked of a sprint that is not future.
	NotPlanned
	// NotRunning is a completion asked of a sprint that is not active.
	NotRunning
	NoDates
	NoStartDate
	NoEndDate
)

// CanStart is what stands in the way of starting a sprint.
func CanStart(sp jira.Sprint) Block {
	if sp.State != jira.SprintFuture {
		return NotPlanned
	}
	switch {
	case sp.Start == nil && sp.End == nil:
		return NoDates
	case sp.Start == nil:
		return NoStartDate
	case sp.End == nil:
		return NoEndDate
	}
	return Clear
}

// CanComplete is what stands in the way of completing a sprint.
func CanComplete(sp jira.Sprint) Block {
	if sp.State != jira.SprintActive {
		return NotRunning
	}
	return Clear
}

// DaysLeft counts calendar days from now to end in loc, so a sprint that ends
// tonight has none left wherever the machine is. It is negative once the end
// has passed.
func DaysLeft(end, now time.Time, loc *time.Location) int {
	e, n := jira.DateOf(end.In(loc)), jira.DateOf(now.In(loc))
	return int(time.Date(e.Year, e.Month, e.Day, 0, 0, 0, 0, time.UTC).
		Sub(time.Date(n.Year, n.Month, n.Day, 0, 0, 0, 0, time.UTC)).Hours() / 24)
}
