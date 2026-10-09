package move

import (
	"context"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// candidatesLimit is how many issues the project suggestions read. It is a page
// and not a walk: the answer is a handful of keys, and paging further only finds
// projects the account has not touched recently.
const candidatesLimit = 50

// Candidates reads the projects behind the account's own recent issues, which is
// the only way to offer a target: /project/search is not on the port, so nothing
// here can enumerate projects and a page of issues is what is left. The projects
// the issues being moved are already in are left out: a move to the project they
// are in is not a move.
func Candidates(ctx context.Context, client appquery.SearchClient, moving []jira.Issue) ([]string, error) {
	search := appquery.NewSearch(client)
	projection := appquery.Projection{Name: "move target", IDs: []string{"project"}}
	// The account's own work first, then anything this token can see at all:
	// a session whose user has nothing assigned would otherwise be offered
	// nothing and have to type a key from memory.
	for _, jql := range []string{"assignee = currentUser() ORDER BY updated DESC", "ORDER BY updated DESC"} {
		result, err := search.Run(ctx, appquery.Request{
			JQL:        jql,
			Projection: projection,
			MaxResults: candidatesLimit,
		})
		if err != nil {
			return nil, err
		}
		if keys := distinctProjects(result.Page.Items); len(keys) > 0 {
			return without(keys, moving), nil
		}
	}
	return without(nil, moving), nil
}

// distinctProjects keeps the order the issues came back in, which is the order
// the query sorted them by and therefore the order worth offering.
func distinctProjects(issues []jira.Issue) []string {
	seen := make(map[string]bool, len(issues))
	out := make([]string, 0, 4)
	for i := range issues {
		key := issues[i].Project.Key
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func without(keys []string, moving []jira.Issue) []string {
	held := make(map[string]bool, 2)
	for i := range moving {
		held[moving[i].Project.Key] = true
	}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if !held[key] {
			out = append(out, key)
		}
	}
	return out
}

// Vocabulary is the target project's issue types and the statuses each one's
// workflow reaches. It is also what proves the project key is real: a key this
// token cannot see answers with a refusal instead.
func Vocabulary(ctx context.Context, vocab jira.FilterVocabulary, project string) ([]jira.IssueTypeStatuses, error) {
	return vocab.IssueTypeStatuses(ctx, project)
}

// Schema reads what the target insists on for one issue type. Which fields are
// mandatory is site configuration, so it is asked for the project and type that
// were actually chosen and never assumed.
func Schema(ctx context.Context, reader jira.SchemaReader, project, typeID string) (jira.Schema, error) {
	return reader.CreateMeta(ctx, project, typeID)
}

// Submit hands the move to the bulk queue. What comes back is a task and not an
// outcome: the issues have not moved when this returns.
func Submit(ctx context.Context, mover jira.Relocator, in jira.MoveRequest) (jira.TaskRef, error) {
	return mover.BulkMove(ctx, in)
}
