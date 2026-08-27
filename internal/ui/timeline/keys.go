package timeline

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = (*Model)(nil)

type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	Earlier  kernel.Binding
	Later    kernel.Binding
	Open     kernel.Binding
	ZoomIn   kernel.Binding
	ZoomOut  kernel.Binding
	Today    kernel.Binding
	Notes    kernel.Binding
	// Hide is the same stroke as Notes with the sentence the open pane needs, so
	// that the overlay does not offer to open what is already open.
	Hide kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Top:      kernel.Bind([]string{"home"}, "home", "first row"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G", "last row"),
		Earlier:  kernel.Bind([]string{"h", "left"}, "←/h", "earlier"),
		Later:    kernel.Bind([]string{"l", "right"}, "→/l", "later"),
		Open:     kernel.Bind([]string{"enter"}, "enter", "open this issue"),
		ZoomIn:   kernel.Bind([]string{"+"}, "+", "zoom in to a shorter period"),
		ZoomOut:  kernel.Bind([]string{"-"}, "-", "zoom out to a longer period"),
		Today:    kernel.Bind([]string{"T"}, "T", "centre the chart on today"),
		Notes:    kernel.Bind([]string{"n"}, "n", "where these dates came from"),
		Hide:     kernel.Bind([]string{"n"}, "n", "hide these notes"),
	}
}

// keySet is the resting state: bars on screen and nothing over them.
func (k keyMap) keySet() kernel.KeySet { return liveSets[keysBrowsing] }

// keyState is which of the view's states the keys belong to. It doubles as the
// generation the chrome memoizes on, so a state that is added has to be added
// here to be drawn.
type keyState int

const (
	keysBrowsing keyState = iota
	keysNothing
	keysNotes
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysBrowsing] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Open, "open"),
			kernel.Terse(k.ZoomIn, "zoom in"),
			kernel.Terse(k.ZoomOut, "zoom out"),
			kernel.Terse(k.Today, "today"),
			kernel.Terse(k.Notes, "notes"),
		},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom},
			{k.Later, k.Earlier},
			{k.Open, k.ZoomIn, k.ZoomOut, k.Today, k.Notes},
		},
	}
	// With no bar to open and no span to zoom over, the notes are the whole of
	// what works: they are where a missing field and an unreadable board are
	// named, which is what an empty chart is usually about.
	sets[keysNothing] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Notes, "notes")},
		Full: [][]kernel.Binding{{k.Notes}},
	}
	sets[keysNotes] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Hide, "hide notes")},
		Full: [][]kernel.Binding{{k.Down, k.Up}, {k.Hide}},
	}
	return sets
}()

// LiveKeys reports the keys that work in the state the chart is actually in. The
// notes pane answers two strokes and the chart's own none, and an empty chart
// offers the notes rather than a zoom over nothing.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowsing
	switch {
	case m.notes:
		state = keysNotes
	case len(m.rows) == 0:
		state = keysNothing
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
	actEarlier
	actLater
	actOpen
	actZoomIn
	actZoomOut
	actToday
	actNotes
)

// tables turn the bindings into a keystroke lookup, built once. The bindings
// stay the single source of truth for what a key does and for what the footer
// says it does, and a keystroke costs one map probe rather than a walk.
func (k keyMap) tables() (chart, notes map[string]action) {
	chart = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Earlier, actEarlier}, binding{k.Later, actLater},
		binding{k.Open, actOpen},
		binding{k.ZoomIn, actZoomIn}, binding{k.ZoomOut, actZoomOut},
		binding{k.Today, actToday}, binding{k.Notes, actNotes},
	)
	notes = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.Hide, actNotes},
	)
	return chart, notes
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
