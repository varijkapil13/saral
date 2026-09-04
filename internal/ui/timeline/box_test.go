package timeline

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
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

// The same box, with a term in force: the bar is one more line the chrome may
// have to give up, and it has to give it up before the box overflows rather
// than after.
func TestTimeline_GivesUpTheFilterBarRatherThanOverflowingAShortBox(t *testing.T) {
	t.Parallel()

	for h := 1; h <= 24; h++ {
		dr := newDriver(t, testDeps(newFake(40)), 120, h)
		term, ok := firstAssignee(dr)
		if !ok {
			t.Fatal("no generated issue carries an assignee, so this case proves nothing")
		}
		dr.send(filter.ChosenMsg{Term: term})
		if got := len(strings.Split(dr.m.View(), "\n")); got != h {
			t.Errorf("at 120x%d with a term in force drew %d lines", h, got)
		}
	}
}
