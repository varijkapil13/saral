package board

import (
	"context"
	"errors"
	"slices"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Step is which of the reads a load is waiting on. A board is three
// questions — which boards, what this one looks like, what is on it — and an
// empty pane that cannot say which of them is outstanding is a pane that looks
// like a hang.
type Step uint8

// The steps of a load, in the order they are asked.
const (
	StepIdle Step = iota
	StepBoards
	StepConfig
	StepSprints
	StepIssues
)

// Site is what a load reads: the board, its sprints and a sprint's issues.
type Site interface {
	jira.BoardReader
	jira.SprintReader
	jira.SprintIssueReader
}

// ListBoards is the boards that draw on a project. An empty list is an answer: a
// project with no board is ordinary.
func ListBoards(ctx context.Context, r jira.BoardReader, project string) ([]jira.Board, error) {
	return r.Boards(ctx, project)
}

// Config is one board's real shape: its columns, whether it estimates and
// whether it ranks.
func Config(ctx context.Context, r jira.BoardReader, boardID int64) (jira.BoardConfig, error) {
	return r.BoardConfig(ctx, boardID)
}

// IndexOfBoard is the position of a board id in a list the site just answered
// with.
func IndexOfBoard(boards []jira.Board, id int64) (int, bool) {
	for i := range boards {
		if boards[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

// Sprints is a board's open sprints. None is a board that runs no sprints at
// all, and Reason is the site's own sentence for it.
type Sprints struct {
	Open   []jira.Sprint
	None   bool
	Reason string
}

// OpenSprints reads the sprints of a board in the states asked for, in the
// order the states are named. A board that has none at all — a Kanban board —
// does not fail to answer; the site answers the read with a 400 and its own
// sentence, "The board does not support sprints", and that is reported as None
// rather than as an error, so what the board holds is still read. Anything else
// the site says — a refusal, a rate limit, a board that is not there, a
// transport failure — is still an error, because each of those means the board
// could not be read, and this cannot.
//
// A 400 and not the board's type decides it: docs/API-NOTES.md says why nothing
// here may branch on kanban or scrum, and a team-managed board reports neither.
// The states are asked for and checked again, since a board with years of
// history behind it is a walk nothing on this path should be doing and an
// adapter that ignored the filter would hand back all of it.
func OpenSprints(ctx context.Context, r jira.SprintReader, boardID int64, limit int, states ...jira.SprintState) (Sprints, error) {
	page, err := r.Sprints(ctx, boardID, states...)
	var invalid *jira.ValidationError
	if errors.As(err, &invalid) {
		reason, _ := jira.Reason(err)
		return Sprints{None: true, Reason: reason}, nil
	}
	if err != nil {
		return Sprints{}, err
	}
	all, err := jira.Collect(ctx, page, limit)
	if err != nil {
		return Sprints{}, err
	}
	out := make([]jira.Sprint, 0, len(all))
	for _, sp := range all {
		if slices.Contains(states, sp.State) {
			out = append(out, sp)
		}
	}
	slices.SortStableFunc(out, func(a, b jira.Sprint) int {
		return slices.Index(states, a.State) - slices.Index(states, b.State)
	})
	return Sprints{Open: out}, nil
}

// CardsQuery is one read of what a board shows.
type CardsQuery struct {
	Plan         Plan
	Projection   appquery.Projection
	QuickFilters []string
	PageSize     int
	SprintLimit  int
	// Probe asks for the active sprints again; otherwise Sprints and NoSprints
	// are what an earlier read answered.
	Probe     bool
	Sprints   []jira.Sprint
	NoSprints bool
	// Sprint is the sprint wanted among the active ones, the first when absent.
	Sprint int64
}

// Cards is what one read of a board answered. Page is empty when the board
// runs sprints and none is active.
type Cards struct {
	Page      jira.Page[jira.Issue]
	Missing   []string
	Fields    []string
	Sprints   []jira.Sprint
	Sprint    jira.Sprint
	NoSprints bool
}

// ReadCards fills a board through the read that applies the board's own saved
// filter and column mapping at the site. Nothing here composes a query: the
// filter behind a board is JQL only the site can run, and a board rebuilt out of
// its statuses is a different board.
//
// A board that runs sprints shows its active sprint and nothing else, so its
// cards are that sprint's; whether it runs them is the sprint read's answer and
// never the board's type, which docs/API-NOTES.md says nothing may branch on.
//
// It asks for the projection given, never for a wildcard, and it carries the
// board's sub-query and whichever of the board's own quick filters are toggled
// on, which are the two parts of a board the endpoint leaves to the caller.
// On a failure, failed names the read that went unanswered.
func ReadCards(ctx context.Context, s Site, search *appquery.Search, q CardsQuery) (out Cards, failed Step, err error) {
	wanted, err := search.Resolve(ctx, q.Projection)
	if err != nil {
		return Cards{}, StepIssues, err
	}
	out = Cards{Missing: wanted.Missing, Fields: wanted.IDs, Sprints: q.Sprints, NoSprints: q.NoSprints}
	if q.Probe {
		found, err := OpenSprints(ctx, s, q.Plan.BoardID, q.SprintLimit, jira.SprintActive)
		if err != nil {
			return Cards{}, StepSprints, err
		}
		out.Sprints, out.NoSprints = found.Open, found.None
	}
	query := jira.BoardQuery{
		Fields:       wanted.IDs,
		SubQuery:     q.Plan.SubQuery,
		QuickFilters: q.QuickFilters,
		MaxResults:   q.PageSize,
	}
	var page jira.Page[jira.Issue]
	switch {
	case out.NoSprints:
		page, err = s.BoardIssues(ctx, q.Plan.BoardID, query)
	case len(out.Sprints) == 0:
		return out, StepIdle, nil
	default:
		out.Sprint = PickSprint(out.Sprints, q.Sprint)
		page, err = s.SprintIssues(ctx, q.Plan.BoardID, out.Sprint.ID, query)
	}
	if err != nil {
		return Cards{}, StepIssues, err
	}
	out.Page = page
	return out, StepIdle, nil
}

// PickSprint is the sprint with the id wanted, or the first.
func PickSprint(sprints []jira.Sprint, want int64) jira.Sprint {
	for _, sp := range sprints {
		if sp.ID == want {
			return sp
		}
	}
	return sprints[0]
}

// SprintFieldName is the name Jira gives the sprint field whatever language the
// site is in: jira.ResolveField compares UntranslatedName first, and that one
// does not move with the locale. Nothing here writes down a customfield id.
const SprintFieldName = "Sprint"

// BacklogQuery is one whole load of a backlog. WantID is a board id the caller
// already believes it is drawing — from a stored snapshot or a board a project
// switch carried over — resolved against the boards the read answers with so a
// revalidation lands on the same board rather than always the first one the
// site lists. Zero means nothing was hinted, and At is used as it always was.
// Projection is what the issues are read with, given the sprint field and the
// board's configuration.
type BacklogQuery struct {
	Project     string
	At          int
	WantID      int64
	PageSize    int
	SprintLimit int
	Projection  func(sprint jira.FieldRef, config jira.BoardConfig) appquery.Projection
}

// Backlog is everything one read of a backlog answered with. No boards is an
// answer rather than a failure, and so is a site with no sprint field: both
// arrive with the rest of it empty.
type Backlog struct {
	Boards  []jira.Board
	BoardAt int
	Config  jira.BoardConfig
	Sprints []jira.Sprint
	Field   jira.FieldRef
	Page    jira.Page[jira.Issue]
	Missing []string
	// Fields is the ids the page was read with, which a single issue read back
	// after a create asks for too.
	Fields []string
	// NoSprints is the site's own sentence for a board that has none — a Kanban
	// board answers the sprint read with a 400 — and "" for a board that has
	// sprints, or none open. It is not a failure: the backlog is still read.
	NoSprints string
}

// BacklogSite is what a backlog load reads.
type BacklogSite interface {
	jira.BoardReader
	jira.SprintReader
}

// LoadBacklog is which boards the project has, the configuration of the one
// on screen, its open sprints, active first, and the issues in its backlog.
//
// The issues come from the read that asks the site what this board holds rather
// than from a query composed here: a board's saved filter is JQL only the site
// can run, so a set rebuilt out of the statuses its columns map is a different
// board. Which of them are unscheduled is still worked out by the caller, from
// the sprint value on each issue, because the port answers what a board holds
// and what a board's backlog is and nothing about one sprint.
//
// It is one call because each step decides the next: the rank field comes out
// of the board configuration and the projection comes out of that, so a fan-out
// would only be four requests waiting on each other anyway.
func LoadBacklog(ctx context.Context, s BacklogSite, search *appquery.Search, q BacklogQuery) (Backlog, error) {
	boards, err := ListBoards(ctx, s, q.Project)
	if err != nil {
		return Backlog{}, err
	}
	if len(boards) == 0 {
		return Backlog{}, nil
	}
	at := q.At
	if q.WantID != 0 {
		if idx, found := IndexOfBoard(boards, q.WantID); found {
			at = idx
		}
	}
	at = min(max(at, 0), len(boards)-1)
	config, err := Config(ctx, s, boards[at].ID)
	if err != nil {
		return Backlog{}, err
	}
	sprints, err := OpenSprints(ctx, s, boards[at].ID, q.SprintLimit, jira.SprintActive, jira.SprintFuture)
	if err != nil {
		return Backlog{}, err
	}
	catalogue, err := search.Fields(ctx)
	if err != nil {
		return Backlog{}, err
	}
	out := Backlog{Boards: boards, BoardAt: at, Config: config, Sprints: sprints.Open, NoSprints: sprints.Reason}
	field, ok := sprintField(catalogue)
	if !ok {
		return out, nil
	}
	out.Field = field
	wanted, err := search.Resolve(ctx, q.Projection(out.Field, config))
	if err != nil {
		return Backlog{}, err
	}
	page, err := s.BoardIssues(ctx, boards[at].ID, jira.BoardQuery{
		Fields:     wanted.IDs,
		SubQuery:   config.SubQuery,
		MaxResults: q.PageSize,
	})
	if err != nil {
		return Backlog{}, err
	}
	out.Page, out.Missing, out.Fields = page, wanted.Missing, wanted.IDs
	return out, nil
}

func sprintField(catalogue []jira.Field) (jira.FieldRef, bool) {
	field, err := jira.ResolveField(catalogue, SprintFieldName)
	return field.Ref(), err == nil
}

// NextPage writes the page in hand before reading the next, so a walk's pages
// reach the cache in order. stored is what the write said, and err the read.
func NextPage(ctx context.Context, page jira.Page[jira.Issue], put func() error) (next jira.Page[jira.Issue], stored, err error) {
	if put != nil {
		stored = put()
	}
	next, err = page.Next(ctx)
	return next, stored, err
}

// Reread reads one issue back by the fields it was drawn with. It does not
// search: the index trails a write.
func Reread(ctx context.Context, r jira.IssueReader, key string, fields []string) (jira.Issue, error) {
	return r.IssueFields(ctx, key, fields)
}
