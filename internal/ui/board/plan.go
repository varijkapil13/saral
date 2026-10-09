package board

import (
	"slices"

	appboard "github.com/varijkapil13/saral/internal/app/board"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
	"github.com/varijkapil13/saral/pkg/jira"
)

// projectionFor is the plan's projection, widened by what a roomy card draws
// beyond it while the look is roomy. Labels are already in it.
func projectionFor(p appboard.Plan, look card.Look) appquery.Projection {
	proj := p.Projection()
	if look != card.Roomy {
		return proj
	}
	for _, id := range card.RoomyFields {
		if !slices.Contains(proj.IDs, id) {
			proj = proj.With(id)
		}
	}
	return proj
}

// orderWords says how the board decides the order in a column, which is a
// property of the board and not of this session: a board with a rank field
// ranks, and one without shows whatever its filter sorted by.
func orderWords(p appboard.Plan) string {
	if p.Ordering == jira.OrderRank {
		return "ranked"
	}
	return "ordered by its filter"
}
