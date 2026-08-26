package kernel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// privMsg is a message only the view that asked for it knows what to do with,
// which is what every read, page and write in this program answers with.
type privMsg struct{ n int }

// asker is a view that holds an address and records what it is given. It counts
// what it recognises rather than everything, so that the size and focus messages
// the kernel sends on a push do not have to be spelt out in every assertion.
type asker struct {
	id   string
	addr Addr
	got  []privMsg
}

func newAsker(id string) *asker { return &asker{id: id, addr: NewAddr()} }

func (a *asker) Init() tea.Cmd { return nil }

func (a *asker) Update(msg tea.Msg) (View, tea.Cmd) {
	if mine, ours := msg.(privMsg); ours {
		a.got = append(a.got, mine)
	}
	return a, nil
}

func (a *asker) View() string { return a.id + " body" }

func (a *asker) Addr() Addr { return a.addr }

// answer is what a view's own command produces once it has been addressed.
func answer(to ...Addr) ReplyMsg { return ReplyMsg{To: to, Msg: privMsg{n: 1}} }

// The bug this is all for: a view's answer used to be delivered to whatever the
// stack had on top when it landed, which is never the view that asked — a view
// is blurred exactly by something being pushed over it.
func TestReply_AnAnswerGoesToTheViewThatAskedAndNotToWhatIsOnTop(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := newAsker("board")
	RegisterView(spec("board", 1, "", board))
	palette := newAsker(PaletteViewID)
	RegisterView(spec(PaletteViewID, 0, "", palette))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "ctrl+k")
	if len(m.stack) != 2 {
		t.Fatalf("the palette did not open: stack depth %d", len(m.stack))
	}

	next, _ := m.Update(answer(board.addr))
	m = next.(Model)

	if len(board.got) != 1 {
		t.Errorf("the view that asked was given %d of its answers, want 1", len(board.got))
	}
	if len(palette.got) != 0 {
		t.Errorf("the view on top was given %d answers it never asked for", len(palette.got))
	}
}

// A root switched away from is parked rather than discarded, so it is still
// waiting for what it asked for and its answer is there when it comes back.
func TestReply_AnAnswerForARootParkedOffScreenStillReachesIt(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := newAsker("board")
	RegisterView(spec("board", 1, "", board))
	backlog := newAsker("backlog")
	RegisterView(spec("backlog", 2, "", backlog))

	m := newAt(t, testDeps(), 120, 30)
	m, _ = press(m, "g", "2")

	if _, cmd := m.Update(answer(board.addr)); cmd != nil {
		t.Error("delivering an answer to a parked root asked for more work")
	}

	if len(board.got) != 1 {
		t.Errorf("the parked root was given %d of its answers, want 1", len(board.got))
	}
	if len(backlog.got) != 0 {
		t.Errorf("the root on screen was given %d answers it never asked for", len(backlog.got))
	}
}

// A discarded view has no frame left to draw into, so its answer goes nowhere
// rather than into whatever took its place.
func TestReply_AnAnswerForADiscardedViewIsDropped(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := newAsker("board")
	RegisterView(spec("board", 1, "", board))

	m := newAt(t, testDeps(), 120, 30)
	detail := newAsker("detail")
	next, _ := m.Update(PushMsg{View: detail, ID: "detail", Title: "PROJ-1"})
	m = next.(Model)
	m, _ = press(m, "esc")

	if _, cmd := m.Update(answer(detail.addr)); cmd != nil {
		t.Error("an answer nobody is waiting for asked for more work")
	}

	if len(detail.got) != 0 {
		t.Errorf("a view the kernel has thrown away was given %d answers", len(detail.got))
	}
	if len(board.got) != 0 {
		t.Errorf("the view underneath was given %d answers addressed to another view", len(board.got))
	}
}

// A view held inside another is not an entry and cannot be delivered to, so its
// answer names the holder after itself. The kernel takes the most particular
// address it can see: the held view where it is on the stack in its own right,
// and the holder where it is not.
func TestReply_TheMostParticularAddressTheKernelCanSeeWins(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := newAsker("board")
	RegisterView(spec("board", 1, "", board))

	m := newAt(t, testDeps(), 120, 30)
	pane, held := newAsker("pane"), newAsker("thread")
	next, _ := m.Update(PushMsg{View: pane, ID: "pane", Title: "PROJ-1"})
	m = next.(Model)

	next, _ = m.Update(answer(held.addr, pane.addr))
	m = next.(Model)
	if len(pane.got) != 1 {
		t.Errorf("the holder was given %d answers for the view it holds, want 1", len(pane.got))
	}
	if len(held.got) != 0 {
		t.Errorf("a view the kernel cannot see was delivered to %d times", len(held.got))
	}

	next, _ = m.Update(PushMsg{View: held, ID: "thread", Title: "PROJ-1", Lent: true})
	m = next.(Model)
	m.Update(answer(held.addr, pane.addr))

	if len(held.got) != 1 {
		t.Errorf("the lent view on the stack was given %d of its answers, want 1", len(held.got))
	}
	if len(pane.got) != 1 {
		t.Errorf("the holder was given %d answers, want only the one from before it was lent", len(pane.got))
	}
}

// The other half of the rule, and the one that keeps a cursor blink where it is:
// a message with no address on it is still the top of the stack's. A widget's
// tick belongs to whoever is being looked at.
func TestReply_AMessageWithNoAddressStillGoesToTheTop(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	board := newAsker("board")
	RegisterView(spec("board", 1, "", board))

	m := newAt(t, testDeps(), 120, 30)
	pane := newAsker("pane")
	next, _ := m.Update(PushMsg{View: pane, ID: "pane", Title: "PROJ-1"})
	m = next.(Model)

	m.Update(privMsg{n: 1})

	if len(pane.got) != 1 {
		t.Errorf("the top of the stack was given %d unaddressed messages, want 1", len(pane.got))
	}
	if len(board.got) != 0 {
		t.Errorf("an unaddressed message reached %d views under the top one", len(board.got))
	}
}

// Reply is what a view wraps its own command in, and the envelope has to survive
// whatever the command answers with — including nothing at all, which is what a
// command that decided there was nothing to do returns.
func TestReply_WrapsWhatACommandAnswersWithAndLetsNothingThrough(t *testing.T) {
	at := NewAddr()
	if got := Reply(nil, at); got != nil {
		t.Error("wrapping no command produced one")
	}

	quiet := Reply(func() tea.Msg { return nil }, at)
	if got := quiet(); got != nil {
		t.Errorf("a command that answered with nothing was wrapped into %T", got)
	}

	sent := Reply(func() tea.Msg { return privMsg{n: 7} }, at)
	reply, addressed := sent().(ReplyMsg)
	if !addressed {
		t.Fatalf("the command came back as %T, want it addressed", sent())
	}
	if len(reply.To) != 1 || reply.To[0] != at {
		t.Errorf("the answer is addressed to %v, want %v", reply.To, at)
	}
	if got, ours := reply.Msg.(privMsg); !ours || got.n != 7 {
		t.Errorf("the envelope carries %#v, want the message the command produced", reply.Msg)
	}
}

// Two views must never share an address, or an answer to one is drawn into the
// other — two detail panes on one stack are two different issues.
func TestReply_EveryAddressIsItsOwn(t *testing.T) {
	seen := make(map[Addr]bool, 1000)
	for range 1000 {
		at := NewAddr()
		if at == 0 {
			t.Fatal("an address came back as the zero value, which the kernel resolves to nothing")
		}
		if seen[at] {
			t.Fatalf("%v was handed out twice", at)
		}
		seen[at] = true
	}
}
