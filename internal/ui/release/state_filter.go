package release

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// stateFilter narrows the rows to one kind of version. It is local, like the
// sort: the site is read unfiltered because the same list feeds the flow's
// choice of where open work can move.
type stateFilter uint8

const (
	filterAll stateFilter = iota
	filterUnreleased
	filterReleased
	filterArchived
	filterCount
)

// filterNames are what the memory keeps and the summary line says. They are
// words rather than numbers so that a reordered enum reads an old value back
// as what it was.
var filterNames = [filterCount]string{"all", stateUnreleased, stateReleased, stateArchived}

// filterMemoryKey is where the filter in force is kept for this profile.
const filterMemoryKey = "state"

func (f stateFilter) name() string { return filterNames[f] }

func (f stateFilter) next() stateFilter { return (f + 1) % filterCount }

// keeps is whether a row drawn in a state belongs under this filter. An
// overdue version is still unreleased, and an archived one is only ever
// archived, whatever else is true of it.
func (f stateFilter) keeps(state string) bool {
	switch f {
	case filterUnreleased:
		return state == stateUnreleased || state == stateOverdue
	case filterReleased:
		return state == stateReleased
	case filterArchived:
		return state == stateArchived
	default:
		return true
	}
}

// shows is how the empty line names what the next press brings back.
func (f stateFilter) shows() string {
	if f == filterAll {
		return "every version"
	}
	return "the " + f.name() + " ones"
}

func filterByName(name string) (stateFilter, bool) {
	for f := range filterCount {
		if filterNames[f] == name {
			return f, true
		}
	}
	return filterAll, false
}

func recallFilter(d kernel.Deps, view string) stateFilter {
	name, ok := kernel.Recall(d, view, filterMemoryKey)
	if !ok {
		return filterAll
	}
	f, _ := filterByName(name)
	return f
}

func (m *Model) rememberFilter() {
	value := m.filter.name()
	if m.filter == filterAll {
		value = ""
	}
	kernel.Keep(m.deps, m.viewID(), filterMemoryKey, value)
}

// cycleFilter moves to the next of all, unreleased, released and archived.
func (m *Model) cycleFilter() tea.Cmd {
	if m.saving || m.mode != browsing {
		return nil
	}
	m.setFilter(m.filter.next())
	return nil
}

func (m *Model) setFilter(f stateFilter) {
	under := m.selectedID()
	m.filter = f
	m.sum = ""
	m.rememberFilter()
	m.reorder()
	m.moveOnto(under)
}
