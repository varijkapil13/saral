package kernel

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func stacked(t *testing.T, m Model, v View, id, title string) Model {
	t.Helper()

	next, _ := m.Update(PushMsg{View: v, ID: id, Title: title})
	return next.(Model)
}

func poppedTo(t *testing.T, m Model, id, title string) (Model, tea.Cmd) {
	t.Helper()

	next, cmd := m.Update(PopToMsg{ID: id, Title: title})
	return next.(Model), cmd
}

func TestPopTo_CollapsesToTheNamedEntry(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	first, second, third := &stubView{id: "issue"}, &stubView{id: "issue"}, &stubView{id: "issue"}
	m = stacked(t, m, first, "issue", "PROJ-1")
	m = stacked(t, m, second, "issue", "PROJ-2")
	m = stacked(t, m, third, "issue", "PROJ-3")

	m, _ = poppedTo(t, m, "issue", "PROJ-1")
	if len(m.stack) != 2 || m.top().view != View(first) {
		t.Fatalf("depth %d, want 2 with PROJ-1 on top", len(m.stack))
	}
	if second.closed != 1 || third.closed != 1 || first.closed != 0 {
		t.Errorf("closed first=%d second=%d third=%d, want 0 1 1", first.closed, second.closed, third.closed)
	}
	if first.width == 0 || first.height == 0 {
		t.Error("the target was not resized")
	}
}

func TestPopTo_PicksTheTopmostMatch(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	lower, upper, above := &stubView{id: "issue"}, &stubView{id: "issue"}, &stubView{id: "issue"}
	m = stacked(t, m, lower, "issue", "PROJ-1")
	m = stacked(t, m, upper, "issue", "PROJ-1")
	m = stacked(t, m, above, "issue", "PROJ-2")

	m, _ = poppedTo(t, m, "issue", "PROJ-1")
	if len(m.stack) != 3 || m.top().view != View(upper) {
		t.Fatalf("depth %d, want the upper PROJ-1 on top of 3", len(m.stack))
	}
	if lower.closed != 0 || above.closed != 1 {
		t.Errorf("closed lower=%d above=%d, want 0 1", lower.closed, above.closed)
	}
}

func TestPopTo_UnknownEntryChangesNothing(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	a, b := &stubView{id: "issue"}, &stubView{id: "issue"}
	m = stacked(t, m, a, "issue", "PROJ-1")
	m = stacked(t, m, b, "issue", "PROJ-2")

	for _, c := range []struct{ id, title string }{
		{"issue", "PROJ-9"},
		{"sheet", "PROJ-1"},
		{"issue", ""},
		{"issue", "PROJ-2"},
	} {
		var cmd tea.Cmd
		m, cmd = poppedTo(t, m, c.id, c.title)
		if len(m.stack) != 3 || m.top().view != View(b) || a.closed+b.closed != 0 {
			t.Fatalf("PopTo(%q, %q) moved the stack: depth %d", c.id, c.title, len(m.stack))
		}
		if cmd != nil {
			t.Errorf("PopTo(%q, %q) returned a command for a no-op", c.id, c.title)
		}
	}
}

func TestPopTo_ABlockerAboveTheTargetIsAskedOrRefused(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	t.Run("refused when it is not on top", func(t *testing.T) {
		m := newAt(t, testDeps(), 120, 30)
		target := &stubView{id: "issue"}
		draft := &askingStub{stubView: stubView{id: "compose", blocks: "unsaved"}}
		above := &stubView{id: "issue"}
		m = stacked(t, m, target, "issue", "PROJ-1")
		m = stacked(t, m, draft, "compose", "PROJ-1")
		m = stacked(t, m, above, "issue", "PROJ-2")

		m, _ = poppedTo(t, m, "issue", "PROJ-1")
		if len(m.stack) != 4 || draft.asked != 0 || above.closed != 0 {
			t.Fatalf("depth %d asked %d closed %d; a blocker not on top is refused untouched", len(m.stack), draft.asked, above.closed)
		}
		if m.status != "unsaved" || m.statusLevel != LevelWarn {
			t.Errorf("status %q/%v, want the blocker's reason as a warning", m.status, m.statusLevel)
		}
	})

	t.Run("asked when the blocker is on top", func(t *testing.T) {
		m := newAt(t, testDeps(), 120, 30)
		target := &stubView{id: "issue"}
		asker := &askingStub{stubView: stubView{id: "compose", blocks: "unsaved"}}
		m = stacked(t, m, target, "issue", "PROJ-1")
		m = stacked(t, m, asker, "compose", "PROJ-1")

		m, _ = poppedTo(t, m, "issue", "PROJ-1")
		if asker.asked != 1 || len(m.stack) != 3 || asker.closed != 0 {
			t.Fatalf("asked %d depth %d closed %d, want one ask and nothing dropped", asker.asked, len(m.stack), asker.closed)
		}
	})
}

func TestPopTo_ReplaysAfterTheAskIsAnswered(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	target := &stubView{id: "issue"}
	asker := &askingStub{stubView: stubView{id: "compose", blocks: "unsaved"}}
	m = stacked(t, m, target, "issue", "PROJ-1")
	m = stacked(t, m, asker, "compose", "PROJ-1")
	m, _ = poppedTo(t, m, "issue", "PROJ-1")

	asker.blocks = ""
	next, _ := m.Update(ProceedMsg{})
	m = next.(Model)
	if len(m.stack) != 2 || m.top().view != View(target) || asker.closed != 1 {
		t.Fatalf("depth %d closed %d, want the stack collapsed to PROJ-1", len(m.stack), asker.closed)
	}

	next, _ = m.Update(ProceedMsg{})
	if again := next.(Model); len(again.stack) != 2 {
		t.Error("a second answer replayed a gesture that was already spent")
	}
}

func TestPopTo_ClosesEachDroppedEntryOnceButNotLentOnes(t *testing.T) {
	resetRegistry()
	t.Cleanup(resetRegistry)
	RegisterView(spec("board", 1, "", &stubView{id: "board"}))

	m := newAt(t, testDeps(), 120, 30)
	target, owned, thread, top := &stubView{id: "issue"}, &stubView{id: "issue"}, &stubView{id: "comments"}, &stubView{id: "issue"}
	m = stacked(t, m, target, "issue", "PROJ-1")
	m = stacked(t, m, owned, "issue", "PROJ-2")
	next, _ := m.Update(Lend("comments", "PROJ-2", thread)().(PushMsg))
	m = next.(Model)
	m = stacked(t, m, top, "issue", "PROJ-3")

	m, _ = poppedTo(t, m, "issue", "PROJ-1")
	if len(m.stack) != 2 {
		t.Fatalf("depth %d, want 2", len(m.stack))
	}
	if owned.closed != 1 || top.closed != 1 || target.closed != 0 {
		t.Errorf("closed owned=%d top=%d target=%d, want 1 1 0", owned.closed, top.closed, target.closed)
	}
	if thread.closed != 0 {
		t.Errorf("the lent view was closed %d times; its lender still holds it", thread.closed)
	}
}
