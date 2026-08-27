//go:build !race

package timeline

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

// The chart is virtualized vertically, so ten thousand bars cost what twenty
// cost per frame.
func TestBudget_TimelineScrollingCostsTheSameOnTenThousandBarsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkTimelineSteadyScroll10k)
	small := testing.Benchmark(BenchmarkTimelineSteadyScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a steady frame: %d allocations over ten thousand bars, %d over twenty", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 10k-bar chart allocates %d per frame against %d for a 20-bar one; the render is not virtualized vertically",
			bigAllocs, smallAllocs)
	}
	// The one allocation left is the frame string itself, which View has to
	// return; everything behind it is memoized.
	if bigAllocs > 1 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself", bigAllocs)
	}
}

// And horizontally, which is the half only this view has. A pan moves every bar,
// so it repaints the window whatever happens; what must not grow with it is the
// calendar behind the window.
//
// 1491 allocations, 64KB and 279us over ten years on an M2 Pro; 1433, 60KB and
// 264us over a thousand. All three are compared, because each catches a
// different way of getting this wrong: the count catches a repaint sized by the
// chart, the bytes catch a buffer sized by the span, and the time catches a walk
// over the span that allocates nothing. A render that built the whole calendar
// and took a screenful out of it measured 2.3MB and 1.08ms over the thousand
// years against 85KB and 274us over the ten.
func TestBudget_TimelinePanningCostsTheSameOverAThousandYearsAsOverTen(t *testing.T) {
	ten := testing.Benchmark(BenchmarkTimelinePanADecade)
	thousand := testing.Benchmark(BenchmarkTimelinePanAMillennium)

	over, under := thousand.AllocsPerOp(), ten.AllocsPerOp()
	t.Logf("a pan: %d allocations over a thousand years of calendar, %d over ten, ceiling 1700", over, under)
	if over > under+64 {
		t.Errorf("panning a thousand years allocates %d per frame against %d for ten; the repaint is sized "+
			"by the calendar rather than by the chart", over, under)
	}
	if over > 1700 || under > 1700 {
		t.Errorf("panning allocates %d and %d per frame, over the ceiling of 1700; a pan repaints the "+
			"window and they measured 1433 and 1491 when the ceiling was set", over, under)
	}

	overBytes, underBytes := thousand.AllocedBytesPerOp(), ten.AllocedBytesPerOp()
	t.Logf("a pan: %d bytes over a thousand years of calendar, %d over ten", overBytes, underBytes)
	if overBytes > underBytes+underBytes/2 {
		t.Errorf("panning a thousand years allocates %d bytes a frame against %d for ten; something in the "+
			"render is sized by the calendar rather than by the chart", overBytes, underBytes)
	}

	slow, quick := time.Duration(thousand.NsPerOp()), time.Duration(ten.NsPerOp())
	t.Logf("a pan: %s over a thousand years of calendar, %s over ten", slow, quick)
	if slow > 2*quick {
		t.Errorf("panning a thousand years of calendar took %s a frame against %s for ten; a walk over the "+
			"whole span can cost no allocations at all, so this is the half that catches it", slow, quick)
	}
	if slow > 16*time.Millisecond {
		t.Errorf("panning took %s a frame, want under the 16ms in docs/PERFORMANCE.md", slow)
	}
}

// A zoom drops the memo and repaints, and what is budgeted is that it repaints
// the window rather than the chart: ten thousand bars cost what two hundred do.
//
// 1758 on an M2 Pro for both, every run of each.
func TestBudget_TimelineAZoomRepaintsAWindowAndNotTheWholeChart(t *testing.T) {
	big := testing.Benchmark(BenchmarkTimelineZoom10k)
	small := testing.Benchmark(BenchmarkTimelineZoom200)

	over, under := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a zoom: %d allocations over ten thousand bars, %d over two hundred, ceiling 2000", over, under)
	if over > under+16 {
		t.Errorf("zooming a 10k-bar chart allocates %d against %d for a 200-bar one; the repaint is sized "+
			"by the chart rather than by the window", over, under)
	}
	if over > 2000 {
		t.Errorf("a zoom allocates %d times, over the ceiling of 2000; it measured 1758 when the ceiling was set", over)
	}
}

// Walking a fresh bar into view on every frame misses the memo by construction,
// which is what says the miss itself is bounded: the row the cursor left, the row
// it arrived on and the row a scroll brought in, and not a window.
//
// 122 on an M2 Pro, every run.
func TestBudget_TimelineAMemoMissCostsThreeRowsAndNotAWindow(t *testing.T) {
	got := testing.Benchmark(BenchmarkTimelineWalk10k).AllocsPerOp()
	t.Logf("a frame that renders the rows a keypress moved: %d allocations, ceiling 135", got)
	if got > 135 {
		t.Errorf("a frame that renders the rows a keypress moved allocates %d times, over the ceiling of "+
			"135; it measured 122 when the ceiling was set, and a window of forty rows would be an order "+
			"of magnitude more", got)
	}
}

func TestBudget_TimelineKeystrokeToFrameAtTenThousandBars(t *testing.T) {
	res := testing.Benchmark(BenchmarkTimelineWalk10k)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s at 10k bars, want under the 16ms in docs/PERFORMANCE.md", per)
	}
	zoom := testing.Benchmark(BenchmarkTimelineZoom10k)
	if per := time.Duration(zoom.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("a zoom took %s at 10k bars, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_TimelineFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkTimelineRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_TimelineRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := stocked(t, 10000, 3650, 120, 40)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0, false) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
	if got := testing.AllocsPerRun(200, func() { _ = m.headingLine(); _ = m.rulerLine() }); got != 0 {
		t.Errorf("the two lines above the chart allocate %.1f times a frame, want none; they change on a "+
			"pan, a zoom and a resize and on nothing else", got)
	}
}
