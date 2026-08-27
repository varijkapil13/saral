package attach

import (
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var _ kernel.KeyReporter = (*Model)(nil)

// keyMap is what the pane answers to.
//
// g is bound to nothing on purpose: the kernel buffers it as the view-switch
// prefix and never forwards it, so a binding on it would advertise a stroke that
// cannot arrive.
type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	// Show draws the file here where it is an image and hands it to the desktop
	// where it is not, because a terminal cannot draw a spreadsheet.
	Show kernel.Binding
	// Open is the desktop handler for anything, including an image somebody would
	// rather see at full size.
	Open   kernel.Binding
	Upload kernel.Binding
	Delete kernel.Binding
	// Grow folds the list away and gives the preview the whole box. An image
	// scaled into six rows is not one anybody can look at.
	Grow kernel.Binding
	Send kernel.Binding
	// Cancel is esc while a path is being typed. The kernel keeps esc for itself
	// everywhere else, which is what closes the pane.
	Cancel kernel.Binding
	// Confirm and Keep are the two answers a deletion waits for. Keep is the same
	// stroke as Cancel and means something else, which is why it is spelt
	// separately: here esc leaves the file where it is rather than closing a
	// prompt.
	Confirm kernel.Binding
	Keep    kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Top:      kernel.Bind([]string{"home"}, "home", "first file"),
		Bottom:   kernel.Bind([]string{"end"}, "end", "last file"),
		Show:     kernel.Bind([]string{"enter"}, "enter", "show this file"),
		Open:     kernel.Bind([]string{"o"}, "o", "open it outside the terminal"),
		Upload:   kernel.Bind([]string{"u"}, "u", "attach a file"),
		Delete:   kernel.Bind([]string{"d"}, "d", "delete this file"),
		Grow:     kernel.Bind([]string{"z"}, "z", "give the preview the whole pane"),
		Send:     kernel.Bind([]string{"ctrl+s", "enter"}, "ctrl+s", "attach it"),
		Cancel:   kernel.Bind([]string{"esc"}, "esc", "leave it unattached"),
		Confirm:  kernel.Bind([]string{"y"}, "y", "delete it"),
		Keep:     kernel.Bind([]string{"esc"}, "esc", "keep it"),
	}
}

// keySet is the resting record: files on the list and a token that may add and
// remove them. It is what the command sweep holds a palette entry's key against,
// so every key a command teaches is named here.
func (k keyMap) keySet() kernel.KeySet { return liveSets[keysReadingWrite] }

// keyState is which of the pane's states the keys belong to. It doubles as the
// generation the memoized chrome repaints on, so a state that is added has to be
// added here to be drawn.
//
// Whether files may be added is part of the state rather than a flag over it: the
// footer shows only keys that work, and the sets have to be stored rather than
// assembled per frame, so the two answers are two entries.
type keyState int

const (
	keysReading keyState = iota
	keysReadingWrite
	keysEmpty
	keysEmptyWrite
	keysTyping
	keysConfirming
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	motions := []kernel.Binding{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom}
	var sets [keyStates]kernel.KeySet
	sets[keysReading] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Show, "show"),
			kernel.Terse(k.Open, "open"),
			kernel.Terse(k.Grow, "bigger"),
		},
		Full: [][]kernel.Binding{motions, {k.Show, k.Open, k.Grow}},
	}
	sets[keysReadingWrite] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Show, "show"),
			kernel.Terse(k.Open, "open"),
			kernel.Terse(k.Upload, "attach"),
			kernel.Terse(k.Delete, "delete"),
			kernel.Terse(k.Grow, "bigger"),
		},
		Full: [][]kernel.Binding{motions, {k.Show, k.Open, k.Grow}, {k.Upload, k.Delete}},
	}
	// Nothing attached and nothing this token may attach: the way out is the
	// kernel's own and naming a key of this pane's would name one that is refused.
	sets[keysEmpty] = kernel.KeySet{Full: [][]kernel.Binding{{}}}
	sets[keysEmptyWrite] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Upload, "attach")},
		Full: [][]kernel.Binding{{k.Upload}},
	}
	sets[keysTyping] = kernel.KeySet{
		Acts: []kernel.Binding{k.Send, k.Cancel},
		Full: [][]kernel.Binding{{k.Send, k.Cancel}, {widget.KillLine}},
	}
	sets[keysConfirming] = kernel.KeySet{
		Acts: []kernel.Binding{k.Confirm, k.Keep},
		Full: [][]kernel.Binding{{k.Confirm, k.Keep}},
	}
	return sets
}()

// LiveKeys reports the keys that work in the state the pane is actually in.
// Typing a path spends every letter on the path, a deletion waiting for an answer
// takes two keys and no others, and a pane with nothing on it offers only what a
// token that may attach can do.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := m.keyState()
	return liveSets[state], int(state)
}

func (m *Model) keyState() keyState {
	switch {
	case m.mode == typing:
		return keysTyping
	case m.mode == confirming:
		return keysConfirming
	case len(m.files) > 0 && m.canWrite:
		return keysReadingWrite
	case len(m.files) > 0:
		return keysReading
	case m.canWrite:
		return keysEmptyWrite
	default:
		return keysEmpty
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
	actShow
	actOpen
	actUpload
	actDelete
	actGrow
	actSend
	actCancel
	actConfirm
)

// tables turn the bindings into a keystroke lookup, built once per pane. The
// bindings stay the single source of truth for what a key does and for what the
// footer says it does.
//
// There is one per state rather than one shared, because the same stroke is a
// different answer in each: enter shows a file on the list and sends the path in
// the prompt, and y is a letter somebody is typing in one state and the go-ahead
// for a deletion in another.
func (k keyMap) tables() (browse, prompt, confirm map[string]action) {
	browse = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.Show, actShow}, binding{k.Open, actOpen},
		binding{k.Upload, actUpload}, binding{k.Delete, actDelete},
		binding{k.Grow, actGrow},
	)
	prompt = table(binding{k.Send, actSend}, binding{k.Cancel, actCancel})
	confirm = table(binding{k.Confirm, actConfirm}, binding{k.Keep, actCancel})
	return browse, prompt, confirm
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
