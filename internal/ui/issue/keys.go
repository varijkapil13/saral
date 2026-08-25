package issue

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
	Edit     kernel.Binding
	Move     kernel.Binding
	Comments kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:   kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down: kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		// The pager is the widget's and key() hands it every stroke it did not
		// take, so these four have to spell what viewport.DefaultKeyMap answers
		// to. A stroke written here that the widget does not bind does nothing.
		PageUp:   kernel.Bind([]string{"pgup", "b"}, "b/pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "space", "f"}, "f/pgdn", "page down"),
		HalfUp:   kernel.Bind([]string{"u", "ctrl+u"}, "u/ctrl+u", "half page up"),
		HalfDown: kernel.Bind([]string{"d", "ctrl+d"}, "d/ctrl+d", "half page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "top"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G", "bottom"),
		Edit:     editBinding(),
		Move:     moveBinding(),
		Comments: commentsBinding(),
	}
}

// keySet is the pane's whole answer: what can be done to the issue on screen in
// Acts, and the thirteen strokes that only move around inside it in the overlay,
// which is the one with room for them.
func (k keyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Edit, "edit"),
			kernel.Terse(k.Move, "status"),
			k.Comments,
		},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.HalfDown, k.HalfUp, k.Top, k.Bottom},
			{k.Edit, k.Move, k.Comments},
		},
	}
}

// The detail pane implements no kernel.KeyReporter: it scrolls a document, and
// every one of its keys works whatever it is showing, so the registry's answer
// is already the live one. The two panes it opens are a different matter, and
// answer for themselves in edit_keys.go.
