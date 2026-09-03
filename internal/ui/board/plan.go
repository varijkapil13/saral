package board

import (
	"strings"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// planColumn is one column of a board as the site defines it. StatusIDs is the
// whole of what decides which issues belong in it: a status is matched by the id
// the site minted, never by the name it shows, because two distinct statuses on
// one site can share a display name and every name is translated on a site in
// another language.
type planColumn struct {
	name     string
	statuses []string
	min, max *int
}

// plan is a board configuration resolved into what this view draws from. It is
// built once per configuration read, so nothing about a board is worked out
// again per frame or per issue.
type plan struct {
	boardID int64
	name    string
	kind    jira.BoardType
	columns []planColumn
	// byStatus is the status id a column is reached by. An id absent from it
	// belongs to no column, which is a status the board does not map and an
	// issue the board does not show.
	byStatus map[string]int
	// estimate is the field the board measures issues in, and estimates says
	// whether it measures at all. A nil Estimation is a board that does not
	// estimate; EstimationNone is a Scrum board that turned it off. Neither has
	// a field, and BoardConfig.Estimates is what tells them from a board that
	// does.
	estimate  jira.FieldRef
	estimates bool
	ordering  jira.Ordering
	// subQuery is the Kanban-only condition deciding which resolved issues the
	// board still shows, and it is empty on a Scrum board. It travels with the
	// read because the endpoint that applies a board's filter does not apply
	// this: without it the done column is every issue the project ever finished.
	subQuery string
}

func newPlan(cfg jira.BoardConfig) plan {
	p := plan{
		boardID:  cfg.BoardID,
		name:     cfg.Name,
		kind:     cfg.Type,
		columns:  make([]planColumn, 0, len(cfg.Columns)),
		byStatus: make(map[string]int, len(cfg.Columns)*4),
		ordering: cfg.Ordering(),
		subQuery: strings.TrimSpace(cfg.SubQuery),
	}
	if cfg.Estimates() {
		p.estimate, p.estimates = cfg.Estimation.Field, true
	}
	for _, col := range cfg.Columns {
		at := len(p.columns)
		kept := make([]string, 0, len(col.StatusIDs))
		for _, id := range col.StatusIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, taken := p.byStatus[id]; taken {
				continue
			}
			p.byStatus[id] = at
			kept = append(kept, id)
		}
		p.columns = append(p.columns, planColumn{name: col.Name, statuses: kept, min: col.Min, max: col.Max})
	}
	return p
}

// columnOf is the column a status belongs to.
func (p plan) columnOf(statusID string) (int, bool) {
	at, ok := p.byStatus[statusID]
	return at, ok
}

// projection is what a card needs: a list row's fields, the two more the
// filter picker's own facets need beyond that (reporter and labels — the
// other four are already in ListProjection), plus the estimation field when
// the board has one. The id comes from the board configuration, so no
// customfield is written down and a board that does not estimate asks for
// nothing extra.
func (p plan) projection() app.Projection {
	proj := app.ListProjection().With("reporter", "labels")
	if !p.estimates {
		return proj
	}
	return proj.With(p.estimate.ID)
}

// orderWords says how the board decides the order in a column, which is a
// property of the board and not of this session: a board with a rank field
// ranks, and one without shows whatever its filter sorted by.
func (p plan) orderWords() string {
	if p.ordering == jira.OrderRank {
		return "ranked"
	}
	return "ordered by its filter"
}
