package search

import (
	"context"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// TextPageSize is how many issues one page of a text search asks for.
const TextPageSize = 50

const textOrderByUpdated = " ORDER BY updated DESC"

// TextSearch runs free-text searches and key lookups for the search view.
type TextSearch struct {
	search *appquery.Search
	reader jira.IssueReader
}

// NewTextSearch returns a TextSearch over s; reader may be nil when keys are not looked up.
func NewTextSearch(s *appquery.Search, reader jira.IssueReader) *TextSearch {
	return &TextSearch{search: s, reader: reader}
}

func textProjection() appquery.Projection { return appquery.ListProjection().With("project") }

// Compose turns a text query into JQL, narrowed to project when inProject is set and project is not empty.
func Compose(tq jira.TextQuery, inProject bool, project string) (string, bool) {
	if tq.Empty() {
		return "", false
	}
	clause := tq.Clause(jira.TextAll)
	if inProject && project != "" {
		return "project = " + jira.QuoteJQL(project) + " AND " + clause + textOrderByUpdated, true
	}
	return clause + textOrderByUpdated, true
}

// Run fetches the first page of jql.
func (t *TextSearch) Run(ctx context.Context, jql string) (appquery.Result, error) {
	return t.search.Run(ctx, appquery.Request{JQL: jql, Projection: textProjection(), MaxResults: TextPageSize})
}

// Key reads one issue by key with the fields a result row shows.
func (t *TextSearch) Key(ctx context.Context, key string) (jira.Issue, error) {
	return t.reader.IssueFields(ctx, key, textProjection().IDs)
}

// Next fetches the page after page.
func (t *TextSearch) Next(ctx context.Context, page jira.Page[jira.Issue]) (jira.Page[jira.Issue], error) {
	return page.Next(ctx)
}

// Invalidate drops the cached field catalogue.
func (t *TextSearch) Invalidate() { t.search.Invalidate() }
