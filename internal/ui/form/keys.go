package form

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = (*Model)(nil)

type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	Edit     kernel.Binding
	Clear    kernel.Binding
	Submit   kernel.Binding
	Retype   kernel.Binding
	// Choose is enter on the list of issue types, where it picks one rather than
	// opening an editor.
	Choose kernel.Binding
	// DocDone is ctrl+d in the long-text pane, where enter is a newline. It is
	// the same stroke Clear is on in the field list and means the opposite there,
	// which is the whole reason a footer has to answer for one state at a time.
	DocDone kernel.Binding

	// The chooser takes typing, so it moves on the arrows and the readline
	// keys only: j and k are values a filter has to be able to hold.
	Prev   kernel.Binding
	Next   kernel.Binding
	Toggle kernel.Binding
	Accept kernel.Binding
	Done   kernel.Binding
}

// defaultKeys is the form's keymap. Nothing here uses a bare letter the kernel
// already spends — q, r, R, ? and the digits — because a field list is a root
// gesture context and those would never reach it.
func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down", "tab"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Top:      kernel.Bind([]string{"home"}, "home", "first row"),
		Bottom:   kernel.Bind([]string{"end"}, "end", "last row"),
		Edit:     kernel.Bind([]string{"enter"}, "enter", "edit this field"),
		Clear:    kernel.Bind([]string{"ctrl+d"}, "ctrl+d", "empty this field"),
		Submit:   kernel.Bind([]string{"ctrl+s"}, "ctrl+s", "create the issue"),
		Retype:   kernel.Bind([]string{"ctrl+t"}, "ctrl+t", "change the issue type"),
		Prev:     kernel.Bind([]string{"up", "ctrl+p"}, "↑", "previous value"),
		Next:     kernel.Bind([]string{"down", "ctrl+n"}, "↓", "next value"),
		Toggle:   kernel.Bind([]string{"tab"}, "tab", "pick or unpick"),
		Accept:   kernel.Bind([]string{"enter"}, "enter", "take this value"),
		Done:     kernel.Bind([]string{"esc"}, "esc", "close the editor, keeping what is typed"),
		Choose:   kernel.Bind([]string{"enter"}, "enter", "use this issue type"),
		DocDone:  kernel.Bind([]string{"ctrl+d"}, "ctrl+d", "finish this text"),
	}
}

// keySet is the resting state: the field list with no editor over it. It is
// built from the same bindings the keystrokes are matched against, so a hint
// cannot go stale.
func (k keyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Edit, "edit"),
			kernel.Terse(k.Clear, "empty"),
			kernel.Terse(k.Submit, "create"),
			kernel.Terse(k.Retype, "type"),
		},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom},
			{k.Edit, k.Clear, k.Submit, k.Retype},
		},
	}
}

// keyState is which of the form's five states the keys belong to: choosing an
// issue type, the field list, and the three editors the list opens over itself.
type keyState int

const (
	keysTypes keyState = iota
	keysFields
	keysText
	keysDoc
	keysChoosing
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is called on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	// The editors keep their keys terse for the same reason the field list does:
	// "close the editor, keeping what is typed" is forty-three columns, and a row
	// that gives that up gives up the way out of the editor with it.
	keepAndClose := kernel.Terse(k.Done, "keep and close")
	sets[keysTypes] = kernel.KeySet{
		Acts: []kernel.Binding{k.Choose},
		Full: [][]kernel.Binding{{k.Down, k.Up, k.Top, k.Bottom, k.Choose}},
	}
	sets[keysFields] = k.keySet()
	sets[keysText] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Accept, "take it"), keepAndClose},
		Full: [][]kernel.Binding{{k.Accept, k.Done}},
	}
	sets[keysDoc] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.DocDone, "finish"), keepAndClose},
		Full: [][]kernel.Binding{{k.DocDone, k.Done}},
	}
	sets[keysChoosing] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Toggle, "pick"), kernel.Terse(k.Accept, "take it"), keepAndClose},
		Full: [][]kernel.Binding{{k.Next, k.Prev, k.PageDown, k.PageUp}, {k.Toggle, k.Accept, k.Done}},
	}
	return sets
}()

// LiveKeys reports the keys that work in the state the form is actually in.
// ctrl+d empties a field in the list and finishes the text in the long-text
// pane, and a footer that named one of those while the other was on screen is
// the drift this answers.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysFields
	switch {
	case m.edit == editText:
		state = keysText
	case m.edit == editDoc:
		state = keysDoc
	case m.edit == editChoose:
		state = keysChoosing
	case m.stage == stageTypes:
		state = keysTypes
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
	actEdit
	actClear
	actSubmit
	actRetype
	actToggle
	actAccept
	actDone
)

// tables turn the bindings into a keystroke lookup, built once per view.
func (k keyMap) tables() (list, chooser map[string]action) {
	list = table(
		binding{k.Up, actUp}, binding{k.Down, actDown},
		binding{k.PageUp, actPageUp}, binding{k.PageDown, actPageDown},
		binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Edit, actEdit}, binding{k.Clear, actClear},
		binding{k.Submit, actSubmit}, binding{k.Retype, actRetype},
	)
	chooser = table(
		binding{k.Prev, actUp}, binding{k.Next, actDown},
		binding{k.PageUp, actPageUp}, binding{k.PageDown, actPageDown},
		binding{k.Toggle, actToggle}, binding{k.Accept, actAccept},
		binding{k.Done, actDone},
	)
	return list, chooser
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
