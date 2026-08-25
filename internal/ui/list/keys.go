package list

import "github.com/varijkapil13/saral/internal/ui/kernel"

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
	Save     kernel.Binding
	Accept   kernel.Binding
	Clear    kernel.Binding
	// Unfilter takes an accepted filter off from the browsing state. esc is not
	// one of its keys: the kernel keeps that for itself in a root view, so naming
	// it here would advertise a stroke that never arrives.
	Unfilter kernel.Binding
	// Slot, Take and Drop answer the gesture that binds the search on screen to
	// a number key. bindKey reads the digit and the y itself, because every other
	// key ends the gesture and a table would have to enumerate the alphabet to
	// say so.
	Slot kernel.Binding
	Take kernel.Binding
	Drop kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f", "space"}, "pgdn", "page down"),
		HalfUp:   kernel.Bind([]string{"ctrl+u"}, "ctrl+u", "half page up"),
		HalfDown: kernel.Bind([]string{"ctrl+d"}, "ctrl+d", "half page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "first row"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G", "last row"),
		Open:     kernel.Bind([]string{"enter"}, "enter", "open"),
		Filter:   kernel.Bind([]string{"/"}, "/", "filter"),
		Save:     kernel.Bind([]string{"s"}, "s", "save this query to a key"),
		Accept:   kernel.Bind([]string{"enter"}, "enter", "keep filter"),
		Clear:    kernel.Bind([]string{"esc", "ctrl+g"}, "esc", "clear filter"),
		Unfilter: kernel.Bind([]string{"ctrl+g"}, "ctrl+g", "clear filter"),
		Slot:     kernel.Bind(digits, "1-9", "the key to bind it to"),
		Take:     kernel.Bind([]string{"y"}, "y", "take the key"),
		Drop:     kernel.Bind([]string{"esc"}, "esc", "leave it alone"),
	}
}

var digits = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

// keySet is the resting state: a list nobody is typing into. Nothing here is a
// second copy of the bindings above — both come from the same value, so a key
// that moves cannot leave a stale hint behind.
func (k keyMap) keySet() kernel.KeySet { return k.browsing(false) }

// browsing is the resting state, with or without a filter already narrowing the
// rows. The narrowed one offers the key that clears it.
func (k keyMap) browsing(narrowed bool) kernel.KeySet {
	short := []kernel.Binding{k.Down, k.Up, k.Open, k.Filter}
	actions := []kernel.Binding{k.Open, k.Filter, k.Save}
	if narrowed {
		short = []kernel.Binding{k.Down, k.Up, k.Open, k.Unfilter}
		actions = []kernel.Binding{k.Open, k.Filter, k.Unfilter, k.Save}
	}
	return kernel.KeySet{
		Short: short,
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.HalfDown, k.HalfUp, k.Top, k.Bottom},
			actions,
		},
	}
}

// keyState is which of the view's four states the keys belong to. It doubles as
// the generation the chrome memoizes on, so a state that is added has to be
// added here to be drawn.
type keyState int

const (
	keysBrowsing keyState = iota
	keysNarrowed
	keysFiltering
	keysPickingSlot
	keysConfirmingSlot
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is called on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysBrowsing] = k.keySet()
	sets[keysNarrowed] = k.browsing(true)
	sets[keysFiltering] = kernel.KeySet{
		Short: []kernel.Binding{k.Accept, k.Clear},
		Full:  [][]kernel.Binding{{k.Accept, k.Clear}},
	}
	sets[keysPickingSlot] = kernel.KeySet{
		Short: []kernel.Binding{k.Slot, k.Drop},
		Full:  [][]kernel.Binding{{k.Slot, k.Drop}},
	}
	sets[keysConfirmingSlot] = kernel.KeySet{
		Short: []kernel.Binding{k.Take, k.Drop},
		Full:  [][]kernel.Binding{{k.Take, k.Drop}},
	}
	return sets
}()

// LiveKeys reports the keys that work in the state the list is actually in. An
// open filter answers enter and esc and nothing else; the gesture that binds a
// number key answers a digit, then a y; a list already narrowed by a filter
// offers the key that widens it again.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowsing
	switch {
	case m.filtering:
		state = keysFiltering
	case m.bind == bindPick:
		state = keysPickingSlot
	case m.bind == bindConfirm:
		state = keysConfirmingSlot
	case m.keptFilter():
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
	actSave
	actAccept
	actClear
)

// tables turn the bindings into a keystroke lookup, built once. The bindings
// stay the single source of truth for what a key does and for what the footer
// says it does, and a keystroke costs one map probe rather than a walk over
// every binding — which on this path is per keypress, at ten thousand rows.
func (k keyMap) tables() (normal, filtering map[string]action) {
	normal = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.HalfDown, actHalfDown}, binding{k.HalfUp, actHalfUp},
		binding{k.Go, actGo}, binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Open, actOpen}, binding{k.Filter, actFilter},
		binding{k.Unfilter, actClear}, binding{k.Save, actSave},
	)
	filtering = table(binding{k.Accept, actAccept}, binding{k.Clear, actClear})
	return normal, filtering
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
