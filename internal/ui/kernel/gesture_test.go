package kernel

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func twoRoots(t *testing.T) (board, backlog *stubView) {
	t.Helper()
	resetRegistry()
	t.Cleanup(resetRegistry)
	board, backlog = &stubView{id: "board"}, &stubView{id: "backlog"}
	RegisterView(spec("board", 1, "", board))
	RegisterView(spec("backlog", 2, "", backlog))
	return board, backlog
}

func pushed(t *testing.T, m Model, msg PushMsg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func proceed(m Model) Model {
	next, _ := m.Update(ProceedMsg{})
	return next.(Model)
}

func TestProceed_ARootSwitchAskedAboutIsCompleted(t *testing.T) {
	twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	asker := &askingStub{stubView: stubView{id: "issue", blocks: "unsaved"}}
	m = pushed(t, m, PushMsg{View: asker, ID: "issue", Title: "PROJ-1"})

	m, _ = press(m, "g", "2")
	if asker.asked != 1 {
		t.Fatalf("AskClose was called %d times, want once", asker.asked)
	}
	asker.blocks = ""
	m = proceed(m)
	if len(m.stack) != 1 || m.stack[0].spec.ID != "backlog" {
		t.Fatalf("the root switch was not completed: %d deep on %q", len(m.stack), m.stack[0].spec.ID)
	}
	if asker.closed != 1 {
		t.Errorf("the pane the switch threw away was closed %d times, want once", asker.closed)
	}
}

func TestProceed_AQuitAskedAboutQuits(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	asker := &askingStub{stubView: stubView{id: "board", blocks: "unsaved"}}
	RegisterView(spec("board", 1, "", asker))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "q")
	if m.quitting {
		t.Fatal("quit before the view answered")
	}
	asker.blocks = ""
	next, cmd := m.Update(ProceedMsg{})
	if !next.(Model).quitting || cmd == nil {
		t.Fatal("Proceed did not carry on with the quit it was asked about")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("Proceed quit without handing the runtime tea.Quit")
	}
}

func TestProceed_APopAskedAboutPops(t *testing.T) {
	twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	asker := &askingStub{stubView: stubView{id: "issue", blocks: "unsaved"}}
	m = pushed(t, m, PushMsg{View: asker, ID: "issue"})

	m, _ = press(m, "esc")
	asker.blocks = ""
	if m = proceed(m); len(m.stack) != 1 || m.stack[0].spec.ID != "board" {
		t.Fatalf("Proceed did not pop: %d deep", len(m.stack))
	}
}

// An answer that lands once the stack has moved on is for a gesture nobody is
// waiting on any more, and replaying it would pop the wrong view.
func TestProceed_AnAnswerForAStackThatHasMovedOnDoesNothing(t *testing.T) {
	twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	asker := &askingStub{stubView: stubView{id: "issue", blocks: "unsaved"}}
	m = pushed(t, m, PushMsg{View: asker, ID: "issue"})
	m, _ = press(m, "esc")
	other := &stubView{id: "thread"}
	m = pushed(t, m, PushMsg{View: other, ID: "thread"})

	m = proceed(m)
	if len(m.stack) != 3 || other.closed != 0 {
		t.Fatalf("a stale answer replayed a gesture: %d deep, thread closed %d times", len(m.stack), other.closed)
	}
}

func TestProceed_WithNothingAskedDoesNothing(t *testing.T) {
	twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	m = pushed(t, m, PushMsg{View: &stubView{id: "issue"}, ID: "issue"})
	if m = proceed(m); len(m.stack) != 2 {
		t.Fatalf("Proceed with nothing held up changed the stack to %d deep", len(m.stack))
	}
}

// A view lent over the dirty pane is on top, so the pane underneath would be
// asked to put up a prompt nobody can see, and its answer would pop the lent
// view instead of itself. It is refused in its own words instead.
func TestAskClose_AViewUnderneathIsRefusedRatherThanAsked(t *testing.T) {
	twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	asker := &askingStub{stubView: stubView{id: "issue", blocks: "PROJ-1 has 1 change unsaved"}}
	m = pushed(t, m, PushMsg{View: asker, ID: "issue"})
	thread := &stubView{id: "thread"}
	m = pushed(t, m, PushMsg{View: thread, ID: "thread", Lent: true})

	m, _ = press(m, "g", "2")
	if asker.asked != 0 {
		t.Fatalf("a view under the top was asked %d times", asker.asked)
	}
	if m.status != "PROJ-1 has 1 change unsaved" || m.statusLevel != LevelWarn {
		t.Errorf("the refusal said %q at level %d", m.status, m.statusLevel)
	}
	if len(m.stack) != 3 || m.stack[0].spec.ID != "board" {
		t.Fatalf("the refused switch changed the stack: %d deep on %q", len(m.stack), m.stack[0].spec.ID)
	}
	if m = proceed(m); len(m.stack) != 3 {
		t.Errorf("a refusal left a gesture for Proceed to replay")
	}
}

// A root switch parks the root rather than discarding it, so a root holding
// something is no reason to refuse one. Quitting throws it away and still asks.
func TestBlocker_ARootSwitchDoesNotAskTheRootItParks(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board", blocks: "a card is mid-move"}
	RegisterView(spec("board", 1, "", board))
	RegisterView(spec("backlog", 2, "", &stubView{id: "backlog"}))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "g", "2")
	if m.stack[0].spec.ID != "backlog" {
		t.Fatalf("a root holding something refused the switch that parks it: %q", m.status)
	}
	if board.closed != 0 {
		t.Error("the parked root was discarded")
	}

	m, _ = press(m, "g", "1")
	if m, _ = press(m, "q"); m.quitting {
		t.Error("quit went ahead over a root holding something")
	}
}

