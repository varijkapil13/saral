//go:build !race

package search

import (
	"testing"
	"time"
)

// None of these may be parallel: an allocation count comes from process-wide
// MemStats, so a benchmark run beside another test reports that test's
// allocations as its own.

func TestBudget_SearchKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkSearchKeystroke200)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s over 200 results, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_SearchScrollingCostsTheSameOnTwoHundredRowsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkSearchScroll200).AllocsPerOp()
	small := testing.Benchmark(BenchmarkSearchScroll20).AllocsPerOp()
	if big > small {
		t.Errorf("200 results allocate %d per frame against %d for 20; the render is not virtualized", big, small)
	}
	if big > 1 {
		t.Errorf("a scrolling frame allocates %d times, want the frame string and nothing else", big)
	}
}

func TestBudget_SearchRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	if got := testing.Benchmark(BenchmarkSearchSteadyFrame200).AllocsPerOp(); got > 1 {
		t.Errorf("a steady frame allocates %d times, want the frame string and nothing else", got)
	}
}

// 25 on an M2 Pro when the ceiling was set.
func TestBudget_SearchAMemoMissCostsOneRowAndNotAWindow(t *testing.T) {
	got := testing.Benchmark(BenchmarkSearchWalk200).AllocsPerOp()
	t.Logf("a frame that renders one fresh row: %d allocations, ceiling 28", got)
	if got > 28 {
		t.Errorf("a frame that renders a row it has never rendered allocates %d times, over the ceiling of 28; "+
			"it measured 25 when the ceiling was set, and a window of forty rows would be an order of magnitude more", got)
	}
}

func TestBudget_SearchFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkSearchFullRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}
