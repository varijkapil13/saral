package release

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// ArrangeMsg moves the arrangement of a set on to the next one.
type ArrangeMsg struct{}

// PickProjectMsg opens the filters of a set on its project.
type PickProjectMsg struct{}

// ExcludedMsg shows the versions a plan leaves out, or hides them again.
type ExcludedMsg struct{}

// FindMsg opens the text filter of a set.
type FindMsg struct{}

type setAct uint8

const (
	setActNone setAct = iota
	setActArrange
	setActExcluded
	setActFold
	setActNextFacet
	setActPrevFacet
	setActLess
	setActMore
	setActDone
)

type setKeyMap struct {
	keyMap
	Arrange  kernel.Binding
	Excluded kernel.Binding
	Fold     kernel.Binding

	NextFacet kernel.Binding
	PrevFacet kernel.Binding
	Less      kernel.Binding
	More      kernel.Binding
	Done      kernel.Binding
	Dismiss   kernel.Binding
}

func defaultSetKeys() setKeyMap {
	k := defaultKeys()
	k.Filter = kernel.Canon(kernel.ActFilter, "filter by state or project")
	k.Find = kernel.Canon(kernel.ActFind, "find a version or release")
	return setKeyMap{
		keyMap:   k,
		Arrange:  kernel.Canon(kernel.ActGroup, "group by cross-space release or project"),
		Excluded: kernel.Canon(kernel.ActHidden, "show or hide what the plan excludes"),
		Fold:     kernel.Canon(kernel.ActOpen, "fold a header"),

		NextFacet: kernel.Canon(kernel.ActNextPane, "next filter"),
		PrevFacet: kernel.Canon(kernel.ActPrevPane, "previous filter"),
		Less:      kernel.Canon(kernel.ActLeft, "previous value"),
		More:      kernel.Canon(kernel.ActRight, "next value"),
		Done:      kernel.Bind([]string{"enter"}, "enter", "keep them"),
		Dismiss:   kernel.Bind([]string{"esc"}, "esc", "keep them"),
	}
}

func (k setKeyMap) table() map[string]setAct {
	return table(
		binding[setAct]{k.Arrange, setActArrange}, binding[setAct]{k.Excluded, setActExcluded},
		binding[setAct]{k.Fold, setActFold},
	)
}

func (k setKeyMap) facetTable() map[string]setAct {
	return table(
		binding[setAct]{k.NextFacet, setActNextFacet}, binding[setAct]{k.PrevFacet, setActPrevFacet},
		binding[setAct]{k.Less, setActLess}, binding[setAct]{k.More, setActMore},
		binding[setAct]{k.Done, setActDone}, binding[setAct]{k.Dismiss, setActDone},
	)
}

func (k setKeyMap) keySet() kernel.KeySet { return setSets[setBrowsing] }

type setKeyState int

const (
	setBrowsing setKeyState = iota
	setCounting
	setEditing
	setSaving
	setSorting
	setFinding
	setFaceting
	setKeyStates
)

var setSets = func() [setKeyStates]kernel.KeySet {
	k := defaultSetKeys()
	edit, archive := kernel.Terse(k.Edit, "edit"), kernel.Terse(k.Archive, "archive")
	assign := kernel.Terse(k.Assign, "assign")
	sort, filter := kernel.Terse(k.Sort, "sort"), kernel.Terse(k.Filter, "filter")
	arrange := kernel.Terse(k.Arrange, "arrange")
	excluded, find := kernel.Terse(k.Excluded, "excluded"), kernel.Terse(k.Find, "find")
	motions := [][]kernel.Binding{{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom}}

	var sets [setKeyStates]kernel.KeySet
	sets[setBrowsing] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Release, "release"), edit, archive, assign, sort, arrange, filter, excluded, find,
		},
		Full: append(append([][]kernel.Binding(nil), motions...),
			[]kernel.Binding{k.Release, k.Fold, k.Edit, k.Archive, k.Assign},
			[]kernel.Binding{k.Sort, k.Arrange, k.Filter, k.Excluded, k.Find}),
	}
	sets[setCounting] = kernel.KeySet{
		Acts: []kernel.Binding{edit, archive, assign, sort, arrange, filter, excluded, find},
		Full: append(append([][]kernel.Binding(nil), motions...),
			[]kernel.Binding{k.Fold, k.Edit, k.Archive, k.Assign},
			[]kernel.Binding{k.Sort, k.Arrange, k.Filter, k.Excluded, k.Find}),
	}
	sets[setEditing] = liveSets[keysEditing]
	sets[setSaving] = liveSets[keysSaving]
	sets[setSorting] = liveSets[keysSorting]
	sets[setFinding] = liveSets[keysFinding]
	sets[setFaceting] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{
			kernel.Terse(k.NextFacet, "next"), kernel.Terse(k.Less, "prev value"),
			kernel.Terse(k.More, "next value"), kernel.Terse(k.Done, "keep"),
		},
		Full: [][]kernel.Binding{{k.NextFacet, k.PrevFacet}, {k.Less, k.More}, {k.Done, k.Dismiss}},
	}
	return sets
}()

func (m *Model) setLiveKeys() (set kernel.KeySet, gen int) {
	state := setBrowsing
	switch {
	case m.saving:
		state = setSaving
	case m.mode == editing:
		state = setEditing
	case m.mode == sorting:
		state = setSorting
	case m.mode == finding:
		state = setFinding
	case m.mode == faceting:
		state = setFaceting
	case m.counting != "":
		state = setCounting
	}
	return setSets[state], int(state)
}

func (m *Model) setKey(stroke string) (tea.Cmd, bool) {
	switch m.set.acts[stroke] {
	case setActArrange:
		return m.cycleArrange(), true
	case setActExcluded:
		return m.toggleExcluded(), true
	case setActFold:
		if m.cursor >= 0 && m.cursor < len(m.order) && m.order[m.cursor].v < 0 {
			return m.foldHeader(m.cursor), true
		}
		return nil, true
	case setActNone, setActNextFacet, setActPrevFacet, setActLess, setActMore, setActDone:
	}
	return nil, false
}

func (m *Model) facetKey(stroke string) tea.Cmd {
	s := m.set
	switch s.inFacet[stroke] {
	case setActNextFacet, setActPrevFacet:
		s.facet = (s.facet + 1) % facetCount
	case setActLess:
		m.stepFacet(-1)
	case setActMore:
		m.stepFacet(1)
	case setActDone:
		m.mode = browsing
		m.sum = ""
		m.scrollToCursor()
	case setActNone, setActArrange, setActExcluded, setActFold:
	}
	return nil
}
