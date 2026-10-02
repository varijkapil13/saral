package backlog

import (
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var (
	_ kernel.KeyReporter    = (*Model)(nil)
	_ kernel.KeyStateLister = (*Model)(nil)
)

type keyMap struct {
	Up   kernel.Binding
	Down kernel.Binding
	// Prev and Next step through the destinations while a move is being aimed.
	// The row of them is drawn across the line, so the arrows that move along it
	// are the left and right ones; up and down stay bound because a list is what
	// every other state here is, and a hand already on j/k should not have to
	// notice that this one row is not one.
	Prev     kernel.Binding
	Next     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	HalfUp   kernel.Binding
	HalfDown kernel.Binding
	Go       kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding
	// Pick is the multi-select. space rather than enter, because enter is the
	// answer to the two questions this view asks and one stroke cannot be both.
	Pick    kernel.Binding
	PickAll kernel.Binding
	Unpick  kernel.Binding
	Move    kernel.Binding
	Choose  kernel.Binding
	Back    kernel.Binding
	Confirm kernel.Binding
	// FilterBy opens the same picker the issue list uses — a person, a status, a
	// type, a priority or a label — applied locally against what is already
	// loaded rather than sent to the site; see terms.go.
	FilterBy kernel.Binding
	// Unfilter drops every term FilterBy put in force.
	Unfilter kernel.Binding
	// Sort opens the picker that chooses the order this view reads a section's
	// issues in; see sort.go.
	Sort kernel.Binding
	// SortPrev, SortNext, SortChoose and SortCancel answer that picker: left
	// and right move the cursor over the fields on offer, enter chooses the
	// one under it and esc leaves the order as it was.
	SortPrev   kernel.Binding
	SortNext   kernel.Binding
	SortChoose kernel.Binding
	SortCancel kernel.Binding
	// RankUp, RankDown, RankTop and RankBottom change an issue's rank within
	// its section.
	RankUp     kernel.Binding
	RankDown   kernel.Binding
	RankTop    kernel.Binding
	RankBottom kernel.Binding
	// Mine narrows the backlog to the account this session is signed in as, as
	// an assignee term the chip bar names like any other.
	Mine kernel.Binding
	// Find types a search over the issues loaded, and FindNext and FindPrev walk
	// what it matched once it is kept.
	Find       kernel.Binding
	FindNext   kernel.Binding
	FindPrev   kernel.Binding
	FindKeep   kernel.Binding
	FindCancel kernel.Binding
	// Create opens the create form for the section under the cursor.
	Create kernel.Binding
	Look   kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Canon(kernel.ActUp),
		Down:     kernel.Canon(kernel.ActDown),
		Prev:     kernel.Bind([]string{"left", "h", "up", "k"}, "←/h", "previous"),
		Next:     kernel.Bind([]string{"right", "l", "down", "j"}, "→/l", "next"),
		PageUp:   kernel.Canon(kernel.ActPageUp),
		PageDown: kernel.Canon(kernel.ActPageDown),
		HalfUp:   kernel.Canon(kernel.ActHalfUp),
		HalfDown: kernel.Canon(kernel.ActHalfDown),
		Go:       kernel.Canon(kernel.ActGo),
		Top:      kernel.Canon(kernel.ActTop),
		Bottom:   kernel.Canon(kernel.ActBottom),
		Pick:     kernel.Canon(kernel.ActToggle, "pick or unpick this issue"),
		PickAll:  kernel.Canon(kernel.ActSelectGroup, "pick every issue in this section"),
		Unpick:   kernel.Canon(kernel.ActSelectNone, "unpick everything"),
		Move:     kernel.Canon(kernel.ActMove, "move these issues to a sprint or the backlog"),
		Choose:   kernel.Bind([]string{"enter"}, "enter", "move them here"),
		Back:     kernel.Bind([]string{"esc"}, "esc", "leave them where they are"),
		Confirm:  kernel.Bind([]string{"y"}, "y", "go ahead"),
		FilterBy: kernel.Canon(kernel.ActFilter, "filter by a person, a status, a label"),
		Unfilter: kernel.Canon(kernel.ActClearFilters, "clear filter"),
		Sort:     kernel.Canon(kernel.ActSort),

		SortPrev:   kernel.Canon(kernel.ActSortPrev),
		SortNext:   kernel.Canon(kernel.ActSortNext),
		SortChoose: kernel.Canon(kernel.ActSortChoose),
		SortCancel: kernel.Canon(kernel.ActSortCancel),

		RankUp:     kernel.Canon(kernel.ActRankUp, "rank this issue up"),
		RankDown:   kernel.Canon(kernel.ActRankDown, "rank this issue down"),
		RankTop:    kernel.Canon(kernel.ActRankFirst, "rank this issue first in its section"),
		RankBottom: kernel.Canon(kernel.ActRankLast, "rank this issue last in its section"),
		Mine:       kernel.Canon(kernel.ActMine, "only my issues"),
		Find:       kernel.Canon(kernel.ActFind, "find an issue"),
		FindNext:   kernel.Canon(kernel.ActFindNext, "next issue found"),
		FindPrev:   kernel.Canon(kernel.ActFindPrev, "previous issue found"),
		FindKeep:   kernel.Bind([]string{"enter"}, "enter", "keep this search"),
		FindCancel: kernel.Bind([]string{"esc"}, "esc", "go back to where the search began"),
		Create:     kernel.Canon(kernel.ActCreate, "create an issue in this section"),
		Look:       kernel.Canon(kernel.ActLook, "roomy / compact / lines"),
	}
}

