//go:build !race

package card

import (
	"fmt"
	"testing"
)

// cardAllocs are the per-card ceilings, a tenth over what the machine measures.
var cardAllocs = map[string]int64{
	"roomy/24":    22,
	"roomy/120":   10,
	"compact/24":  15,
	"compact/120": 9,
}

func TestBudget_ACardCostsABoundedNumberOfAllocations(t *testing.T) {
	ran := 0
	for _, c := range benchCases {
		name := fmt.Sprintf("%s/%d", c.look.Word(), c.width)
		ceiling, ok := cardAllocs[name]
		if !ok {
			t.Fatalf("%s has no ceiling", name)
		}
		res := testing.Benchmark(benchRender(c.look, c.width))
		if res.N == 0 {
			t.Fatalf("%s did not run", name)
		}
		ran++
		if got := res.AllocsPerOp(); got > ceiling {
			t.Errorf("a %s card allocates %d times, want at most %d (docs/PERFORMANCE.md)", name, got, ceiling)
		}
		t.Logf("a %s card: %d allocs", name, res.AllocsPerOp())
	}
	if ran != len(cardAllocs) {
		t.Errorf("measured %d cards against %d ceilings", ran, len(cardAllocs))
	}
}
