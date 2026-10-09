package filter

import (
	"context"

	tea "charm.land/bubbletea/v2"

	appsearch "github.com/varijkapil13/saral/internal/app/search"
	appterm "github.com/varijkapil13/saral/internal/app/term"
	"github.com/varijkapil13/saral/pkg/jira"
)

// vocabularyMsg carries the values of a facet the site answers in one read.
type vocabularyMsg struct {
	gen    int
	facet  appterm.Facet
	values []appsearch.Value
}

// peopleMsg carries the accounts a search brought back. The needle travels with
// them so that one is never asked for twice, and complete says the site had
// fewer accounts than it was allowed to send — which is what makes typing
// answerable from what is already held.
type peopleMsg struct {
	gen      int
	facet    appterm.Facet
	needle   string
	people   []jira.User
	complete bool
}

// failedMsg is a read that brought nothing back. The error travels whole so
// that a refusal reaches the user in the words the site used, and the needle
// travels with it so that a question this site never answered can be asked
// again rather than being remembered as asked.
type failedMsg struct {
	gen    int
	facet  appterm.Facet
	needle string
	err    error
}

// findPeople searches the site's accounts, and draws back the ones in force.
func findPeople(ctx context.Context, finder jira.PeopleFinder, f appterm.Facet, q jira.PeopleQuery, inForce []string, gen int) tea.Cmd {
	return func() tea.Msg {
		found, err := appsearch.FindAccounts(ctx, finder, q, inForce)
		if err != nil {
			return failedMsg{gen: gen, facet: f, needle: q.Match, err: err}
		}
		return peopleMsg{gen: gen, facet: f, needle: q.Match, people: found.Users, complete: found.Complete}
	}
}

// vocabulary reads the values of a facet that is not a person.
func vocabulary(ctx context.Context, vocab jira.FilterVocabulary, f appterm.Facet, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		values, err := appsearch.Vocabulary(ctx, vocab, f, project)
		if err != nil {
			return failedMsg{gen: gen, facet: f, err: err}
		}
		return vocabularyMsg{gen: gen, facet: f, values: values}
	}
}
