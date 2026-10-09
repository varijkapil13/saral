package board

import (
	"context"
	"errors"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// MovesOf reads what an issue can do right now. The list is per issue and
// per token and expires, so it is read when it is needed and never kept.
func MovesOf(ctx context.Context, mover jira.Mover, key string) ([]jira.Transition, error) {
	return mover.Transitions(ctx, key)
}

// Apply moves an issue by transition id. A status is not writable on Jira, so a
// column change is a workflow move and never a field set — and the transition is
// named by the id the site gave it, never by the status it lands on.
func Apply(ctx context.Context, mover jira.Mover, key, transitionID string) error {
	return mover.Transition(ctx, key, transitionID, jira.IssuePatch{})
}

// NeedsScreen reports whether a transition insists on a field a board cannot
// fill. Only a required field counts: a screen of optional ones is a move that
// can be made without answering any of them.
func NeedsScreen(tr jira.Transition) bool {
	for i := range tr.Fields {
		if tr.Fields[i].Required {
			return true
		}
	}
	return false
}

// ErrNoMove is a card no workflow move takes into the column it is going to;
// ErrScreen is one whose move needs a field only the issue pane can fill;
// ErrNoMoveTo is one with no move to the status it was sent to.
var (
	ErrNoMove   = errors.New("no workflow move takes it into that column")
	ErrScreen   = errors.New("its move needs a field filled in, which the issue pane asks for")
	ErrNoMoveTo = errors.New("no workflow move takes it to that status")
)

// IntoColumn moves one issue into column col of the plan by the first of its
// own transitions that lands there, and only one landing on status when status
// is not "".
func IntoColumn(ctx context.Context, mover jira.Mover, p Plan, key string, col int, status string) (jira.Status, error) {
	list, err := mover.Transitions(ctx, key)
	if err != nil {
		return jira.Status{}, err
	}
	for _, tr := range list {
		if at, mapped := p.ColumnOf(tr.To.ID); !mapped || at != col {
			continue
		}
		if status != "" && tr.To.ID != status {
			continue
		}
		if NeedsScreen(tr) {
			return jira.Status{}, ErrScreen
		}
		if err := mover.Transition(ctx, key, tr.ID, jira.IssuePatch{}); err != nil {
			return jira.Status{}, err
		}
		return tr.To, nil
	}
	if status != "" {
		return jira.Status{}, ErrNoMoveTo
	}
	return jira.Status{}, ErrNoMove
}

// ColumnTargets is the transitions of one issue that land in column col, the
// first to each status only: each card of a set takes its own transition to the
// status chosen, and a transition id belongs to one issue's workflow.
func ColumnTargets(ctx context.Context, mover jira.Mover, p Plan, key string, col int) ([]jira.Transition, error) {
	list, err := mover.Transitions(ctx, key)
	if err != nil {
		return nil, err
	}
	var into []jira.Transition
	for _, tr := range list {
		if at, mapped := p.ColumnOf(tr.To.ID); mapped && at == col {
			into = append(into, tr)
		}
	}
	return DistinctTargets(into), nil
}

// DistinctTargets keeps the first transition to each status.
func DistinctTargets(list []jira.Transition) []jira.Transition {
	out := make([]jira.Transition, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, tr := range list {
		if seen[tr.To.ID] {
			continue
		}
		seen[tr.To.ID] = true
		out = append(out, tr)
	}
	return out
}

// Lander is what landing a created issue on a board takes.
type Lander interface {
	jira.SprintManager
	jira.Mover
	jira.IssueReader
}

// LandStep is which part of a landing failed.
type LandStep uint8

// The steps of a landing, in order.
const (
	LandSprint LandStep = iota
	LandMove
	LandRead
)

// Landing is a created issue and the column of the plan it was made in.
// OnBoard is false once the board on screen is another one, and Fields is what
// the issue is read back with.
type Landing struct {
	Created jira.Issue
	Sprint  int64
	Col     int
	Plan    Plan
	Fields  []string
	OnBoard bool
}

// Landed is how far a created issue got towards its column. Screen is a move
// that needs fields filled in, and NoMove is a column no move reaches.
type Landed struct {
	Issue  jira.Issue
	Read   bool
	Step   LandStep
	Err    error
	NoMove bool
	Screen *jira.Transition
}

// Land puts a created issue in the column it was made in: into the sprint
// first, since the site creates every issue in the backlog, then through the
// workflow move into the column when it was created in another, then read back.
func Land(ctx context.Context, client Lander, l Landing) Landed {
	key := l.Created.Key
	out := Landed{Issue: l.Created}
	if l.Sprint != 0 {
		if err := client.MoveToSprint(ctx, l.Sprint, []string{key}); err != nil {
			out.Step, out.Err = LandSprint, err
			return out
		}
	}
	if !l.OnBoard {
		return out
	}
	if at, mapped := l.Plan.ColumnOf(l.Created.Status.ID); !mapped || at != l.Col {
		list, err := client.Transitions(ctx, key)
		if err != nil {
			out.Step, out.Err = LandMove, err
			return out
		}
		tr, found := jira.Transition{}, false
		for _, t := range list {
			if at, mapped := l.Plan.ColumnOf(t.To.ID); mapped && at == l.Col {
				tr, found = t, true
				break
			}
		}
		switch {
		case !found:
			out.NoMove = true
		case NeedsScreen(tr):
			out.Screen = &tr
		default:
			if err := client.Transition(ctx, key, tr.ID, jira.IssuePatch{}); err != nil {
				out.Step, out.Err = LandMove, err
				return out
			}
			out.Issue.Status = tr.To
		}
	}
	if len(l.Fields) == 0 {
		out.Read = true
		return out
	}
	iss, err := client.IssueFields(ctx, key, l.Fields)
	if err != nil {
		out.Step, out.Err = LandRead, err
		return out
	}
	iss.Status = out.Issue.Status
	out.Issue, out.Read = iss, true
	return out
}

// MoveInto moves one chunk of issues. A sprint id of zero is the backlog, which
// is its own endpoint rather than a sprint with no number.
func MoveInto(ctx context.Context, mgr jira.SprintManager, sprintID int64, keys []string) error {
	if sprintID == 0 {
		return mgr.MoveToBacklog(ctx, keys)
	}
	return mgr.MoveToSprint(ctx, sprintID, keys)
}

// Settled is what became of an issue created from a backlog.
type Settled struct {
	MoveErr error
	Read    bool
	Issue   jira.Issue
	ReadErr error
}

// Settle moves a created issue into the sprint it was meant for, since the site
// creates every issue in the backlog, and reads it back when reader is not nil:
// by fields, or by the projection want resolved when fields is empty.
func Settle(ctx context.Context, mover jira.SprintManager, reader jira.IssueReader, search *appquery.Search,
	fields []string, want appquery.Projection, key string, sprintID int64,
) Settled {
	var out Settled
	if sprintID != 0 {
		out.MoveErr = mover.MoveToSprint(ctx, sprintID, []string{key})
	}
	if reader == nil {
		return out
	}
	out.Read = true
	if len(fields) == 0 && search != nil {
		wanted, err := search.Resolve(ctx, want)
		if err != nil {
			out.ReadErr = err
			return out
		}
		fields = wanted.IDs
	}
	out.Issue, out.ReadErr = reader.IssueFields(ctx, key, fields)
	return out
}

// FindAssignees is the accounts matching needle that may work in project.
func FindAssignees(ctx context.Context, finder jira.PeopleFinder, needle, project string, limit int) ([]jira.User, error) {
	return finder.FindPeople(ctx, jira.PeopleQuery{Match: needle, Project: project, Limit: limit})
}

// Account is the account this session is signed in as.
func Account(ctx context.Context, who jira.Identifier) (jira.User, error) {
	return who.Me(ctx)
}

// ReadQuickFilters is a board's own quick filters. An error reading them answers
// none: the board still draws without them, the way it draws without an
// estimation field, so a site the token cannot ask a second endpoint of does not
// lose the first one's cards over it.
func ReadQuickFilters(ctx context.Context, reader jira.BoardReader, boardID int64) []jira.QuickFilter {
	found, err := reader.QuickFilters(ctx, boardID)
	if err != nil {
		return nil
	}
	return found
}
