package board

import (
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var _ kernel.KeyReporter = (*Model)(nil)

type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	Left     kernel.Binding
	Right    kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Go       kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	Open     kernel.Binding
	// Pick takes the card under the cursor off the board, which is the keyboard
	// half of a drag. Aiming it is Left and Right, and Drop is what lands it.
	Pick kernel.Binding
	Drop kernel.Binding
	// Cancel puts a picked-up card back. esc is not one of its keys: the kernel
	// keeps that for itself in a root view, so naming it here would advertise a
	// stroke that never arrives.
	Cancel kernel.Binding
	Board  kernel.Binding
	// Sprint shows the next of the sprints a board is running, when it runs
	// more than one at once.
	Sprint kernel.Binding
	// Filters buffers, the way Go does: the digit it takes next is the
	// 1-indexed position of one of this board's own quick filters, toggling it
	// on or off and re-reading the board with the result. Capitalised because
	// lowercase f is the picker below, the more-used of the two.
	Filters kernel.Binding
	// FilterBy opens the same picker the issue list uses — a person, a status, a
	// type, a priority or a label — applied locally against what is already
	// loaded rather than sent to the site; see terms.go.
	FilterBy kernel.Binding
	// Unfilter drops every term FilterBy put in force. It is offered only while
	// one is, the way list.Unfilter is.
	Unfilter kernel.Binding
	// RankUp, RankDown, RankTop and RankBottom change a card's rank within its
	// column; ShiftLeft and ShiftRight land it in the next column without
	// picking it up first.
	RankUp     kernel.Binding
	RankDown   kernel.Binding
	RankTop    kernel.Binding
	RankBottom kernel.Binding
	ShiftLeft  kernel.Binding
	ShiftRight kernel.Binding
	// Mine narrows the board to the account this session is signed in as, as an
	// assignee term the chip bar names like any other.
	Mine kernel.Binding
	// Find types a search over the cards loaded, and FindNext and FindPrev walk
	// what it matched once it is kept.
	Find       kernel.Binding
	FindNext   kernel.Binding
	FindPrev   kernel.Binding
	FindKeep   kernel.Binding
	FindCancel kernel.Binding
	// Lanes cycles the swimlanes, Fold closes or opens the lane under the
	// cursor and FoldAll every lane at once.
	Lanes   kernel.Binding
	Fold    kernel.Binding
	FoldAll kernel.Binding
	// Create opens the create form for the column under the cursor.
	Create kernel.Binding
	// Toggle, PickColumn and Unpick are the multi-select; Assign and Label act on
	// what is picked, or on the card under the cursor when nothing is.
	Toggle     kernel.Binding
	PickColumn kernel.Binding
	Unpick     kernel.Binding
	Assign     kernel.Binding
	Label      kernel.Binding
	// Accept, Next and Prev answer the person and label prompts, Run and
	// Decline the confirmation, and Halt stops a run after the card in flight.
	Accept  kernel.Binding
	Decline kernel.Binding
	Prev    kernel.Binding
	Next    kernel.Binding
	Run     kernel.Binding
	Halt    kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		Left:     kernel.Bind([]string{"h", "left", "shift+tab"}, "←/h", "previous column"),
		Right:    kernel.Bind([]string{"l", "right", "tab"}, "→/l", "next column"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Go:       kernel.Bind([]string{"g"}, "g", "go to"),
		Top:      kernel.Bind([]string{"home"}, "g g", "first card in this column"),
		Bottom:   kernel.Bind([]string{"G", "end"}, "G / g e", "last card in this column"),
		Open:     kernel.Bind([]string{"enter"}, "enter", "open"),
		Pick:     kernel.Bind([]string{"m"}, "m", "move this issue to another column"),
		Drop:     kernel.Bind([]string{"enter"}, "enter", "move it to this column"),
		Cancel:   kernel.Bind([]string{"ctrl+g"}, "ctrl+g", "put it back"),
		Board:    kernel.Bind([]string{"b"}, "b", "another board of this project"),
		Sprint:   kernel.Bind([]string{"s"}, "s", "another sprint running on this board"),
		Filters:  kernel.Bind([]string{"F"}, "F 1-9", "quick filters"),
		FilterBy: kernel.Bind([]string{"f"}, "f", "filter by a person, a status, a label"),
		Unfilter: kernel.Bind([]string{"ctrl+g"}, "ctrl+g", "clear filter"),

		RankUp:     kernel.Bind([]string{"K", "shift+up"}, "K", "rank this card up"),
		RankDown:   kernel.Bind([]string{"J", "shift+down"}, "J", "rank this card down"),
		RankTop:    kernel.Bind([]string{"{"}, "{", "rank this card first in its column"),
		RankBottom: kernel.Bind([]string{"}"}, "}", "rank this card last in its column"),
		ShiftLeft:  kernel.Bind([]string{"H", "shift+left"}, "H", "move this card to the previous column"),
		ShiftRight: kernel.Bind([]string{"L", "shift+right"}, "L", "move this card to the next column"),
		Mine:       kernel.Bind([]string{"M"}, "M", "only my issues"),
		Find:       kernel.Bind([]string{"/"}, "/", "find a card"),
		FindNext:   kernel.Bind([]string{"n"}, "n", "next card found"),
		FindPrev:   kernel.Bind([]string{"N"}, "N", "previous card found"),
		FindKeep:   kernel.Bind([]string{"enter"}, "enter", "keep this search"),
		FindCancel: kernel.Bind([]string{"esc"}, "esc", "go back to where the search began"),

		Lanes:   kernel.Bind([]string{"w"}, "w", "swimlanes: none, by assignee, by parent"),
		Fold:    kernel.Bind([]string{"z"}, "z", "fold or unfold this lane"),
		FoldAll: kernel.Bind([]string{"Z"}, "Z", "fold or unfold every lane"),
		Create:  kernel.Bind([]string{"c"}, "c", "create an issue in this column"),

		Toggle:     kernel.Bind([]string{"space"}, "space", "pick or unpick this card"),
		PickColumn: kernel.Bind([]string{"v"}, "v", "pick every card in this column"),
		Unpick:     kernel.Bind([]string{"x"}, "x", "unpick every card"),
		Assign:     kernel.Bind([]string{"@"}, "@", "assign the picked cards"),
		Label:      kernel.Bind([]string{"+"}, "+", "add a label to the picked cards"),

		Accept:  kernel.Bind([]string{"enter"}, "enter", "take this"),
		Decline: kernel.Bind([]string{"esc"}, "esc", "cancel"),
		Prev:    kernel.Bind([]string{"up"}, "↑", "previous person"),
		Next:    kernel.Bind([]string{"down"}, "↓", "next person"),
		Run:     kernel.Bind([]string{"enter", "y"}, "enter", "go ahead"),
		Halt:    kernel.Bind([]string{"ctrl+g"}, "ctrl+g", "stop after the card in flight"),
	}
}

