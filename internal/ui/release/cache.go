package release

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func (m *Model) versionsCache() (app.VersionsCache, bool) {
	held, ok := m.deps.Cache.(app.VersionsCache)
	return held, ok && held != nil
}

// fromCache draws the versions this project last had before anything is asked
// of the site. It runs in the constructor because kernel.FirstPaint renders a
// frame without calling Init. The open counts are never stored, so every row
// drawn from here says nobody has counted.
func (m *Model) fromCache() {
	held, ok := m.versionsCache()
	if !ok {
		return
	}
	snap, ok := held.Versions(m.deps.Project)
	if !ok {
		return
	}
	m.versions = slices.Clone(snap.Versions)
	m.loaded, m.stale, m.checked = true, snap.Stale, snap.StoredAt
	m.rebuildCells()
}

func (m *Model) keep() tea.Cmd {
	held, ok := m.versionsCache()
	if !ok || !m.loaded {
		return nil
	}
	if err := held.PutVersions(m.deps.Project, m.versions); err != nil {
		return kernel.Warn("these versions could not be stored for next time: " + err.Error())
	}
	return nil
}

func (m *Model) refresh(purge bool) tea.Cmd {
	if !purge {
		return m.load()
	}
	held, ok := m.versionsCache()
	if !ok {
		return m.load()
	}
	if err := held.ForgetVersions(m.deps.Project); err != nil {
		return tea.Batch(kernel.Warn("the stored copy of these versions could not be dropped: "+err.Error()), m.load())
	}
	return m.load()
}
