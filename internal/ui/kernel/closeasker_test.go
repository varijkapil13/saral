package kernel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// askingStub is a Blocker that would rather ask than refuse: AskClose counts
// how many times it was called instead of putting up a prompt of its own, and
// the test resolves it the way a real view would — clearing whatever made it
// block, then sending the ordinary close message itself.
type askingStub struct {
	stubView
	asked int
}

func (s *askingStub) AskClose() tea.Cmd {
	s.asked++
	return nil
}

// Update overrides the promoted one, which would hand the kernel back a bare
// *stubView on the first message and quietly drop AskClose from the stack.
func (s *askingStub) Update(msg tea.Msg) (View, tea.Cmd) {
	_, cmd := s.stubView.Update(msg)
	return s, cmd
}

var (
	_ Blocker    = (*askingStub)(nil)
	_ CloseAsker = (*askingStub)(nil)
)

// A plain Blocker is refused exactly as before: CloseAsker is additive and a
// view that does not implement it sees no change in behaviour.
func TestCloseAsker_APlainBlockerIsStillJustRefused(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	draft := &stubView{id: "compose", blocks: "this comment has not been sent"}
	next, _ := m.Update(PushMsg{View: draft, ID: "compose", Title: "PROJ-1"})
	m = next.(Model)

	m, _ = press(m, "esc")
	if draft.closed != 0 || len(m.stack) != 2 {
		t.Fatalf("a plain Blocker was not simply refused: closed=%d depth=%d", draft.closed, len(m.stack))
	}
}

// esc on a pushed CloseAsker calls AskClose instead of refusing, and leaves
// the view exactly where it was until it answers for itself.
func TestCloseAsker_PopAsksInsteadOfRefusing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	asker := &askingStub{stubView: stubView{id: "issue", blocks: "3 unsaved changes"}}
	next, _ := m.Update(PushMsg{View: asker, ID: "issue", Title: "PROJ-1"})
	m = next.(Model)

	m, _ = press(m, "esc")
	if asker.asked != 1 {
		t.Fatalf("AskClose was called %d times, want once", asker.asked)
	}
	if asker.closed != 0 {
		t.Error("the view was discarded before it answered its own prompt")
	}
	if len(m.stack) != 2 {
		t.Fatalf("esc popped the view anyway: depth %d", len(m.stack))
	}

	// The view answers later by sending the normal close message itself, once
	// its own prompt is resolved and whatever made it block is gone.
	asker.blocks = ""
	next, _ = m.Update(PopMsg{})
	scoped, ok := next.(Model)
	if !ok {
		t.Fatal("PopMsg did not give back a Model")
	}
	if len(scoped.stack) != 1 {
		t.Fatalf("the view's own Pop did not go through once it stopped blocking: depth %d", len(scoped.stack))
	}
	if asker.closed != 1 {
		t.Errorf("the view was closed %d times once it actually left, want once", asker.closed)
	}
}

// A root switch reaches a CloseAsker the same way, even though the entry is
// not the top of the stack the way a pop's is.
func TestCloseAsker_RootSwitchAsksInsteadOfRefusing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	asker := &askingStub{stubView: stubView{id: "issue", blocks: "unsaved"}}
	next, _ := m.Update(PushMsg{View: asker, ID: "issue", Title: "PROJ-1"})
	m = next.(Model)

	m, _ = press(m, "g", "2")
	if asker.asked != 1 {
		t.Fatalf("AskClose was called %d times on a root switch, want once", asker.asked)
	}
	if len(m.stack) != 2 || m.top().view != View(asker) {
		t.Fatal("the root switch went ahead despite the block")
	}
}

// Quitting from a root that is itself a CloseAsker is asked rather than
// refused too — the same mechanism, at the one other refusal site.
func TestCloseAsker_QuitAsksInsteadOfRefusing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	asker := &askingStub{stubView: stubView{id: "board", blocks: "unsaved"}}
	RegisterView(spec("board", 1, "", asker))

	m := newAt(t, testDeps(), 120, 30)
	scoped, _ := press(m, "q")
	if asker.asked != 1 {
		t.Fatalf("AskClose was called %d times on quit, want once", asker.asked)
	}
	if scoped.quitting {
		t.Error("the program quit despite the block")
	}
}
