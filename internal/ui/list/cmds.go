package list

import (
	"context"

	tea "charm.land/bubbletea/v2"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

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

// revalidatedMsg is one row re-read after the issue pane reported a landed
// write elsewhere. It carries its own generation, separate from a search's,
// because neither should cancel the other.
type revalidatedMsg struct {
	gen   int
	key   string
	issue jira.Issue
	err   error
}

func revalidate(ctx context.Context, lister *appsearch.Lister, key string, fields []string, gen int) tea.Cmd {
	return func() tea.Msg {
		iss, err := lister.Revalidate(ctx, key, fields)
		return revalidatedMsg{gen: gen, key: key, issue: iss, err: err}
	}
}

// notStored says that rows the user can see were not written to disk. It is a
// line in the status bar rather than an error, because the rows arrived.
func notStored(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return kernel.Warn("these rows could not be stored for next time: " + err.Error())
}

// storeRows writes the rows on screen back to the stored copy of this query,
// off the update loop: a revalidated row changes what is on screen
// synchronously, and the write that keeps the stored copy in step runs as a
// command like every other write to this cache does.
func storeRows(lister *appsearch.Lister, jql string, issues []jira.Issue, more bool) tea.Cmd {
	return func() tea.Msg {
		if cmd := notStored(lister.Store(jql, issues, more)); cmd != nil {
			return cmd()
		}
		return nil
	}
}

func load(ctx context.Context, lister *appsearch.Lister, jql string, proj appquery.Projection, gen int, w why) tea.Cmd {
	return func() tea.Msg {
		got, err := lister.First(ctx, jql, proj)
		if err != nil {
			return failedMsg{gen: gen, why: w, err: err}
		}
		return loadedMsg{gen: gen, why: w, page: got.Page, missing: got.Missing, stored: got.Stored}
	}
}

func more(ctx context.Context, lister *appsearch.Lister, jql string, have []jira.Issue, page jira.Page[jira.Issue], gen int) tea.Cmd {
	return func() tea.Msg {
		got, err := lister.Next(ctx, jql, have, page)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return pagedMsg{gen: gen, page: got.Page, stored: got.Stored}
	}
}

// reload re-reads the rows the list already has, so that a refresh can patch
// rows in place rather than throw the user's position away.
func reload(ctx context.Context, lister *appsearch.Lister, jql string, proj appquery.Projection, want, gen int, w why) tea.Cmd {
	return patched(ctx, gen, w, func(ctx context.Context) (appsearch.Fetched, error) {
		return lister.Reload(ctx, jql, proj, want)
	})
}

// pageOn reads the page after rows that came off disk.
func pageOn(ctx context.Context, lister *appsearch.Lister, jql string, proj appquery.Projection, have, gen int) tea.Cmd {
	return patched(ctx, gen, whyPage, func(ctx context.Context) (appsearch.Fetched, error) {
		return lister.PageOn(ctx, jql, proj, have)
	})
}

func patched(ctx context.Context, gen int, w why, read func(context.Context) (appsearch.Fetched, error)) tea.Cmd {
	return func() tea.Msg {
		got, err := read(ctx)
		if err != nil {
			return failedMsg{gen: gen, why: w, err: err}
		}
		return patchedMsg{gen: gen, why: w, issues: got.Issues, page: got.Page, stored: got.Stored}
	}
}

// assignedMsg answers the one question a session asks about the credential
// rather than about the site: whether anything anywhere on it is assigned to the
// account the credential belongs to.
type assignedMsg struct {
	has bool
	err error
}

func probeAssigned(ctx context.Context, lister *appsearch.Lister, jql string) tea.Cmd {
	return func() tea.Msg {
		has, err := lister.HasAssigned(ctx, jql)
		return assignedMsg{has: has, err: err}
	}
}
