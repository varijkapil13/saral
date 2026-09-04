package issue

import (
	"context"
	"strconv"
	"strings"

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

// byKey is the JQL that reads one issue. It goes through search rather than
// through the port's Issue method because only search takes a field set, and a
// bare issue read returns every field the site defines.
func byKey(key string) string {
	return "key = " + strconv.Quote(strings.ReplaceAll(key, `"`, ""))
}

func load(ctx context.Context, search *app.Search, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		res, err := search.Run(ctx, app.Request{JQL: byKey(key), Projection: app.DetailProjection(), MaxResults: 1})
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		if len(res.Page.Items) == 0 {
			return failedMsg{gen: gen, err: &jira.NotFoundError{Kind: "issue", ID: key}}
		}
		return loadedMsg{gen: gen, issue: res.Page.Items[0], labels: res.Labels}
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
