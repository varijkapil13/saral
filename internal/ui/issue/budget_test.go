//go:build !race

package issue

import "testing"

// The race detector allocates on its own account, so an allocation ceiling run
// under -race measures the detector rather than the pane, which is what the
// build tag above is for.
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
func TestSteadyFrame_CostsTheFrameAndTheThreadAndNothingElse(t *testing.T) {
	t.Parallel()

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

// A keystroke that only moves the description must not re-render anything: the
// memo key has not moved, so a scroll costs a frame and the keypress itself.
func TestScrolling_CostsNoMoreThanStandingStill(t *testing.T) {
	t.Parallel()

	still := testing.Benchmark(BenchmarkIssueView).AllocsPerOp()
	scrolling := testing.Benchmark(BenchmarkIssueScroll).AllocsPerOp()
	if scrolling > still+1 {
		t.Errorf("scrolling allocates %d times against %d standing still, so a memo is being rebuilt per keystroke",
			scrolling, still)
	}
}