// keySet is the resting state: a board nobody is moving a card on.
func (k keyMap) keySet() kernel.KeySet { return liveSets[keysBrowsing] }

// browsing is the resting state, with and without a term in force. The
// narrowed one offers the key that clears them, the way list.browsing does.
func (k keyMap) browsing(narrowed, picked bool) kernel.KeySet {
	acts := []kernel.Binding{
		k.Open, kernel.Terse(k.Pick, "move"), kernel.Terse(k.Create, "create"), kernel.Terse(k.Find, "find"),
		kernel.Terse(k.Mine, "mine"),
		kernel.Terse(k.Board, "board"), kernel.Terse(k.FilterBy, "filter by"),
		kernel.Terse(k.Filters, "quick filters"),
	}
	if picked {
		acts = []kernel.Binding{
			kernel.Terse(k.Pick, "move them"), kernel.Terse(k.Assign, "assign"), kernel.Terse(k.Label, "label"),
			kernel.Terse(k.Toggle, "pick"), kernel.Terse(k.Unpick, "unpick all"),
		}
	}
	actions := append([]kernel.Binding{k.Open, k.Pick, k.Find, k.Mine, k.Board, k.Sprint, k.FilterBy, k.Filters}, issue.ShareBindings...)
	if narrowed {
		acts = append(acts, kernel.Terse(k.Unfilter, "clear"))
		actions = append(actions, k.Unfilter)
	}
	many := []kernel.Binding{k.Toggle, k.PickColumn, k.Assign, k.Label}
	if picked {
		many = append(many, k.Unpick)
	}
	return kernel.KeySet{
		Acts: acts,
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.Left, k.Right},
			{k.PageDown, k.PageUp, k.Top, k.Bottom},
			{k.ShiftLeft, k.ShiftRight, k.RankUp, k.RankDown, k.RankTop, k.RankBottom},
			actions,
			many,
			{k.Create, k.Lanes, k.Fold, k.FoldAll},
			{k.FindNext, k.FindPrev},
		},
	}
}

