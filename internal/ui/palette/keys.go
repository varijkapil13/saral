package palette

import (
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var _ kernel.KeyReporter = (*Model)(nil)

// keyMap is what the palette answers to. Every letter goes into the filter, so
// moving the selection is arrows and their control-key twins and nothing else —
// a j that moves the cursor is a j nobody can search for.
type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Run      kernel.Binding
	// Open is enter over a cached issue. One stroke, two labels: what it does
	// depends on which half of the list the cursor is in, and the footer names
	// the one on screen.
	Open  kernel.Binding
	Close kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"up", "ctrl+p"}, "↑", "up"),
		Down:     kernel.Bind([]string{"down", "ctrl+n"}, "↓", "down"),
		PageUp:   kernel.Bind([]string{"pgup"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown"}, "pgdn", "page down"),
		Run:      kernel.Bind([]string{"enter"}, "enter", "run it"),
		Open:     kernel.Bind([]string{"enter"}, "enter", "open it"),
		Close:    kernel.Bind([]string{"esc"}, "esc", "close"),
	}
}

// keyState is which of the palette's states the keys belong to. It doubles as
// the generation the memoized chrome repaints on, so a state that is added has
// to be added here to be drawn.
type keyState int

const (
	keysOffering keyState = iota
	keysIssue
	keysNothing
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysOffering] = kernel.KeySet{
		Acts: []kernel.Binding{k.Run, k.Close},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.Run, k.Close},
			{widget.KillLine},
		},
	}
	sets[keysIssue] = kernel.KeySet{
		Acts: []kernel.Binding{k.Open, k.Close},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.Open, k.Close},
			{widget.KillLine},
		},
	}
	sets[keysNothing] = kernel.KeySet{
		Acts: []kernel.Binding{k.Close},
		Full: [][]kernel.Binding{{k.Close}, {widget.KillLine}},
	}
	return sets
}()

// LiveKeys reports the keys that work right now. A filter that matches nothing
// has nothing to run and nothing to move over, and advertising enter there names
// a key that is refused; over a cached issue the same stroke opens rather than
// runs, and the footer says which.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysOffering
	switch {
	case len(m.shown) == 0:
		state = keysNothing
	case m.onIssue():
		state = keysIssue
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
	actRun
	actClose
)

// table turns the bindings into a keystroke lookup, built once. The bindings
// stay the single source of truth for what a key does and for what the footer
// says it does.
func (k keyMap) table() map[string]action {
	entries := []struct {
		b kernel.Binding
		a action
	}{
		{k.Up, actUp}, {k.Down, actDown},
		{k.PageUp, actPageUp}, {k.PageDown, actPageDown},
		{k.Run, actRun}, {k.Open, actRun}, {k.Close, actClose},
	}
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
