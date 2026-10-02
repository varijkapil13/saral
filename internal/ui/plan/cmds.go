package plan

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// plansMsg carries what the site answered with.
type plansMsg struct {
	gen   int
	plans []jira.Plan
}

// releasesMsg carries the versions of one plan's projects. The plan travels with
// them because the answer is filed under the plan and not under the cursor,
// which has usually moved by the time a read lands.
type releasesMsg struct {
	gen      int
	plan     string
	versions []jira.Version
	refused  []refusal
}

// refusal is one project of a plan the site would not list versions for.
type refusal struct {
	project string
	reason  string
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
		plans, err := reader.Plans(ctx)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return plansMsg{gen: gen, plans: plans}
	}
}

// readReleases reads the versions of every project a plan draws from.
//
// A project the token may not browse is left out and named, which is what the
// web UI does with a plan spanning projects the reader cannot see. A failure
// that says nothing about the project — a rate limit, a dead host, a lapsed
// token — fails the whole read, since every other project would hit it too.
func readReleases(ctx context.Context, reader jira.VersionReader, plan string, keys []string, gen int) tea.Cmd {
	return func() tea.Msg {
		out := make([]jira.Version, 0, len(keys)*4)
		var refused []refusal
		for _, key := range keys {
			versions, err := reader.Versions(ctx, key)
			if err != nil {
				if !projectRefused(err) {
					return failedMsg{gen: gen, plan: plan, err: err}
				}
				refused = append(refused, refusal{project: key, reason: refusedReason(err)})
				continue
			}
			out = append(out, versions...)
		}
		return releasesMsg{gen: gen, plan: plan, versions: out, refused: refused}
	}
}

// refusedReason is the site's words without the project, which the row names.
func refusedReason(err error) string {
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

// projectRefused is an answer about one project. Jira refuses a project the
// token cannot browse with a 400 rather than a 403 on the versions endpoint.
func projectRefused(err error) bool {
	var (
		capErr *jira.CapabilityError
		nf     *jira.NotFoundError
		ve     *jira.ValidationError
	)
	return errors.As(err, &capErr) || errors.As(err, &nf) || errors.As(err, &ve)
}
