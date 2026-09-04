package timeline

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// pageSize is how many issues one request asks for, and maxIssues is how many
// pages of them a timeline will walk. A chart is not readable at ten thousand
// bars and the walk has to end somewhere the user can be told about, so the cap
// is named on screen rather than silently applied.
const (
	pageSize  = 50
	maxIssues = 500
)

type loadedMsg struct {
	gen        int
	fields     app.DateFields
	issues     []jira.Issue
	missing    []string
	resolution app.Resolution
	truncated  bool
	stored     error
}

// markersMsg carries the version and sprint boundaries drawn above the bars.
// They are a second read because neither of them is worth failing a chart over:
// notes says which of them could not be had and why.
type markersMsg struct {
	gen      int
	versions []jira.Version
	sprints  []jira.Sprint
	notes    []string
}

// failedMsg is a read that brought no chart back. The error travels whole so the
// refusal reaches the user in the words the site used.
type failedMsg struct {
	gen int
	err error
}

// projection is the date cascade's own fields plus what a bar draws and what
// this program's own local filter matches by. Assignee, reporter, priority
// and labels join it for the same reason board.plan.projection and backlog's
// own read carry reporter and labels: filter.Terms matches against this
// read's own issues, and none of the four is otherwise asked for.
func projection(fields app.DateFields) app.Projection {
	return fields.Projection().With(
		"summary", "issuetype", "status", "parent", "subtasks",
		"assignee", "reporter", "priority", "labels",
	)
}

func load(ctx context.Context, search *app.Search, sprints app.SprintDates, cache app.Cache,
	cfg cascadeConfig, jql string, gen int,
) tea.Cmd {
	return func() tea.Msg {
		catalogue, err := search.Fields(ctx)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		fields := app.ResolveDateFields(catalogue, cfg.start, cfg.end)
		res, err := search.Run(ctx, app.Request{JQL: jql, Projection: projection(fields), MaxResults: pageSize})
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		issues, err := jira.Collect(ctx, res.Page, maxIssues)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		dates := app.NewDates(fields,
			app.WithSprints(sprints),
			app.WithZone(cfg.zone, cfg.zoneReason),
			app.WithNow(cfg.now))
		resolution, err := dates.Resolve(ctx, issues)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return loadedMsg{
			gen: gen, fields: fields, issues: issues, missing: res.Missing,
			resolution: resolution,
			truncated:  len(issues) >= maxIssues && res.Page.HasMore(),
			stored:     keep(cache, jql, issues, res.Page.HasMore()),
		}
	}
}

type markerReader interface {
	jira.VersionReader
	jira.BoardReader
	jira.SprintReader
}

// markers reads the version release dates and the sprint boundaries. Every
// failure is a note rather than an error: a board a token cannot see must not
// empty a chart that has nothing to do with it.
func markers(ctx context.Context, client markerReader, project string, boards bool, boardsWhy string, gen int) tea.Cmd {
	return func() tea.Msg {
		out := markersMsg{gen: gen}
		versions, err := client.Versions(ctx, project)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			reason, _ := jira.Reason(err)
			out.notes = append(out.notes, "no version markers: "+reason)
		}
		out.versions = versions
		if !boards {
			out.notes = append(out.notes, "no sprint markers: "+boardsWhy)
			return out
		}
		found, err := client.Boards(ctx, project)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			reason, _ := jira.Reason(err)
			out.notes = append(out.notes, "no sprint markers: "+reason)
			return out
		}
		for _, board := range found {
			page, err := client.Sprints(ctx, board.ID, jira.SprintActive, jira.SprintFuture, jira.SprintClosed)
			if err != nil {
				reason, _ := jira.Reason(err)
				out.notes = append(out.notes, fmt.Sprintf("no sprint markers from %s: %s", board.Name, reason))
				continue
			}
			sprints, err := jira.Collect(ctx, page, maxSprints)
			if err != nil {
				reason, _ := jira.Reason(err)
				out.notes = append(out.notes, fmt.Sprintf("no sprint markers from %s: %s", board.Name, reason))
				continue
			}
			out.sprints = append(out.sprints, sprints...)
		}
		return out
	}
}

// maxSprints bounds the walk over one board's sprints. A board with years of
// closed sprints behind it would otherwise page through all of them for
// boundaries nobody can see on a chart of the current quarter.
const maxSprints = 200

func keep(cache app.Cache, jql string, issues []jira.Issue, more bool) error {
	if cache == nil {
		return nil
	}
	return cache.PutRows(jql, issues, more)
}

func notStored(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return kernel.Warn("these issues could not be stored for next time: " + err.Error())
}

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}
