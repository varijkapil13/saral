package board

import (
	"strings"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Column is one column of a board as the site defines it. Statuses is the
// whole of what decides which issues belong in it: a status is matched by the id
// the site minted, never by the name it shows, because two distinct statuses on
// one site can share a display name and every name is translated on a site in
// another language.
type Column struct {
	Name     string
	Statuses []string
	Min, Max *int
}

// Plan is a board configuration resolved into what a board draws from. It is
// built once per configuration read, so nothing about a board is worked out
// again per frame or per issue.
type Plan struct {
	BoardID int64
	Name    string
	Kind    jira.BoardType
	Columns []Column
	// ByStatus is the status id a column is reached by. An id absent from it
	// belongs to no column, which is a status the board does not map and an
	// issue the board does not show.
	ByStatus map[string]int
	// Estimate is the field the board measures issues in, and Estimates says
	// whether it measures at all. A nil Estimation is a board that does not
	// estimate; EstimationNone is a Scrum board that turned it off. Neither has
	// a field, and BoardConfig.Estimates is what tells them from a board that
	// does.
	Estimate  jira.FieldRef
	Estimates bool
	Ordering  jira.Ordering
	// SubQuery is the Kanban-only condition deciding which resolved issues the
	// board still shows, and it is empty on a Scrum board. It travels with the
	// read because the endpoint that applies a board's filter does not apply
	// this: without it the done column is every issue the project ever finished.
	SubQuery string
	// Constraint is what the columns' min and max count, and whether they are
	// limits at all: a board keeps both numbers after turning its limits off.
	Constraint jira.ColumnConstraint
}

// NewPlan resolves a board configuration.
func NewPlan(cfg jira.BoardConfig) Plan {
	p := Plan{
		BoardID:  cfg.BoardID,
		Name:     cfg.Name,
		Kind:     cfg.Type,
		Columns:  make([]Column, 0, len(cfg.Columns)),
		ByStatus: make(map[string]int, len(cfg.Columns)*4),
		Ordering: cfg.Ordering(),
		SubQuery: strings.TrimSpace(cfg.SubQuery),

		Constraint: cfg.Constraint,
	}
	if cfg.Estimates() {
		p.Estimate, p.Estimates = cfg.Estimation.Field, true
	}
	for _, col := range cfg.Columns {
		at := len(p.Columns)
		kept := make([]string, 0, len(col.StatusIDs))
		for _, id := range col.StatusIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, taken := p.ByStatus[id]; taken {
				continue
			}
			p.ByStatus[id] = at
			kept = append(kept, id)
		}
		p.Columns = append(p.Columns, Column{Name: col.Name, Statuses: kept, Min: col.Min, Max: col.Max})
	}
	return p
}

// ColumnOf is the column a status belongs to.
func (p Plan) ColumnOf(statusID string) (int, bool) {
	at, ok := p.ByStatus[statusID]
	return at, ok
}

// Projection is what a card needs: a list row's fields, the two more the
// filter picker's own facets need beyond that (reporter and labels — the
// other four are already in ListProjection), the parent a swimlane groups by
// and the project a create lands in, plus the estimation field when the board
// has one. The id comes from the board configuration, so no customfield is
// written down and a board that does not estimate asks for nothing extra.
func (p Plan) Projection() appquery.Projection {
	proj := appquery.ListProjection().With("reporter", "labels", "parent", "project")
	if !p.Estimates {
		return proj
	}
	return proj.With(p.Estimate.ID)
}
