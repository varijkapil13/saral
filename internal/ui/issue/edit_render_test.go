package issue

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func kernelSize(w, h int) kernel.SizeMsg { return kernel.SizeMsg{Width: w, Height: h} }

func TestEditRender_DrawsTheFieldsAtEveryWidth(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		w, h int
	}{
		{"edit_120x38.golden", 120, 38},
		{"edit_100x28.golden", 100, 28},
		{"edit_80x18.golden", 80, 18},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(8)
			p := openEditor(t, f, fullIssue(t, f, "PROJ-6"), tc.w, tc.h)
			golden(t, tc.name, p.frame())
		})
	}
}

func TestEditRender_DrawsWhatItIsAboutToChange(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	p := openEditor(t, f, fullIssue(t, f, "PROJ-6"), 100, 28)

	p.keys("enter")
	p.typed(" twice over")
	p.keys("enter")
	p.keys("down", "down", "right")
	p.keys("ctrl+s")

	golden(t, "edit_confirm_100x28.golden", p.frame())
}

// TestEditRender_SaysWhichFieldsWereNeverRead is the narrow-mask state on
// screen: three rows the issue was not read with, each saying so.
func TestEditRender_SaysWhichFieldsWereNeverRead(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	seed := listSeed(t, f, "PROJ-6")
	f.FailNext(&jira.CapabilityError{Reason: "you need Browse Projects in PROJ"})
	p := openEditor(t, f, seed, 100, 28)

	golden(t, "edit_unread_100x28.golden", p.frame())
}

func TestMoveRender_DrawsTheMovesAndTheScreenOneNeeds(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	iss := fullIssue(t, f, "PROJ-6")

	wide := openMover(t, f, iss, 120, 38)
	golden(t, "move_120x38.golden", wide.frame())

	list := openMover(t, f, iss, 100, 28)
	golden(t, "move_100x28.golden", list.frame())

	narrow := openMover(t, f, iss, 80, 18)
	golden(t, "move_80x18.golden", narrow.frame())

	screen := openMover(t, f, iss, 100, 28)
	at := -1
	for i, move := range screen.mover().moves {
		if move.HasScreen {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("no move on this issue has a screen")
	}
	for range at {
		screen.keys("down")
	}
	screen.keys("enter")
	golden(t, "move_screen_100x28.golden", screen.frame())

	screen.keys("enter")
	golden(t, "move_confirm_100x28.golden", screen.frame())
}

// TestEditRender_FillsTheBoxItWasGiven keeps a pane from leaving the previous
// frame's rows on screen or pushing the footer off the bottom.
func TestEditRender_FillsTheBoxItWasGiven(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	for _, h := range []int{6, 12, 40} {
		p := openEditor(t, f, fullIssue(t, f, "PROJ-6"), 90, h)
		if got := strings.Count(p.frame(), "\n") + 1; got != h {
			t.Errorf("at height %d the editor drew %d lines", h, got)
		}
		mv := openMover(t, f, fullIssue(t, f, "PROJ-6"), 90, h)
		if got := strings.Count(mv.frame(), "\n") + 1; got != h {
			t.Errorf("at height %d the picker drew %d lines", h, got)
		}
	}
}

func BenchmarkEditView(b *testing.B) {
	f := newFake(8)
	iss, err := f.Issue(b.Context(), "PROJ-6")
	if err != nil {
		b.Fatalf("Issue: %v", err)
	}
	view := NewEdit(testDeps(f), iss, withDrafts(draftStore{dir: b.TempDir()}))
	view, _ = view.Update(kernelSize(120, 38))

	b.ReportAllocs()
	for b.Loop() {
		_ = view.View()
	}
}

func BenchmarkMoveView(b *testing.B) {
	f := newFake(8)
	iss, err := f.Issue(b.Context(), "PROJ-6")
	if err != nil {
		b.Fatalf("Issue: %v", err)
	}
	view := NewMove(testDeps(f), iss)
	view, _ = view.Update(kernelSize(120, 38))
	view.Update(movesLoadedMsg{})

	b.ReportAllocs()
	for b.Loop() {
		_ = view.View()
	}
}
