//go:build !race

package app

import (
	"testing"
	"time"
)

// The budgets in docs/PERFORMANCE.md are about the binary that ships. The race
// detector puts about twenty times the cost on a pass of this shape, so
// asserting them under -race would measure the instrumentation instead, and the
// tag on its own would mean they never ran anywhere — ci.yml has a lane without
// the detector that runs exactly these.
//
// Neither may be parallel. An allocation count comes from process-wide MemStats,
// so a benchmark run beside another test is handed that test's allocations
// divided by its own iteration count.

// A page landing is when the cascade runs, and the frame it lands in is the one
// it has to fit inside: a view that stops for longer than a frame while it works
// out where its bars go is a view that stutters when the data arrives.
//
// 1.7 ms and 2,300 allocations for two thousand issues on an M2 Pro, every run.
// The allocation count is about one an issue — the string that says where its
// dates came from — and the ceiling is a tenth above what it measures, which is
// room for a compiler release and not room for a per-field allocation to hide
// in.
func TestBudget_DateCascadeOverATimelineOfIssues(t *testing.T) {
	res := testing.Benchmark(BenchmarkResolveDates2k)
	t.Logf("resolving two thousand issues: %s and %d allocations, ceilings 16ms and 2600",
		time.Duration(res.NsPerOp()), res.AllocsPerOp())

	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("resolving a timeline's worth of issues took %s, want under the 16ms frame budget in docs/PERFORMANCE.md", per)
	}
	if got := res.AllocsPerOp(); got > 2600 {
		t.Errorf("the pass allocates %d times over two thousand issues, over the ceiling of 2600; it measured "+
			"2300 when the ceiling was set, which is the one string an issue that says where its dates came from", got)
	}
}

// The rollup walks a parent's children and a sprint is read once for however
// many issues are in it, and both of those are the kind of thing that turns into
// a walk per issue without anything looking wrong. Five times the issues may
// cost more than five times the time — bigger maps, more parents — but nothing
// like the twenty-five a pass that rescanned its own set would cost.
func TestBudget_DateCascadeCostsNoMoreThanTheIssuesItIsGiven(t *testing.T) {
	small := testing.Benchmark(BenchmarkResolveDates2k)
	big := testing.Benchmark(BenchmarkResolveDates10k)
	if small.NsPerOp() <= 0 {
		t.Fatalf("the two-thousand-issue pass reported %d ns/op, so there is nothing to compare against", small.NsPerOp())
	}

	ratio := float64(big.NsPerOp()) / float64(small.NsPerOp())
	t.Logf("five times the issues cost %.1f times the time (%s against %s), ceiling 8",
		ratio, time.Duration(big.NsPerOp()), time.Duration(small.NsPerOp()))
	if ratio > 8 {
		t.Errorf("five times the issues cost %.1f times the time; the pass is not linear in the issues it is given, "+
			"and a rollup or a sprint read that walks the whole set again would cost twenty-five", ratio)
	}
}
