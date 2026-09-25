package sprint

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func (m *Model) sprintsCache() (app.SprintsCache, bool) {
	held, ok := m.deps.Cache.(app.SprintsCache)
	return held, ok && held != nil
}

// fromCache draws the sprints this project last had before anything is asked of
// the site. It runs in the constructor because kernel.FirstPaint renders a
// frame without calling Init.
func (m *Model) fromCache() {
	held, ok := m.sprintsCache()
	if !ok {
		return
	}
	snap, ok := held.Sprints(m.deps.Project)
	if !ok {
		return
	}
	sprints := slices.Clone(snap.Sprints)
	if !m.showAll {
		sprints = slices.DeleteFunc(sprints, func(sp jira.Sprint) bool { return rankState(sp.State) == rankClosed })
	}
	m.boards, m.more = snap.Boards, snap.More
	m.sprints = sortSprints(sprints)
	m.loaded, m.stale = true, snap.Stale
	m.lay = planLayout(m.width, len(m.boards))
}

// keep stores what is on screen for the next session's first frame. A write
// keeps it too, so a started sprint does not read as planned on the next launch.
func (m *Model) keep() tea.Cmd {
	held, ok := m.sprintsCache()
	if !ok || !m.loaded {
		return nil
	}
	err := held.PutSprints(m.deps.Project, app.SprintsSnapshot{
		Boards: m.boards, More: m.more, Sprints: m.sprints, Closed: m.showAll,
	})
	if err != nil {
		return kernel.Warn("these sprints could not be stored for next time: " + err.Error())
	}
	return nil
}

func (m *Model) purge() tea.Cmd {
	held, ok := m.sprintsCache()
	if !ok {
		return nil
	}
	if err := held.ForgetSprints(m.deps.Project); err != nil {
		return kernel.Warn("the stored copy of these sprints could not be dropped: " + err.Error())
	}
	return nil
}
