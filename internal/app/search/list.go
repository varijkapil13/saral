package search

import (
	"context"
	"slices"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// listPageSize is how many issues one request asks for. /search/jql caps what
// it will send anyway; the number that matters is that it is one screen's worth
// several times over, so paging is invisible while scrolling.
const listPageSize = 50

// Lister reads an issue list page by page and keeps what it read in the cache,
// so that the next session draws these rows before it asks the site anything.
type Lister struct {
	search *appquery.Search
	cache  appcache.Cache
	reader jira.IssueReader
}

// NewLister builds a Lister. A nil client is a session with no site to ask, and
// a nil cache is one with nowhere to keep rows.
func NewLister(client appquery.SearchClient, cache appcache.Cache, reader jira.IssueReader) *Lister {
	l := &Lister{cache: cache, reader: reader}
	if client != nil {
		l.search = appquery.NewSearch(client)
	}
	return l
}

// Fetched is what one read brought back. Stored is the error from writing it
// to the cache: the read worked, and a cache that could not be written is worth
// a warning and nothing more, so it travels beside the rows rather than
// replacing them.
type Fetched struct {
	// Issues is every row a Reload walked; First and Next leave it empty.
	Issues  []jira.Issue
	Page    jira.Page[jira.Issue]
	Missing []string
	Stored  error
}

// Live reports whether there is a site to ask.
func (l *Lister) Live() bool { return l.search != nil }

func listRequest(jql string, proj appquery.Projection) appquery.Request {
	return appquery.Request{JQL: jql, Projection: proj, MaxResults: listPageSize}
}

// ListProjection is the fields a list row asks for, plus extra.
func ListProjection(extra ...string) appquery.Projection {
	p := appquery.ListProjection()
	if len(extra) > 0 {
		p = p.With(extra...)
	}
	return p
}

// First fetches the first page of a query.
func (l *Lister) First(ctx context.Context, jql string, proj appquery.Projection) (Fetched, error) {
	res, err := l.search.Run(ctx, listRequest(jql, proj))
	if err != nil {
		return Fetched{}, err
	}
	return Fetched{
		Page: res.Page, Missing: res.Missing,
		Stored: l.Store(jql, res.Page.Items, res.Page.HasMore()),
	}, nil
}

// Next fetches the page after the one in hand. The rows already on screen come
// with it so that what is stored is the whole of what the user has scrolled
// through, not just its last page.
func (l *Lister) Next(ctx context.Context, jql string, have []jira.Issue, page jira.Page[jira.Issue]) (Fetched, error) {
	next, err := page.Next(ctx)
	if err != nil {
		return Fetched{}, err
	}
	whole := make([]jira.Issue, 0, len(have)+len(next.Items))
	whole = append(append(whole, have...), next.Items...)
	return Fetched{Page: next, Stored: l.Store(jql, whole, next.HasMore())}, nil
}

// Reload re-reads the rows a list already has, walking as many pages as it
// takes to get want of them. It exists so that a refresh can patch rows in
// place rather than throw the user's position away and start again at row one.
func (l *Lister) Reload(ctx context.Context, jql string, proj appquery.Projection, want int) (Fetched, error) {
	res, err := l.search.Run(ctx, listRequest(jql, proj))
	if err != nil {
		return Fetched{}, err
	}
	page := res.Page
	issues := slices.Clone(page.Items)
	for len(issues) < want && page.HasMore() {
		page, err = page.Next(ctx)
		if err != nil {
			return Fetched{}, err
		}
		issues = append(issues, page.Items...)
	}
	return Fetched{Issues: issues, Page: page, Stored: l.Store(jql, issues, page.HasMore())}, nil
}

// PageOn reads the page after have rows that came off disk. Stored rows carry
// no cursor to follow, so the page after them is reached by asking the search
// again and walking to where they end.
func (l *Lister) PageOn(ctx context.Context, jql string, proj appquery.Projection, have int) (Fetched, error) {
	return l.Reload(ctx, jql, proj, have+listPageSize)
}

// Stored is what the last session left on disk for jql.
func (l *Lister) Stored(jql string) (appcache.Snapshot, bool) {
	if l.cache == nil {
		return appcache.Snapshot{}, false
	}
	return l.cache.Rows(jql)
}

// Store writes rows for jql to the cache, so the next session draws them first.
func (l *Lister) Store(jql string, issues []jira.Issue, more bool) error {
	if l.cache == nil {
		return nil
	}
	return l.cache.PutRows(jql, issues, more)
}

// Purge drops what is known about jql: the field catalogue the search resolved
// against and the stored rows. The error is the cache's.
func (l *Lister) Purge(jql string) error {
	if l.search != nil {
		l.search.Invalidate()
	}
	if l.cache == nil {
		return nil
	}
	return l.cache.Forget(jql)
}

// HasAssigned runs jql one row deep and reports whether anything matched. One
// row is the whole answer — what is asked is whether there is any work, not
// what it is — so the projection is the narrow one and the page is the
// smallest a site will send.
func (l *Lister) HasAssigned(ctx context.Context, jql string) (bool, error) {
	res, err := l.search.Run(ctx, appquery.Request{JQL: jql, Projection: appquery.ListProjection(), MaxResults: 1})
	if err != nil {
		return false, err
	}
	return len(res.Page.Items) > 0, nil
}

// Revalidate re-reads one issue by the fields it was last drawn with, using
// the issue endpoint rather than the search: the search is eventually
// consistent, and a read straight after a write has to see it.
func (l *Lister) Revalidate(ctx context.Context, key string, fields []string) (jira.Issue, error) {
	return l.reader.IssueFields(ctx, key, fields)
}

// RowChange is what a fetch brought back held against what was there before.
type RowChange struct{ Added, Gone, Updated int }

// Any reports whether anything moved.
func (c RowChange) Any() bool { return c.Added > 0 || c.Gone > 0 || c.Updated > 0 }

// DiffRows compares two reads of the same search by key and by when each
// issue was last touched, which is as much as a list projection knows about a
// row.
func DiffRows(before, after []jira.Issue) RowChange {
	was := make(map[string]int64, len(before))
	for i := range before {
		was[before[i].Key] = before[i].Updated.UnixNano()
	}
	var c RowChange
	for i := range after {
		when, had := was[after[i].Key]
		switch {
		case !had:
			c.Added++
		case when != after[i].Updated.UnixNano():
			c.Updated++
		}
		delete(was, after[i].Key)
	}
	c.Gone = len(was)
	return c
}