// OpenThen hands its message to the view it opened and to nobody else, and to
// nobody at all when the open does not happen.
func TestOpenThen_DeliversOnlyWhereTheOpenLanded(t *testing.T) {
	cases := map[string]struct {
		over    *stubView
		onRoot  bool
		landed  bool
		blocker *askingStub
	}{
		"from another root":     {landed: true},
		"already on it":         {onRoot: true, landed: true},
		"over a pushed view":    {over: &stubView{id: "x"}, landed: true},
		"refused by a draft":    {over: &stubView{id: "x", blocks: "draft"}},
		"held up by a question": {blocker: &askingStub{stubView: stubView{id: "x", blocks: "draft"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			board, backlog := twoRoots(t)
			m := newAt(t, testDeps(), 120, 30)
			switch {
			case tc.onRoot:
				m, _ = press(m, "g", "2")
			case tc.over != nil:
				m = pushed(t, m, PushMsg{View: tc.over, ID: "x"})
			case tc.blocker != nil:
				m = pushed(t, m, PushMsg{View: tc.blocker, ID: "x"})
			}
			board.seen, backlog.seen = nil, nil

			next, _ := m.Update(OpenMsg{ID: "backlog", Then: RunQueryMsg{JQL: "then"}})
			m = next.(Model)
			if slices.Contains(board.seen, "query:then") {
				t.Errorf("the message reached a view the open was not for: %v", board.seen)
			}
			if tc.over != nil && saw(tc.over, "query:then") {
				t.Errorf("the message reached the view pushed over the root: %v", tc.over.seen)
			}
			if tc.blocker != nil && saw(&tc.blocker.stubView, "query:then") {
				t.Errorf("the message reached the view asked about the switch: %v", tc.blocker.seen)
			}
			if got := slices.Contains(backlog.seen, "query:then"); got != tc.landed {
				t.Errorf("delivered=%v, want %v: %v", got, tc.landed, backlog.seen)
			}
			if tc.blocker == nil {
				return
			}
			tc.blocker.blocks = ""
			m = proceed(m)
			if !slices.Contains(backlog.seen, "query:then") || m.stack[0].spec.ID != "backlog" {
				t.Errorf("the open Proceed replayed did not carry its message: %v", backlog.seen)
			}
		})
	}
}

func TestOpenThen_DeliversNothingToAViewTheSiteDoesNotAllow(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := &stubView{id: "board"}
	RegisterView(spec("board", 1, "", board))
	RegisterView(spec("plans", 2, "plans", &stubView{id: "plans"}))

	d := testDeps()
	d.Caps.Plans.OK = false
	m := newAt(t, d, 120, 30)
	board.seen = nil
	next, _ := m.Update(OpenMsg{ID: "plans", Then: RunQueryMsg{JQL: "then"}})
	if slices.Contains(board.seen, "query:then") {
		t.Errorf("a refused open delivered to the view still on screen: %v", board.seen)
	}
	if next.(Model).stack[0].spec.ID != "board" {
		t.Error("an unavailable view opened")
	}
}

type backStub struct {
	stubView
	wants bool
}

func (s *backStub) WantsBack() bool { return s.wants }

func (s *backStub) Update(msg tea.Msg) (View, tea.Cmd) {
	_, cmd := s.stubView.Update(msg)
	return s, cmd
}

var _ BackClaimer = (*backStub)(nil)

func TestWantsBack_ARootViewCanClaimEsc(t *testing.T) {
	for name, wants := range map[string]bool{"claiming": true, "not claiming": false} {
		t.Run(name, func(t *testing.T) {
			resetRegistry()
			t.Cleanup(resetRegistry)
			root := &backStub{stubView: stubView{id: "board"}, wants: wants}
			RegisterView(spec("board", 1, "", root))

			m := newAt(t, testDeps(), 120, 30)
			next, _ := m.Update(StatusMsg{Text: "something happened"})
			m = next.(Model)
			root.seen = nil
			m, _ = press(m, "esc")
			if got := saw(&root.stubView, "key:esc"); got != wants {
				t.Errorf("esc reached the root=%v, want %v", got, wants)
			}
			if m.status != "" {
				t.Errorf("esc left the status line up: %q", m.status)
			}
		})
	}
}

func TestWantsBack_IsNotAskedOfAPushedView(t *testing.T) {
	twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	over := &backStub{stubView: stubView{id: "pane"}, wants: true}
	m = pushed(t, m, PushMsg{View: over, ID: "pane"})
	if m, _ = press(m, "esc"); len(m.stack) != 1 {
		t.Fatal("a pushed view claiming esc kept itself on the stack")
	}
}

func TestTop_IsTheViewOnTopOrNil(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	if top := (Model{}).Top(); top != nil {
		t.Errorf("an empty stack has %v on top", top)
	}
	board, _ := twoRoots(t)
	m := newAt(t, testDeps(), 120, 30)
	if m.Top() != View(board) {
		t.Error("Top is not the root")
	}
	pane := &stubView{id: "pane"}
	if m = pushed(t, m, PushMsg{View: pane, ID: "pane"}); m.Top() != View(pane) {
		t.Error("Top is not the pushed view")
	}
}
