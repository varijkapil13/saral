//go:build !race

package attach

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

// The list is virtualized, so two thousand files cost what twenty cost per frame.
//
// The absolute ceiling is the half that matters: a comparison of two benchmarks
// passes just as happily at nine hundred allocations a frame. What is left after
// the memos is the frame string, the caption and the preview's own sentence —
// both of which name the file under the cursor, so both are rebuilt by the very
// keystroke this measures. 32 on an M2 Pro, every run.
func TestBudget_AttachScrollingCostsTheSameOnTwoThousandFilesAsOnTwenty(t *testing.T) {
	big := testing.Benchmark(BenchmarkPaneSteadyScroll2000)
	small := testing.Benchmark(BenchmarkPaneSteadyScroll20)

	bigAllocs := big.AllocsPerOp()
	if smallAllocs := small.AllocsPerOp(); bigAllocs > smallAllocs {
		t.Errorf("a 2000-file pane allocates %d per frame against %d for a 20-file one; "+
			"the render is not virtualized", bigAllocs, smallAllocs)
	}
	t.Logf("a steady scrolled frame: %d allocations, ceiling 40", bigAllocs)
	if bigAllocs > 40 {
		t.Errorf("a steady-state frame allocates %d times, over the ceiling of 40; it measured 32 "+
			"when the ceiling was set, and the rows are meant to come out of the memo", bigAllocs)
	}
}

func TestBudget_AttachKeystrokeToFrame(t *testing.T) {
	res := testing.Benchmark(BenchmarkPaneKeystroke)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s over two thousand files, want under the 16ms in "+
			"docs/PERFORMANCE.md", per)
	}
}

// A frame nothing has changed costs the string View has to return and nothing
// behind it: the rows, the head, the caption and the preview region are all
// memoized on keys that have not moved.
func TestBudget_AttachFullRedrawAt200x60(t *testing.T) {
	res := testing.Benchmark(BenchmarkPaneRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under the 4ms in docs/PERFORMANCE.md", per)
	}
	if got := res.AllocsPerOp(); got > 1 {
		t.Errorf("standing still costs %d allocations a frame, want the frame string and nothing else", got)
	}
}

func TestBudget_AttachRowsAreMemoizedSoAFrameCostsNothingToRedraw(t *testing.T) {
	m := stocked(t, 2000, 120, 40)
	_ = m.row(0)

	if got := testing.AllocsPerRun(200, func() { _ = m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}

// Building the key for a row may not allocate either. Formatting the size and the
// date to look one up costs two allocations a row on every frame, which is more
// than the whole of the rest of a memoized frame.
func TestBudget_AttachAMemoLookupCostsNothing(t *testing.T) {
	m := stocked(t, 2000, 120, 40)
	att := &m.files[0]
	if got := testing.AllocsPerRun(200, func() {
		_ = rowKey{
			id: att.ID, name: att.Filename, who: att.Author.DisplayName,
			size: att.Size, created: att.Created.Unix(), loc: m.deps.Caps.Location(),
			lay: m.lay, selected: false, gen: m.styles.gen,
		}
	}); got != 0 {
		t.Errorf("building a row's memo key allocates %.1f times, want none", got)
	}
}
