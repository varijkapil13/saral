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
