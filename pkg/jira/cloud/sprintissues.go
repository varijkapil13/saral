package cloud

import (
	"context"
	"strconv"

	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.SprintIssueReader = (*Client)(nil)

func boardSprintIssuesPath(boardID, sprintID int64) string {
	return boardPath + "/" + strconv.FormatInt(boardID, 10) + "/sprint/" + strconv.FormatInt(sprintID, 10) + "/issue"
}

// SprintIssues lists what a board shows of one sprint.
//
// It is the board's route to a sprint rather than /sprint/{id}/issue, because
// what the board draws is its filter narrowed to the sprint: an issue in the
// sprint that the board's filter does not match is on another board's sprint
// view and not on this one. The rest is BoardIssues — the column mapping is
// applied at the site, the order is rank order, and the sub-query is the
// caller's to send.
//
// A sprint id comes from Sprints on the same board. A board with no sprints
// refuses that listing with a 400, which is how a caller learns there is no
// sprint to ask for without reading the board's type.
func (c *Client) SprintIssues(ctx context.Context, boardID, sprintID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	if err := sprintIDCheck(sprintID); err != nil {
		return jira.Page[jira.Issue]{}, err
	}
	return c.boardIssuePages(ctx, boardSprintIssuesPath(boardID, sprintID), boardID, q)
}
