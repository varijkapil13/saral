package sprint

import (
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

var _ kernel.KeyReporter = (*Model)(nil)

// keyMap is what the view answers to: the list, the form that fills a sprint
// in, and the confirm that stands in front of the two moves nothing can undo.
//
// g is bound to nothing on purpose: the kernel buffers it as the view-switch
// prefix and never forwards it, so a binding on it would advertise a stroke
// that cannot arrive.
type keyMap struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Top      kernel.Binding
	Bottom   kernel.Binding

	New      kernel.Binding
	Edit     kernel.Binding
	Start    kernel.Binding
	Complete kernel.Binding
	// Closed is one binding for both directions. What it does is named as the
	// toggle it is, because a second label for the same stroke is a second
	// answer to the question of what o does.
	Closed kernel.Binding

	Field     kernel.Binding
	PrevField kernel.Binding
	Save      kernel.Binding
	Discard   kernel.Binding

	Yes kernel.Binding
	No  kernel.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:       kernel.Bind([]string{"k", "up"}, "↑/k", "up"),
		Down:     kernel.Bind([]string{"j", "down"}, "↓/j", "down"),
		PageUp:   kernel.Bind([]string{"pgup", "ctrl+b"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown", "ctrl+f"}, "pgdn", "page down"),
		Top:      kernel.Bind([]string{"home"}, "home", "first sprint"),
		Bottom:   kernel.Bind([]string{"end"}, "end", "last sprint"),

		New:      kernel.Bind([]string{"n"}, "n", "plan a new sprint"),
		Edit:     kernel.Bind([]string{"e", "enter"}, "e", "edit name, goal and dates"),
		Start:    kernel.Bind([]string{"s"}, "s", "start this sprint"),
		Complete: kernel.Bind([]string{"c"}, "c", "complete this sprint"),
		Closed:   kernel.Bind([]string{"o"}, "o", "show or hide closed sprints"),

		Field:     kernel.Bind([]string{"tab", "down"}, "tab", "next field"),
		PrevField: kernel.Bind([]string{"shift+tab", "up"}, "shift+tab", "previous field"),
		Save:      kernel.Bind([]string{"ctrl+s"}, "ctrl+s", "send it to Jira"),
		Discard:   kernel.Bind([]string{"esc"}, "esc", "discard this edit"),

		Yes: kernel.Bind([]string{"y"}, "y", "go ahead"),
		No:  kernel.Bind([]string{"esc"}, "esc", "leave it alone"),
	}
}

// keySet is the resting record: everything this view can do to a board's
// sprints. It is an inventory rather than a state, because which of start and
// complete works depends on the sprint under the cursor and the footer is
// answered by LiveKeys.
func (k keyMap) keySet() kernel.KeySet {
	return kernel.KeySet{
		Acts: []kernel.Binding{
			kernel.Terse(k.Edit, "edit"),
			kernel.Terse(k.New, "new"),
			kernel.Terse(k.Closed, "closed"),
		},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom},
			{k.Start, k.Complete, k.Edit, k.New, k.Closed},
		},
	}
}

// keyState is which of the view's states the keys belong to. It doubles as the
// generation the memoized chrome repaints on, so a state that is added has to
// be added here to be drawn.
type keyState int

const (
	// keysWaiting is every state with no sprint to act on: the first read, a
	// session with no connection, a refusal, and a project whose boards are
	// none. The toggle still works, and it is the only thing that does.
	keysWaiting keyState = iota
	keysEmpty
	keysFuture
	keysActive
	keysClosed
	keysForm
	keysConfirm
	keysWorking
	keyStates
)

