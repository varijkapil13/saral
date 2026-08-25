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
