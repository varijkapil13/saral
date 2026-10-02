package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
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
	Act      kernel.Binding
	Editor   kernel.Binding
	Save     kernel.Binding
	UndoRow  kernel.Binding
	UndoAll  kernel.Binding
	Assign   kernel.Binding
	Move     kernel.Binding
	Comments kernel.Binding
	Parent   kernel.Binding
	Children kernel.Binding
}

var (
	sidebarKeys     = []string{"<"}
	descriptionKeys = []string{">"}
	resetKeys       = []string{"="}
)

// The three strokes that move the boundary between the regions. Every letter
// this pane has is spent on the document or on the issue, so punctuation is what
// was left; < and > point the way the divider goes rather than naming a region,
// which is the half that reads the same to a reader who came for the prose and
// one who came for the fields.
func sidebarBinding() kernel.Binding {
	return kernel.Local("issue", "sidebar", sidebarKeys, "<", "wider sidebar")
}

func descriptionBinding() kernel.Binding {
	return kernel.Local("issue", "description", descriptionKeys, ">", "wider description")
}

func resetBinding() kernel.Binding {
	return kernel.Local("issue", "reset", resetKeys, "=", "reset the split")
}

// editBinding is e, which edits the row under the sidebar cursor in place, or
// opens the description's inline editor while the description region has the
// keyboard. Act is the same gesture from enter, named for the row rather than
// for the key so the two never drift.
func editBinding() kernel.Binding {
	return kernel.Canon(kernel.ActEdit, "edit fields")
}

func actBinding() kernel.Binding {
	return kernel.Canon(kernel.ActOpen, "edit this row")
}

func editorBinding() kernel.Binding {
	return kernel.Canon(kernel.ActEditExternal, "open in $EDITOR")
}

func saveBinding() kernel.Binding {
	return kernel.Canon(kernel.ActSave, "save changes")
}

func undoRowBinding() kernel.Binding {
	return kernel.Canon(kernel.ActRevert, "revert this")
}

func undoAllBinding() kernel.Binding {
	return kernel.Canon(kernel.ActRevertAll, "revert all")
}

// assignBinding opens the assignee picker from wherever the cursor already
// is, rather than only from the Assignee row itself.
func assignBinding() kernel.Binding {
	return kernel.Canon(kernel.ActAssign)
}

func parentBinding() kernel.Binding {
	return kernel.Canon(kernel.ActParent, "open the parent")
}

func childrenBinding() kernel.Binding {
	return kernel.Canon(kernel.ActChildren, "list the children")
}

func openRefBinding() kernel.Binding {
	return kernel.Canon(kernel.ActOpen, "open it")
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Canon(kernel.ActUp),
		Down:     kernel.Canon(kernel.ActDown),
		PageUp:   kernel.Canon(kernel.ActPageUp),
		PageDown: kernel.Canon(kernel.ActPageDown),
		HalfUp:   kernel.Canon(kernel.ActHalfUp),
		HalfDown: kernel.Canon(kernel.ActHalfDown),
		Go:       kernel.Canon(kernel.ActGo, "go to"),
		Top:      kernel.Canon(kernel.ActTop, "top"),
		Bottom:   kernel.Canon(kernel.ActBottom, "bottom"),
		// The renderer never wraps code and never cuts a table, so a description
		// really does reach past its box: a Go signature is around eighty cells
		// and the widest box the wide mode gives it is seventy-eight.
		Left:     kernel.Canon(kernel.ActLeft, "pan left"),
		Right:    kernel.Canon(kernel.ActRight, "pan right"),
		Pane:     kernel.Canon(kernel.ActNextPane, "next pane"),
		PrevPane: kernel.Canon(kernel.ActPrevPane, "previous pane"),
		Expands:  kernel.Canon(kernel.ActFold, "expand or collapse"),
		Sidebar:  sidebarBinding(),
		Describe: descriptionBinding(),
		Reset:    resetBinding(),
		Edit:     editBinding(),
		Act:      actBinding(),
		Editor:   editorBinding(),
		Save:     saveBinding(),
		UndoRow:  undoRowBinding(),
		UndoAll:  undoAllBinding(),
		Assign:   assignBinding(),
		Move:     moveBinding(),
		Comments: commentsBinding(),
		Parent:   parentBinding(),
		Children: childrenBinding(),
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
	actEditor
	actSave
	actUndoRow
	actUndoAll
	actAssign
	actMove
	actComments
	actParent
	actChildren
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
		{k.Edit, actEdit}, {k.Act, actEdit}, {k.Editor, actEditor},
		{k.Save, actSave}, {k.UndoRow, actUndoRow}, {k.UndoAll, actUndoAll},
		{k.Assign, actAssign}, {k.Move, actMove}, {k.Comments, actComments},
		{k.Parent, actParent}, {k.Children, actChildren},
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
			kernel.Terse(k.Save, "save"),
			kernel.Terse(k.Move, "status"),
			k.Comments,
		},
		Menu: ShareBindings,
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
			{k.Pane, k.Edit, k.Act, k.Editor, k.Save, k.UndoRow, k.UndoAll, k.Assign, k.Move, k.Comments, k.Parent, k.Children},
			collabKeys,
		},
	}
}

