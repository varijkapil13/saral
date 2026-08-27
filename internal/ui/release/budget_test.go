//go:build !race

package release

import (
	"testing"
	"time"
)

// The budgets in docs/PERFORMANCE.md are about the binary that ships, so a
// budget test is built without the race detector and CI runs it in the lane that
// has none. The tag on its own would mean it never ran anywhere; the lane is
// what makes it a gate.
//
// None of these may be parallel. An allocation count comes from process-wide
// MemStats, so a benchmark run beside another test is handed that test's
// allocations divided by its own iteration count.

// The list is virtualized, so two thousand versions cost a frame what twenty
// cost. The absolute ceiling is the half that matters: a comparison on its own
// passes just as happily at nine hundred allocations a frame.
func TestBudget_ReleasesScrollingCostsTheSameOnTwoThousandVersionsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkReleasesSteadyScroll2000)
	small := testing.Benchmark(BenchmarkReleasesSteadyScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a steady frame: %d allocations over 2000 versions, %d over 20", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 2000-version list allocates %d per frame against %d for a 20-version one; "+
			"the render is not virtualized", bigAllocs, smallAllocs)
	}
	// The two left are the frame string View has to return and the spelling of
	// the keystroke that asked for it; everything behind them is memoized.
	if bigAllocs > 2 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the "+
			"frame itself and the keystroke", bigAllocs)
	}
}

// Walking a fresh row into view on every frame misses the memo by construction,
// which is what says the miss itself is bounded: the rows that moved are
// rendered and the window around them is not.
//
// 106 on an M2 Pro, every run.
func TestBudget_ReleasesAMemoMissCostsTheRowsThatMovedAndNotAWindow(t *testing.T) {
	got := testing.Benchmark(BenchmarkReleasesWalk).AllocsPerOp()
	t.Logf("a frame that renders fresh rows: %d allocations, ceiling 130", got)
	if got > 130 {
		t.Errorf("a frame that renders rows it has never rendered allocates %d times, over the "+
			"ceiling of 130; it measured 106 when the ceiling was set, and a window of forty rows "+
			"would be an order of magnitude more", got)
	}
}

func TestBudget_ReleasesKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkReleasesKeystroke)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s over two thousand versions, want under the 16ms in "+
			"docs/PERFORMANCE.md", per)
	}
}

func TestBudget_ReleasesFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkReleasesRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_ReleasesRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := stocked(t, 40, 120, 30)
	_ = m.View()

	if got := testing.AllocsPerRun(200, func() { _ = m.row(1, false) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}

// The release screen is a list like any other, so it virtualizes like one: a
// project with two thousand versions to move the open issues to costs a frame
// what one with twenty costs.
func TestBudget_ReleaseFlowScrollingCostsTheSameOnTwoThousandVersionsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkFlowSteadyScroll2000)
	small := testing.Benchmark(BenchmarkFlowSteadyScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a steady frame: %d allocations over 2000 versions, %d over 20", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 2000-version picker allocates %d per frame against %d for a 20-version one; "+
			"the render is not virtualized", bigAllocs, smallAllocs)
	}
	if bigAllocs > 4 {
		t.Errorf("a steady-state frame allocates %d times, want the memo and the chrome to carry "+
			"all but the frame itself", bigAllocs)
	}
}

func TestBudget_ReleaseFlowFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkFlowRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}
