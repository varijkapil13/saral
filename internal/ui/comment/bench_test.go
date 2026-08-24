package comment

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// thread builds a view holding n comments at a given size without touching the
// network: the page arrives as the message a read would have produced.
func thread(tb testing.TB, n, w, h int) *Model {
	tb.Helper()

	d := kernel.Deps{
		Caps:  jira.Capabilities{TimeZone: time.UTC},
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Site:  "bench.example.atlassian.net",
		Now:   func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	m := build(d, "PROJ-1")
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)

	at := time.Date(2026, time.February, 11, 9, 38, 0, 0, time.UTC)
	comments := make([]jira.Comment, 0, n)
	for i := range n {
		comments = append(comments, jira.Comment{
			ID:      strconv.Itoa(20000 + i),
			Author:  jira.User{DisplayName: "Another User"},
			Body:    adf.NewDoc(adf.NewNode("paragraph", adf.NewText("comment number "+strconv.Itoa(i)))),
			Created: at.Add(time.Duration(i) * time.Minute),
			Updated: at.Add(time.Duration(i) * time.Minute),
		})
	}
	next, _ = m.Update(loadedMsg{gen: m.gen, page: jira.NewPage(comments, nil)})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

func scroll(b *testing.B, m *Model) {
	b.Helper()

	var down, up tea.Msg = keyPress("j"), keyPress("k")
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

// BenchmarkThreadSteadyScroll10k and its twenty-comment twin are the pair that
// matters: docs/PERFORMANCE.md asks that a long thread and a short one cost the
// same per frame.
func BenchmarkThreadSteadyScroll10k(b *testing.B) { scroll(b, thread(b, 10000, 120, 40)) }

func BenchmarkThreadSteadyScroll20(b *testing.B) { scroll(b, thread(b, 20, 120, 40)) }

// BenchmarkThreadWalk10k walks a fresh comment into view on every frame, which
// is the worst case: every frame misses the memo by construction.
func BenchmarkThreadWalk10k(b *testing.B) {
	m := thread(b, 10000, 120, 40)
	next, _ := m.Update(keyPress("g"))
	m, _ = next.(*Model)
	next, _ = m.Update(keyPress("g"))
	m, _ = next.(*Model)

	down := keyPress("j")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
		if m.cursor >= len(m.comments)-1 {
			next, _ = m.Update(keyPress("g"))
			m, _ = next.(*Model)
			next, _ = m.Update(keyPress("g"))
			m, _ = next.(*Model)
		}
	}
}

func BenchmarkThreadRedraw200x60(b *testing.B) {
	m := thread(b, 10000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkThreadRenderBlock is the per-comment cost the memo protects.
func BenchmarkThreadRenderBlock(b *testing.B) {
	m := thread(b, 20, 120, 40)
	c := &m.comments[0]
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = renderBlock(c, 120, false, m.styles, m.deps.Theme, time.UTC)
	}
}

func TestScrolling_CostsTheSameOnTenThousandCommentsAsOnTwenty(t *testing.T) {
	t.Parallel()

	big := testing.Benchmark(BenchmarkThreadSteadyScroll10k)
	small := testing.Benchmark(BenchmarkThreadSteadyScroll20)

	bigAllocs, smallAllocs := big.AllocsPerOp(), small.AllocsPerOp()
	if bigAllocs > smallAllocs {
		t.Errorf("a 10k-comment thread allocates %d per frame against %d for a 20-comment one; the render is not virtualized",
			bigAllocs, smallAllocs)
	}
	// What is left is the frame string View has to return and the header joined
	// onto it; every comment behind them is memoized.
	if bigAllocs > 2 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself", bigAllocs)
	}
}

func TestBlockRendering_CostsNothingOnceMemoized(t *testing.T) {
	t.Parallel()

	m := thread(t, 200, 120, 40)
	before := len(m.blocks.made)
	for range 50 {
		_ = m.blockLines(m.cursor)
	}
	if got := len(m.blocks.made); got != before {
		t.Errorf("drawing one comment fifty times rendered it %d more times", got-before)
	}
}
