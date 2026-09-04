package list

import (
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var _ kernel.KeyReporter = (*Model)(nil)

type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	HalfUp   kernel.Binding
	HalfDown kernel.Binding
	Go       kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	Open     kernel.Binding
	Filter   kernel.Binding
	// FilterBy opens the picker: a facet, then one of the values this site
	// actually holds for it. It is a different thing from Filter, which narrows
	// the rows already loaded by what is typed.
	FilterBy kernel.Binding
	All      kernel.Binding
	Edit     kernel.Binding
	// Sort opens the picker that chooses the order the search on screen runs
	// in. Save moved off "s" to make room for it.
	Sort   kernel.Binding
	Save   kernel.Binding
	Accept kernel.Binding
	Clear  kernel.Binding
	// Unfilter takes an accepted filter off from the browsing state. esc is not
	// one of its keys: the kernel keeps that for itself in a root view, so naming
	// it here would advertise a stroke that never arrives.
	Unfilter kernel.Binding
	// Run and Keep answer the prompt that shows the search on screen: one runs
	// what has been typed into it, the other leaves the search alone.
	Run  kernel.Binding
	Keep kernel.Binding
	// Slot, Take and Drop answer the gesture that binds the search on screen to
	// a number key. bindKey reads the digit and the y itself, because every other
	// key ends the gesture and a table would have to enumerate the alphabet to
	// say so.
	Slot kernel.Binding
	Take kernel.Binding
	Drop kernel.Binding
	// SortPrev, SortNext, SortChoose and SortCancel answer the picker: left and
	// right move the cursor over the fields on offer, enter chooses the one
	// under it and esc leaves the order as it was.
	SortPrev   kernel.Binding
	SortNext   kernel.Binding
	SortChoose kernel.Binding
	SortCancel kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:         kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:       kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:     kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown:   kernel.Bind([]string{"pgdown", "ctrl+f", "space"}, "pgdn", "page down"),
		HalfUp:     kernel.Bind([]string{"ctrl+u"}, "ctrl+u", "half page up"),
		HalfDown:   kernel.Bind([]string{"ctrl+d"}, "ctrl+d", "half page down"),
		Go:         kernel.Bind([]string{"g"}, "g", "go to"),
		Top:        kernel.Bind([]string{"home"}, "g g", "first row"),
		Bottom:     kernel.Bind([]string{"G", "end"}, "G / g e", "last row"),
		Open:       kernel.Bind([]string{"enter"}, "enter", "open"),
		Filter:     kernel.Bind([]string{"/"}, "/", "filter"),
		FilterBy:   kernel.Bind([]string{"f"}, "f", "filter by a person, a status, a label"),
		All:        kernel.Bind([]string{"a"}, "a", "all issues"),
		Edit:       kernel.Bind([]string{"e"}, "e", "edit this search"),
		Sort:       kernel.Bind([]string{"s"}, "s", "sort"),
		Save:       kernel.Bind([]string{"S"}, "S", "save this query to a key"),
		Accept:     kernel.Bind([]string{"enter"}, "enter", "keep filter"),
		Clear:      kernel.Bind([]string{"esc", "ctrl+g"}, "esc", "clear filter"),
		Unfilter:   kernel.Bind([]string{"ctrl+g"}, "ctrl+g", "clear everything narrowing these rows"),
		Run:        kernel.Bind([]string{"enter"}, "enter", "run this search"),
		Keep:       kernel.Bind([]string{"esc", "ctrl+g"}, "esc", "keep the one on screen"),
		Slot:       kernel.Bind(digits, "1-9", "the key to bind it to"),
		Take:       kernel.Bind([]string{"y"}, "y", "take the key"),
		Drop:       kernel.Bind([]string{"esc"}, "esc", "leave it alone"),
		SortPrev:   kernel.Bind([]string{"left", "h"}, "←/h", "previous field"),
		SortNext:   kernel.Bind([]string{"right", "l"}, "→/l", "next field"),
		SortChoose: kernel.Bind([]string{"enter"}, "enter", "choose this order"),
		SortCancel: kernel.Bind([]string{"esc"}, "esc", "leave the order as it is"),
	}
}

var digits = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

// keySet is the resting state: a list nobody is typing into. Nothing here is a
// second copy of the bindings above — both come from the same value, so a key
// that moves cannot leave a stale hint behind.
func (k keyMap) keySet() kernel.KeySet { return k.browsing(false) }

// browsing is the resting state, with or without a term or a filter already
// narrowing the rows. The narrowed one offers the key that clears them.
//
// Everything the list can do is offered, in the order it gets used. Nothing is
// left out of the inventory to make it fit: whatever the row cannot hold folds
// into a +N, and the overlay lists it.
func (k keyMap) browsing(narrowed bool) kernel.KeySet {
	all, search := kernel.Terse(k.All, "all"), kernel.Terse(k.Edit, "search")
	sort, save := kernel.Terse(k.Sort, "sort"), kernel.Terse(k.Save, "save")
	by := kernel.Terse(k.FilterBy, "filter by")
	acts := []kernel.Binding{k.Open, by, k.Filter, all, search, sort, save}
	actions := []kernel.Binding{k.Open, k.FilterBy, k.Filter, k.All, k.Edit, k.Sort, k.Save}
	if narrowed {
		acts = []kernel.Binding{k.Open, by, k.Filter, kernel.Terse(k.Unfilter, "clear"), all, search, sort, save}
		actions = []kernel.Binding{k.Open, k.FilterBy, k.Filter, k.Unfilter, k.All, k.Edit, k.Sort, k.Save}
	}
	return kernel.KeySet{
		Acts: acts,
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.HalfDown, k.HalfUp, k.Top, k.Bottom},
			actions,
		},
	}
}

