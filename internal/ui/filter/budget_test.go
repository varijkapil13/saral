//go:build !race

package filter

import (
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/app"
)

// The budgets in docs/PERFORMANCE.md are about the binary that ships, so a
// budget test is built without the race detector and CI runs it in the lane that
// has none. The tag on its own would mean it never ran anywhere; the lane is
// what makes it a gate.
//
// None of these may be parallel. An allocation count comes from process-wide
// MemStats, so a benchmark run beside another test is handed that test's
// allocations divided by its own iteration count.

// The picker is virtualized, so a vocabulary of two thousand costs what one of
// twenty costs per frame.
func TestBudget_PickerScrollingCostsTheSameOnTwoThousandRowsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(func(b *testing.B) { scrollOver(b, 2000) })
	small := testing.Benchmark(func(b *testing.B) { scrollOver(b, 20) })
	if big.AllocsPerOp() > small.AllocsPerOp() {
		t.Errorf("a 2000-row picker allocates %d per frame against %d for a 20-row one; the render is not virtualized",
			big.AllocsPerOp(), small.AllocsPerOp())
	}
}

func TestBudget_PickerKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkPickerKeystroke)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s over two thousand values, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_PickerFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkPickerRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

// Ranking is the work a keystroke does over everything held, and it may not
// allocate per candidate: both buffers are reused and app.Pattern folds case
// without copying either side.
func TestBudget_RankingReusesItsBuffers(t *testing.T) {
	all := labelValues(manyLabels(2000))
	pattern := app.NewPattern("serv")
	shown, ranks := make([]int, 0, len(all)), make([]ranked, 0, len(all))
	shown, ranks = rank(all, pattern, shown, ranks)
	if got := testing.AllocsPerRun(50, func() {
		shown, ranks = rank(all, pattern, shown[:0], ranks[:0])
	}); got != 0 {
		t.Errorf("ranking two thousand values allocates %.1f times, want none", got)
	}
}

func TestBudget_PickerRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	dr := newDriver(t, testDeps(newFake(40)), 120, 30)
	dr.pick(FacetLabel)
	_ = dr.m.View()

	if got := testing.AllocsPerRun(200, func() { _ = dr.m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}
