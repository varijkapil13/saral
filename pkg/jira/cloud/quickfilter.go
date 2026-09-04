package cloud

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/varijkapil13/saral/pkg/jira"
)

// quickFilterPageSize is how many quick filters one request asks for. A board
// with more than a handful is unusual, so this is generous rather than tuned.
const quickFilterPageSize = 50

// quickFilterBound is how many quick filters a walk will take before it stops,
// for the reason boardBound exists: an endpoint answering something unexpected
// must not become an unbounded read on a first-paint path.
const quickFilterBound = 200

func quickFilterPath(boardID int64) string {
	return boardPath + "/" + strconv.FormatInt(boardID, 10) + "/quickfilter"
}

// QuickFilters lists a board's own quick filters, in the order the board draws
// them. Each is JQL meant for BoardQuery.QuickFilters and nothing else — see
// that field's doc in pkg/jira/types.go.
func (c *Client) QuickFilters(ctx context.Context, boardID int64) ([]jira.QuickFilter, error) {
	if err := boardIDCheck(boardID); err != nil {
		return nil, err
	}
	path := quickFilterPath(boardID)
	id := strconv.FormatInt(boardID, 10)
	op := http.MethodGet + " " + path
	page, err := offsetPages(ctx, c, func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(url.Values{}, startAt, quickFilterPageSize),
			kind:   "board",
			id:     id,
		}
	}, func(resp *response) ([]jira.QuickFilter, int, bool, error) {
		rows, total, isLast, err := decodeAgilePage[apiQuickFilter](resp, op)
		if err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.QuickFilter, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.domain())
		}
		return out, total, isLast, nil
	})
	if err != nil {
		return nil, boardRefusal(err)
	}
	filters, err := jira.Collect(ctx, page, quickFilterBound)
	if err != nil {
		return nil, boardRefusal(err)
	}
	return filters, nil
}

// apiQuickFilter is one row of the quick filter collection.
type apiQuickFilter struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	JQL         string `json:"jql"`
	Description string `json:"description"`
	Position    int    `json:"position"`
}

func (f apiQuickFilter) domain() jira.QuickFilter {
	return jira.QuickFilter{
		ID:          f.ID,
		Name:        f.Name,
		JQL:         f.JQL,
		Description: f.Description,
		Position:    f.Position,
	}
}