// keyState is which of the board's states the keys belong to. It doubles as the
// generation the memoized chrome repaints on, so a state that is added has to be
// added here to be drawn.
type keyState int

const (
	keysBrowsing keyState = iota
	keysNarrowed
	keysHolding
	keysMoving
	keysPickingFilter
	keysFinding
	keysPicked
	keysPickedNarrowed
	keysAskingPerson
	keysAskingLabel
	keysConfirming
	keysRunning
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysBrowsing] = k.browsing(false, false)
	sets[keysNarrowed] = k.browsing(true, false)
	sets[keysPicked] = k.browsing(false, true)
	sets[keysPickedNarrowed] = k.browsing(true, true)
	// A card in hand can only be aimed and landed, so the whole inventory is the
	// two answers and the two ways of aiming. enter means something else here
	// than it does above, which is the reason a state reports for itself.
	sets[keysHolding] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Drop, "move it here"), kernel.Terse(k.Cancel, "put it back")},
		Full: [][]kernel.Binding{
			{k.Left, k.Right},
			{k.Drop, k.Cancel},
		},
	}
	// A move the site has not answered yet offers nothing: every key is refused
	// until it does, and naming one would name a stroke being refused.
	sets[keysMoving] = kernel.KeySet{}
	// F has been pressed and the digit has not arrived. The board holds the
	// keyboard for that one stroke, so the row names what the digit does here
	// rather than the saved query a bare digit runs everywhere else.
	sets[keysPickingFilter] = kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Bind(digitKeys, "1-9", "quick filter"),
			kernel.Bind([]string{"esc"}, "esc", "cancel"),
		},
		Full: [][]kernel.Binding{{kernel.Bind(digitKeys, "1-9", "toggle that quick filter")}},
	}
	// / is taking typing: every printable stroke goes into the search, so the
	// row names the two strokes that end it.
	sets[keysFinding] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.FindKeep, "keep"), kernel.Terse(k.FindCancel, "cancel")},
		Full: [][]kernel.Binding{{k.FindKeep, k.FindCancel}, {widget.KillLine}},
	}
	// @ and + take typing: a name to look an account up by, or a label.
	sets[keysAskingPerson] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Accept, "choose"), kernel.Terse(k.Decline, "cancel")},
		Full: [][]kernel.Binding{{k.Prev, k.Next}, {k.Accept, k.Decline}, {widget.KillLine}},
	}
	sets[keysAskingLabel] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Accept, "add it"), kernel.Terse(k.Decline, "cancel")},
		Full: [][]kernel.Binding{{k.Accept, k.Decline}, {widget.KillLine}},
	}
	sets[keysConfirming] = kernel.KeySet{
		Acts: []kernel.Binding{k.Run, kernel.Terse(k.Decline, "leave them as they are")},
		Full: [][]kernel.Binding{{k.Run, k.Decline}},
	}
	sets[keysRunning] = kernel.KeySet{
		Acts: []kernel.Binding{kernel.Terse(k.Halt, "stop")},
		Full: [][]kernel.Binding{{k.Halt}},
	}
	return sets
}()

// LiveKeys reports the keys that work in the state the board is actually in. A
// card in hand answers enter and ctrl+g for two things the resting state does
// not, a move in flight answers nothing at all, and a board narrowed by a term
// offers the key that clears them.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowsing
	switch {
	case m.bulk != nil:
		state = m.bulk.keyState()
	case m.moving:
		state = keysMoving
	case m.card != nil:
		state = keysHolding
	case m.pendingFilter:
		state = keysPickingFilter
	case m.finding:
		state = keysFinding
	case len(m.picked) > 0 && len(m.terms) > 0:
		state = keysPickedNarrowed
	case len(m.picked) > 0:
		state = keysPicked
	case len(m.terms) > 0:
		state = keysNarrowed
	}
	return liveSets[state], int(state)
}

