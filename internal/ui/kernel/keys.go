package kernel

import (
	"charm.land/bubbles/v2/key"
)

// Binding is a keybinding. It is aliased so that a view package registers keys
// without importing bubbles itself.
type Binding = key.Binding

// Bind builds a keybinding: the keys it matches, how the key is written in
// help, and what it does.
func Bind(keys []string, shown, desc string) Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(shown, desc))
}

// Matches reports whether a key press triggers any of the bindings. A disabled
// binding never matches, which is how a footer hint and its key stay in step.
func Matches(msg interface{ String() string }, bindings ...Binding) bool {
	return key.Matches(msg, bindings...)
}

// GlobalKeys are the keys that work in every view. They are registered under
// GlobalScope so the help overlay lists them alongside the view's own.
type GlobalKeys struct {
	Quit    Binding
	Back    Binding
	Help    Binding
	Palette Binding
	Refresh Binding
	Purge   Binding
	// Go is the prefix the view slots sit behind. The kernel buffers it rather
	// than forwarding it, because two views already spend g on gg and ge.
	Go Binding
	// Slot switches to a footer slot. Its keys are the bare digits because that
	// is what arrives after the prefix; its help text is the whole gesture.
	Slot Binding
	// Saved runs the query bound to a number key. Same nine digits, pressed
	// alone, and only in a root view.
	Saved Binding
}

// DefaultGlobalKeys is the keymap from docs/UX.md. Vim keys and arrows are both
// always bound inside views; these are the ones the kernel itself handles.
func DefaultGlobalKeys() GlobalKeys {
	return GlobalKeys{
		Quit:    Bind([]string{"q", "ctrl+c"}, "q", "quit"),
		Back:    Bind([]string{"esc"}, "esc", "back"),
		Help:    Bind([]string{"?"}, "?", "help"),
		Palette: Bind([]string{"ctrl+k"}, "ctrl+k", "commands"),
		Refresh: Bind([]string{"r"}, "r", "refresh"),
		Purge:   Bind([]string{"R"}, "R", "refetch everything"),
		Go:      Bind([]string{"g"}, "g", "go to"),
		Slot:    Bind(digits, "g 1-9", "switch view"),
		Saved:   Bind(digits, "1-9", "saved query"),
	}
}

var digits = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

// KeySet renders the global keys for the footer and the help overlay.
func (g GlobalKeys) KeySet() KeySet {
	return KeySet{
		Short: []Binding{g.Help, g.Palette, g.Quit},
		Full:  [][]Binding{{g.Saved, g.Slot, g.Back, g.Refresh, g.Purge}, {g.Palette, g.Help, g.Quit}},
	}
}

// keyMap adapts a KeySet to the interface the help component consumes.
type keyMap struct {
	short []Binding
	full  [][]Binding
}

func (k keyMap) ShortHelp() []Binding  { return k.short }
func (k keyMap) FullHelp() [][]Binding { return k.full }

func mergeKeys(view, global KeySet) keyMap {
	km := keyMap{
		short: append(append([]Binding(nil), view.Short...), global.Short...),
		full:  append([][]Binding(nil), view.Full...),
	}
	km.full = append(km.full, global.Full...)
	return km
}