// keySet is the resting state: rows on screen with nothing picked yet.
func (k keyMap) keySet() kernel.KeySet { return k.browsing(false, false) }

// browsing is the resting state, with and without a selection and with and
// without a term in force. The selection is what x is for and the term is
// what ctrl+g is for, so each key is offered only where there is something for
// it to act on.
func (k keyMap) browsing(picked, narrowed bool) kernel.KeySet {
	pick, all := kernel.Terse(k.Pick, "pick"), kernel.Terse(k.PickAll, "pick all")
	move := kernel.Terse(k.Move, "move")
	by := kernel.Terse(k.FilterBy, "filter by")
	sort := kernel.Terse(k.Sort, "sort")
	find, mine := kernel.Terse(k.Find, "find"), kernel.Terse(k.Mine, "mine")
	create, look := kernel.Terse(k.Create, "create"), kernel.Terse(k.Look, "look")
	acts := []kernel.Binding{pick, all, move, create, find, mine, by, sort, look}
	actions := append([]kernel.Binding{k.Pick, k.PickAll, k.Move, k.Create, k.Find, k.Mine, k.FilterBy, k.Sort, k.Look}, issue.ShareBindings...)
	if picked {
		acts = append(acts, kernel.Terse(k.Unpick, "unpick all"))
		actions = append(actions, k.Unpick)
	}
	if narrowed {
		acts = append(acts, kernel.Terse(k.Unfilter, "clear"))
		actions = append(actions, k.Unfilter)
	}
	return kernel.KeySet{
		Acts: acts,
		Menu: issue.ShareBindings,
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.HalfDown, k.HalfUp, k.Top, k.Bottom},
			{k.RankUp, k.RankDown, k.RankTop, k.RankBottom},
			actions,
			{k.FindNext, k.FindPrev},
		},
	}
}

// keyState is which of the view's states the keys belong to. It doubles as the
// generation the chrome memoizes on, so a state that is added has to be added
// here to be drawn.
type keyState int

const (
	keysBrowsing keyState = iota
	keysPicked
	keysNarrowed
	keysPickedNarrowed
	keysChoosing
	keysConfirming
	keysMoving
	keysSorting
	keysFinding
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	var sets [keyStates]kernel.KeySet
	sets[keysBrowsing] = k.browsing(false, false)
	sets[keysPicked] = k.browsing(true, false)
	sets[keysNarrowed] = k.browsing(false, true)
	sets[keysPickedNarrowed] = k.browsing(true, true)
	sets[keysChoosing] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{
			kernel.Terse(k.Next, "choose"), kernel.Terse(k.Choose, "move here"),
			kernel.Terse(k.Back, "cancel"),
		},
		Full: [][]kernel.Binding{{k.Next, k.Prev}, {k.Choose, k.Back}},
	}
	sets[keysConfirming] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{kernel.Terse(k.Confirm, "go ahead"), kernel.Terse(k.Back, "cancel")},
		Full: [][]kernel.Binding{{k.Confirm, k.Back}},
	}
	// A move in flight has nothing of its own to offer: the chunks the site has
	// left are the only thing that ends it, and naming a key here would name one
	// that is refused.
	sets[keysMoving] = kernel.KeySet{Mode: kernel.Modal}
	sets[keysSorting] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{
			kernel.Terse(k.SortPrev, "prev"), kernel.Terse(k.SortNext, "next"), k.SortChoose, k.SortCancel,
		},
		Full: [][]kernel.Binding{{k.SortPrev, k.SortNext}, {k.SortChoose, k.SortCancel}},
	}
	sets[keysFinding] = kernel.KeySet{
		Mode: kernel.Modal,
		Acts: []kernel.Binding{kernel.Terse(k.FindKeep, "keep"), kernel.Terse(k.FindCancel, "cancel")},
		Full: [][]kernel.Binding{{k.FindKeep, k.FindCancel}, {widget.KillLine}},
	}
	return sets
}()

