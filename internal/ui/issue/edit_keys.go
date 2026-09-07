package issue

import (
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// MoveViewID is the scope the transition picker registers its keys under. It
// is not a footer slot: it is opened with the issue it is about, and a
// registry constructor has no issue to open it with.
const MoveViewID = "issue.move"

// moveBinding is the stroke that opens the transition picker. It lives here
// rather than in the detail pane's own keymap so that everything about it is
// in one place, and is named in that keymap so the footer and the help
// overlay advertise it.
func moveBinding() kernel.Binding {
	return kernel.Bind([]string{"t"}, "t", "change status")
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
		Acts: []kernel.Binding{k.Act},
		Full: [][]kernel.Binding{{k.Down, k.Up, k.Act}},
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
			Acts: []kernel.Binding{
				kernel.Terse(k.Prev, "previous"),
				kernel.Terse(k.Next, "next"),
				filled,
			},
			Full: [][]kernel.Binding{{k.Down, k.Up, k.Prev, k.Next}, {filled, k.Cancel}},
		},
		moveConfirm: {
			Acts: []kernel.Binding{k.Yes, k.Cancel},
			Full: [][]kernel.Binding{{k.Yes, k.Cancel}},
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

var _ kernel.KeyReporter = (*moveModel)(nil)
