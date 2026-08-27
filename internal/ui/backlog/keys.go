package backlog

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
	// Pick is the multi-select. space rather than enter, because enter is the
	// answer to the two questions this view asks and one stroke cannot be both.
	Pick    kernel.Binding
	PickAll kernel.Binding
	Unpick  kernel.Binding
	Move    kernel.Binding
	Choose  kernel.Binding
	Back    kernel.Binding
	Confirm kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		HalfUp:   kernel.Bind([]string{"ctrl+u"}, "ctrl+u", "half page up"),
		HalfDown: kernel.Bind([]string{"ctrl+d"}, "ctrl+d", "half page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "first row"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G", "last row"),
		Pick:     kernel.Bind([]string{"space"}, "space", "pick or unpick this issue"),
		PickAll:  kernel.Bind([]string{"v"}, "v", "pick every issue in this section"),
		Unpick:   kernel.Bind([]string{"x"}, "x", "unpick everything"),
		Move:     kernel.Bind([]string{"m"}, "m", "move these issues to a sprint or the backlog"),
		Choose:   kernel.Bind([]string{"enter"}, "enter", "move them here"),
		Back:     kernel.Bind([]string{"esc"}, "esc", "leave them where they are"),
		Confirm:  kernel.Bind([]string{"y"}, "y", "go ahead"),
	}
}

// keySet is the resting state: rows on screen with nothing picked yet.
func (k keyMap) keySet() kernel.KeySet { return k.browsing(false) }

// browsing is the resting state, with and without a selection. The selection is
// what x is for, so the key that drops one is offered only where there is one.
func (k keyMap) browsing(picked bool) kernel.KeySet {
	pick, all := kernel.Terse(k.Pick, "pick"), kernel.Terse(k.PickAll, "pick all")
	move := kernel.Terse(k.Move, "move")
	acts := []kernel.Binding{pick, all, move}
	actions := []kernel.Binding{k.Pick, k.PickAll, k.Move}
	if picked {
		acts = append(acts, kernel.Terse(k.Unpick, "unpick all"))
		actions = append(actions, k.Unpick)
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
	keysPicked
	keysChoosing
	keysConfirming
	keysMoving
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysBrowsing] = k.browsing(false)
	sets[keysPicked] = k.browsing(true)
	sets[keysChoosing] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Choose, "move here"), kernel.Terse(k.Back, "cancel")},
		Full: [][]kernel.Binding{{k.Down, k.Up}, {k.Choose, k.Back}},
	}
	sets[keysConfirming] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Confirm, "go ahead"), kernel.Terse(k.Back, "cancel")},
		Full: [][]kernel.Binding{{k.Confirm, k.Back}},
	}
	// A move in flight has nothing of its own to offer: the chunks the site has
	// left are the only thing that ends it, and naming a key here would name one
	// that is refused.
	sets[keysMoving] = kernel.KeySet{}
	return sets
}()

// LiveKeys reports the keys that work in the state the backlog is actually in.
// A selection offers the key that schedules it, the sprint list and the confirm
// answer two strokes each, and a move in flight answers nothing.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowsing
	switch {
	case m.mode == choosing:
		state = keysChoosing
	case m.mode == confirming:
		state = keysConfirming
	case m.mode == movingIssues:
		state = keysMoving
	case len(m.picked) > 0:
		state = keysPicked
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
	actHalfUp
	actHalfDown
	actGo
	actTop
	actBottom
	actPick
	actPickGroup
	actClear
	actMove
	actChoose
	actBack
	actConfirm
)

// tables turn the bindings into a keystroke lookup, built once. The bindings
// stay the single source of truth for what a key does and for what the footer
// says it does, and a keystroke costs one map probe rather than a walk over
// every binding.
func (k keyMap) tables() (browse, chooser, confirm map[string]action) {
	browse = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.HalfDown, actHalfDown}, binding{k.HalfUp, actHalfUp},
		binding{k.Go, actGo}, binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Pick, actPick}, binding{k.PickAll, actPickGroup},
		binding{k.Unpick, actClear}, binding{k.Move, actMove},
	)
	chooser = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.Choose, actChoose}, binding{k.Back, actBack},
	)
	confirm = table(binding{k.Confirm, actConfirm}, binding{k.Back, actBack})
	return browse, chooser, confirm
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
