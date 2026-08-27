package plan

import (
	"context"

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
// A project that answers with nothing is not an error and not a gap: a project
// with no versions on it is ordinary. A project that refuses fails the whole
// read, because a plan drawn from two projects and answered for by one would be
// a shorter list that nothing on screen explained.
func readReleases(ctx context.Context, reader jira.VersionReader, plan string, keys []string, gen int) tea.Cmd {
	return func() tea.Msg {
		out := make([]jira.Version, 0, len(keys)*4)
		for _, key := range keys {
			versions, err := reader.Versions(ctx, key)
			if err != nil {
				return failedMsg{gen: gen, plan: plan, err: err}
			}
			out = append(out, versions...)
		}
		return releasesMsg{gen: gen, plan: plan, versions: out}
	}
}
