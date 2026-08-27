package backlog

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// loadedMsg carries everything one read of a board answered with. No boards is
// an answer rather than a failure, and so is a site with no sprint field: both
// arrive here with the rest of it empty.
type loadedMsg struct {
	gen     int
	boards  []jira.Board
	boardAt int
	config  jira.BoardConfig
	sprints []jira.Sprint
	field   jira.FieldRef
	page    jira.Page[jira.Issue]
	missing []string
}

// pagedMsg carries the page after the one already in hand.
type pagedMsg struct {
	gen  int
	page jira.Page[jira.Issue]
}

// movedMsg is one chunk of a move the site accepted.
type movedMsg struct {
	gen   int
	at    int
	moved int
}

// moveFailedMsg is the chunk a move stopped on. The chunks before it moved, and
// at is which one refused, so the view can say how much of the selection is
// still where it was.
type moveFailedMsg struct {
	gen int
	at  int
	err error
}

// failedMsg is a read that brought nothing back. The error travels whole so
// that a refusal reaches the user in the words the site used.
type failedMsg struct {
	gen int
	err error
}

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	if cancel == nil {
		return cmd
	}
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}

// sprintFieldName is the name Jira gives the sprint field whatever language the
// site is in: jira.ResolveField compares UntranslatedName first, and that one
// does not move with the locale. Nothing here writes down a customfield id.
const sprintFieldName = "Sprint"

// read is one whole load of a backlog: which boards the project has, the
// configuration of the one on screen, its open sprints, and the issues in its
// backlog.
//
// The issues come from the read that asks the site what this board holds rather
// than from a query composed here: a board's saved filter is JQL only the site
// can run, so a set rebuilt out of the statuses its columns map is a different
// board. Which of them are unscheduled is still worked out here, from the sprint
// value on each issue, because the port answers what a board holds and what a
// board's backlog is and nothing about one sprint — and a section that listed
// only the backlog would have to say "0 issues" about a sprint holding twenty.
//
// It is one command because each step decides the next: the rank field comes out
// of the board configuration and the projection comes out of that, so a fan-out
// would only be four requests waiting on each other anyway.
func read(ctx context.Context, s site, search *app.Search, project string, at, gen int) tea.Cmd {
	return func() tea.Msg {
		boards, err := s.Boards(ctx, project)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		if len(boards) == 0 {
			return loadedMsg{gen: gen}
		}
		at = min(max(at, 0), len(boards)-1)
		config, err := s.BoardConfig(ctx, boards[at].ID)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		sprints, err := openSprints(ctx, s, boards[at].ID)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		catalogue, err := search.Fields(ctx)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		out := loadedMsg{gen: gen, boards: boards, boardAt: at, config: config, sprints: sprints}
		field, err := jira.ResolveField(catalogue, sprintFieldName)
		if err != nil {
			return out
		}
		out.field = field.Ref()
		// The rank field is named by the board configuration, by id, so it is
		// added to the projection rather than looked up by a name.
		projection := app.ListProjection().With(out.field.ID)
		if config.RankFieldID != "" {
			projection = projection.With(config.RankFieldID)
		}
		wanted, err := search.Resolve(ctx, projection)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		page, err := s.BoardIssues(ctx, boards[at].ID, jira.BoardQuery{
			Fields:     wanted.IDs,
			SubQuery:   config.SubQuery,
			MaxResults: pageSize,
		})
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		out.page, out.missing = page, wanted.Missing
		return out
	}
}

func nextPage(ctx context.Context, page jira.Page[jira.Issue], gen int) tea.Cmd {
	return func() tea.Msg {
		next, err := page.Next(ctx)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return pagedMsg{gen: gen, page: next}
	}
}

// moveInto moves one chunk. A sprint id of zero is the backlog, which is its own
// endpoint rather than a sprint with no number.
func moveInto(ctx context.Context, mgr jira.SprintManager, sprintID int64, keys []string, at, gen int) tea.Cmd {
	return func() tea.Msg {
		var err error
		if sprintID == 0 {
			err = mgr.MoveToBacklog(ctx, keys)
		} else {
			err = mgr.MoveToSprint(ctx, sprintID, keys)
		}
		if err != nil {
			return moveFailedMsg{gen: gen, at: at, err: err}
		}
		return movedMsg{gen: gen, at: at, moved: len(keys)}
	}
}

// openSprints is the sprints a backlog can move issues into: the active ones
// first, then the future ones. The states are asked for and checked again, since
// a board with years of history behind it is a walk nothing on this path should
// be doing and an adapter that ignored the filter would hand back all of it.
func openSprints(ctx context.Context, r jira.SprintReader, boardID int64) ([]jira.Sprint, error) {
	page, err := r.Sprints(ctx, boardID, jira.SprintActive, jira.SprintFuture)
	if err != nil {
		return nil, err
	}
	all, err := jira.Collect(ctx, page, sprintLimit)
	if err != nil {
		return nil, err
	}
	out := make([]jira.Sprint, 0, len(all))
	for _, sp := range all {
		if sp.State == jira.SprintActive || sp.State == jira.SprintFuture {
			out = append(out, sp)
		}
	}
	slices.SortStableFunc(out, func(a, b jira.Sprint) int {
		return stateOrder(a.State) - stateOrder(b.State)
	})
	return out, nil
}

func stateOrder(s jira.SprintState) int {
	if s == jira.SprintActive {
		return 0
	}
	return 1
}

// sprintIDsIn reads the ids out of the json shape of a sprint value.
func sprintIDsIn(text string) []int64 {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") {
		return nil
	}
	var wire []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wire); err != nil {
		return nil
	}
	out := make([]int64, 0, len(wire))
	for _, one := range wire {
		if one.ID != 0 {
			out = append(out, one.ID)
		}
	}
	return out
}
