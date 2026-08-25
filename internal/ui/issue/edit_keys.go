package issue

import "github.com/varijkapil13/saral/internal/ui/kernel"

// EditViewID and MoveViewID are the scopes the two pushed panes register their
// keys under. Neither is a footer slot: both are opened with the issue they are
// about, and a registry constructor has no issue to open them with.
const (
	EditViewID = "issue.edit"
	MoveViewID = "issue.move"
)

// editOpenKeys are the two bindings the detail pane hangs off. They live here
// rather than in the detail pane's keymap so that everything about editing an
// issue is in one place, and are named in that keymap so the footer and the
// help overlay advertise them.
func editBinding() kernel.Binding {
	return kernel.Bind([]string{"e"}, "e", "edit fields")
}

func moveBinding() kernel.Binding {
	return kernel.Bind([]string{"t"}, "t", "change status")
}

type editKeyMap struct {
	Up      kernel.Binding
	Down    kernel.Binding
	Act     kernel.Binding
	Prev    kernel.Binding
	Next    kernel.Binding
	Clear   kernel.Binding
	Save    kernel.Binding
	Discard kernel.Binding
	Accept  kernel.Binding
	Cancel  kernel.Binding
	Yes     kernel.Binding
}

func defaultEditKeys() editKeyMap {
	return editKeyMap{
		Up:      kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:    kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		Act:     kernel.Bind([]string{"enter"}, "enter", "change this field"),
		Prev:    kernel.Bind([]string{"h", "left"}, "←/h", "previous value"),
		Next:    kernel.Bind([]string{"l", "right"}, "→/l", "next value"),
		Clear:   kernel.Bind([]string{"delete"}, "del", "empty this field"),
		Save:    kernel.Bind([]string{"ctrl+s"}, "ctrl+s", "save"),
		Discard: kernel.Bind([]string{"X"}, "X", "discard changes"),
		Accept:  kernel.Bind([]string{"enter"}, "enter", "keep this value"),
		Cancel:  kernel.Bind([]string{"esc"}, "esc", "leave the value alone"),
		Yes:     kernel.Bind([]string{"y"}, "y", "go ahead"),
	}
}

// keySet is the resting state: the row list with no field open and nothing
// waiting to be answered.
func (k editKeyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Short: []kernel.Binding{k.Down, k.Up, k.Act, k.Save},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.Act, k.Clear},
			{k.Prev, k.Next},
			{k.Save, k.Discard},
		},
	}
}

// editLiveSets is one set per stage, built once at start-up. LiveKeys is called
// on every frame, so it hands back a stored value rather than assembling one.
var editLiveSets = func() [5]kernel.KeySet {
	k := defaultEditKeys()
	// esc backs out of three different things here, and what it leaves behind is
	// different each time, so each stage names it for itself.
	reread := kernel.Bind([]string{"y"}, "y", "re-read it and put your edits back on top")
	notYet := kernel.Bind([]string{"esc"}, "esc", "do not save yet")
	asItIs := kernel.Bind([]string{"esc"}, "esc", "leave it as it is for now")
	return [5]kernel.KeySet{
		stageBrowse: k.keySet(),
		stageTyping: {
			Short: []kernel.Binding{k.Accept, k.Cancel},
			Full:  [][]kernel.Binding{{k.Accept, k.Cancel}},
		},
		stageConfirm: {
			Short: []kernel.Binding{k.Yes, notYet},
			Full:  [][]kernel.Binding{{k.Yes, notYet}},
		},
		// A save in flight answers nothing of its own, and the footer then shows
		// the globals alone, which is the truth.
		stageSaving: {},
		stageConflict: {
			Short: []kernel.Binding{reread, asItIs},
			Full:  [][]kernel.Binding{{reread, asItIs}},
		},
	}
}()

// LiveKeys reports the keys that work in the stage the pane is actually in.
// enter commits a value while a field is open and opens one when none is, and y
// answers two different questions — go ahead with the save, and re-read after a
// conflict — so what the footer calls it has to come from the stage.
func (m *editModel) LiveKeys() (set kernel.KeySet, gen int) {
	return editLiveSets[m.stage], int(m.stage)
}

type moveKeyMap struct {
	Up     kernel.Binding
	Down   kernel.Binding
	Act    kernel.Binding
	Prev   kernel.Binding
	Next   kernel.Binding
	Yes    kernel.Binding
	Cancel kernel.Binding
}

func defaultMoveKeys() moveKeyMap {
	return moveKeyMap{
		Up:     kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:   kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		Act:    kernel.Bind([]string{"enter"}, "enter", "choose"),
		Prev:   kernel.Bind([]string{"h", "left"}, "←/h", "previous value"),
		Next:   kernel.Bind([]string{"l", "right"}, "→/l", "next value"),
		Yes:    kernel.Bind([]string{"y"}, "y", "go ahead"),
		Cancel: kernel.Bind([]string{"esc"}, "esc", "back"),
	}
}

// keySet is the resting state: the list of moves this issue can make from here.
func (k moveKeyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Short: []kernel.Binding{k.Down, k.Up, k.Act},
		Full:  [][]kernel.Binding{{k.Down, k.Up, k.Act}},
	}
}

// moveLiveSets is one set per stage, built once at start-up.
var moveLiveSets = func() [4]kernel.KeySet {
	k := defaultMoveKeys()
	// enter takes the move under the cursor in the list and finishes the screen
	// once it is filled in, so it is named for whichever of those is on screen.
	filled := kernel.Bind([]string{"enter"}, "enter", "use these values")
	return [4]kernel.KeySet{
		moveList: k.keySet(),
		moveScreen: {
			Short: []kernel.Binding{k.Down, k.Up, k.Prev, k.Next, filled},
			Full:  [][]kernel.Binding{{k.Down, k.Up, k.Prev, k.Next}, {filled, k.Cancel}},
		},
		moveConfirm: {
			Short: []kernel.Binding{k.Yes, k.Cancel},
			Full:  [][]kernel.Binding{{k.Yes, k.Cancel}},
		},
		moveDoing: {},
	}
}()

// LiveKeys reports the keys that work in the stage the picker is actually in. A
// transition screen moves between its fields and cycles their values; the list
// of moves does neither.
func (m *moveModel) LiveKeys() (set kernel.KeySet, gen int) {
	return moveLiveSets[m.stage], int(m.stage)
}

var (
	_ kernel.KeyReporter = (*editModel)(nil)
	_ kernel.KeyReporter = (*moveModel)(nil)
)
