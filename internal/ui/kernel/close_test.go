package kernel

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The three things a blur has always meant, told apart. Every one of these
// drives the kernel the way a keypress does rather than calling pop or open, so
// what is asserted is the gesture and not the helper under it.

func TestClose_APoppedViewIsDiscarded(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))

	m := newAt(t, testDeps(), 120, 30)
	detail := &stubView{id: "detail", content: "PROJ-1 detail"}
	next, _ := m.Update(PushMsg{View: detail, ID: "detail", Title: "PROJ-1"})
	m = next.(Model)

	press(m, "esc")
	if detail.closed != 1 {
		t.Errorf("the popped view was closed %d times, want once", detail.closed)
	}
	if board.closed != 0 {
		t.Errorf("the view underneath was closed %d times; it is the one being come back to", board.closed)
	}
}

// The regression that started all this. A palette over a loading thread cancelled
// the load, because the only thing the kernel said was FocusMsg{false} and being
// covered says that too.
func TestClose_AViewPushedOverIsNotDiscarded(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	palette := &stubView{id: PaletteViewID}
	RegisterView(spec(PaletteViewID, 0, "", palette))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+k")

	if len(m.stack) != 2 {
		t.Fatalf("the palette did not open: stack depth %d", len(m.stack))
	}
	if board.closed != 0 {
		t.Errorf("opening the palette closed the view it opened over (%d times), which is the read it would cancel",
			board.closed)
	}
	if !slices.Contains(board.seen, "blur") {
		t.Errorf("the covered view was never told it lost the keyboard: %v", board.seen)
	}
}

func TestClose_ARootSwitchedAwayFromIsNotDiscarded(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "g", "2")

	if board.closed != 0 {
		t.Errorf("the root switched away from was closed %d times; it is parked and comes back on g1", board.closed)
	}
	m, _ = press(m, "g", "1")
	if board.closed != 0 {
		t.Errorf("coming back found a view that had been closed %d times", board.closed)
	}
	if m.top().view != View(board) {
		t.Error("g1 built a second instance rather than resuming the parked one")
	}
}

func TestClose_ARootSwitchDiscardsWhateverWasPushedOverIt(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	detail := &stubView{id: "detail"}
	form := &stubView{id: "form"}
	for _, v := range []*stubView{detail, form} {
		next, _ := m.Update(PushMsg{View: v, ID: v.id, Title: v.id})
		m = next.(Model)
	}

	m, _ = press(m, "g", "2")
	if len(m.stack) != 1 {
		t.Fatalf("a switch left %d entries on the stack, want the new root alone", len(m.stack))
	}
	for _, v := range []*stubView{detail, form} {
		if v.closed != 1 {
			t.Errorf("%s was closed %d times, want once: a switch throws away everything over the root", v.id, v.closed)
		}
	}
	if board.closed != 0 {
		t.Errorf("the root under them was closed %d times, want never", board.closed)
	}
}

func TestClose_AProjectSwitchDiscardsNothing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	parked := &stubView{id: "backlog"}
	RegisterView(spec("backlog", 2, "", parked))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "g", "2")
	m, _ = press(m, "g", "1")
	detail := &stubView{id: "detail"}
	next, _ := m.Update(PushMsg{View: detail, ID: "detail", Title: "PROJ-1"})
	m = next.(Model)

	next, _ = m.Update(ProjectMsg{Project: "OPS"})
	scoped, ok := next.(Model)
	if !ok {
		t.Fatal("the re-scope did not give back a Model")
	}
	if scoped.deps.Project != "OPS" {
		t.Fatalf("the session is scoped to %q, want OPS", scoped.deps.Project)
	}

	for _, v := range []*stubView{board, parked, detail} {
		if v.closed != 0 {
			t.Errorf("%s was closed %d times by a re-scope; every view hears the new project and stays", v.id, v.closed)
		}
	}
}

