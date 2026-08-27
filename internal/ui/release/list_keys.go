package release

import "github.com/varijkapil13/saral/internal/ui/kernel"

var _ kernel.KeyReporter = (*Model)(nil)

// keyMap is what the versions list answers to. It is two keymaps in one value:
// the rows are browsed with vim keys like every other list here, and the editor
// is typed into, where every letter belongs to a field and only tab, the arrows
// and two control keys mean anything else.
//
// g is bound as the first half of this view's own g g, which the kernel buffers
// and hands over in the order it was typed. Nothing else here may claim a
// kernel global: q, r, R, ?, esc, ctrl+k and a bare digit never reach a view
// that is not taking typing.
type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Go       kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding

	// Release opens the flow rather than releasing anything. The flow is where
	// the count is shown and the decision is made, and it is the only way into
	// the write.
	Release kernel.Binding
	New     kernel.Binding
	Edit    kernel.Binding
	// Archive is one key for both directions: an archived version is
	// unarchived by the same stroke, because it is one flag and a second key
	// would be a second thing to learn.
	Archive kernel.Binding

	NextField kernel.Binding
	PrevField kernel.Binding
	Save      kernel.Binding
	// Cancel is esc while the editor is open. The kernel keeps esc for itself
	// in a root view, so it arrives here only because the editor is taking
	// typing.
	Cancel kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "first version"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G", "last version"),

		Release: kernel.Bind([]string{"enter"}, "enter", "release this version"),
		New:     kernel.Bind([]string{"n"}, "n", "new version"),
		Edit:    kernel.Bind([]string{"e"}, "e", "edit this version"),
		Archive: kernel.Bind([]string{"A"}, "A", "archive or unarchive it"),

		NextField: kernel.Bind([]string{"tab", "down"}, "tab", "next field"),
		PrevField: kernel.Bind([]string{"shift+tab", "up"}, "shift+tab", "previous field"),
		Save:      kernel.Bind([]string{"ctrl+s"}, "ctrl+s", "save this version"),
		Cancel:    kernel.Bind([]string{"esc"}, "esc", "leave it alone"),
	}
}

// keySet is the resting state: a list of versions nobody is typing into.
func (k keyMap) keySet() kernel.KeySet { return liveSets[keysBrowsing] }

// keyState is which of the list's states the keys belong to. It doubles as the
// generation the memoized chrome repaints on, so a state that is added has to be
// added here to be drawn.
type keyState int

const (
	keysBrowsing keyState = iota
	keysCounting
	keysEditing
	keysSaving
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	create, edit := kernel.Terse(k.New, "new"), kernel.Terse(k.Edit, "edit")
	archive := kernel.Terse(k.Archive, "archive")
	motions := [][]kernel.Binding{
		{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom},
	}

	var sets [keyStates]kernel.KeySet
	sets[keysBrowsing] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Release, "release"), create, edit, archive},
		Full: append(append([][]kernel.Binding(nil), motions...),
			[]kernel.Binding{k.Release, k.New, k.Edit, k.Archive}),
	}
	// While the site is being asked what is open on a version, releasing is the
	// one thing that cannot be done: the count is what the decision is made
	// against. Everything else still works, so the row says so rather than
	// falling back to the globals.
	sets[keysCounting] = kernel.KeySet{
		Acts: []kernel.Binding{create, edit, archive},
		Full: append(append([][]kernel.Binding(nil), motions...),
			[]kernel.Binding{k.New, k.Edit, k.Archive}),
	}
	sets[keysEditing] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Save, "save"), kernel.Terse(k.Cancel, "leave it")},
		Full: [][]kernel.Binding{
			{k.NextField, k.PrevField},
			{k.Save, k.Cancel},
		},
	}
	// A save in flight answers nothing at all. The text is still on screen and
	// still the reader's, and a key that appeared to take it back while the
	// write was out with the site would be a lie about which of the two won.
	sets[keysSaving] = kernel.KeySet{}
	return sets
}()

// LiveKeys reports the keys that work in the state the list is actually in.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowsing
	switch {
	case m.saving:
		state = keysSaving
	case m.mode == editing:
		state = keysEditing
	case m.counting != "":
		state = keysCounting
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
	actGo
	actTop
	actBottom
	actRelease
	actNew
	actEdit
	actArchive
	actNextField
	actPrevField
	actSave
	actCancel
)

// tables are the two keystroke lookups, one per half of the keymap.
func (k keyMap) tables() (browsing, editor map[string]action) {
	browsing = table(
		binding[action]{k.Down, actDown}, binding[action]{k.Up, actUp},
		binding[action]{k.PageDown, actPageDown}, binding[action]{k.PageUp, actPageUp},
		binding[action]{k.Go, actGo}, binding[action]{k.Top, actTop},
		binding[action]{k.Bottom, actBottom},
		binding[action]{k.Release, actRelease}, binding[action]{k.New, actNew},
		binding[action]{k.Edit, actEdit}, binding[action]{k.Archive, actArchive},
	)
	editor = table(
		binding[action]{k.NextField, actNextField}, binding[action]{k.PrevField, actPrevField},
		binding[action]{k.Save, actSave}, binding[action]{k.Cancel, actCancel},
	)
	return browsing, editor
}

// binding pairs a keybinding with what it does. It is generic because the list
// and the flow both dispatch this way over action enums of their own.
type binding[A comparable] struct {
	b kernel.Binding
	a A
}

// table turns bindings into a keystroke lookup, built once. The bindings stay
// the single source of truth for what a key does and for what the footer says it
// does, and a keystroke costs one map probe rather than a walk over every
// binding.
func table[A comparable](entries ...binding[A]) map[string]A {
	out := make(map[string]A, len(entries)*2)
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
