//go:build !race

package kernel

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
// allocations divided by its own iteration count — a scroll that costs one
// allocation reads as hundreds. These also register a view, which is a global.

// The ceilings are absolute, about a tenth above what an M2 Pro measures at
// go1.27. The counts are exact rather than noisy — five runs of each produced
// the same number every time — so a tenth is room for a compiler release to move
// one, not room for a memo to stop being hit.
func TestBudget_AFrameCostsWhatTheChromeCosts(t *testing.T) {
	for _, tc := range []struct {
		what     string
		bench    func(*testing.B)
		measured int64
		ceiling  int64
	}{
		{"a frame at 200x60", BenchmarkFrame, 297, 330},
		{"a frame whose header names a project", BenchmarkFrameScopedToAProject, 297, 330},
		{"a keystroke and the frame it produces", BenchmarkKeyToFrame, 310, 345},
		{"a frame with the mouse on", BenchmarkFrameMouseOn, 324, 360},
		// None of the three overlays is a steady state — nothing repaints one until
		// the next key — but each is a frame, and each has a benchmark, so the
		// figures docs/PERFORMANCE.md quotes are ones this table checks.
		{"a frame with the help overlay up", BenchmarkFrameWithTheHelpOverlayUp, 629, 700},
		{"a frame with the right-click menu open", BenchmarkFrameWithTheMenuOpen, 800, 880},
		{"a frame with the destinations up", BenchmarkFrameWithTheDestinationsUp, 1120, 1240},
	} {
		got := testing.Benchmark(tc.bench).AllocsPerOp()
		t.Logf("%s: %d allocations, ceiling %d", tc.what, got, tc.ceiling)
		if got > tc.ceiling {
			t.Errorf("%s allocates %d times, over the ceiling of %d in docs/PERFORMANCE.md; "+
				"it measured %d when the ceiling was set", tc.what, got, tc.ceiling, tc.measured)
		}
	}
}

func TestBudget_KeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkKeyToFrame)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

func TestBudget_FullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkFrame)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
}
