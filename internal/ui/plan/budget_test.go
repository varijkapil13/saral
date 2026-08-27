//go:build !race

package plan

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

// The list is virtualized, so a profile defining two thousand plans costs what
// one defining twenty costs per frame.
func TestBudget_PlansScrollingCostsTheSameOnTwoThousandPlansAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkPlansSteadyScroll2000)
	small := testing.Benchmark(BenchmarkPlansSteadyScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a steady frame over 2000 plans: %d allocations; over 20: %d; ceiling 1", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 2000-plan list allocates %d per frame against %d for a 20-plan one; the render is not virtualized",
			bigAllocs, smallAllocs)
	}
	// The one allocation left is the frame string itself, which View has to
	// return; the rows, the chrome and the reason line are all memoized.
	if bigAllocs > 1 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself",
			bigAllocs)
	}
}

// Walking a fresh row into view on every frame misses the memo by construction
// past its limit, which is what says the miss itself is bounded: the two rows
// whose highlight changed are rendered and the window around them is not.
//
// 70 on an M2 Pro, every run.
func TestBudget_APlansMemoMissCostsTwoRowsAndNotAWindow(t *testing.T) {
	got := testing.Benchmark(BenchmarkPlansWalk).AllocsPerOp()
	t.Logf("a frame that renders two fresh rows: %d allocations, ceiling 80", got)
	if got > 80 {
		t.Errorf("a frame that renders the two rows whose highlight moved allocates %d times, over the "+
			"ceiling of 80; it measured 70 when the ceiling was set, and a window of forty rows would "+
			"be an order of magnitude more", got)
	}
}

func TestBudget_PlansKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkPlansWalk)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s over two thousand plans, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_PlansFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkPlansRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

// A redraw of a screen nothing has changed on is the frame string and nothing
// else: every row and both chrome lines are memo hits.
func TestBudget_PlansStandingStillCostsTheFrameAndNothingElse(t *testing.T) {
	got := testing.Benchmark(BenchmarkPlansRedraw200x60).AllocsPerOp()
	t.Logf("a redraw with nothing moved: %d allocations, ceiling 2", got)
	if got > 2 {
		t.Errorf("redrawing an unchanged frame allocates %d times, want the memo to carry all but the "+
			"frame string itself", got)
	}
}

func TestBudget_PlanRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := stocked(t, 2000, 120, 40)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}
