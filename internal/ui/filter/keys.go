package filter

import (
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var _ kernel.KeyReporter = (*Model)(nil)

// keyMap is what the picker answers to. It is two keymaps in one value: the
// facets are browsed with vim keys like every other list here, and the values
// are typed for, where every letter belongs to the needle and only the arrows
// and their control-key twins move a selection.
//
// g is bound to nothing on purpose: the kernel buffers it as the view-switch
// prefix and never forwards it, so a binding on it would advertise a stroke
// that cannot arrive.
type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	Choose   kernel.Binding

	TypeUp       kernel.Binding
	TypeDown     kernel.Binding
	TypePageUp   kernel.Binding
	TypePageDown kernel.Binding
	// Use is enter over a value, which is the same stroke as Choose over a facet
	// and a different sentence: one opens the values, the other puts one in
	// force and closes.
	Use kernel.Binding
	// Back is esc while a value is being typed for. The kernel keeps esc for
	// itself everywhere else, which is what closes the picker from the facets.
	Back kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Top:      kernel.Bind([]string{"home"}, "home", "first row"),
		Bottom:   kernel.Bind([]string{"end"}, "end", "last row"),
		Choose:   kernel.Bind([]string{"enter"}, "enter", "choose what to filter by"),

		TypeUp:       kernel.Bind([]string{"up", "ctrl+p"}, "↑", "up"),
		TypeDown:     kernel.Bind([]string{"down", "ctrl+n"}, "↓", "down"),
		TypePageUp:   kernel.Bind([]string{"pgup"}, "pgup", "page up"),
		TypePageDown: kernel.Bind([]string{"pgdown"}, "pgdn", "page down"),
		Use:          kernel.Bind([]string{"enter"}, "enter", "filter by this value"),
		Back:         kernel.Bind([]string{"esc"}, "esc", "back to the facets"),
	}
}

// keySet is the resting state, which is the facets: the picker opens on them
// and needs nothing of the site to draw them.
func (k keyMap) keySet() kernel.KeySet { return liveSets[keysFacets] }

// keyState is which of the picker's states the keys belong to. It doubles as
// the generation the memoized chrome repaints on, so a state that is added has
// to be added here to be drawn.
type keyState int

const (
	keysFacets keyState = iota
	keysValues
	keysNothing
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysFacets] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Choose, "choose")},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom},
			{k.Choose},
		},
	}
	sets[keysValues] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Use, "use it"), kernel.Terse(k.Back, "facets")},
		Full: [][]kernel.Binding{
			{k.TypeDown, k.TypeUp, k.TypePageDown, k.TypePageUp},
			{k.Use, k.Back},
			{widget.KillLine},
		},
	}
	// Nothing to choose is not nothing to do: the way back is the whole of what
	// works, and naming enter there names a stroke that is refused.
	sets[keysNothing] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Back, "facets")},
		Full: [][]kernel.Binding{{k.Back}, {widget.KillLine}},
	}
	return sets
}()

// LiveKeys reports the keys that work in the state the picker is actually in.
// The facets answer vim keys and enter; typing for a value spends every letter
// on the needle, so only the arrows move and esc is the way back; and a list
// with nothing on it offers the way back and nothing else.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysFacets
	switch {
	case m.state != pickValue:
	case len(m.shown) == 0:
		state = keysNothing
	default:
		state = keysValues
	}
	return liveSets[state], int(state)
}

type action uint8

const (
	actNone action = iota
	actUp
	actDown
	actPageUp
	actPageDown
	actTop
	actBottom
	actChoose
	actBack
)

// tables turn the bindings into a keystroke lookup, built once per picker. The
// bindings stay the single source of truth for what a key does and for what the
// footer says it does.
func (k keyMap) tables() (facets, values map[string]action) {
	facets = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Choose, actChoose},
	)
	values = table(
		binding{k.TypeDown, actDown}, binding{k.TypeUp, actUp},
		binding{k.TypePageDown, actPageDown}, binding{k.TypePageUp, actPageUp},
		binding{k.Use, actChoose}, binding{k.Back, actBack},
	)
	return facets, values
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
