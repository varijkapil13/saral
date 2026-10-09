package form

import (
	"context"

	tea "charm.land/bubbletea/v2"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
)

// typesFoundMsg carries the issue types a create form can offer.
type typesFoundMsg struct {
	gen   int
	types []jira.IssueType
}

// typesFailedMsg carries a search that could not say which types exist.
type typesFailedMsg struct {
	gen int
	err error
}

// schemaLoadedMsg carries one issue type's create screen.
type schemaLoadedMsg struct {
	gen    int
	screen appissue.Screen
	schema jira.Schema
}

// schemaFailedMsg carries a create screen that could not be read.
type schemaFailedMsg struct {
	gen int
	err error
}

// accountMsg carries the authenticated account, which is what a person picker
// offers when the field states no list of its own.
type accountMsg struct {
	gen  int
	user jira.User
}

// createdMsg carries the issue Jira stored.
type createdMsg struct {
	gen   int
	issue jira.Issue
}

// createFailedMsg carries a create Jira refused. The error travels whole so
// that a validation failure can be put on the fields it is about.
type createFailedMsg struct {
	gen int
	err error
}

// loadTypes reads the issue types in use in one project.
func loadTypes(ctx context.Context, search *appquery.Search, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		types, err := appissue.Types(ctx, search, project)
		if err != nil {
			return typesFailedMsg{gen: gen, err: err}
		}
		return typesFoundMsg{gen: gen, types: types}
	}
}

func loadSchema(ctx context.Context, client jira.SchemaReader, cache *appissue.Schemas, key appissue.Screen, gen int) tea.Cmd {
	return func() tea.Msg {
		schema, err := appissue.CreateScreen(ctx, client, cache, key)
		if err != nil {
			return schemaFailedMsg{gen: gen, err: err}
		}
		return schemaLoadedMsg{gen: gen, screen: key, schema: schema}
	}
}

// loadAccount reads the authenticated account. A failure is not reported: it
// costs a person picker one candidate, and there is nothing the user can do.
func loadAccount(ctx context.Context, client jira.Identifier, gen int) tea.Cmd {
	return func() tea.Msg {
		user, err := appissue.Account(ctx, client)
		if err != nil {
			return nil
		}
		return accountMsg{gen: gen, user: user}
	}
}

func create(ctx context.Context, client jira.IssueWriter, in jira.IssueInput, gen int) tea.Cmd {
	return func() tea.Msg {
		issue, err := appissue.Create(ctx, client, in)
		if err != nil {
			return createFailedMsg{gen: gen, err: err}
		}
		return createdMsg{gen: gen, issue: issue}
	}
}

// withCancel makes a command release its context however it ends.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}