// The states LiveKeys answers for, in a fixed order so the array it indexes
// into needs no allocation to build. lkBrowse* is the resting sidebar, split by
// which region has the keyboard and whether anything is dirty — the footer
// names save and undo only where there is something to save or undo.
const (
	lkBrowseDesc = iota
	lkBrowseDescDirty
	lkBrowseDetails
	lkBrowseDetailsDirty
	lkBrowseRef
	lkBrowseRefDirty
	lkBrowseComments
	lkBrowseCommentsDirty
	lkTyping
	lkDocEdit
	lkPicking
	lkPickFields
	lkPickConfirm
	lkSaving
	lkLeaving
	lkCount
)

// sideLiveSets is one set per state, built once at start-up. LiveKeys is called
// on every frame, so it hands back a stored value rather than assembling one.
//
// Acts is terse, the way the footer row needs it; Full carries the same keys
// at their full length in a column of its own, which is what lets the ?
// overlay say "edit fields" where the row had room only for "edit" —
// kernel.mergeKeys picks the longer description any column gives one key.
var sideLiveSets = func() [lkCount]kernel.KeySet {
	k := defaultKeys()
	baseActs := []kernel.Binding{kernel.Terse(k.Pane, "pane")}
	baseFull := []kernel.Binding{k.Pane}
	dirtyActs := []kernel.Binding{kernel.Terse(k.Save, "save"), kernel.Terse(k.UndoRow, "revert"), k.UndoAll}
	dirtyFull := []kernel.Binding{k.Save, k.UndoRow, k.UndoAll}
	commentsDirtyActs := []kernel.Binding{kernel.Terse(k.Save, "save"), k.UndoAll}
	commentsDirtyFull := []kernel.Binding{k.Save, k.UndoAll}
	restActs := []kernel.Binding{kernel.Terse(k.Move, "status"), k.Comments}
	restFull := append([]kernel.Binding{k.Assign, k.Move, k.Comments, k.Parent, k.Children}, collabKeys...)

	build := func(dirty bool, editActs, editFull []kernel.Binding) kernel.KeySet {
		acts := append(append([]kernel.Binding{}, baseActs...), editActs...)
		full := append(append([]kernel.Binding{}, baseFull...), editFull...)
		switch {
		case dirty && editActs == nil:
			acts = append(acts, commentsDirtyActs...)
			full = append(full, commentsDirtyFull...)
		case dirty:
			acts = append(acts, dirtyActs...)
			full = append(full, dirtyFull...)
		}
		acts = append(acts, restActs...)
		full = append(full, restFull...)
		return kernel.KeySet{Acts: acts, Full: [][]kernel.Binding{acts, full}, Menu: ShareBindings}
	}

	descActs, descFull := []kernel.Binding{kernel.Terse(k.Edit, "edit"), k.Editor}, []kernel.Binding{k.Edit, k.Editor}
	detailsActs, detailsFull := []kernel.Binding{kernel.Terse(k.Act, "edit")}, []kernel.Binding{k.Act, k.Edit}

	open := openRefBinding()
	refActs, refFull := []kernel.Binding{kernel.Terse(open, "open")}, []kernel.Binding{open}

	accept := kernel.Bind([]string{"enter"}, "enter", "keep this value")
	cancelTyping := kernel.Bind([]string{"esc"}, "esc", "leave it alone")
	commit := kernel.Bind([]string{"ctrl+s"}, "ctrl+s", "keep this description")
	cancelDoc := kernel.Bind([]string{"esc"}, "esc", "put it aside")
	saveNow := kernel.Bind([]string{"y"}, "y", "save")
	discardNow := kernel.Bind([]string{"n"}, "n", "discard")
	stay := kernel.Bind([]string{"esc"}, "esc", "stay")

	var out [lkCount]kernel.KeySet
	out[lkBrowseDesc] = build(false, descActs, descFull)
	out[lkBrowseDescDirty] = build(true, descActs, descFull)
	out[lkBrowseDetails] = build(false, detailsActs, detailsFull)
	out[lkBrowseDetailsDirty] = build(true, detailsActs, detailsFull)
	out[lkBrowseRef] = build(false, refActs, refFull)
	out[lkBrowseRefDirty] = build(true, refActs, refFull)
	out[lkBrowseComments] = build(false, nil, nil)
	out[lkBrowseCommentsDirty] = build(true, nil, nil)
	out[lkTyping] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{accept, cancelTyping},
		Full: [][]kernel.Binding{{accept, cancelTyping}, {widget.KillLine}},
	}
	out[lkDocEdit] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{commit, cancelDoc},
		Full: [][]kernel.Binding{{commit, cancelDoc}, {widget.KillLine}},
	}
	choose := kernel.Bind([]string{"enter"}, "enter", "choose")
	cancelPick := kernel.Bind([]string{"esc"}, "esc", "cancel")
	up, down := kernel.Bind([]string{"up"}, "↑", "up"), kernel.Bind([]string{"down"}, "↓", "down")
	out[lkPicking] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{choose, cancelPick},
		Full: [][]kernel.Binding{{down, up, choose, cancelPick}},
	}
	prev := kernel.Bind([]string{"left"}, "←", "previous value")
	next := kernel.Bind([]string{"right"}, "→", "next value")
	continueField := kernel.Bind([]string{"enter"}, "enter", "continue")
	backToList := kernel.Bind([]string{"esc"}, "esc", "back")
	out[lkPickFields] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{kernel.Terse(prev, "previous"), kernel.Terse(next, "next"), continueField},
		Full: [][]kernel.Binding{{down, up, prev, next}, {continueField, backToList}},
	}
	moveYes := kernel.Bind([]string{"y"}, "y", "move it")
	out[lkPickConfirm] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{moveYes, backToList},
		Full: [][]kernel.Binding{{moveYes, backToList}},
	}
	// A save in flight answers nothing of its own, and the footer then shows the
	// globals alone, which is the truth.
	out[lkSaving] = kernel.KeySet{Mode: kernel.Modal}
	out[lkLeaving] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{saveNow, discardNow, stay},
		Full: [][]kernel.Binding{{saveNow, discardNow, stay}},
	}
	return out
}()