// A view that refuses to close is not closed, which is the same order the two
// have always been asked in: Blocker first, and only then the news.
func TestClose_ARefusedPopDiscardsNothing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	draft := &stubView{id: "compose", blocks: "this comment has not been sent"}
	next, _ := m.Update(PushMsg{View: draft, ID: "compose", Title: "PROJ-1"})
	m = next.(Model)

	m, _ = press(m, "esc")
	if draft.closed != 0 {
		t.Errorf("a view that refused to close was closed %d times", draft.closed)
	}
	if len(m.stack) != 2 {
		t.Fatalf("the refusal did not keep the view on the stack: depth %d", len(m.stack))
	}
}

// A lent view is the pusher's, and the pusher goes on drawing it: the issue pane
// hands over the very thread its sidebar shows.
func TestClose_ALentViewIsDroppedWithoutBeingDiscarded(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	owner := &stubView{id: "detail"}
	next, _ := m.Update(PushMsg{View: owner, ID: "detail", Title: "PROJ-1"})
	m = next.(Model)

	thread := &stubView{id: "comments"}
	msg, ok := Lend("comments", "PROJ-1", thread)().(PushMsg)
	if !ok {
		t.Fatal("Lend did not produce a PushMsg")
	}
	if !msg.Lent {
		t.Fatal("Lend produced a push the kernel would take ownership of")
	}
	next, _ = m.Update(msg)
	m = next.(Model)

	m, _ = press(m, "esc")
	if thread.closed != 0 {
		t.Errorf("the lent view was closed %d times; its owner is still drawing it", thread.closed)
	}
	if len(m.stack) != 2 || m.top().view != View(owner) {
		t.Fatalf("esc did not come back to the view that lent the thread: depth %d", len(m.stack))
	}

	m, _ = press(m, "esc")
	if owner.closed != 1 {
		t.Errorf("the lender was closed %d times, want once", owner.closed)
	}
}

// A switch away while the lent view is on top throws the lender out and leaves
// the lent one to it.
func TestClose_ARootSwitchLeavesALentViewToItsLender(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	owner := &stubView{id: "detail"}
	next, _ := m.Update(PushMsg{View: owner, ID: "detail", Title: "PROJ-1"})
	m = next.(Model)
	thread := &stubView{id: "comments"}
	next, _ = m.Update(PushMsg{View: thread, ID: "comments", Title: "PROJ-1", Lent: true})
	m = next.(Model)

	press(m, "g", "2")
	if owner.closed != 1 {
		t.Errorf("the lender was closed %d times, want once", owner.closed)
	}
	if thread.closed != 0 {
		t.Errorf("the kernel closed a view it had only borrowed (%d times)", thread.closed)
	}
}

// Quitting discards everything and tells nothing: the process is ending, so
// there is no frame left to draw and no command that would run.
func TestClose_QuittingTellsNobody(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))

	for _, key := range []string{"q", "ctrl+c"} {
		m := newAt(t, testDeps(), 120, 30)
		board.closed = 0
		m, cmd := press(m, key)
		if cmd == nil {
			t.Fatalf("%s did not quit", key)
		}
		if !m.quitting {
			t.Fatalf("%s did not put the model into quitting", key)
		}
		if board.closed != 0 {
			t.Errorf("%s closed %d views; quitting is the one discard the kernel stays out of", key, board.closed)
		}
	}
}

// bareView implements nothing optional, which is most of what a view is allowed
// to be.
type bareView struct{}

func (bareView) Init() tea.Cmd                  { return nil }
func (bareView) Update(tea.Msg) (View, tea.Cmd) { return bareView{}, nil }
func (bareView) View() string                   { return "bare" }
func TestClose_AViewThatDoesNotWantTheNewsIsJustDropped(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	next, _ := m.Update(PushMsg{View: bareView{}, ID: "bare", Title: "Bare"})
	m = next.(Model)

	m, _ = press(m, "esc")
	if len(m.stack) != 1 {
		t.Errorf("the stack is %d deep after esc, want the root alone", len(m.stack))
	}
}
