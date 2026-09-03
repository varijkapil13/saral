package kernel

import (
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Binding is a keybinding. It is aliased so that a view package registers keys
// without importing bubbles itself.
type Binding = key.Binding

// Bind builds a keybinding: the keys it matches, how the key is written in
// help, and what it does.
func Bind(keys []string, shown, desc string) Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(shown, desc))
}

// Terse relabels a binding for the footer's action cell: the same strokes, the
// same key label, and a description short enough for a row shared with the root
// cell and the globals. The binding it was made from keeps the sentence, and that
// is what the ? overlay lists — "edit" on the row, "edit fields" in the overlay.
//
// It takes a binding rather than a pair of strings so that a key cannot be spelt
// twice: what a view tells the user to press has one source.
func Terse(b Binding, desc string) Binding {
	return Bind(b.Keys(), b.Help().Key, desc)
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
	// than forwarding it, because five views spend g on gestures of their own,
	// and answers it with the destinations while it waits.
	Go Binding
	// Slot switches to a footer slot. Its keys are the bare digits because that
	// is what arrives after the prefix; its help text is the whole gesture.
	Slot Binding
	// Saved runs the query bound to a number key. Same nine digits, pressed
	// alone, and only in a root view.
	Saved Binding
	// Jump completes the g prefix the way Slot completes it for a digit: it
	// opens the palette already armed with what gets typed next, so a key or a
	// pasted URL is a third route to the same place ctrl+k and the CLI
	// argument reach, per docs/UX.md principle 3. Its key is i rather than the
	// more obvious k or j: the destinations overlay this prefix now opens
	// already spends both, and up/down, on moving its own cursor.
	Jump Binding
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
		Go:      Bind([]string{"g"}, "g", "where to go"),
		Slot:    Bind(digits, "g 1-9", "switch view"),
		Saved:   Bind(digits, "1-9", "saved query"),
		Jump:    Bind([]string{"i"}, "g i", "jump to an issue"),
	}
}

var digits = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

// KeySet renders the global keys for the footer and the help overlay.
func (g GlobalKeys) KeySet() KeySet {
	return KeySet{
		Short: []Binding{g.Help, g.Palette, g.Quit},
		Full:  [][]Binding{{g.Saved, g.Go, g.Slot, g.Jump, g.Back, g.Refresh, g.Purge}, {g.Palette, g.Help, g.Quit}},
	}
}

// keyMap adapts a KeySet to the interface the help component consumes.
type keyMap struct {
	short []Binding
	full  [][]Binding
}

func (k keyMap) ShortHelp() []Binding  { return k.short }
func (k keyMap) FullHelp() [][]Binding { return k.full }

// mergeKeys builds what the ? overlay lists, and it lists every key exactly once.
//
// The actions lead, in the order the row shows them, because what somebody opened
// the pane to do matters more than how to scroll it — but spelt out: the row has
// space for "edit" and the overlay has space for "edit fields", so the description
// comes from the view's Full entry for the same key. The rest of Full is drawn
// with the actions taken out of it, and a column with nothing left is not drawn at
// all. Without that the overlay carried each action twice, once terse and once in
// full, and two columns of it pushed the globals off the right of the screen.
func mergeKeys(view, global KeySet) keyMap {
	km := keyMap{
		short: append(append([]Binding(nil), view.Short...), global.Short...),
		full:  make([][]Binding, 0, len(view.Full)+len(global.Full)+1),
	}
	if len(view.Acts) > 0 {
		km.full = append(km.full, spellOut(view.Acts, view.Full))
	}
	for _, column := range view.Full {
		if rest := without(column, view.Acts); len(rest) > 0 {
			km.full = append(km.full, rest)
		}
	}
	km.full = append(km.full, global.Full...)
	return km
}

// spellOut is the actions with the longest description any column gives the same
// key, so that the overlay says what the row had no room to.
func spellOut(acts []Binding, full [][]Binding) []Binding {
	out := make([]Binding, len(acts))
	for i, act := range acts {
		out[i] = act
		for _, column := range full {
			for _, b := range column {
				if b.Help().Key == act.Help().Key && len(b.Help().Desc) > len(out[i].Help().Desc) {
					out[i] = b
				}
			}
		}
	}
	return out
}

// without is a column with the keys the leading column has already named taken
// out. Comparison is by label, because that is what a reader sees.
func without(column, acts []Binding) []Binding {
	out := make([]Binding, 0, len(column))
	for _, b := range column {
		named := false
		for _, act := range acts {
			if act.Help().Key == b.Help().Key {
				named = true
				break
			}
		}
		if !named {
			out = append(out, b)
		}
	}
	return out
}

// Stroke turns a binding's first stroke back into the keypress that triggers it,
// so that clicking what the footer advertises arrives at the view as the key it
// named rather than as a second code path. It is the inverse of the spelling
// tea.Key.String produces, which is what key.Matches compares against.
//
// It reports false for a stroke it cannot spell rather than sending a key that
// means something else: an unrecognised name would arrive as its first rune.
func Stroke(b Binding) (tea.KeyPressMsg, bool) {
	keys := b.Keys()
	if len(keys) == 0 {
		return tea.KeyPressMsg{}, false
	}
	return parseStroke(keys[0])
}

// namedKeys spells the keys that are not a single rune. It covers what this
// program binds; anything outside it is refused rather than guessed at.
var namedKeys = map[string]rune{
	"enter":     tea.KeyEnter,
	"tab":       tea.KeyTab,
	"esc":       tea.KeyEscape,
	"space":     tea.KeySpace,
	"backspace": tea.KeyBackspace,
	"delete":    tea.KeyDelete,
	"insert":    tea.KeyInsert,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
	"pgup":      tea.KeyPgUp,
	"pgdown":    tea.KeyPgDown,
}

var strokeMods = []struct {
	prefix string
	mod    tea.KeyMod
}{
	{"ctrl+", tea.ModCtrl},
	{"alt+", tea.ModAlt},
	{"shift+", tea.ModShift},
}

func parseStroke(s string) (tea.KeyPressMsg, bool) {
	var mod tea.KeyMod
	for again := true; again; {
		again = false
		for _, m := range strokeMods {
			if rest, cut := strings.CutPrefix(s, m.prefix); cut {
				s, mod, again = rest, mod|m.mod, true
				break
			}
		}
	}
	if code, named := namedKeys[s]; named {
		return tea.KeyPressMsg{Code: code, Mod: mod}, true
	}
	r, size := utf8.DecodeRuneInString(s)
	if size != len(s) || r == utf8.RuneError {
		return tea.KeyPressMsg{}, false
	}
	// Text is what a field types; a modified key produces none.
	press := tea.KeyPressMsg{Code: r, Mod: mod}
	if mod == 0 {
		press.Text = s
	}
	return press, true
}
