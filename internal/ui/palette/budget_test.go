//go:build !race

package palette

import (
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/app"
)

// ctrl+k builds the palette from scratch and every keystroke re-ranks both
// halves of the list, so all three of these are on the 16ms keystroke budget in
// docs/PERFORMANCE.md rather than on a start-up one.
//
// The race detector puts about twenty times the cost on these paths, so the tag
// is what keeps the numbers the binary's; ci.yml's budgets job is what keeps the
// tag from meaning they run nowhere. None may be parallel: an allocation count
// comes from process-wide MemStats, and a benchmark run beside another test is
// handed that test's allocations divided by its own iteration count.

func TestBudget_PaletteKeystrokeOverTwoThousandCommands(t *testing.T) {
	res := testing.Benchmark(BenchmarkPaletteKeystroke2000)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("a keystroke into the filter took %s over 2000 commands, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

// The cache half of the palette is on the same budget as the command half, and
// it is the half that grows with what the session has read.
func TestBudget_PaletteKeystrokeOverEveryCachedIssue(t *testing.T) {
	res := testing.Benchmark(BenchmarkPaletteKeystrokeCached)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("a keystroke over %d cached issues took %s, want under the 16ms in docs/PERFORMANCE.md",
			app.DefaultIssueBound, per)
	}
}

func TestBudget_PaletteOpeningIsOnTheKeystrokeBudget(t *testing.T) {
	res := testing.Benchmark(BenchmarkPaletteOpen64)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("building the palette over 64 commands took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}

// The picker is a list that ranks on every keystroke like the palette itself, so
// it is on the same budget.
func TestBudget_ProjectPickerKeystroke(t *testing.T) {
	res := testing.Benchmark(BenchmarkProjectKeystroke)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("a keystroke into the project picker took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}
