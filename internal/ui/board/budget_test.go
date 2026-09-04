//go:build !race

package board

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

// The grid is virtualized down both axes, so a board of ten thousand cards costs
// per frame what a board of twenty costs. The absolute half is what matters: a
// guard that only compares two numbers passes just as happily at nine hundred
// allocations a frame.
func TestBudget_BoardScrollCostsTheFrameAndNothingElse(t *testing.T) {
	big := testing.Benchmark(BenchmarkBoardView10k)
	small := testing.Benchmark(BenchmarkBoardView20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	if bigAllocs > smallAllocs {
		t.Errorf("a 10k-card board allocates %d per frame against %d for a 20-card one; the grid is not virtualized",
			bigAllocs, smallAllocs)
	}
	// The one allocation left is the frame string itself, which View has to
	// return; every line and every card behind it is memoized.
	if bigAllocs > 1 {
		t.Errorf("a steady-state frame allocates %d times, want the memos to carry all but the frame itself", bigAllocs)
	}
}

// The bar under the grid is on every frame it is on, so it is memoized the way
// the summary line is: a term in force costs no more than a steady-state frame
// without one.
func TestBudget_BoardScrollingCostsTheSameUnderATermInForce(t *testing.T) {
	termed := testing.Benchmark(BenchmarkBoardView10kTermed).AllocsPerOp()
	plain := testing.Benchmark(BenchmarkBoardView10k).AllocsPerOp()
	if termed > plain {
		t.Errorf("a steady-state frame under a term allocates %d times against %d with nothing in force; "+
			"the bar under the grid is being rebuilt per frame", termed, plain)
	}
	if termed > 1 {
		t.Errorf("a steady-state frame under a term allocates %d times, want the frame string and nothing else", termed)
	}
}

// The columns are virtualized as well as the rows, which is the axis a list view
// does not have: a board of fifty columns draws the handful that fit.
func TestBudget_BoardColumnsAreVirtualizedAsWellAsItsRows(t *testing.T) {
	wide := testing.Benchmark(BenchmarkBoardWideView).AllocsPerOp()
	narrow := testing.Benchmark(BenchmarkBoardView10k).AllocsPerOp()
	t.Logf("a frame of a 50-column board: %d allocations, against %d for a 4-column one", wide, narrow)
	if wide > narrow {
		t.Errorf("a 50-column board allocates %d per frame against %d for a 4-column one; the columns are not virtualized",
			wide, narrow)
	}
}

// Walking a fresh card into view on every frame misses both memos by
// construction, which is what says the miss itself is bounded: two lines are
// rebuilt and two cards with them, not a screen of either.
//
// 93 on an M2 Pro, every run: the ceiling moved from 72 when a card's key
// picked up the status category colour a column caption already carries —
// one more Style.Render per resting card, the same call list.go's own status
// cell already makes and already budgets for.
func TestBudget_ABoardMemoMissCostsTwoLinesAndNotAScreen(t *testing.T) {
	got := testing.Benchmark(BenchmarkBoardWalk10k).AllocsPerOp()
	t.Logf("a frame that moves the cursor one card: %d allocations, ceiling 105", got)
	if got > 105 {
		t.Errorf("moving the cursor one card allocates %d times, over the ceiling of 105; it measured 93 "+
			"when the ceiling was set, and a screen of thirty-six lines would be an order of magnitude more", got)
	}
}

func TestBudget_BoardKeystrokeToFrame(t *testing.T) {
	for name, res := range map[string]testing.BenchmarkResult{
		"down a column":      testing.Benchmark(BenchmarkBoardWalk10k),
		"across the columns": testing.Benchmark(BenchmarkBoardAcross),
	} {
		if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
			t.Errorf("keystroke to frame %s took %s over ten thousand cards, want under the 16ms in docs/PERFORMANCE.md",
				name, per)
		}
	}
}

func TestBudget_BoardFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkBoardRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

// A card already drawn costs nothing to draw again, which is what the memo is
// for and the thing that stops a wide board paying per cell per frame.
func TestBudget_BoardCardsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := marked(t, 4, 2000, 120, 40)
	if got := testing.AllocsPerRun(200, func() { _ = m.cell(0, 0) }); got != 0 {
		t.Errorf("a memoized card allocates %.1f times, want none", got)
	}
	if got := testing.AllocsPerRun(200, func() { _ = m.line(0) }); got != 0 {
		t.Errorf("a memoized grid line allocates %.1f times, want none", got)
	}
}
