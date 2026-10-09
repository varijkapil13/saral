package release

import (
	tea "charm.land/bubbletea/v2"

	apprelease "github.com/varijkapil13/saral/internal/app/release"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// filterMemoryKey is where the filter in force is kept for this profile.
const filterMemoryKey = "state"

// shows is how the empty line names what the next press brings back.
func shows(f apprelease.Filter) string {
	if f == apprelease.FilterAll {
		return "every version"
	}
	return "the " + f.Name() + " ones"
}

func recallFilter(d kernel.Deps, view string) apprelease.Filter {
	name, ok := kernel.Recall(d, view, filterMemoryKey)
	if !ok {
		return apprelease.FilterAll
	}
	f, _ := apprelease.FilterNamed(name)
	return f
}

func (m *Model) rememberFilter() {
	value := m.filter.Name()
	if m.filter == apprelease.FilterAll {
		value = ""
	}
	kernel.Keep(m.deps, m.viewID(), filterMemoryKey, value)
}

// cycleFilter moves to the next of all, unreleased, released and archived.
func (m *Model) cycleFilter() tea.Cmd {
	if m.saving || m.mode != browsing {
		return nil
	}
	m.setFilter(m.filter.Next())
	return nil
}

func (m *Model) setFilter(f apprelease.Filter) {
	under := m.selectedID()
	m.filter = f
	m.sum = ""
	m.rememberFilter()
	m.reorder()
	m.moveOnto(under)
}
