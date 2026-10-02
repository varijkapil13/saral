package search

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

type searchMsg struct {
	gen int
	res app.Result
	err error
}

type keyMsg struct {
	gen   int
	issue jira.Issue
	err   error
}

type pagedMsg struct {
	gen  int
	page jira.Page[jira.Issue]
	err  error
}

type settledMsg struct{ gen int }

// Each command takes a context of its own under the run's: the run's context is
// cancelled by the next run, and a command that cancelled it when it finished
// would cut short whichever of its siblings was still out.
func searchCmd(parent context.Context, s *app.Search, req app.Request, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, done := context.WithCancel(parent)
		defer done()
		res, err := s.Run(ctx, req)
		return searchMsg{gen: gen, res: res, err: err}
	}
}

func keyCmd(parent context.Context, r jira.IssueReader, key string, fields []string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, done := context.WithCancel(parent)
		defer done()
		iss, err := r.IssueFields(ctx, key, fields)
		return keyMsg{gen: gen, issue: iss, err: err}
	}
}

func pageCmd(parent context.Context, page jira.Page[jira.Issue], gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, done := context.WithCancel(parent)
		defer done()
		next, err := page.Next(ctx)
		return pagedMsg{gen: gen, page: next, err: err}
	}
}
