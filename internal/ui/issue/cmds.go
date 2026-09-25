package issue

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// loadedMsg is one issue read with the detail projection, and what this site
// calls the fields it came back with. The labels travel with the answer because
// a custom field's ID differs per site and its name is translated, so neither
// can be written down here.
type loadedMsg struct {
	gen    int
	issue  jira.Issue
	labels app.FieldLabels
}

type failedMsg struct {
	gen int
	err error
}

// editMetaMsg carries the site's answer about which fields belong on this
// issue's screen right now. There is no failed counterpart: see loadEditMeta.
type editMetaMsg struct {
	gen  int
	meta jira.EditMeta
}

func load(ctx context.Context, search *app.Search, reader jira.IssueReader, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		iss, labels, err := search.ReadIssue(ctx, reader, key, app.DetailProjection())
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return loadedMsg{gen: gen, issue: iss, labels: labels}
	}
}

// savedMsg is one dirty-set patch that has landed, or failed to.
type savedMsg struct {
	gen int
	err error
}

// saveDirtyPatch sends the whole dirty set as one request.
func saveDirtyPatch(ctx context.Context, client app.IssueEditor, key string, base app.EditBase, patch jira.IssuePatch, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := app.SaveIssue(ctx, client, key, base, patch); err != nil {
			return savedMsg{gen: gen, err: err}
		}
		return savedMsg{gen: gen}
	}
}

// loadEditMeta asks the site which fields are on this issue's screen right
// now.
//
// A failure here — 403, 429, a transport error — is never reported. editmeta
// is an ordering and relevance signal for fields the issue read already
// brought back, never the reason one is drawn or hidden, so a read that did
// not arrive is answered with no message at all rather than with a failedMsg:
// the sidebar draws exactly what it drew before, which is what a nil Cmd
// result already means to this pane's Update loop.
func loadEditMeta(ctx context.Context, reader jira.SchemaReader, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		meta, err := reader.EditMeta(ctx, key)
		if err != nil {
			return nil
		}
		return editMetaMsg{gen: gen, meta: meta}
	}
}
