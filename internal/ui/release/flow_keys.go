package release

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = (*Flow)(nil)

// flowKeyMap is what the release screen answers to. It binds no esc: the flow is
// pushed, so the kernel's own esc pops it, and that is what leaving a version
// alone means here.
//
// The confirm is y and not enter. enter chooses on both list screens, and a
// stroke that means "next" on two screens and "release it" on the third is how
// somebody releases a version they were reading about.
type flowKeyMap struct {
	Up   kernel.Binding
	Down kernel.Binding
	// Choose and Use are the same stroke on two screens with two sentences: one
	// takes a decision, the other takes the version the issues move to.
	Choose  kernel.Binding
	Use     kernel.Binding
	Confirm kernel.Binding
	Again   kernel.Binding
}

func defaultFlowKeys() flowKeyMap {
	return flowKeyMap{
		Up:      kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:    kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		Choose:  kernel.Bind([]string{"enter"}, "enter", "choose what happens to the open issues"),
		Use:     kernel.Bind([]string{"enter"}, "enter", "move the open issues to this version"),
		Confirm: kernel.Bind([]string{"y"}, "y", "release it"),
		Again:   kernel.Bind([]string{"enter"}, "enter", "start again"),
	}
}

// keySet is the resting state: the three things that can happen to the issues
// still open on the version.
func (k flowKeyMap) keySet() kernel.KeySet { return flowSets[flowChoosing] }

// flowSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one. The
// index is the state itself, which is also the generation the chrome repaints
// on.
var flowSets = func() [flowKeyStates]kernel.KeySet {
	k := defaultFlowKeys()
	var sets [flowKeyStates]kernel.KeySet
	sets[flowChoosing] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Choose, "choose")},
		Full: [][]kernel.Binding{{k.Down, k.Up}, {k.Choose}},
	}
	sets[flowPicking] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Use, "move them here")},
		Full: [][]kernel.Binding{{k.Down, k.Up}, {k.Use}},
	}
	sets[flowConfirming] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Confirm, "go ahead")},
		Full: [][]kernel.Binding{{k.Confirm}},
	}
	// A release in flight answers nothing. It is one write, it cannot be taken
	// back half way, and a key that appeared to stop it would be a claim about
	// which of the two won.
	sets[flowWorking] = kernel.KeySet{}
	sets[flowStuck] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Again, "start again")},
		Full: [][]kernel.Binding{{k.Again}},
	}
	return sets
}()

// LiveKeys reports the keys that work on the screen the flow is actually
// showing.
func (f *Flow) LiveKeys() (set kernel.KeySet, gen int) {
	return flowSets[f.state], int(f.state)
}

type flowAction uint8

const (
	flowNone flowAction = iota
	flowUp
	flowDown
	flowChoose
	flowConfirm
)

// table turns the bindings into a keystroke lookup, built once per flow. enter
// is one action on every screen because what it does there is the screen's
// business; y reaches the write and is refused everywhere but the confirm.
func (k flowKeyMap) table() map[string]flowAction {
	return table(
		binding[flowAction]{k.Down, flowDown}, binding[flowAction]{k.Up, flowUp},
		binding[flowAction]{k.Choose, flowChoose},
		binding[flowAction]{k.Confirm, flowConfirm},
	)
}
