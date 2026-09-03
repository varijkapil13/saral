package settings

import "github.com/varijkapil13/saral/internal/ui/kernel"

// settingsKeys is what the screen answers to. Every row shape agrees on the
// same four directions and the same one key to act, so nothing here varies by
// what is under the cursor.
type settingsKeys struct {
	Up, Down, Left, Right, Choose kernel.Binding
}

func defaultKeys() settingsKeys {
	return settingsKeys{
		Up:     kernel.Bind([]string{"up", "k"}, "↑", "move"),
		Down:   kernel.Bind([]string{"down", "j"}, "↓", "move"),
		Left:   kernel.Bind([]string{"left", "h"}, "←", "change"),
		Right:  kernel.Bind([]string{"right", "l"}, "→", "change"),
		Choose: kernel.Bind([]string{"enter", "space"}, "enter", "apply, open or run"),
	}
}

func keySet() kernel.KeySet {
	k := defaultKeys()
	return kernel.KeySet{
		Acts: []kernel.Binding{k.Choose},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.Left, k.Right},
			{k.Choose},
		},
	}
}

type action uint8

const (
	actNone action = iota
	actUp
	actDown
	actLeft
	actRight
	actChoose
)

func (k settingsKeys) table() map[string]action {
	entries := []struct {
		b kernel.Binding
		a action
	}{
		{k.Up, actUp}, {k.Down, actDown},
		{k.Left, actLeft}, {k.Right, actRight},
		{k.Choose, actChoose},
	}
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