// keyState is which of the view's states the keys belong to. It doubles as the
// generation the chrome memoizes on, so a state that is added has to be added
// here to be drawn.
type keyState int

const (
	keysBrowsing keyState = iota
	keysNarrowed
	keysFiltering
	keysPickingSlot
	keysConfirmingSlot
	keysAsking
	keysSorting
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is called on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysBrowsing] = k.keySet()
	sets[keysNarrowed] = k.browsing(true)
	// A prompt keeps its words: two answers to one question always fit, and what
	// they are called is the whole point of asking.
	sets[keysFiltering] = kernel.KeySet{
		Acts: []kernel.Binding{k.Accept, k.Clear},
		Full: [][]kernel.Binding{{k.Accept, k.Clear}, {widget.KillLine}},
	}
	sets[keysPickingSlot] = kernel.KeySet{
		Acts: []kernel.Binding{k.Slot, k.Drop},
		Full: [][]kernel.Binding{{k.Slot, k.Drop}},
	}
	sets[keysConfirmingSlot] = kernel.KeySet{
		Acts: []kernel.Binding{k.Take, k.Drop},
		Full: [][]kernel.Binding{{k.Take, k.Drop}},
	}
	sets[keysAsking] = kernel.KeySet{
		Acts: []kernel.Binding{k.Run, k.Keep},
		Full: [][]kernel.Binding{{k.Run, k.Keep}, {widget.KillLine}},
	}
	sets[keysSorting] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.SortPrev, "prev"), kernel.Terse(k.SortNext, "next"), k.SortChoose, k.SortCancel,
		},
		Full: [][]kernel.Binding{{k.SortPrev, k.SortNext}, {k.SortChoose, k.SortCancel}},
	}
	return sets
}()

// LiveKeys reports the keys that work in the state the list is actually in. An
// open filter answers enter and esc and nothing else; the prompt holding the
// search answers the same two strokes for two other things; the gesture that
// binds a number key answers a digit, then a y; a list already narrowed by a
// term or a filter offers the key that clears them.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowsing
	switch {
	case m.filtering:
		state = keysFiltering
	case m.asking:
		state = keysAsking
	case m.sorting:
		state = keysSorting
	case m.bind == bindPick:
		state = keysPickingSlot
	case m.bind == bindConfirm:
		state = keysConfirmingSlot
	case m.keptFilter() || len(m.terms) > 0:
		state = keysNarrowed
	}
	return liveSets[state], int(state)
}

type action uint8

const (
	actNone action = iota
	actDown
	actUp
	actPageDown
	actPageUp
	actHalfDown
	actHalfUp
	actGo
	actTop
	actBottom
	actOpen
	actFilter
	actFilterBy
	actAll
	actEdit
	actSort
	actSave
	actAccept
	actClear
	actRun
	actKeep
	actSortPrev
	actSortNext
	actSortChoose
	actSortCancel
)

// tables turn the bindings into a keystroke lookup, built once. The bindings
// stay the single source of truth for what a key does and for what the footer
// says it does, and a keystroke costs one map probe rather than a walk over
// every binding — which on this path is per keypress, at ten thousand rows.
func (k keyMap) tables() (normal, filtering, asking, sorting map[string]action) {
	normal = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.HalfDown, actHalfDown}, binding{k.HalfUp, actHalfUp},
		binding{k.Go, actGo}, binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Open, actOpen}, binding{k.Filter, actFilter},
		binding{k.FilterBy, actFilterBy}, binding{k.Unfilter, actClear},
		binding{k.All, actAll}, binding{k.Edit, actEdit},
		binding{k.Sort, actSort}, binding{k.Save, actSave},
	)
	filtering = table(binding{k.Accept, actAccept}, binding{k.Clear, actClear})
	asking = table(binding{k.Run, actRun}, binding{k.Keep, actKeep})
	sorting = table(
		binding{k.SortPrev, actSortPrev}, binding{k.SortNext, actSortNext},
		binding{k.SortChoose, actSortChoose}, binding{k.SortCancel, actSortCancel},
	)
	return normal, filtering, asking, sorting
}

type binding struct {
	b kernel.Binding
	a action
}

func table(entries ...binding) map[string]action {
	out := make(map[string]action, len(entries)*2)
	for _, e := range entries {
		if !e.b.Enabled() {
			continue
		}
		for _, stroke := range e.b.Keys() {
			out[stroke] = e.a
		}
	}
	return out
}
