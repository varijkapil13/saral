package plan

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	appplan "github.com/varijkapil13/saral/internal/app/plan"
	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Defined is a plan this profile defines itself.
type Defined = appplan.Defined

// plansMsg carries what the site answered with.
type plansMsg struct {
	gen   int
	plans []jira.Plan
}

// releasesMsg carries the versions of one plan's projects. The plan travels with
// them because the answer is filed under the plan and not under the cursor,
// which has usually moved by the time a read lands.
type releasesMsg struct {
	gen  int
	plan string
	got  appplan.Releases
}

type refusal struct {
	kind   string
	ref    string
	reason string
}

// failedMsg is a read that brought nothing back. The error travels whole so
// that a refusal reaches the user in the words the site used, and the plan says
// which read failed: an empty one is the plans themselves.
type failedMsg struct {
	gen  int
	plan string
	err  error
}

func readPlans(ctx context.Context, reader jira.PlanReader, gen int) tea.Cmd {
	return func() tea.Msg {
		plans, err := appplan.ReadPlans(ctx, reader)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return plansMsg{gen: gen, plans: plans}
	}
}

func readReleases(ctx context.Context, reader appplan.Reader, plan jira.Plan, known map[string]string, gen int) tea.Cmd {
	return func() tea.Msg {
		got, err := appplan.ReadReleases(ctx, reader, plan, known)
		if err != nil {
			return failedMsg{gen: gen, plan: plan.ID, err: err}
		}
		return releasesMsg{gen: gen, plan: plan.ID, got: got}
	}
}

// refusedReason is the site's words without the project, which the row names.
func refusedReason(err error) string {
	switch {
	case errors.Is(err, appplan.ErrNotBoardID):
		return "the site did not name it by a board id"
	case errors.Is(err, appplan.ErrBoardEmpty):
		return "the site named no project behind it, which is also what it answers for a board you cannot view"
	}
	var nf *jira.NotFoundError
	if errors.As(err, &nf) {
		if nf.Detail != "" {
			return nf.Detail
		}
		return "it does not exist, or you cannot see it"
	}
	reason, _ := jira.Reason(err)
	return reason
}

// problemWords says why a defined plan renders to no search.
func problemWords(err error) string {
	var bad *appplan.FilterIDError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &bad):
		return "a filter is named by its numeric id, and " + appplan.Quote(bad.Filter) + " is not one"
	case errors.Is(err, appplan.ErrNothingToDraw):
		return "this plan names no project, no filter and no JQL, so there is nothing to draw"
	}
	return err.Error()
}

// datesWords says where this plan's bars would take their start and end from,
// and the empty string when it leaves that to the profile's own mapping.
func datesWords(d *Defined) string {
	start, end := d.Fields()
	if len(start) == 0 && len(end) == 0 {
		return ""
	}
	return field(start) + " " + arrow + " " + field(end)
}

func field(names []string) string {
	if len(names) == 0 {
		return "the profile's mapping"
	}
	return strings.Join(names, " or ")
}

// arrow is written out rather than taken from the theme because it is part of a
// sentence the plan rows carry, and the glyph set is not known here.
const arrow = "->"

// derive stands in for the profile's plans from what Deps already holds.
func derive(project string, saved appsearch.SavedQueries) []Defined {
	all := saved.All()
	queries := make([]appplan.Query, 0, len(all))
	for _, q := range all {
		queries = append(queries, appplan.Query{Name: q.Name, JQL: q.JQL})
	}
	return appplan.Derive(project, queries)
}

// originOf says where a plan on screen came from, which is the difference a
// user cannot act on unless the screen names it.
func originOf(d *Defined, derived bool) string {
	switch {
	case !derived:
		return "defined in this profile"
	case len(d.Projects) > 0:
		return "this session's project"
	default:
		return "a saved query"
	}
}
