package issue

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// peopleFoundMsg carries the accounts one assignee search brought back. needle
// is what was asked for, so a keystroke changed while this was in flight is
// never mistaken for the query it actually answers.
type peopleFoundMsg struct {
	gen    int
	needle string
	people []jira.User
}

// findAssignees searches the site's accounts for the assignee picker. Project
// scopes it to accounts assignable in this project, which is what drops the
// app accounts for free — see jira.PeopleQuery's own documentation.
func findAssignees(ctx context.Context, finder jira.PeopleFinder, project, match string, limit, gen int) tea.Cmd {
	return func() tea.Msg {
		people, err := finder.FindPeople(ctx, jira.PeopleQuery{Match: match, Project: project, Limit: limit})
		if err != nil {
			return editFailedMsg{gen: gen, err: err}
		}
		return peopleFoundMsg{gen: gen, needle: match, people: people}
	}
}

// meLoadedMsg is this session's own account, or why it could not be read. It
// carries no generation: fetchMe asks for it once per pane and keeps the
// answer for as long as the pane is open, so nothing here can arrive for a
// question that has since changed.
type meLoadedMsg struct {
	me  jira.User
	err error
}

func fetchMeCmd(ctx context.Context, ident jira.Identifier) tea.Cmd {
	return func() tea.Msg {
		me, err := ident.Me(ctx)
		if err != nil {
			return meLoadedMsg{err: err}
		}
		return meLoadedMsg{me: me}
	}
}
