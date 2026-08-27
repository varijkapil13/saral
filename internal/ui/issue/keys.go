package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

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
	Left     kernel.Binding
	Right    kernel.Binding
	Pane     kernel.Binding
	PrevPane kernel.Binding
	Expands  kernel.Binding
	Sidebar  kernel.Binding
	Describe kernel.Binding
	Reset    kernel.Binding
	Edit     kernel.Binding
	Move     kernel.Binding
	Comments kernel.Binding
}

// The three strokes that move the boundary between the regions. Every letter
// this pane has is spent on the document or on the issue, so punctuation is what
// was left; < and > point the way the divider goes rather than naming a region,
// which is the half that reads the same to a reader who came for the prose and
// one who came for the fields.
func sidebarBinding() kernel.Binding {
	return kernel.Bind([]string{"<"}, "<", "wider sidebar")
}

func descriptionBinding() kernel.Binding {
	return kernel.Bind([]string{">"}, ">", "wider description")
}

func resetBinding() kernel.Binding {
	return kernel.Bind([]string{"="}, "=", "reset the split")
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "b"}, "b/pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "space", "f"}, "f/pgdn", "page down"),
		HalfUp:   kernel.Bind([]string{"u", "ctrl+u"}, "u/ctrl+u", "half page up"),
		HalfDown: kernel.Bind([]string{"d", "ctrl+d"}, "d/ctrl+d", "half page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "top"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G / g e", "bottom"),
		// The renderer never wraps code and never cuts a table, so a description
		// really does reach past its box: a Go signature is around eighty cells
		// and the widest box the wide mode gives it is seventy-eight.
		Left:     kernel.Bind([]string{"h", "left"}, "←/h", "pan left"),
		Right:    kernel.Bind([]string{"l", "right"}, "→/l", "pan right"),
		Pane:     kernel.Bind([]string{"tab"}, "tab", "next pane"),
		PrevPane: kernel.Bind([]string{"shift+tab"}, "shift+tab", "previous pane"),
		Expands:  kernel.Bind([]string{"z"}, "z", "expand or collapse"),
		Sidebar:  sidebarBinding(),
		Describe: descriptionBinding(),
		Reset:    resetBinding(),
		Edit:     editBinding(),
		Move:     moveBinding(),
		Comments: commentsBinding(),
	}
}

// action is what one stroke does here.
type action uint8

// The actions, in the order the keymap declares them.
const (
	actNone action = iota
	actUp
	actDown
	actPageUp
	actPageDown
	actHalfUp
	actHalfDown
	actTop
	actBottom
	actLeft
	actRight
	actGo
	actPane
	actPrevPane
	actExpands
	actSidebar
	actDescribe
	actReset
	actEdit
	actMove
	actComments
)

// steps is the motion each action means, for the actions that are one.
var steps = map[action]step{
	actUp:       stepUp,
	actDown:     stepDown,
	actPageUp:   stepPageUp,
	actPageDown: stepPageDown,
	actHalfUp:   stepHalfUp,
	actHalfDown: stepHalfDown,
	actTop:      stepTop,
	actBottom:   stepBottom,
}

// strokes is every stroke this pane answers to and what it does, built once at
// start-up. Holding a keypress against a list of bindings builds a variadic
// slice per call, and a held-down j runs this path on every frame — so the
// bindings are turned inside out here rather than walked per keystroke, which is
// what the list and the comment thread already do.
var strokes = func() map[string]action {
	k := defaultKeys()
	out := make(map[string]action, 24)
	for _, pair := range []struct {
		binding kernel.Binding
		does    action
	}{
		{k.Up, actUp}, {k.Down, actDown},
		{k.PageUp, actPageUp}, {k.PageDown, actPageDown},
		{k.HalfUp, actHalfUp}, {k.HalfDown, actHalfDown},
		{k.Top, actTop}, {k.Bottom, actBottom},
		{k.Left, actLeft}, {k.Right, actRight},
		{k.Go, actGo},
		{k.Pane, actPane}, {k.PrevPane, actPrevPane},
		{k.Expands, actExpands},
		{k.Sidebar, actSidebar}, {k.Describe, actDescribe}, {k.Reset, actReset},
		{k.Edit, actEdit}, {k.Move, actMove}, {k.Comments, actComments},
	} {
		for _, stroke := range pair.binding.Keys() {
			out[stroke] = pair.does
		}
	}
	return out
}()

// keySet is the pane's whole answer: what can be done to the issue on screen in
// Acts, and the strokes that only move around inside it in the overlay, which is
// the one with room for them.
//
// tab is an action rather than a motion because below ninety columns it is the
// only way to reach the fields and the thread at all, and above it the rails say
// which region the keyboard is in. z is not: a description with no expand in it
// has nothing for it to open, and the footer names what can be done rather than
// every stroke the pane answers to. Nor are the three that move the divider:
// below the breakpoint there is no divider on screen to move, and a row naming a
// key that answers with a refusal is the failure principle 2 describes.
func (k keyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Pane, "pane"),
			kernel.Terse(k.Edit, "edit"),
			kernel.Terse(k.Move, "status"),
			k.Comments,
		},
		// Two columns of motions and one of actions. The overlay is one row of
		// columns shared with the globals, which take 47 of a 120-column screen,
		// and the actions column another 21 — so a third motion column, or a
		// description long enough to widen one of these two, pushes the globals
		// off the right of it. The three splitting strokes go in the second
		// column, whose key cell already holds shift+tab and whose widest
		// sentence is still expand or collapse, so the row does not grow.
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp, k.Right, k.Left},
			{k.HalfDown, k.HalfUp, k.Top, k.Bottom, k.PrevPane, k.Expands,
				k.Sidebar, k.Describe, k.Reset},
			{k.Pane, k.Edit, k.Move, k.Comments},
		},
	}
}

// threadStrokes is the stroke each motion is spelt as in the thread's own
// keymap. The thread is a view rather than a list of lines, so a motion aimed at
// it is handed over as the keypress it binds — and the two keymaps do not agree
// about every one of them: half a page is ctrl+u and ctrl+d there and u and d
// here, because this pane spends u and d on the document.
var threadStrokes = [stepCount]string{
	stepUp:       "k",
	stepDown:     "j",
	stepPageUp:   "pgup",
	stepPageDown: "pgdown",
	stepHalfUp:   "ctrl+u",
	stepHalfDown: "ctrl+d",
	stepTop:      "home",
	stepBottom:   "G",
}

// threadPanLeft and threadPanRight are the strokes the thread pans by. It pans
// for the same reason the description does — the renderer never wraps code — and
// it spells the two strokes the same way.
var threadPanLeft, threadPanRight = press("h"), press("l")

// press spells one stroke as the keypress the kernel would deliver. A stroke it
// cannot spell back leaves a zero value.
func press(stroke string) tea.KeyPressMsg {
	msg, _ := kernel.Stroke(kernel.Bind([]string{stroke}, stroke, stroke))
	return msg
}

// threadSteps is threadStrokes as keypresses, spelt once at start-up rather than
// per motion. A stroke the kernel cannot spell back leaves a zero entry.
var threadSteps = func() [stepCount]tea.KeyPressMsg {
	var out [stepCount]tea.KeyPressMsg
	for at, name := range threadStrokes {
		out[at] = press(name)
	}
	return out
}()

// The detail pane implements no kernel.KeyReporter: one key set answers for the
// whole view whichever region has the keyboard, so a stroke cannot mean one
// thing in the description and something else beside it. The two panes it opens
// are a different matter, and answer for themselves in edit_keys.go.