// LiveKeys reports the keys that work in the state the pane is actually in:
// which region has the keyboard, whether a row is being typed into or the
// description textarea is open, a save in flight, and the leave prompt.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	idx := m.liveKeyIndex()
	return sideLiveSets[idx], idx
}

func (m *Model) liveKeyIndex() int {
	if m.leaving && m.stage != sideSaving {
		return lkLeaving
	}
	switch m.stage {
	case sideSaving:
		return lkSaving
	case sideTyping:
		return lkTyping
	case sideDocEdit:
		return lkDocEdit
	case sidePicking:
		return m.pickLiveKeyIndex()
	case sideBrowse:
	}
	dirty := m.anyDirty()
	switch m.focus {
	case regionDesc:
		if dirty {
			return lkBrowseDescDirty
		}
		return lkBrowseDesc
	case regionDetails:
		if cr := m.currentCursorRow(); cr != nil && (cr.kind == rkRef || cr.kind == rkMore) {
			if dirty {
				return lkBrowseRefDirty
			}
			return lkBrowseRef
		}
		if dirty {
			return lkBrowseDetailsDirty
		}
		return lkBrowseDetails
	default:
		if dirty {
			return lkBrowseCommentsDirty
		}
		return lkBrowseComments
	}
}

// pickLiveKeyIndex is the inline list's own three states: the candidates, a
// chosen status move's required-fields screen, and its confirmation.
func (m *Model) pickLiveKeyIndex() int {
	if m.pick == nil {
		return lkPicking
	}
	if m.pick.kind == rkStatus && m.pick.move != nil {
		if m.pick.confirming {
			return lkPickConfirm
		}
		return lkPickFields
	}
	return lkPicking
}

// KeyStates is every set the pane ever reports, its sheets' included.
func (m *Model) KeyStates() []kernel.KeySet {
	out := append([]kernel.KeySet{defaultKeys().keySet()}, sideLiveSets[:]...)
	for _, k := range sheetKeysets {
		out = append(out, k.sets[:]...)
	}
	return out
}

var (
	_ kernel.KeyReporter    = (*Model)(nil)
	_ kernel.KeyStateLister = (*Model)(nil)
)

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

// The detail pane's own LiveKeys, further down this file, is what actually
// answers for the keys that work: which region has the keyboard and the
// sidebar's own editing state both move it, and a single registered keySet
// cannot express either.
