package widget

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
)

// KillLine deletes from the cursor to the end of the line — the oldest readline
// habit there is in a terminal. bubbles binds it to ctrl+k, which the kernel
// keeps for the command palette and never forwards to a view even while one is
// taking typing, so every text widget in this program answers to alt+k instead.
//
// It is a kernel.Binding by alias, so a view registers it in the key set of the
// state its field is typed in. Nobody guesses alt+k, and ? is a character inside
// a field, so docs/UX.md is where it is taught.
var KillLine = key.NewBinding(key.WithKeys("alt+k"), key.WithHelp("alt+k", "delete to end of line"))

// NewInput is bubbles' single-line input with this program's keymap. Every text
// field is built through here rather than through textinput.New, so a new field
// cannot quietly keep a binding the kernel swallows.
func NewInput() textinput.Model {
	in := textinput.New()
	in.KeyMap.DeleteAfterCursor = KillLine
	return in
}

// NewArea is the multi-line editor, with the same correction.
func NewArea() textarea.Model {
	ta := textarea.New()
	ta.KeyMap.DeleteAfterCursor = KillLine
	return ta
}
