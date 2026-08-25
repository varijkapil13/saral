//go:build !race

package app

import (
	"testing"
	"time"
)

// The budgets in docs/PERFORMANCE.md are about the binary that ships. The race
// detector puts around twenty times the cost on a scan of this shape, so
// asserting them under -race would measure the instrumentation instead.

func TestIndexSearch_StaysUnderTheKeystrokeBudgetAtTenThousandIssues(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		of   func(*testing.B)
	}{
		{name: "a pattern of three runes", of: BenchmarkIndexSearch10k},
		{name: "the first rune typed, which nearly everything matches", of: BenchmarkIndexSearchOneRune10k},
		{name: "every hit rather than a screenful", of: BenchmarkIndexSearchUnbounded10k},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := testing.Benchmark(tc.of)
			if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
				t.Errorf("ranking ten thousand cached issues took %s, want under the 16ms in docs/PERFORMANCE.md", per)
			}
		})
	}
}

// A keystroke is allowed the answer it hands back and nothing else: the rows,
// the pattern and the ranking buffer all outlive the call.
func TestIndexSearch_AllocatesOnlyTheAnswerItHandsBack(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkIndexSearch10k)
	if got := res.AllocsPerOp(); got > 1 {
		t.Errorf("a search allocates %d times, want only the slice it returns", got)
	}
}

// Rebuilding is the cost the generation counter exists to keep off the
// keystroke path, and it still has to fit inside one when it is paid.
func TestIndexRebuild_StaysUnderTheKeystrokeBudgetAtTenThousandIssues(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkIndexRebuild10k)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("rebuilding over ten thousand cached issues took %s, want under the 16ms in docs/PERFORMANCE.md", per)
	}
}
