//go:build !race

package app

import (
	"testing"
	"time"
)

// The budget in docs/PERFORMANCE.md is about the binary that ships. The race
// detector puts around twenty times the cost on a decode-heavy path, so
// asserting the budget under -race would measure the instrumentation instead.
func TestCacheRead_StaysUnderTheFirstPaintBudget(t *testing.T) {
	res := testing.Benchmark(BenchmarkCacheReadFirstPaint)
	if per := time.Duration(res.NsPerOp()); per > 5*time.Millisecond {
		t.Errorf("reading a screen of rows off disk took %s, want under the 5ms in docs/PERFORMANCE.md", per)
	}
}