// liveSets is one set per state, built once at start-up. LiveKeys is asked on
// every frame, so it hands back a stored value rather than assembling one.
var liveSets = func() [keyStates]kernel.KeySet {
	k := defaultKeys()
	motions := []kernel.Binding{k.Down, k.Up, k.PageDown, k.PageUp, k.Top, k.Bottom}
	closed := kernel.Terse(k.Closed, "closed")
	edit, add := kernel.Terse(k.Edit, "edit"), kernel.Terse(k.New, "new")
	start, done := kernel.Terse(k.Start, "start"), kernel.Terse(k.Complete, "complete")

	var sets [keyStates]kernel.KeySet
	sets[keysWaiting] = kernel.KeySet{
		Acts: []kernel.Binding{closed},
		Full: [][]kernel.Binding{{k.Closed}},
	}
	sets[keysEmpty] = kernel.KeySet{
		Acts: []kernel.Binding{add, closed},
		Full: [][]kernel.Binding{{k.New, k.Closed}},
	}
	// A future sprint is started and never completed, and an active one the
	// other way round: the port allows one move from each state, so naming both
	// would name a stroke that comes back refused.
	sets[keysFuture] = kernel.KeySet{
		Acts: []kernel.Binding{start, edit, add, closed},
		Full: [][]kernel.Binding{motions, {k.Start, k.Edit, k.New, k.Closed}},
	}
	sets[keysActive] = kernel.KeySet{
		Acts: []kernel.Binding{done, edit, add, closed},
		Full: [][]kernel.Binding{motions, {k.Complete, k.Edit, k.New, k.Closed}},
	}
	// A closed sprint takes only its name and its goal, which is still an edit.
	sets[keysClosed] = kernel.KeySet{
		Acts: []kernel.Binding{edit, add, closed},
		Full: [][]kernel.Binding{motions, {k.Edit, k.New, k.Closed}},
	}
	// A prompt keeps its words: two or three answers to one question always
	// fit, and what they are called is the whole point of asking.
	sets[keysForm] = kernel.KeySet{
		Acts: []kernel.Binding{k.Save, k.Field, k.Discard},
		Full: [][]kernel.Binding{{k.Field, k.PrevField}, {k.Save, k.Discard}},
	}
	sets[keysConfirm] = kernel.KeySet{
		Acts: []kernel.Binding{k.Yes, k.No},
		Full: [][]kernel.Binding{{k.Yes, k.No}},
	}
	// A write in flight answers nothing of its own, and the footer then shows
	// the globals alone, which is the truth.
	sets[keysWorking] = kernel.KeySet{}
	return sets
}()

// LiveKeys reports the keys that work in the state the view is actually in.
// Which lifecycle move is on offer comes from the sprint under the cursor,
// because future to active to closed is the whole of what the port allows.
func (m *Model) LiveKeys() (set kernel.KeySet, gen int) {
	state := m.keyState()
	return liveSets[state], int(state)
}

func (m *Model) keyState() keyState {
	switch {
	case m.inflight != opNone:
		return keysWorking
	case m.state == filling:
		return keysForm
	case m.state == confirming:
		return keysConfirm
	case len(m.boards) == 0:
		return keysWaiting
	case len(m.sprints) == 0:
		return keysEmpty
	}
	switch m.selected().State {
	case jira.SprintFuture:
		return keysFuture
	case jira.SprintActive:
		return keysActive
	default:
		return keysClosed
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
	actNew
	actEdit
	actStart
	actComplete
	actClosed
	actNextField
	actPrevField
	actSave
	actDiscard
	actYes
	actNo
)

// tables turn the bindings into a keystroke lookup, built once per view. The
// bindings stay the single source of truth for what a key does and for what the
// footer says it does, and a keystroke costs one map probe rather than a walk
// over every binding.
func (k keyMap) tables() (rows, form, confirm map[string]action) {
	rows = table(
		binding{k.Down, actDown}, binding{k.Up, actUp},
		binding{k.PageDown, actPageDown}, binding{k.PageUp, actPageUp},
		binding{k.Top, actTop}, binding{k.Bottom, actBottom},
		binding{k.New, actNew}, binding{k.Edit, actEdit},
		binding{k.Start, actStart}, binding{k.Complete, actComplete},
		binding{k.Closed, actClosed},
	)
	form = table(
		binding{k.Field, actNextField}, binding{k.PrevField, actPrevField},
		binding{k.Save, actSave}, binding{k.Discard, actDiscard},
	)
	confirm = table(binding{k.Yes, actYes}, binding{k.No, actNo})
	return rows, form, confirm
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