// digitKeys are the strokes a quick filter is chosen with. They are not a
// binding on the keymap: they only ever mean anything in the one state below,
// and the kernel spends a bare digit on a saved query everywhere else.
var digitKeys = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

type action uint8

const (
	actNone action = iota
	actUp
	actDown
	actLeft
	actRight
	actPageUp
	actPageDown
	actGo
	actTop
	actBottom
	actOpen
	actPick
	actDrop
	actCancel
	actBoard
	actSprint
	actFilter
	actFilterBy
	actUnfilter
	actRankUp
	actRankDown
	actRankTop
	actRankBottom
	actShiftLeft
	actShiftRight
	actMine
	actFind
	actFindNext
	actFindPrev
	actFindKeep
	actFindCancel
	actLanes
	actFold
	actFoldAll
	actCreate
	actToggle
	actPickColumn
	actUnpick
	actAssign
	actLabel
	actAccept
	actDecline
	actPrev
	actNext
	actRun
	actHalt
)

// tables turn the bindings into a keystroke lookup, built once per board. The
// bindings stay the single source of truth for what a key does and for what the
// footer says it does, and a keystroke costs one map probe rather than a walk
// over every binding.
func (k keyMap) tables() (browsing, holding, finding map[string]action) {
	browse, hold, find := k.entries()
	return table(browse...), table(hold...), table(find...)
}

// bulkTables are the three states a bulk change passes through before and
// while it runs: typing a person or a label, confirming, and running.
func (k keyMap) bulkTables() (asking, confirming, running map[string]action) {
	ask, confirm, run := k.bulkEntries()
	return table(ask...), table(confirm...), table(run...)
}

func (k keyMap) bulkEntries() (asking, confirming, running []binding) {
	asking = []binding{{k.Accept, actAccept}, {k.Decline, actDecline}, {k.Prev, actPrev}, {k.Next, actNext}}
	confirming = []binding{{k.Run, actRun}, {k.Decline, actDecline}}
	running = []binding{{k.Halt, actHalt}}
	return asking, confirming, running
}

// entries are the three tables as lists, which is what lets a test see a stroke
// bound twice in one of them: a map keeps whichever came last.
//
// A card in hand answers only the keys the holding state advertises: the two
// that aim it and the two that end the gesture. A motion that moved the cursor
// here would leave the card behind whatever it moved to.
func (k keyMap) entries() (browsing, holding, finding []binding) {
	browsing = []binding{
		{k.Up, actUp}, {k.Down, actDown},
		{k.Left, actLeft}, {k.Right, actRight},
		{k.PageUp, actPageUp}, {k.PageDown, actPageDown},
		{k.Go, actGo}, {k.Top, actTop}, {k.Bottom, actBottom},
		{k.Open, actOpen}, {k.Pick, actPick}, {k.Board, actBoard},
		{k.Sprint, actSprint},
		{k.Filters, actFilter}, {k.FilterBy, actFilterBy},
		{k.Unfilter, actUnfilter},
		{k.RankUp, actRankUp}, {k.RankDown, actRankDown},
		{k.RankTop, actRankTop}, {k.RankBottom, actRankBottom},
		{k.ShiftLeft, actShiftLeft}, {k.ShiftRight, actShiftRight},
		{k.Mine, actMine}, {k.Find, actFind},
		{k.FindNext, actFindNext}, {k.FindPrev, actFindPrev},
		{k.Lanes, actLanes}, {k.Fold, actFold}, {k.FoldAll, actFoldAll},
		{k.Create, actCreate},
		{k.Toggle, actToggle}, {k.PickColumn, actPickColumn}, {k.Unpick, actUnpick},
		{k.Assign, actAssign}, {k.Label, actLabel},
	}
	holding = []binding{
		{k.Left, actLeft}, {k.Right, actRight},
		{k.Drop, actDrop}, {k.Cancel, actCancel},
	}
	finding = []binding{{k.FindKeep, actFindKeep}, {k.FindCancel, actFindCancel}}
	return browsing, holding, finding
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
