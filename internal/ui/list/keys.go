package list

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
	Open     kernel.Binding
	Filter   kernel.Binding
	Accept   kernel.Binding
	Clear    kernel.Binding
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
		Top:      kernel.Bind([]string{"home"}, "g g", "first row"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G", "last row"),
		Open:     kernel.Bind([]string{"enter"}, "enter", "open"),
		Filter:   kernel.Bind([]string{"/"}, "/", "filter"),
		Accept:   kernel.Bind([]string{"enter"}, "enter", "keep filter"),
		Clear:    kernel.Bind([]string{"esc", "ctrl+g"}, "esc", "clear filter"),
	}
}

// keySet is what the footer and the help overlay show. Nothing here is a second
// copy of the bindings above: both come from the same value, so a key that
// moves cannot leave a stale hint behind.
func (k keyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Short: []kernel.Binding{k.Down, k.Up, k.Open, k.Filter},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.HalfDown, k.HalfUp, k.Top, k.Bottom},
			{k.Open, k.Filter, k.Accept, k.Clear},
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
	actOpen
	actFilter
	actAccept
	actClear
)

// tables turn the bindings into a keystroke lookup, built once. The bindings
// stay the single source of truth for what a key does and for what the footer
// says it does, and a keystroke costs one map probe rather than a walk over
// every binding — which on this path is per keypress, at ten thousand rows.
func (k keyMap) tables() (normal, filtering map[string]action) {
	normal = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.HalfDown, actHalfDown}, binding{k.HalfUp, actHalfUp},
		binding{k.Go, actGo}, binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Open, actOpen}, binding{k.Filter, actFilter},
	)
	filtering = table(binding{k.Accept, actAccept}, binding{k.Clear, actClear})
	return normal, filtering
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
