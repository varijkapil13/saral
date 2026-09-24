package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.Worklogger = (*Client)(nil)

// worklogPageSize is the page length a worklog read asks for. The endpoint
// caps it at 5000, and an issue with more than fifty entries is rare enough
// that a second page is cheaper than a large first one for every issue.
const worklogPageSize = 50

func worklogPath(key string) string { return issuePath + "/" + url.PathEscape(key) + "/worklog" }

// apiWorklogPage is the worklog envelope: an offset page like the Agile API's,
// whose array is called worklogs and not values, so the shared envelope would
// decode it as empty.
type apiWorklogPage struct {
	StartAt    int          `json:"startAt"`
	MaxResults int          `json:"maxResults"`
	Total      *int         `json:"total"`
	Worklogs   []apiWorklog `json:"worklogs"`
}

type apiWorklog struct {
	ID               flexString      `json:"id"`
	IssueID          flexString      `json:"issueId"`
	Author           *apiUser        `json:"author"`
	UpdateAuthor     *apiUser        `json:"updateAuthor"`
	Comment          *adf.Doc        `json:"comment"`
	Started          timestamp       `json:"started"`
	TimeSpentSeconds int64           `json:"timeSpentSeconds"`
	Created          timestamp       `json:"created"`
	Updated          timestamp       `json:"updated"`
	Visibility       json.RawMessage `json:"visibility"`
}

type apiWorklogWrite struct {
	Started          string   `json:"started"`
	TimeSpentSeconds int64    `json:"timeSpentSeconds"`
	Comment          *adf.Doc `json:"comment,omitempty"`
}

func (w apiWorklog) domain() jira.Worklog {
	out := jira.Worklog{
		ID:      string(w.ID),
		IssueID: string(w.IssueID),
		Started: w.Started.Time,
		Spent:   time.Duration(w.TimeSpentSeconds) * time.Second,
		Created: w.Created.Time,
		Updated: w.Updated.Time,
	}
	if w.Author != nil {
		out.Author = w.Author.domain()
	}
	if w.UpdateAuthor != nil {
		updated := w.UpdateAuthor.domain()
		out.UpdateAuthor = &updated
	}
	if w.Comment != nil {
		out.Comment = *w.Comment
	}
	out.Visibility = decodeVisibility(w.Visibility)
	return out
}

// Worklogs lists the time logged on an issue, oldest first, one offset page at
// a time.
func (c *Client) Worklogs(ctx context.Context, key string) (jira.Page[jira.Worklog], error) {
	id, err := issueKey(key)
	if err != nil {
		return jira.Page[jira.Worklog]{}, err
	}
	path := worklogPath(id)
	op := http.MethodGet + " " + path
	return offsetPages(ctx, c, func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(url.Values{}, startAt, worklogPageSize),
			kind:   "issue",
			id:     id,
		}
	}, func(resp *response) ([]jira.Worklog, int, bool, error) {
		var page apiWorklogPage
		if err := resp.decode(op, &page); err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.Worklog, 0, len(page.Worklogs))
		for i := range page.Worklogs {
			out = append(out, page.Worklogs[i].domain())
		}
		total := -1
		if page.Total != nil {
			total = *page.Total
		}
		return out, total, total < 0 && len(page.Worklogs) < page.MaxResults, nil
	})
}

// AddWorklog logs time on an issue.
//
// The time goes out as timeSpentSeconds. timeSpent, the "1d 2h" form beside it,
// reads a day and a week as however long the site's time tracking settings say
// they are, so the same string logs different amounts on two sites. The
// remaining estimate is adjusted the site's default way, which is to take the
// logged time off it.
func (c *Client) AddWorklog(ctx context.Context, key string, in jira.WorklogInput) (jira.Worklog, error) {
	id, err := issueKey(key)
	if err != nil {
		return jira.Worklog{}, err
	}
	seconds := int64(in.Spent / time.Second)
	switch {
	case seconds <= 0:
		return jira.Worklog{}, invalidField("timeSpentSeconds", "a worklog needs a positive amount of time")
	case in.Started.IsZero():
		return jira.Worklog{}, invalidField("started", "a worklog needs the time the work started")
	}
	body := apiWorklogWrite{
		Started:          in.Started.Format(platformTimeLayout),
		TimeSpentSeconds: seconds,
	}
	if !in.Comment.IsZero() {
		comment := in.Comment
		body.Comment = &comment
	}
	var stored apiWorklog
	err = c.doJSON(ctx, request{
		method: http.MethodPost,
		path:   worklogPath(id),
		body:   body,
		kind:   "issue",
		id:     id,
	}, &stored)
	if err != nil {
		return jira.Worklog{}, err
	}
	return stored.domain(), nil
}
