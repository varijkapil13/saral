//go:build !race

package backlog

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

// The backlog is virtualized, so ten thousand issues cost what twenty cost per
// frame.
func TestBudget_BacklogScrollingCostsTheSameOnTenThousandRowsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkBacklogSteadyScroll10k)
	small := testing.Benchmark(BenchmarkBacklogSteadyScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a steady frame: %d allocations over ten thousand issues, %d over twenty", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 10k-issue backlog allocates %d per frame against %d for a 20-issue one; the render is not virtualized",
			bigAllocs, smallAllocs)
	}
	// The one allocation left is the frame string itself, which View has to
	// return; every row and every line under them is memoized.
	if bigAllocs > 1 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself", bigAllocs)
	}
}

// Walking a fresh row into view on every frame misses the memo by construction,
// which is what says the miss itself is bounded: one row is rendered and the
// window around it is not.
//
// 43 on an M2 Pro, every run.
func TestBudget_BacklogAMemoMissCostsOneRowAndNotAWindow(t *testing.T) {
	got := testing.Benchmark(BenchmarkBacklogWalk10k).AllocsPerOp()
	t.Logf("a frame that renders one fresh row: %d allocations, ceiling 52", got)
	if got > 52 {
		t.Errorf("a frame that renders a row it has never rendered allocates %d times, over the "+
			"ceiling of 52; it measured 43 when the ceiling was set, and a window of forty rows "+
			"would be an order of magnitude more", got)
	}
}

func TestBudget_BacklogKeystrokeToFrameAtTenThousandIssues(t *testing.T) {
	res := testing.Benchmark(BenchmarkBacklogWalk10k)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s at 10k issues, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

// Picking an issue changes what one row looks like and nothing else, so it costs
// one row and the frame rather than the screenful a memo reset would cost.
func TestBudget_BacklogPickingAnIssueCostsOneRowAndTheFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkBacklogPickAndFrame)
	got := res.AllocsPerOp()
	t.Logf("a pick and the frame after it: %d allocations, ceiling 4", got)
	if got > 4 {
		t.Errorf("picking an issue allocates %d times, over the ceiling of 4; it measured 2 at every "+
			"benchmark length when the ceiling was set, and clearing the whole memo instead of "+
			"letting the row's own key miss measured 766", got)
	}
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("picking an issue took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_BacklogFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkBacklogRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_BacklogRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := stocked(t, 10000, 120, 40)
	if !m.rows[0].head || m.rows[1].head {
		t.Fatalf("the fixture does not draw a section head then an issue, so this guard is not "+
			"measuring both kinds of row: %+v", m.rows[:2])
	}
	for at, what := range map[int]string{0: "a section head", 1: "an issue row"} {
		if got := testing.AllocsPerRun(200, func() { _ = m.line(at) }); got != 0 {
			t.Errorf("%s allocates %.1f times once memoized, want none", what, got)
		}
	}
}

// Regrouping is what a move does after the site accepts a chunk: every issue is
// placed into its section again and every section is put back in rank order. It
// happens between two frames, so it is on the keystroke budget.
func TestBudget_BacklogRegroupingAfterAMoveIsOnTheKeystrokeBudget(t *testing.T) {
	res := testing.Benchmark(BenchmarkBacklogRegroup10k)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("regrouping ten thousand issues took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}
