package board

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = (*Model)(nil)

type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	Left     kernel.Binding
	Right    kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Go       kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	Open     kernel.Binding
	// Pick takes the card under the cursor off the board, which is the keyboard
	// half of a drag. Aiming it is Left and Right, and Drop is what lands it.
	Pick kernel.Binding
	Drop kernel.Binding
	// Cancel puts a picked-up card back. esc is not one of its keys: the kernel
	// keeps that for itself in a root view, so naming it here would advertise a
	// stroke that never arrives.
	Cancel kernel.Binding
	Board  kernel.Binding
	// Filters buffers, the way Go does: the digit it takes next is the
	// 1-indexed position of one of this board's own quick filters, toggling it
	// on or off and re-reading the board with the result.
	Filters kernel.Binding
	// FilterBy opens the same picker the issue list uses — a person, a status, a
	// type, a priority or a label — applied locally against what is already
	// loaded rather than sent to the site; see terms.go. Capitalised because
	// lowercase f is already the board's own quick filters.
	FilterBy kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		Left:     kernel.Bind([]string{"h", "left", "shift+tab"}, "←/h", "previous column"),
		Right:    kernel.Bind([]string{"l", "right", "tab"}, "→/l", "next column"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "first card in this column"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G / g e", "last card in this column"),
		Open:     kernel.Bind([]string{"enter"}, "enter", "open"),
		Pick:     kernel.Bind([]string{"m"}, "m", "move this issue to another column"),
		Drop:     kernel.Bind([]string{"enter"}, "enter", "move it to this column"),
		Cancel:   kernel.Bind([]string{"ctrl+g"}, "ctrl+g", "put it back"),
		Board:    kernel.Bind([]string{"b"}, "b", "another board of this project"),
		Filters:  kernel.Bind([]string{"f"}, "f 1-9", "quick filters"),
		FilterBy: kernel.Bind([]string{"F"}, "F", "filter by a person, a status, a label"),
	}
}

// keySet is the resting state: a board nobody is moving a card on.
func (k keyMap) keySet() kernel.KeySet { return liveSets[keysBrowsing] }

// keyState is which of the board's states the keys belong to. It doubles as the
// generation the memoized chrome repaints on, so a state that is added has to be
// added here to be drawn.
type keyState int

const (
	keysBrowsing keyState = iota
	keysHolding
	keysMoving
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysBrowsing] = kernel.KeySet{
		Acts: []kernel.Binding{k.Open, kernel.Terse(k.Pick, "move"), kernel.Terse(k.Board, "board")},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.Left, k.Right},
			{k.PageDown, k.PageUp, k.Top, k.Bottom},
			{k.Open, k.Pick, k.Board, k.Filters, k.FilterBy},
		},
	}
	// A card in hand can only be aimed and landed, so the whole inventory is the
	// two answers and the two ways of aiming. enter means something else here
	// than it does above, which is the reason a state reports for itself.
	sets[keysHolding] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Drop, "move it here"), kernel.Terse(k.Cancel, "put it back")},
		Full: [][]kernel.Binding{
			{k.Left, k.Right},
			{k.Drop, k.Cancel},
		},
	}
	// A move the site has not answered yet offers nothing: every key is refused
	// until it does, and naming one would name a stroke being refused.
	sets[keysMoving] = kernel.KeySet{}
	return sets
}()

// LiveKeys reports the keys that work in the state the board is actually in. A
// card in hand answers enter and ctrl+g for two things the resting state does
// not, and a move in flight answers nothing at all.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowsing
	switch {
	case m.moving:
		state = keysMoving
	case m.card != nil:
		state = keysHolding
	}
	return liveSets[state], int(state)
}

type action uint8

const (
	actNone action = iota
	actUp
	actDown
	actLeft
	actRight
	actPageUp
	actPageDown
	actGo
	actTop
	actBottom
	actOpen
	actPick
	actDrop
	actCancel
	actBoard
	actFilter
	actFilterBy
)

// tables turn the bindings into a keystroke lookup, built once per board. The
// bindings stay the single source of truth for what a key does and for what the
// footer says it does, and a keystroke costs one map probe rather than a walk
// over every binding.
func (k keyMap) tables() (browsing, holding map[string]action) {
	browsing = table(
		binding{k.Up, actUp}, binding{k.Down, actDown},
		binding{k.Left, actLeft}, binding{k.Right, actRight},
		binding{k.PageUp, actPageUp}, binding{k.PageDown, actPageDown},
		binding{k.Go, actGo}, binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Open, actOpen}, binding{k.Pick, actPick}, binding{k.Board, actBoard},
		binding{k.Filters, actFilter}, binding{k.FilterBy, actFilterBy},
	)
	// A card in hand answers only the keys the holding state advertises: the two
	// that aim it and the two that end the gesture. A motion that moved the
	// cursor here would leave the card behind whatever it moved to.
	holding = table(
		binding{k.Left, actLeft}, binding{k.Right, actRight},
		binding{k.Drop, actDrop}, binding{k.Cancel, actCancel},
	)
	return browsing, holding
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