// KeyStates lists every set the view reports.
func (*Model) KeyStates() []kernel.KeySet { return liveSets[:] }

// LiveKeys reports the keys that work in the state the backlog is actually in.
// A selection offers the key that schedules it, a term in force offers the key
// that clears it, the sprint list and the confirm answer two strokes each, and
// a move in flight answers nothing.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := keysBrowsing
	switch {
	case m.mode == choosing:
		state = keysChoosing
	case m.mode == confirming:
		state = keysConfirming
	case m.mode == sorting:
		state = keysSorting
	case m.mode == finding:
		state = keysFinding
	case m.mode == movingIssues:
		state = keysMoving
	case len(m.picked) > 0 && len(m.terms) > 0:
		state = keysPickedNarrowed
	case len(m.picked) > 0:
		state = keysPicked
	case len(m.terms) > 0:
		state = keysNarrowed
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
	actHalfUp
	actHalfDown
	actGo
	actTop
	actBottom
	actPick
	actPickGroup
	actClear
	actMove
	actChoose
	actBack
	actConfirm
	actFilterBy
	actClearFilter
	actSort
	actSortPrev
	actSortNext
	actSortChoose
	actSortCancel
	actRankUp
	actRankDown
	actRankTop
	actRankBottom
	actMine
	actFind
	actFindNext
	actFindPrev
	actFindKeep
	actFindCancel
	actCreate
	actLook
)

// tables turn the bindings into a keystroke lookup, built once. The bindings
// stay the single source of truth for what a key does and for what the footer
// says it does, and a keystroke costs one map probe rather than a walk over
// every binding.
func (k keyMap) tables() (browse, chooser, confirm, sorting, finding map[string]action) {
	b, c, cf, so, fi := k.entries()
	return table(b...), table(c...), table(cf...), table(so...), table(fi...)
}

// entries are the tables as lists, which is what lets a test see a stroke bound
// twice in one of them: a map keeps whichever came last.
func (k keyMap) entries() (browse, chooser, confirm, sorting, finding []binding) {
	browse = []binding{
		{k.Down, actDown}, {k.Up, actUp},
		{k.PageDown, actPageDown}, {k.PageUp, actPageUp},
		{k.HalfDown, actHalfDown}, {k.HalfUp, actHalfUp},
		{k.Go, actGo}, {k.Top, actTop}, {k.Bottom, actBottom},
		{k.Pick, actPick}, {k.PickAll, actPickGroup},
		{k.Unpick, actClear}, {k.Move, actMove},
		{k.FilterBy, actFilterBy}, {k.Unfilter, actClearFilter},
		{k.Sort, actSort},
		{k.RankUp, actRankUp}, {k.RankDown, actRankDown},
		{shiftUp, actRankUp}, {shiftDown, actRankDown},
		{k.RankTop, actRankTop}, {k.RankBottom, actRankBottom},
		{k.Mine, actMine}, {k.Find, actFind},
		{k.FindNext, actFindNext}, {k.FindPrev, actFindPrev},
		{k.Create, actCreate},
		{k.Look, actLook},
	}
	chooser = []binding{
		{k.Next, actDown}, {k.Prev, actUp},
		{k.Choose, actChoose}, {k.Back, actBack},
	}
	confirm = []binding{{k.Confirm, actConfirm}, {k.Back, actBack}}
	sorting = []binding{
		{k.SortPrev, actSortPrev}, {k.SortNext, actSortNext},
		{k.SortChoose, actSortChoose}, {k.SortCancel, actSortCancel},
	}
	finding = []binding{{k.FindKeep, actFindKeep}, {k.FindCancel, actFindCancel}}
	return browse, chooser, confirm, sorting, finding
}

var (
	shiftUp   = kernel.Bind([]string{"shift+up"}, "shift+up", "rank this issue up")
	shiftDown = kernel.Bind([]string{"shift+down"}, "shift+down", "rank this issue down")
)

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
