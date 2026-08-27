package timeline

import (
	"strings"
	"testing"
)

// A box too short for the lines around the bars gives them up rather than
// overflowing it, whatever height the kernel hands over.
func TestTimeline_GivesUpItsChromeRatherThanOverflowingAShortBox(t *testing.T) {
	t.Parallel()

	for h := 1; h <= 24; h++ {
		dr := newDriver(t, testDeps(newFake(40)), 120, h)
		got := len(strings.Split(dr.m.View(), "\n"))
		if got != h {
			t.Errorf("at 120x%d drew %d lines", h, got)
		}
		dr.key("n")
		if got := len(strings.Split(dr.m.View(), "\n")); got != h {
			t.Errorf("notes at 120x%d drew %d lines", h, got)
		}
	}
}
