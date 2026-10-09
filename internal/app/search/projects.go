package search

import (
	"context"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// RecentProjects reads the projects behind this account's own recent issues, and
// then anything it can see at all, reading at most limit issues per query. Both
// queries ask for one field.
//
// The port exposes no project-list method, so a narrow read is the only answer
// there is.
func RecentProjects(ctx context.Context, s *appquery.Search, limit int) ([]jira.ProjectRef, error) {
	projection := appquery.Projection{Name: "project picker", IDs: []string{"project"}}
	for _, jql := range []string{"assignee = currentUser() ORDER BY updated DESC", "ORDER BY updated DESC"} {
		result, err := s.Run(ctx, appquery.Request{JQL: jql, Projection: projection, MaxResults: limit})
		if err != nil {
			return nil, err
		}
		if found := projectsDistinct(result.Page.Items); len(found) > 0 {
			return found, nil
		}
	}
	return nil, nil
}

// projectsDistinct keeps the order the issues came back in, which is the order
// the query sorted them by and therefore the order worth offering.
func projectsDistinct(issues []jira.Issue) []jira.ProjectRef {
	seen := make(map[string]bool, len(issues))
	out := make([]jira.ProjectRef, 0, 4)
	for i := range issues {
		ref := issues[i].Project
		if ref.Key == "" || seen[ref.Key] {
			continue
		}
		seen[ref.Key] = true
		out = append(out, ref)
	}
	return out
}
