package list

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pageSize is how many issues one request asks for. /search/jql caps what it
// will send anyway; the number that matters is that it is one screen's worth
// several times over, so paging is invisible while scrolling.
const pageSize = 50

// loadedMsg carries a first page, replacing whatever the list held.
type loadedMsg struct {
	gen     int
	why     why
	page    jira.Page[jira.Issue]
	missing []string
	stored  error
}

// pagedMsg carries the page after the one the list already has.
type pagedMsg struct {
	gen    int
	page   jira.Page[jira.Issue]
	stored error
}

// patchedMsg carries a re-read of the rows already on screen. It is a separate
// outcome from loadedMsg because it must not move the cursor: docs/UX.md
// principle 5 is that a background refresh patches rows and nothing else.
type patchedMsg struct {
	gen    int
	why    why
	issues []jira.Issue
	page   jira.Page[jira.Issue]
	stored error
}

// failedMsg is any search that did not produce rows. The error travels whole so
// that the status line can use the wording the error itself carries, and what
// the request was for travels with it so that a refusal is one of the answers a
// refresh gives rather than an error from nowhere in particular.
type failedMsg struct {
	gen int
	why why
	err error
}

func request(jql string) app.Request {
	return app.Request{JQL: jql, Projection: app.ListProjection(), MaxResults: pageSize}
}

// keep stores what a fetch brought back, so that the next session draws these
// rows before it asks the site anything.
//
// The error travels back with the rows rather than replacing them: the fetch
// worked, and a cache that could not be written is worth a line in the status
// bar and nothing more.
func keep(cache app.Cache, jql string, issues []jira.Issue, more bool) error {
	if cache == nil {
		return nil
	}
	return cache.PutRows(jql, issues, more)
}

// notStored says that rows the user can see were not written to disk. It is a
// line in the status bar rather than an error, because the rows arrived.
func notStored(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return kernel.Warn("these rows could not be stored for next time: " + err.Error())
}

// load fetches the first page of a query.
func load(ctx context.Context, search *app.Search, cache app.Cache, jql string, gen int, w why) tea.Cmd {
	return func() tea.Msg {
		res, err := search.Run(ctx, request(jql))
		if err != nil {
			return failedMsg{gen: gen, why: w, err: err}
		}
		return loadedMsg{
			gen: gen, why: w, page: res.Page, missing: res.Missing,
			stored: keep(cache, jql, res.Page.Items, res.Page.HasMore()),
		}
	}
}

// more fetches the page after the one in hand. The rows already on screen come
// with it so that what is stored is the whole of what the user has scrolled
// through, not just its last page.
func more(ctx context.Context, cache app.Cache, jql string, have []jira.Issue, page jira.Page[jira.Issue], gen int) tea.Cmd {
	return func() tea.Msg {
		next, err := page.Next(ctx)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		whole := make([]jira.Issue, 0, len(have)+len(next.Items))
		whole = append(append(whole, have...), next.Items...)
		return pagedMsg{gen: gen, page: next, stored: keep(cache, jql, whole, next.HasMore())}
	}
}

// reload re-reads the rows the list already has, walking as many pages as it
// took to get them. It exists so that a refresh can patch rows in place rather
// than throw the user's position away and start again at row one.
func reload(ctx context.Context, search *app.Search, cache app.Cache, jql string, want, gen int, w why) tea.Cmd {
	return func() tea.Msg {
		res, err := search.Run(ctx, request(jql))
		if err != nil {
			return failedMsg{gen: gen, why: w, err: err}
		}
		page := res.Page
		issues := slices.Clone(page.Items)
		for len(issues) < want && page.HasMore() {
			page, err = page.Next(ctx)
			if err != nil {
				return failedMsg{gen: gen, why: w, err: err}
			}
			issues = append(issues, page.Items...)
		}
		return patchedMsg{
			gen: gen, why: w, issues: issues, page: page,
			stored: keep(cache, jql, issues, page.HasMore()),
		}
	}
}
