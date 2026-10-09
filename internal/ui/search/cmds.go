package search

import (
	"context"

	tea "charm.land/bubbletea/v2"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/pkg/jira"
)

type searchMsg struct {
	gen int
	res appquery.Result
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
func searchCmd(parent context.Context, s *appsearch.TextSearch, jql string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, done := context.WithCancel(parent)
		defer done()
		res, err := s.Run(ctx, jql)
		return searchMsg{gen: gen, res: res, err: err}
	}
}

func keyCmd(parent context.Context, s *appsearch.TextSearch, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, done := context.WithCancel(parent)
		defer done()
		iss, err := s.Key(ctx, key)
		return keyMsg{gen: gen, issue: iss, err: err}
	}
}

func pageCmd(parent context.Context, s *appsearch.TextSearch, page jira.Page[jira.Issue], gen int) tea.Cmd {
	return func() tea.Msg {
		ctx, done := context.WithCancel(parent)
		defer done()
		next, err := s.Next(ctx, page)
		return pagedMsg{gen: gen, page: next, err: err}
	}
}
