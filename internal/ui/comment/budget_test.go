//go:build !race

package comment

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
// MemStats, so a benchmark run beside sixty other tests is handed their
// allocations divided by its own iteration count.

func TestBudget_ScrollingCostsTheSameOnTenThousandCommentsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkThreadSteadyScroll10k)
	small := testing.Benchmark(BenchmarkThreadSteadyScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	if bigAllocs > smallAllocs {
		t.Errorf("a 10k-comment thread allocates %d per frame against %d for a 20-comment one; the render is not virtualized",
			bigAllocs, smallAllocs)
	}
	// What is left is the frame string View has to return; every comment behind it
	// is memoized.
	if bigAllocs > 2 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself", bigAllocs)
	}
}

func TestBudget_KeystrokeToFrameOnATenThousandCommentThread(t *testing.T) {
	res := testing.Benchmark(BenchmarkThreadWalk10k)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s walking fresh comments into view, "+
			"want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_FullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkThreadRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}
