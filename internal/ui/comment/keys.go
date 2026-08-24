package comment

import "github.com/varijkapil13/saral/internal/ui/kernel"

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
	Write    kernel.Binding
	Edit     kernel.Binding
	Delete   kernel.Binding
	Send     kernel.Binding
	Cancel   kernel.Binding
	Confirm  kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f", "space"}, "pgdn", "page down"),
		HalfUp:   kernel.Bind([]string{"ctrl+u"}, "ctrl+u", "half page up"),
		HalfDown: kernel.Bind([]string{"ctrl+d"}, "ctrl+d", "half page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "oldest"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G", "newest"),
		Write:    kernel.Bind([]string{"a", "c"}, "a", "write a comment"),
		Edit:     kernel.Bind([]string{"e"}, "e", "edit this one"),
		Delete:   kernel.Bind([]string{"d"}, "d", "delete this one"),
		Send:     kernel.Bind([]string{"ctrl+s"}, "ctrl+s", "send"),
		Cancel:   kernel.Bind([]string{"esc"}, "esc", "put it aside"),
		Confirm:  kernel.Bind([]string{"y"}, "y", "delete it"),
	}
}

// keySet is what the footer and the help overlay show. It is derived from the
// bindings above rather than written twice, so a key that moves cannot leave a
// stale hint behind.
func (k keyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Short: []kernel.Binding{k.Down, k.Up, k.Write, k.Edit, k.Delete},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.HalfDown, k.HalfUp, k.Top, k.Bottom},
			{k.Write, k.Edit, k.Delete, k.Send, k.Cancel},
		},
	}
}

type action uint8

const (
	actNone action = iota
	actDown
	actUp
	actPageDown
	actPageUp
	actHalfDown
	actHalfUp
	actGo
	actTop
	actBottom
	actWrite
	actEdit
	actDelete
	actSend
	actCancel
	actConfirm
)

// tables turn the bindings into a keystroke lookup, built once per model. The
// bindings stay the one source of truth for what a key does and for what the
// footer says it does.
func (k keyMap) tables() (browse, confirm map[string]action) {
	browse = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.HalfDown, actHalfDown}, binding{k.HalfUp, actHalfUp},
		binding{k.Go, actGo}, binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Write, actWrite}, binding{k.Edit, actEdit}, binding{k.Delete, actDelete},
	)
	confirm = table(binding{k.Confirm, actConfirm})
	return browse, confirm
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
