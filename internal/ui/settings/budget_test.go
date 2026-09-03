//go:build !race

package settings

import (
	"testing"
	"time"
)

// The budgets in docs/PERFORMANCE.md are about the binary that ships, so a budget
// test is built without the race detector and CI runs it in the lane that has
// none. The tag on its own would mean it never ran anywhere; the lane is what
// makes it a gate.
//
// None of these may be parallel. An allocation count comes from process-wide
// MemStats, so a benchmark run beside another test is handed that test's
// allocations divided by its own iteration count.

// settingsFrameAllocs is the ceiling one steady-state frame is held to, with
// about a tenth of headroom over what the machine measures.
const settingsFrameAllocs = 67

func TestBudget_SettingsRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	res := testing.Benchmark(BenchmarkSettingsRedraw)
	if got := res.AllocsPerOp(); got > settingsFrameAllocs {
		t.Errorf("a steady-state redraw allocates %d times, want at most %d: the row cache is meant "+
			"to carry a hit's shape and options along with its rendered strings rather than "+
			"re-deriving them (docs/PERFORMANCE.md)", got, settingsFrameAllocs)
	}
	t.Logf("a steady-state redraw: %d allocs a frame", res.AllocsPerOp())
}

func TestBudget_SettingsKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkSettingsMoveCursor)
	per := time.Duration(res.NsPerOp())
	t.Logf("moving the cursor and drawing the frame: %s", per)
	if per > 16*time.Millisecond {
		t.Errorf("moving the cursor took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
	if got := res.AllocsPerOp(); got > settingsFrameAllocs+2 {
		t.Errorf("a keystroke and its frame allocate %d times, want at most %d: two rows' cache "+
			"misses plus the steady-state frame (docs/PERFORMANCE.md)", got, settingsFrameAllocs+2)
	}
}

// settingsRowRenderAllocs is one row's own cost with nothing memoized, the
// ceiling BenchmarkSettingsRowRender is held to.
const settingsRowRenderAllocs = 34

func TestBudget_SettingsRowRenderCostsWhatAMemoMissPays(t *testing.T) {
	res := testing.Benchmark(BenchmarkSettingsRowRender)
	if got := res.AllocsPerOp(); got > settingsRowRenderAllocs {
		t.Errorf("rendering one row with nothing memoized allocates %d times, want at most %d",
			got, settingsRowRenderAllocs)
	}
	t.Logf("one row rendered from scratch: %d allocs", res.AllocsPerOp())
}
