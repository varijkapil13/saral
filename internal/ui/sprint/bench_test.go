package sprint

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// stocked is a view already holding a board's sprints, without a site behind
// it: they arrive as the message a read would have produced, which is what
// keeps a benchmark off the network and off a fake's locks.
//
// It has a zone manager, because a running session always has one and a marked
// row is the shape the budget has to hold in.
func stocked(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	d := kernel.Deps{
		Caps:    fullCaps(),
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Zones:   mgr,
		Now:     func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	m, ok := New(d).(*Model)
	if !ok {
		tb.Fatal("New did not return a *Model")
	}
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)
	next, _ = m.Update(loadedMsg{
		gen:     m.gen,
		boards:  []jira.Board{{ID: 1, Name: "PROJ board"}},
		sprints: many(n),
	})
	m, _ = next.(*Model)
	if len(m.sprints) != n {
		tb.Fatalf("the view holds %d sprints, want %d", len(m.sprints), n)
	}
	_ = m.View()
	return m
}

// BenchmarkSprintsWalk walks a fresh row into view on every frame, which is the
// worst case: every frame misses the memo by construction.
//
// It goes back to the top on reaching the bottom. Without that the cursor stops
// on the last row and every iteration after the two thousandth is a memo hit, so
// what it reports is a blend of walking and standing still weighted by how many
// iterations the machine got through — a number about the machine and not about
// the code.
func BenchmarkSprintsWalk(b *testing.B) {
	m := stocked(b, 2000, 120, 40)
	var down, top tea.Msg = keyPress("j"), keyPress("home")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
		if m.cursor >= m.rowCount()-1 {
			next, _ = m.Update(top)
			m, _ = next.(*Model)
		}
	}
}

// BenchmarkSprintsScroll is the steady state: the window moves and every row it
// lands on is already rendered. The twenty-sprint twin is what it is held against.
func BenchmarkSprintsScroll(b *testing.B) { scrollOver(b, 2000) }

func BenchmarkSprintsScroll20(b *testing.B) { scrollOver(b, 20) }

func BenchmarkSprintsRedraw200x60(b *testing.B) {
	m := stocked(b, 2000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func scrollOver(b *testing.B, n int) {
	m := stocked(b, n, 120, 40)
	var down, up tea.Msg = keyPress("down"), keyPress("up")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		key := down
		if i%2 == 1 {
			key = up
		}
		next, _ := m.Update(key)
		m, _ = next.(*Model)
		_ = m.View()
	}
}
