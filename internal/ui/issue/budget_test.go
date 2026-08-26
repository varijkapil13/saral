//go:build !race

package issue

import (
	"testing"
	"time"
)

// The race detector allocates on its own account, so an allocation ceiling run
// under -race measures the detector rather than the pane, which is what the
// build tag above is for. The tag alone would mean these never ran anywhere, so
// ci.yml has a lane without the detector that runs exactly them.
//
// None of them may be parallel either. An allocation count comes from
// process-wide MemStats, so a benchmark run beside another test is handed that
// test's allocations divided by its own iteration count.
//
// Below the breakpoint the thread is off screen and a frame costs one
// allocation: the string the caller keeps. The description, the fields and the
// identity header are all memoized on a key carrying the width, the theme, the
// read the data came from and the expands that are open, and the gutter, the
// padding and the truncation are appended into a buffer the pane reuses.
//
// Above it the thread is on screen and costs one more, which is the comment view
// building its own frame afresh on every call. Splitting that frame into rows is
// skipped while it has not changed, so the extra one is its own and not the
// split.
func TestBudget_SteadyFrameCostsTheFrameAndTheThreadAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bench   func(*testing.B)
		ceiling int64
	}{
		{"narrow, with the thread off screen", BenchmarkIssueViewNarrow, 1},
		{"wide, with the thread beside the fields", BenchmarkIssueView, 2},
	} {
		if got := testing.Benchmark(tc.bench).AllocsPerOp(); got > tc.ceiling {
			t.Errorf("a steady-state frame %s allocates %d times, want at most %d", tc.name, got, tc.ceiling)
		}
	}
}

// A drag is a stream of motion messages, so what it costs per message is the
// thing to hold down. Three claims, in the order they matter:
//
// A frame drawn while the boundary is held costs what a frame at rest costs.
// Holding one changes nothing about what is on screen, so nothing about drawing
// it may change either.
//
// A motion that does not move the boundary — most of them, since a pointer
// dragging a column wanders up and down it — costs no render. It is a keystroke
// that moves no memo plus the two interface boxes a mouse message pays and a
// keypress does not: the widget takes a tea.MouseMsg and the thread takes a
// tea.Msg, and a message arrives here as neither.
//
// And a motion that does move it is a resize of two regions, which is what it
// costs. There is no cheaper honest answer: both panes have a width they have
// never been rendered at, and the lines are what a width means.
func TestBudget_DragCostsAFrameWhileHeldAndAResizeWhileMoving(t *testing.T) {
	rest := testing.Benchmark(BenchmarkIssueView).AllocsPerOp()
	if held := testing.Benchmark(BenchmarkIssueDragFrame).AllocsPerOp(); held > rest {
		t.Errorf("a frame with the boundary held allocates %d times against %d at rest", held, rest)
	}

	const boxes = 2
	scrolling := testing.Benchmark(BenchmarkIssueScroll).AllocsPerOp()
	if holding := testing.Benchmark(BenchmarkIssueDragHold).AllocsPerOp(); holding > scrolling+boxes {
		t.Errorf("a motion that moves the boundary nowhere allocates %d times against %d for a keystroke "+
			"that moves nothing, so something is being rendered per motion message", holding, scrolling)
	}

	resize := testing.Benchmark(BenchmarkIssueResize).AllocsPerOp()
	if moving := testing.Benchmark(BenchmarkIssueDragMove).AllocsPerOp(); moving > resize {
		t.Errorf("a motion that moves the boundary allocates %d times against %d for the resize it is",
			moving, resize)
	}
}

// A keystroke that only moves the description must not re-render anything: the
// memo key has not moved, so a scroll costs a frame and the keypress itself.
func TestBudget_ScrollingCostsNoMoreThanStandingStill(t *testing.T) {
	still := testing.Benchmark(BenchmarkIssueView).AllocsPerOp()
	scrolling := testing.Benchmark(BenchmarkIssueScroll).AllocsPerOp()
	if scrolling > still+1 {
		t.Errorf("scrolling allocates %d times against %d standing still, so a memo is being rebuilt per keystroke",
			scrolling, still)
	}
}

func TestBudget_KeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkIssueScroll)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_FullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkIssueRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}
