package sprint

import (
	tea "charm.land/bubbletea/v2"

	appsprint "github.com/varijkapil13/saral/internal/app/sprint"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func (m *Model) store() appsprint.Cache { return appsprint.NewCache(m.deps.Cache) }

// fromCache draws the sprints this project last had before anything is asked of
// the site. It runs in the constructor because kernel.FirstPaint renders a
// frame without calling Init.
func (m *Model) fromCache() {
	held, ok := m.store().Load(m.deps.Project, m.showAll)
	if !ok {
		return
	}
	m.boards, m.more = held.Boards, held.More
	m.sprints = held.Sprints
	m.loaded, m.stale = true, held.Stale
	m.lay = planLayout(m.width, len(m.boards))
}

// keep stores what is on screen for the next session's first frame. A write
// keeps it too, so a started sprint does not read as planned on the next launch.
func (m *Model) keep() tea.Cmd {
	if !m.loaded {
		return nil
	}
	l := appsprint.Listing{Boards: m.boards, More: m.more, Sprints: m.sprints}
	if err := m.store().Keep(m.deps.Project, l, m.showAll); err != nil {
		return kernel.Warn("these sprints could not be stored for next time: " + err.Error())
	}
	return nil
}

func (m *Model) purge() tea.Cmd {
	if err := m.store().Forget(m.deps.Project); err != nil {
		return kernel.Warn("the stored copy of these sprints could not be dropped: " + err.Error())
	}
	return nil
}
