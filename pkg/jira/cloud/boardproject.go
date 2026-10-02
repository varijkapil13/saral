package cloud

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/varijkapil13/saral/pkg/jira"
)

const boardProjectPageSize = 50

const boardProjectBound = 200

var _ jira.BoardProjectReader = (*Client)(nil)

func boardProjectPath(boardID int64) string {
	return boardPath + "/" + strconv.FormatInt(boardID, 10) + "/project"
}

// BoardProjects lists a board's projects; a board the token cannot view answers 200 with none, so empty proves nothing.
func (c *Client) BoardProjects(ctx context.Context, boardID int64) ([]jira.ProjectRef, error) {
	if err := boardIDCheck(boardID); err != nil {
		return nil, err
	}
	path := boardProjectPath(boardID)
	id := strconv.FormatInt(boardID, 10)
	op := http.MethodGet + " " + path
	page, err := offsetPages(ctx, c, func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(url.Values{}, startAt, boardProjectPageSize),
			kind:   "board",
			id:     id,
		}
	}, func(resp *response) ([]jira.ProjectRef, int, bool, error) {
		rows, total, isLast, err := decodeAgilePage[apiBoardProject](resp, op)
		if err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.ProjectRef, 0, len(rows))
		for _, row := range rows {
			if row.ID == "" && row.Key == "" {
				continue
			}
			out = append(out, jira.ProjectRef{ID: string(row.ID), Key: row.Key, Name: row.Name})
		}
		return out, total, isLast, nil
	})
	if err != nil {
		return nil, boardRefusal(err)
	}
	projects, err := jira.Collect(ctx, page, boardProjectBound+1)
	if err != nil {
		return nil, boardRefusal(err)
	}
	if len(projects) > boardProjectBound {
		return nil, &jira.ValidationError{Messages: []string{fmt.Sprintf(
			"board %s spans more than %d projects, which is more than this reads in one answer",
			id, boardProjectBound)}}
	}
	return projects, nil
}

type apiBoardProject struct {
	ID   flexString `json:"id"`
	Key  string     `json:"key"`
	Name string     `json:"name"`
}
