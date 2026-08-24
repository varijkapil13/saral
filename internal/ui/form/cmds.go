package form

import (
	"context"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// typeSample is how many issues are read to find out which issue types this
// project actually uses. It is one page, not a walk: the port has no endpoint
// that lists a project's issue types, so the answer comes from the issues the
// account can see, the same way onboarding finds a project key.
const typeSample = 50

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
	screen screen
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
func loadTypes(ctx context.Context, search *app.Search, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		result, err := search.Run(ctx, app.Request{
			JQL:        "project = " + quote(project) + " ORDER BY created DESC",
			Projection: app.Projection{Name: "issue type picker", IDs: []string{"issuetype"}},
			MaxResults: typeSample,
		})
		if err != nil {
			return typesFailedMsg{gen: gen, err: err}
		}
		return typesFoundMsg{gen: gen, types: distinctTypes(result.Page.Items)}
	}
}

// distinctTypes keeps the order the issues came back in, which is newest first
// and therefore the order worth offering.
func distinctTypes(issues []jira.Issue) []jira.IssueType {
	seen := make(map[string]bool, len(issues))
	out := make([]jira.IssueType, 0, 6)
	for i := range issues {
		typ := issues[i].Type
		if typ.ID == "" || seen[typ.ID] {
			continue
		}
		seen[typ.ID] = true
		out = append(out, typ)
	}
	return out
}

// loadSchema reads a create screen, from the cache when it is still fresh.
func loadSchema(ctx context.Context, client jira.SchemaReader, cache *schemaCache, key screen, gen int) tea.Cmd {
	return func() tea.Msg {
		if schema, ok := cache.get(key); ok {
			return schemaLoadedMsg{gen: gen, screen: key, schema: schema}
		}
		schema, err := client.CreateMeta(ctx, key.project, key.issueType)
		if err != nil {
			return schemaFailedMsg{gen: gen, err: err}
		}
		cache.put(key, schema)
		return schemaLoadedMsg{gen: gen, screen: key, schema: schema}
	}
}

// loadAccount reads the authenticated account. A failure is not reported: it
// costs a person picker one candidate, and there is nothing the user can do.
func loadAccount(ctx context.Context, client jira.Identifier, gen int) tea.Cmd {
	return func() tea.Msg {
		user, err := client.Me(ctx)
		if err != nil {
			return nil
		}
		return accountMsg{gen: gen, user: user}
	}
}

// create asks Jira to store the issue.
func create(ctx context.Context, client jira.IssueWriter, in jira.IssueInput, gen int) tea.Cmd {
	return func() tea.Msg {
		issue, err := client.CreateIssue(ctx, in)
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

// quote writes a project key as JQL takes it. The key is whatever the session
// was opened against and nothing about it is written down here.
func quote(s string) string {
	return strconv.Quote(strings.ReplaceAll(s, `"`, ""))
}
