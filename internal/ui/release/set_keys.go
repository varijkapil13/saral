package release

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// ArrangeMsg moves the arrangement of a set on to the next one.
type ArrangeMsg struct{}

// PickProjectMsg moves the project filter of a set on to the next project.
type PickProjectMsg struct{}

// ExcludedMsg shows the versions a plan leaves out, or hides them again.
type ExcludedMsg struct{}

// FindMsg opens the text filter of a set.
type FindMsg struct{}

type setAct uint8

const (
	setActNone setAct = iota
	setActArrange
	setActPick
	setActExcluded
)

type setKeyMap struct {
	keyMap
	Arrange  kernel.Binding
	Pick     kernel.Binding
	Excluded kernel.Binding
}

func defaultSetKeys() setKeyMap {
	k := defaultKeys()
	k.Release = kernel.Bind([]string{"enter"}, "enter", "release it, or fold a header")
	k.Find = kernel.Bind([]string{"/"}, "/", "find a version or release")
	return setKeyMap{
		keyMap:   k,
		Arrange:  kernel.Bind([]string{"v"}, "v", "group by cross-space release or project"),
		Pick:     kernel.Bind([]string{"p"}, "p", "show one project, or all"),
		Excluded: kernel.Bind([]string{"x"}, "x", "show or hide what the plan excludes"),
	}
}

func (k setKeyMap) table() map[string]setAct {
	return table(
		binding[setAct]{k.Arrange, setActArrange}, binding[setAct]{k.Pick, setActPick},
		binding[setAct]{k.Excluded, setActExcluded},
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
	setKeyStates
)

var setSets = func() [setKeyStates]kernel.KeySet {
	k := defaultSetKeys()
	edit, archive := kernel.Terse(k.Edit, "edit"), kernel.Terse(k.Archive, "archive")
	assign := kernel.Terse(k.Assign, "assign")
	sort, filter := kernel.Terse(k.Sort, "sort"), kernel.Terse(k.Filter, "state")
	arrange, pick := kernel.Terse(k.Arrange, "arrange"), kernel.Terse(k.Pick, "project")
	excluded, find := kernel.Terse(k.Excluded, "excluded"), kernel.Terse(k.Find, "find")
	motions := [][]kernel.Binding{{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom}}

	var sets [setKeyStates]kernel.KeySet
	sets[setBrowsing] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Release, "open"), edit, archive, assign, sort, arrange, pick, filter, excluded, find,
		},
		Full: append(append([][]kernel.Binding(nil), motions...),
			[]kernel.Binding{k.Release, k.Edit, k.Archive, k.Assign},
			[]kernel.Binding{k.Sort, k.Arrange, k.Pick, k.Filter, k.Excluded, k.Find}),
	}
	sets[setCounting] = kernel.KeySet{
		Acts: []kernel.Binding{edit, archive, assign, sort, arrange, pick, filter, excluded, find},
		Full: append(append([][]kernel.Binding(nil), motions...),
			[]kernel.Binding{k.Edit, k.Archive, k.Assign},
			[]kernel.Binding{k.Sort, k.Arrange, k.Pick, k.Filter, k.Excluded, k.Find}),
	}
	sets[setEditing] = liveSets[keysEditing]
	sets[setSaving] = liveSets[keysSaving]
	sets[setSorting] = liveSets[keysSorting]
	sets[setFinding] = liveSets[keysFinding]
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
	case m.counting != "":
		state = setCounting
	}
	return setSets[state], int(state)
}

func (m *Model) setKey(stroke string) (tea.Cmd, bool) {
	switch m.set.acts[stroke] {
	case setActArrange:
		return m.cycleArrange(), true
	case setActPick:
		return m.cyclePick(), true
	case setActExcluded:
		return m.toggleExcluded(), true
	case setActNone:
	}
	if m.acts[stroke] == actRelease && m.cursor >= 0 && m.cursor < len(m.order) && m.order[m.cursor].v < 0 {
		return m.foldHeader(m.cursor), true
	}
	return nil, false
}
