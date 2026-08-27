//go:build !race

package form

import (
	"testing"
	"time"
)

// A create screen carries whatever custom fields a long-lived project has
// accumulated, so both budgets in docs/PERFORMANCE.md are held at 200 fields
// rather than at the eight a small project shows.
//
// The race detector puts about twenty times the cost on these paths, so the tag
// is what keeps the numbers the binary's; ci.yml's budgets job is what keeps the
// tag from meaning they run nowhere. Neither may be parallel: an allocation
// count comes from process-wide MemStats, and a benchmark run beside another
// test is handed that test's allocations divided by its own iteration count.

func TestBudget_FormKeystrokeToFrameOnALongScreen(t *testing.T) {
	res := testing.Benchmark(BenchmarkFormWalk200)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s on a 200-field screen, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_FormFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkFormRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}

// The screen is virtualized, so a project with two hundred custom fields costs a
// frame what one with eight costs. The absolute half is what matters: comparing
// two benchmarks against each other passes just as happily at nine hundred
// allocations a frame.
//
// 1 at either length, every run: the frame string View has to return, with every
// field behind it memoized.
func TestBudget_FormScrollingCostsTheSameOnTwoHundredFieldsAsOnEight(t *testing.T) {
	big := testing.Benchmark(BenchmarkFormSteadyScroll200)
	small := testing.Benchmark(BenchmarkFormSteadyScroll8)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a steady frame: %d allocations over two hundred fields, %d over eight, ceiling 1", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 200-field form allocates %d per frame against %d for an 8-field one; "+
			"the render is not virtualized", bigAllocs, smallAllocs)
	}
	if bigAllocs > 1 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself",
			bigAllocs)
	}
}

func TestBudget_FormFieldsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := built(t, 200, 120, 40)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}
