//go:build !race

package release

import (
	"testing"
	"time"
)

func TestBudget_ReleaseSetScrollingCostsTheSameOnTwoThousandVersionsAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkReleaseSetSteadyScroll2000)
	small := testing.Benchmark(BenchmarkReleaseSetSteadyScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	t.Logf("a steady frame: %d allocations over 2000 versions, %d over 20", bigAllocs, smallAllocs)
	if bigAllocs > smallAllocs {
		t.Errorf("a 2000-version set allocates %d per frame against %d for a 20-version one; "+
			"the render is not virtualized", bigAllocs, smallAllocs)
	}
	if bigAllocs > 2 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the "+
			"frame itself and the keystroke", bigAllocs)
	}
}

func TestBudget_ReleaseSetHeadersAreMemoized(t *testing.T) {
	m := stockedSet(t, 400, 40, 120, 30)
	header := -1
	for at, sl := range m.order {
		if sl.v < 0 {
			header = at
			break
		}
	}
	if header < 0 {
		t.Fatal("the set drew no header")
	}
	_ = m.row(header, false)

	if got := testing.AllocsPerRun(200, func() { _ = m.row(header, false) }); got != 0 {
		t.Errorf("a memoized header allocates %.1f times, want none", got)
	}
}

func TestBudget_ReleaseSetTypingAFilterKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkReleaseSetFindKeystroke)
	per := time.Duration(res.NsPerOp())
	t.Logf("a letter typed into the filter to frame over 2000 versions in 200 groups: %s", per)
	if per > 16*time.Millisecond {
		t.Errorf("a filter keystroke took %s over two thousand versions, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_ReleaseSetRegroupKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkReleaseSetRegroupKeystroke)
	per := time.Duration(res.NsPerOp())
	t.Logf("v to frame over 2000 versions in 200 groups: %s", per)
	if per > 16*time.Millisecond {
		t.Errorf("v to frame took %s over two thousand versions, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_ReleaseSetFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkReleaseSetRedraw200x60)
	per := time.Duration(res.NsPerOp())
	t.Logf("a full redraw at 200x60: %s", per)
	if per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}
