//go:build !race

package move

import (
	"testing"
	"time"
)

// The budgets in docs/PERFORMANCE.md are about the binary that ships, so a budget
// test is built without the race detector and CI runs it in the lane that has
// none. The tag on its own would mean it never ran anywhere; the lane is what
// makes it a gate.
//
// None of these may be parallel. An allocation count comes from process-wide
// MemStats, so a benchmark run beside another test is handed that test's
// allocations divided by its own iteration count.

// confirmFrameAllocs is the ceiling one frame of the confirm screen is held to:
// the frame string and the handful of lines above and below the rows, with about
// a tenth of headroom over what the machine measures.
const confirmFrameAllocs = 6

// The confirm screen is virtualized, so a move of a thousand issues costs what a
// move of twenty costs per frame. The absolute ceiling is the half that matters:
// comparing two benchmarks against each other passes just as happily at nine
// hundred allocations a frame.
func TestBudget_MoveScrollingCostsTheSameOnAThousandIssuesAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkMoveConfirmScroll1000)
	small := testing.Benchmark(BenchmarkMoveConfirmScroll20)
	if big.AllocsPerOp() > small.AllocsPerOp() {
		t.Errorf("a thousand-issue confirm screen allocates %d per frame against %d for a twenty-issue one; "+
			"the render is not virtualized", big.AllocsPerOp(), small.AllocsPerOp())
	}
	if got := big.AllocsPerOp(); got > confirmFrameAllocs {
		t.Errorf("a steady-state frame allocates %d times, want at most %d: the memo is meant to carry "+
			"all but the frame itself (docs/PERFORMANCE.md)", got, confirmFrameAllocs)
	}
	t.Logf("a thousand issues: %d allocs a frame; twenty: %d", big.AllocsPerOp(), small.AllocsPerOp())
}

func TestBudget_MoveKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkMoveKeystroke)
	per := time.Duration(res.NsPerOp())
	t.Logf("keystroke to frame over a thousand issues: %s", per)
	if per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s over a thousand issues, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_MoveRemapKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkMoveRemapKeystroke)
	per := time.Duration(res.NsPerOp())
	t.Logf("cycling a target status and drawing the frame: %s", per)
	if per > 16*time.Millisecond {
		t.Errorf("cycling a target status took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_MoveFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkMoveRedraw200x60)
	per := time.Duration(res.NsPerOp())
	t.Logf("a full redraw at 200x60: %s", per)
	if per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_MoveRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := stocked(t, 1000, 120, 30)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}
