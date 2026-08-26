//go:build !race

package list

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
// allocations divided by its own iteration count. That is what made these four
// fail on a runner and pass on a laptop: the detector slows a frame down twenty
// times, the benchmark reaches a twelfth of the iterations in its second, and
// the neighbours' allocations divided by that are what the assertion read.

func TestBudget_ScrollingCostsTheSameOnTenThousandRowsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkListSteadyScroll10k)
	small := testing.Benchmark(BenchmarkListSteadyScroll20)

	bigAllocs := big.AllocsPerOp()
	smallAllocs := small.AllocsPerOp()
	if bigAllocs > smallAllocs {
		t.Errorf("a 10k-row list allocates %d per frame against %d for a 20-row list; the render is not virtualized",
			bigAllocs, smallAllocs)
	}
	// The one allocation left is the frame string itself, which View has to
	// return; everything behind it is memoized.
	if bigAllocs > 1 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself", bigAllocs)
	}
}

// The clickable cells are the reason to check this twice: a marked row is a
// longer string built out of more pieces, and if the marks were applied outside
// the memo the whole window would be rebuilt on every frame.
func TestBudget_ScrollingCostsTheSameWithTheMouseOn(t *testing.T) {
	if got := testing.Benchmark(BenchmarkListSteadyScrollMarked10k).AllocsPerOp(); got > 1 {
		t.Errorf("a steady-state frame with the mouse on allocates %d times, want the memo to carry all but the frame itself", got)
	}
}

// The line that names the terms is on every frame they are in force, so it is
// memoized the way the summary is. Held two ways: against a scroll with nothing
// under the rows, which catches the line being rebuilt per frame whatever a
// frame costs, and against the frame string, which is all a frame may cost.
func TestBudget_ScrollingCostsTheSameUnderTermsInForce(t *testing.T) {
	termed := testing.Benchmark(BenchmarkListSteadyScrollTermed10k)
	plain := testing.Benchmark(BenchmarkListSteadyScroll10k)
	if termed.AllocsPerOp() > plain.AllocsPerOp() {
		t.Errorf("a steady-state frame under two terms allocates %d times against %d with no line under the rows; "+
			"the line naming them is being rebuilt per frame", termed.AllocsPerOp(), plain.AllocsPerOp())
	}
	if got := termed.AllocsPerOp(); got > 1 {
		t.Errorf("a steady-state frame under two terms allocates %d times, want the frame string and nothing else", got)
	}
}

// The line that names an accepted filter is on every frame it is on, so it is
// memoized the way the summary is.
func TestBudget_ScrollingCostsTheSameUnderAFilterThatHasBeenAccepted(t *testing.T) {
	if got := testing.Benchmark(BenchmarkListSteadyScrollFiltered10k).AllocsPerOp(); got > 1 {
		t.Errorf("a steady-state frame under a kept filter allocates %d times, want the frame string and nothing else", got)
	}
}

// Walking a fresh row into view on every frame misses the memo by construction,
// so this is the one that says the miss itself is cheap: a row is built into a
// buffer the view reuses, and the frame string is what is left.
func TestBudget_WalkingIntoFreshRowsCostsTheFrameAndNothingElse(t *testing.T) {
	if got := testing.Benchmark(BenchmarkListWalk10k).AllocsPerOp(); got > 1 {
		t.Errorf("a frame that renders a row it has never rendered allocates %d times, want the frame string alone", got)
	}
}

func TestBudget_RowRenderingCostsNothingOnceMemoized(t *testing.T) {
	m := loaded(t, 10000, 120, 40)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0, false) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}

func TestBudget_KeystrokeToFrameAtTenThousandRows(t *testing.T) {
	res := testing.Benchmark(BenchmarkListWalk10k)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s at 10k rows, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_FullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkListRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

// This is the half of the cold-start budget a Go test can hold: building the
// view, sizing it and drawing the first frame out of what is on disk. The other
// half is process start, which only the binary can be asked about — ci.yml times
// `saral --bench-first-paint` for that.
func TestBudget_FirstPaintFromCache(t *testing.T) {
	res := testing.Benchmark(BenchmarkFirstPaintFromCache)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("painting the first frame from cache took %s, want it to fit inside a frame; "+
			"the whole start-up budget it sits under is 60ms", per)
	}
}
