package form

import "github.com/varijkapil13/saral/internal/ui/kernel"

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
		Top:      kernel.Bind([]string{"home"}, "home", "first field"),
		Bottom:   kernel.Bind([]string{"end"}, "end", "last field"),
		Edit:     kernel.Bind([]string{"enter"}, "enter", "edit this field"),
		Clear:    kernel.Bind([]string{"ctrl+d"}, "ctrl+d", "empty this field"),
		Submit:   kernel.Bind([]string{"ctrl+s"}, "ctrl+s", "create the issue"),
		Retype:   kernel.Bind([]string{"ctrl+t"}, "ctrl+t", "change the issue type"),
		Prev:     kernel.Bind([]string{"up", "ctrl+p"}, "↑", "previous value"),
		Next:     kernel.Bind([]string{"down", "ctrl+n"}, "↓", "next value"),
		Toggle:   kernel.Bind([]string{"tab"}, "tab", "pick or unpick"),
		Accept:   kernel.Bind([]string{"enter"}, "enter", "take this value"),
		Done:     kernel.Bind([]string{"esc"}, "esc", "close the editor, keeping what is typed"),
	}
}

// keySet is what the footer and the help overlay show, built from the same
// bindings the keystrokes are matched against so a hint cannot go stale.
func (k keyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Short: []kernel.Binding{k.Down, k.Up, k.Edit, k.Submit},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom},
			{k.Edit, k.Clear, k.Submit, k.Retype},
			{k.Accept, k.Toggle, k.Done},
		},
	}
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
