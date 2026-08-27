package move

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = (*Model)(nil)

// keyMap is what the wizard answers to.
//
// esc is the kernel's everywhere but the one step that takes typing, so going
// back a step is shift+tab — the stroke onboarding already spends on the same
// question. g is bound to nothing: the kernel buffers it as the view-switch
// prefix and never forwards it.
type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	Act      kernel.Binding
	Prev     kernel.Binding
	Next     kernel.Binding
	Type     kernel.Binding
	Back     kernel.Binding
	Yes      kernel.Binding
	Notify   kernel.Binding
	Cancel   kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Top:      kernel.Bind([]string{"home"}, "home", "first row"),
		Bottom:   kernel.Bind([]string{"end"}, "end", "last row"),
		Act:      kernel.Bind([]string{"enter"}, "enter", "use the project under the cursor"),
		Prev:     kernel.Bind([]string{"h", "left"}, "←/h", "previous value"),
		Next:     kernel.Bind([]string{"l", "right"}, "→/l", "next value"),
		Type:     kernel.Bind([]string{"i"}, "i", "type a project key"),
		Back:     kernel.Bind([]string{"shift+tab"}, "shift+tab", "back a step"),
		Yes:      kernel.Bind([]string{"y"}, "y", "move them"),
		Notify:   kernel.Bind([]string{"n"}, "n", "email the watchers, or do not"),
		Cancel:   kernel.Bind([]string{"esc"}, "esc", "back to the projects"),
	}
}

// keySet is the resting state, which is the target project: the wizard opens on
// it with whatever the last session cached and nothing asked of the site.
func (k keyMap) keySet() kernel.KeySet { return liveSets[stepTarget] }

// liveSets is one set per step, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [steps]kernel.KeySet {
	k := defaultKeys()
	// enter answers a different question at every step, and a prompt keeps its
	// own words: what the footer calls it has to come from the step.
	lookUp := kernel.Bind([]string{"enter"}, "enter", "look this key up")
	useType := kernel.Bind([]string{"enter"}, "enter", "use this issue type")
	onward := kernel.Bind([]string{"enter"}, "enter", "carry on")
	closeIt := kernel.Bind([]string{"enter"}, "enter", "close")

	motions := []kernel.Binding{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom}
	var sets [steps]kernel.KeySet
	sets[stepTarget] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Act, "use it"), kernel.Terse(k.Type, "type a key")},
		Full: [][]kernel.Binding{motions, {k.Act, k.Type}},
	}
	sets[stepTyping] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(lookUp, "look it up"), kernel.Terse(k.Cancel, "the list")},
		Full: [][]kernel.Binding{{lookUp, k.Cancel}},
	}
	sets[stepType] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(useType, "use it"), kernel.Terse(k.Back, "back")},
		Full: [][]kernel.Binding{motions, {useType, k.Back}},
	}
	sets[stepStatus] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Prev, "previous"), kernel.Terse(k.Next, "next"),
			kernel.Terse(onward, "carry on"), kernel.Terse(k.Back, "back"),
		},
		Full: [][]kernel.Binding{motions, {k.Prev, k.Next}, {onward, k.Back}},
	}
	sets[stepFields] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Prev, "previous"), kernel.Terse(k.Next, "next"),
			kernel.Terse(onward, "carry on"), kernel.Terse(k.Back, "back"),
		},
		Full: [][]kernel.Binding{motions, {k.Prev, k.Next}, {onward, k.Back}},
	}
	sets[stepConfirm] = kernel.KeySet{
		Acts: []kernel.Binding{k.Yes, kernel.Terse(k.Notify, "notify"), kernel.Terse(k.Back, "back")},
		Full: [][]kernel.Binding{motions, {k.Yes, k.Notify, k.Back}},
	}
	// A queue that has been handed the move answers nothing of its own, and the
	// footer then shows the globals alone, which is the truth.
	sets[stepRunning] = kernel.KeySet{}
	sets[stepDone] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(closeIt, "close")},
		Full: [][]kernel.Binding{motions, {closeIt}},
	}
	return sets
}()

// LiveKeys reports the keys that work at the step the wizard is actually on. A
// run in flight answers nothing, and the step after it answers only the way out.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	return liveSets[m.step], int(m.step)
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
	actAct
	actPrev
	actNext
	actType
	actBack
	actYes
	actNotify
	actCancel
)

// table turns the bindings into a keystroke lookup, built once per wizard. The
// bindings stay the single source of truth for what a key does and for what the
// footer says it does.
func (k keyMap) table() map[string]action {
	return table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Act, actAct}, binding{k.Prev, actPrev}, binding{k.Next, actNext},
		binding{k.Type, actType}, binding{k.Back, actBack},
		binding{k.Yes, actYes}, binding{k.Notify, actNotify},
	)
}

// typingTable is the strokes the project-key field does not spend on text.
func (k keyMap) typingTable() map[string]action {
	return table(binding{k.Act, actAct}, binding{k.Cancel, actCancel})
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
