package cloud

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	vocabPriorityPath = "/rest/api/3/priority/search"
	vocabLabelPath    = "/rest/api/3/label"
)

// vocabPriorityBound is how many priorities a walk will take before it stops. A
// site has a handful; the bound is there so that a paged endpoint answering
// something unexpected cannot become an unbounded read on a first-paint path.
const vocabPriorityBound = 500

// vocabLabelPage is the page size the label endpoint is asked for. It is also
// its own default, which is worth sending anyway: a walk whose page size comes
// from the site changes length when the site does.
const vocabLabelPage = 1000

func vocabProjectStatusesPath(projectKey string) string {
	return "/rest/api/3/project/" + url.PathEscape(projectKey) + "/statuses"
}

// IssueTypeStatuses lists each of a project's issue types with the statuses its
// workflow can reach.
//
// The endpoint answers a bare array — no envelope, no paging — of issue types
// each carrying its own statuses, so two types in one project can and do differ.
// Each status arrives with its category nested inside it, which is the copy to
// read: the name beside it is localised and, in a team-managed project, is
// reused by a project-scoped status with a different id.
func (c *Client) IssueTypeStatuses(ctx context.Context, projectKey string) ([]jira.IssueTypeStatuses, error) {
	project := strings.TrimSpace(projectKey)
	if project == "" {
		return nil, &jira.ValidationError{Fields: []jira.FieldError{{
			Field:   "projectKey",
			Message: "a project is required: statuses are per project, and there is no site-wide answer to give",
		}}}
	}

	var body []vocabIssueTypeStatuses
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   vocabProjectStatusesPath(project),
		kind:   "project",
		id:     project,
	}, &body)
	if err != nil {
		return nil, err
	}
	out := make([]jira.IssueTypeStatuses, 0, len(body))
	for _, entry := range body {
		out = append(out, entry.domain())
	}
	return out, nil
}

// Priorities lists the site's priorities in the order they rank in.
//
// It reads the paged /priority/search rather than the bare-array /priority,
// which Atlassian deprecated. Both were seen answering; the paged one is the one
// with a future.
func (c *Client) Priorities(ctx context.Context) ([]jira.Priority, error) {
	page, err := offsetPages(ctx, c, func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   vocabPriorityPath,
			query:  pagedQuery(nil, startAt, 0),
			kind:   "the site's priorities",
			id:     vocabPriorityPath,
		}
	}, func(resp *response) ([]jira.Priority, int, bool, error) {
		rows, total, isLast, err := decodeAgilePage[vocabPriority](resp, http.MethodGet+" "+vocabPriorityPath)
		if err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.Priority, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.domain())
		}
		return out, total, isLast, nil
	})
	if err != nil {
		return nil, err
	}
	return jira.Collect(ctx, page, vocabPriorityBound)
}

// Labels lists every label in use on the site.
//
// The endpoint takes no query. One sent anyway is accepted and ignored — the
// answer to a narrowed request was byte-identical to the unnarrowed one — so
// nothing is sent, and a caller filtering by what somebody typed filters what it
// walked rather than asking the site to.
//
// The values are bare strings, not objects, and they are whatever anyone has
// ever typed: they carry non-ASCII, and a width taken with len() over one is
// wrong.
func (c *Client) Labels(ctx context.Context) (jira.Page[string], error) {
	return offsetPages(ctx, c, func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   vocabLabelPath,
			query:  pagedQuery(nil, startAt, vocabLabelPage),
			kind:   "the site's labels",
			id:     vocabLabelPath,
		}
	}, func(resp *response) ([]string, int, bool, error) {
		return decodeAgilePage[string](resp, http.MethodGet+" "+vocabLabelPath)
	})
}

type vocabIssueTypeStatuses struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Subtask  bool          `json:"subtask"`
	IconURL  string        `json:"iconUrl"`
	Statuses []vocabStatus `json:"statuses"`
}

func (t vocabIssueTypeStatuses) domain() jira.IssueTypeStatuses {
	out := jira.IssueTypeStatuses{
		Type: jira.IssueType{ID: t.ID, Name: t.Name, Subtask: t.Subtask, IconURL: t.IconURL},
	}
	out.Statuses = make([]jira.Status, 0, len(t.Statuses))
	for _, status := range t.Statuses {
		out.Statuses = append(out.Statuses, status.domain())
	}
	return out
}

// vocabStatus is a status on a project's issue type. Its iconUrl is not read at
// all: a status created through the API carries the bare site root there and one
// a template made carries a placeholder, so no value of it renders anything.
type vocabStatus struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category *struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

func (s vocabStatus) domain() jira.Status {
	out := jira.Status{ID: s.ID, Name: s.Name}
	if s.Category != nil {
		out.Category = jira.ParseStatusCategory(s.Category.Key)
	}
	return out
}

type vocabPriority struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (p vocabPriority) domain() jira.Priority {
	return jira.Priority{ID: p.ID, Name: p.Name}
}
