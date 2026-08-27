//go:build !race

package sprint

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

// The list is virtualized, so a board with two thousand sprints behind it costs
// what one with twenty costs per frame.
func TestBudget_SprintScrollingCostsTheSameOnTwoThousandSprintsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(func(b *testing.B) { scrollOver(b, 2000) })
	small := testing.Benchmark(func(b *testing.B) { scrollOver(b, 20) })

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a steady frame allocates %d over two thousand sprints and %d over twenty", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 2000-sprint list allocates %d per frame against %d for a 20-sprint one; the render is not virtualized",
			bigAllocs, smallAllocs)
	}
	// The allocation left is the frame string itself, which View has to return;
	// the rows and the chrome above them are memoized. The ceiling is absolute
	// because a comparison alone passes just as happily at nine hundred.
	if bigAllocs > 2 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself", bigAllocs)
	}
}

func TestBudget_SprintsKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkSprintsWalk)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s over two thousand sprints, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_SprintsFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkSprintsRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_SprintRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := stocked(t, 200, 120, 30)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}
