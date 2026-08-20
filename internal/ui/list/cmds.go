package list

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pageSize is how many issues one request asks for. /search/jql caps what it
// will send anyway; the number that matters is that it is one screen's worth
// several times over, so paging is invisible while scrolling.
const pageSize = 50

// loadedMsg carries a first page, replacing whatever the list held.
type loadedMsg struct {
	gen     int
	page    jira.Page[jira.Issue]
	missing []string
}

// pagedMsg carries the page after the one the list already has.
type pagedMsg struct {
	gen  int
	page jira.Page[jira.Issue]
}

// patchedMsg carries a re-read of the rows already on screen. It is a separate
// outcome from loadedMsg because it must not move the cursor: docs/UX.md
// principle 5 is that a background refresh patches rows and nothing else.
type patchedMsg struct {
	gen    int
	issues []jira.Issue
	page   jira.Page[jira.Issue]
}

// failedMsg is any search that did not produce rows. The error travels whole so
// that the status line can use the wording the error itself carries.
type failedMsg struct {
	gen int
	err error
}

func request(jql string) app.Request {
	return app.Request{JQL: jql, Projection: app.ListProjection(), MaxResults: pageSize}
}

// load fetches the first page of a query.
func load(ctx context.Context, search *app.Search, jql string, gen int) tea.Cmd {
	return func() tea.Msg {
		res, err := search.Run(ctx, request(jql))
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return loadedMsg{gen: gen, page: res.Page, missing: res.Missing}
	}
}

// more fetches the page after the one in hand.
func more(ctx context.Context, page jira.Page[jira.Issue], gen int) tea.Cmd {
	return func() tea.Msg {
		next, err := page.Next(ctx)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return pagedMsg{gen: gen, page: next}
	}
}

// reload re-reads the rows the list already has, walking as many pages as it
// took to get them. It exists so that a refresh can patch rows in place rather
// than throw the user's position away and start again at row one.
func reload(ctx context.Context, search *app.Search, jql string, want, gen int) tea.Cmd {
	return func() tea.Msg {
		res, err := search.Run(ctx, request(jql))
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		page := res.Page
		issues := slices.Clone(page.Items)
		for len(issues) < want && page.HasMore() {
			page, err = page.Next(ctx)
			if err != nil {
				return failedMsg{gen: gen, err: err}
			}
			issues = append(issues, page.Items...)
		}
		return patchedMsg{gen: gen, issues: issues, page: page}
	}
}
