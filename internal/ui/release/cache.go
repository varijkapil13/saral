package release

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	apprelease "github.com/varijkapil13/saral/internal/app/release"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func (m *Model) versionsCache() apprelease.Cache { return apprelease.NewCache(m.deps.Cache) }

// fromCache draws the versions this project last had before anything is asked
// of the site. It runs in the constructor because kernel.FirstPaint renders a
// frame without calling Init. The open counts are never stored, so every row
// drawn from here says nobody has counted.
func (m *Model) fromCache() {
	snap, ok := m.versionsCache().Recall(m.deps.Project)
	if !ok {
		return
	}
	m.versions = slices.Clone(snap.Versions)
	m.loaded, m.stale, m.checked = true, snap.Stale, snap.StoredAt
	m.rebuildCells()
}

func (m *Model) keep() tea.Cmd {
	if m.set != nil {
		return nil
	}
	held := m.versionsCache()
	if !held.Held() || !m.loaded {
		return nil
	}
	if err := held.Keep(m.deps.Project, m.versions); err != nil {
		return kernel.Warn("these versions could not be stored for next time: " + err.Error())
	}
	return nil
}

func (m *Model) refresh(purge bool) tea.Cmd {
	if m.set != nil {
		return m.reloadSet()
	}
	if !purge {
		return m.load()
	}
	held := m.versionsCache()
	if !held.Held() {
		return m.load()
	}
	if err := held.Forget(m.deps.Project); err != nil {
		return tea.Batch(kernel.Warn("the stored copy of these versions could not be dropped: "+err.Error()), m.load())
	}
	return m.load()
}
