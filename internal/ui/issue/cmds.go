package issue

import (
	"context"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// commentCap bounds how much of a thread is read. A thread longer than this is
// a conversation nobody scrolls to the end of, and reading it all would be one
// request per fifty comments.
const commentCap = 200

type loadedMsg struct {
	gen   int
	issue jira.Issue
}

type commentsMsg struct {
	gen      int
	comments []jira.Comment
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
		return loadedMsg{gen: gen, issue: res.Page.Items[0]}
	}
}

func comments(ctx context.Context, client jira.CommentReader, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		page, err := client.Comments(ctx, key)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		all, err := jira.Collect(ctx, page, commentCap)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return commentsMsg{gen: gen, comments: all}
	}
}
