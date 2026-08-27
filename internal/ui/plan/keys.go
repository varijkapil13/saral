package plan

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = (*Model)(nil)

// keyMap is what the view answers to. Open and Close are one stroke and two
// sentences, because what enter does to the plan under the cursor depends on
// whether it is already open.
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
	Open     kernel.Binding
	Close    kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Top:      kernel.Bind([]string{"home"}, "home", "first plan"),
		Bottom:   kernel.Bind([]string{"end"}, "end", "last plan"),
		Open:     kernel.Bind([]string{"enter"}, "enter", "show what this plan is made of"),
		Close:    kernel.Bind([]string{"enter"}, "enter", "hide what this plan is made of"),
	}
}

// keySet is the resting state, which is a plan on the cursor and closed: the
// view opens on the plans it can draw without asking anything.
func (k keyMap) keySet() kernel.KeySet { return liveSets[keysClosed] }

// keyState is which of the view's states the keys belong to. It doubles as the
// generation the memoized chrome repaints on, so a state that is added has to
// be added here to be drawn.
type keyState int

const (
	keysClosed keyState = iota
	keysOpen
	keysNothing
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	motions := []kernel.Binding{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom}
	var sets [keyStates]kernel.KeySet
	sets[keysClosed] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Open, "sources")},
		Full: [][]kernel.Binding{motions, {k.Open}},
	}
	sets[keysOpen] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Close, "hide")},
		Full: [][]kernel.Binding{motions, {k.Close}},
	}
	// With no plan under the cursor there is nothing enter could open, and
	// naming it would name a stroke that is refused. The way out and the way to
	// ask again are both the kernel's own.
	sets[keysNothing] = kernel.KeySet{}
	return sets
}()

// LiveKeys reports the keys that work in the state the view is actually in:
// enter opens the plan under the cursor, closes it once it is open, and does
// nothing at all when there is no plan to be on.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysNothing
	if at := m.planUnderCursor(); at >= 0 {
		state = keysClosed
		if m.open[m.plans[at].plan.ID] {
			state = keysOpen
		}
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
	actToggle
)

// table turns the bindings into a keystroke lookup, built once per view. The
// bindings stay the single source of truth for what a key does and for what the
// footer says it does.
func (k keyMap) table() map[string]action {
	return table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Open, actToggle}, binding{k.Close, actToggle},
	)
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
