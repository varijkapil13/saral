//go:build !race

package palette

import (
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/app"
)

// ctrl+k builds the palette from scratch and every keystroke re-ranks both
// halves of the list, so all three of these are on the 16ms keystroke budget in
// docs/PERFORMANCE.md rather than on a start-up one.
//
// The race detector puts about twenty times the cost on these paths, so the tag
// is what keeps the numbers the binary's; ci.yml's budgets job is what keeps the
// tag from meaning they run nowhere. None may be parallel: an allocation count
// comes from process-wide MemStats, and a benchmark run beside another test is
// handed that test's allocations divided by its own iteration count.

func TestBudget_PaletteKeystrokeOverTwoThousandCommands(t *testing.T) {
	res := testing.Benchmark(BenchmarkPaletteKeystroke2000)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("a keystroke into the filter took %s over 2000 commands, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

// The cache half of the palette is on the same budget as the command half, and
// it is the half that grows with what the session has read.
func TestBudget_PaletteKeystrokeOverEveryCachedIssue(t *testing.T) {
	res := testing.Benchmark(BenchmarkPaletteKeystrokeCached)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("a keystroke over %d cached issues took %s, want under the 16ms in docs/PERFORMANCE.md",
			app.DefaultIssueBound, per)
	}
}

func TestBudget_PaletteOpeningIsOnTheKeystrokeBudget(t *testing.T) {
	res := testing.Benchmark(BenchmarkPaletteOpen64)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("building the palette over 64 commands took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

// The picker is a list that ranks on every keystroke like the palette itself, so
// it is on the same budget.
func TestBudget_ProjectPickerKeystroke(t *testing.T) {
	res := testing.Benchmark(BenchmarkProjectKeystroke)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("a keystroke into the project picker took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

// The list is virtualized, so a registry of two thousand commands costs a frame
// what one of twenty costs. The absolute ceiling is the half that matters: a
// comparison of two benchmarks passes just as happily at nine hundred
// allocations a frame.
//
// 29 on an M2 Pro at either length, every run. The palette is not memoized down
// to one allocation a frame the way a list view is — it rebuilds the input line,
// the group heads and the rows it shows on every keystroke, because a keystroke
// re-ranks the whole list and there is nothing stable to key a memo on.
func TestBudget_PaletteScrollingCostsTheSameOnTwoThousandCommandsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkPaletteScroll2000)
	small := testing.Benchmark(BenchmarkPaletteScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a scrolled frame: %d allocations over two thousand commands, %d over twenty, ceiling 34", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 2000-command palette allocates %d per frame against %d for a 20-command one; "+
			"the drawing is not virtualized", bigAllocs, smallAllocs)
	}
	if bigAllocs > 34 {
		t.Errorf("a scrolled frame allocates %d times, over the ceiling of 34; it measured 29 when the "+
			"ceiling was set, and a window drawn over the whole registry would be two orders of "+
			"magnitude more", bigAllocs)
	}
}

// Standing still costs one allocation less than scrolling: the frame is drawn
// again and no cursor moved to make the row under it change.
func TestBudget_PaletteStandingStillCostsNoMoreThanScrolling(t *testing.T) {
	long := testing.Benchmark(BenchmarkPaletteRedraw2000)
	short := testing.Benchmark(BenchmarkPaletteRedraw20)

	longAllocs, shortAllocs := long.AllocsPerOp(), short.AllocsPerOp()
	t.Logf("a redrawn frame: %d allocations over two thousand commands, %d over twenty, ceiling 34", longAllocs, shortAllocs)
	if longAllocs > shortAllocs {
		t.Errorf("2000 commands allocate %d per frame against %d for twenty; the drawing is not virtualized",
			longAllocs, shortAllocs)
	}
	if longAllocs > 34 {
		t.Errorf("a redrawn frame allocates %d times, over the ceiling of 34; it measured 28 when the "+
			"ceiling was set", longAllocs)
	}
}

func TestBudget_PaletteRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := opened(t, 2000, 120, 40)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}
