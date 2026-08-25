//go:build !race

package richtext

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// The budgets here are about the binary that ships. The race detector puts its
// own allocations on every one of these paths, so asserting them under -race
// would measure the instrumentation instead — the same reason
// internal/app/index_budget_test.go is built the same way.

// TestBudget_Render holds the allocation cost of a render to a ceiling. A pane
// memoizes this, so it is not a per-frame cost — but a resize re-renders every
// document on screen.
//
// The ceilings are per line rather than per document, because the lines are the
// answer the caller keeps and the floor is therefore one allocation each. What
// they hold is roughly a third above what this machine measures.
func TestBudget_Render(t *testing.T) {
	t.Parallel()
	d := load(t, "kitchen.json")
	for _, tc := range []struct {
		width   int
		colour  bool
		perLine float64
	}{
		{120, false, 6.0},
		{80, false, 6.0},
		{80, true, 8.0},
		{40, true, 8.0},
	} {
		opt := options(tc.width)
		if tc.colour {
			opt.Styles = NewStyles(colourPalette())
		}
		lines := len(Render(d, opt).Lines)
		got := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Render(d, opt)
			}
		})
		allocs := float64(got.AllocsPerOp())
		if want := float64(lines) * tc.perLine; allocs > want {
			t.Errorf("at width %d (colour=%v) a render costs %.0f allocations over %d lines, "+
				"which is more than %.1f a line; %s",
				tc.width, tc.colour, allocs, lines, tc.perLine, got.MemString())
		}
	}
}

// TestBudget_Summary is the one a list row pays, once per visible row.
func TestBudget_Summary(t *testing.T) {
	t.Parallel()
	d := load(t, "kitchen.json")
	got := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			Summary(d, 80)
		}
	})
	if allocs := got.AllocsPerOp(); allocs > 8 {
		t.Errorf("a summary costs %d allocations: %s", allocs, got.MemString())
	}
}

// TestBudget_ScalesWithTheDocument catches the quadratic mistakes: a wider
// document must not cost more per line than a narrow one.
func TestBudget_ScalesWithTheDocument(t *testing.T) {
	t.Parallel()
	opt := options(80)
	cost := func(d adf.Doc) (float64, int) {
		lines := len(Render(d, opt).Lines)
		got := testing.Benchmark(func(b *testing.B) {
			for b.Loop() {
				Render(d, opt)
			}
		})
		return float64(got.AllocsPerOp()) / float64(lines), lines
	}
	one, oneLines := cost(load(t, "kitchen.json"))
	five, fiveLines := cost(long(t, 5))
	if five > one*1.5 {
		t.Errorf("%d lines cost %.2f allocations each against %.2f over %d lines, so the cost is not linear",
			fiveLines, five, one, oneLines)
	}
}
