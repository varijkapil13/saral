package filter

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// stocked is a picker already holding a vocabulary, without a site behind it:
// the values arrive as the message a read would have produced, which is what
// keeps a benchmark off the network and off a fake's locks.
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
	m.state, m.facet, m.complete = pickValue, FacetLabel, true
	_ = m.input.Focus()
	next, _ = m.Update(vocabularyMsg{gen: m.gen, facet: FacetLabel, values: labelValues(manyLabels(n))})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

func manyLabels(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, "team-"+itoa(i%37)+"-service-"+itoa(i))
	}
	return out
}

// BenchmarkPickerKeystroke is the path the owner asked for speed on: a rune
// typed into the needle, ranked against everything held, and drawn.
func BenchmarkPickerKeystroke(b *testing.B) {
	m := stocked(b, 2000, 120, 40)
	keys := []tea.Msg{tea.KeyPressMsg{Code: 's', Text: "s"}, tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// BenchmarkPickerScroll is the steady state: the window moves and every row it
// lands on is already rendered.
func BenchmarkPickerScroll(b *testing.B) {
	m := stocked(b, 2000, 120, 40)
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

func BenchmarkPickerRedraw200x60(b *testing.B) {
	m := stocked(b, 2000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkRankValues(b *testing.B) {
	all := labelValues(manyLabels(2000))
	pattern := app.NewPattern("serv")
	shown, ranks := make([]int, 0, len(all)), make([]ranked, 0, len(all))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		shown, ranks = rank(all, pattern, shown[:0], ranks[:0])
	}
	if len(shown) == 0 {
		b.Fatal("the pattern matched nothing, so this measured a walk and no ranking")
	}
}

// The picker is virtualized, so a vocabulary of two thousand costs what one of
// twenty costs per frame.
func TestPickerScrolling_CostsTheSameOnTwoThousandRowsAsOnTwenty(t *testing.T) {
	t.Parallel()

	big := testing.Benchmark(func(b *testing.B) { scrollOver(b, 2000) })
	small := testing.Benchmark(func(b *testing.B) { scrollOver(b, 20) })
	if big.AllocsPerOp() > small.AllocsPerOp() {
		t.Errorf("a 2000-row picker allocates %d per frame against %d for a 20-row one; the render is not virtualized",
			big.AllocsPerOp(), small.AllocsPerOp())
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

func TestPickerKeystrokeToFrame_StaysUnderTheBudget(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkPickerKeystroke)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s over two thousand values, want under 16ms", per)
	}
}

func TestPickerFullRedraw_StaysUnderTheBudgetAt200x60(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkPickerRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under 4ms", per)
	}
}

// Ranking is the work a keystroke does over everything held, and it may not
// allocate per candidate: both buffers are reused and app.Pattern folds case
// without copying either side.
func TestRanking_ReusesItsBuffers(t *testing.T) {
	all := labelValues(manyLabels(2000))
	pattern := app.NewPattern("serv")
	shown, ranks := make([]int, 0, len(all)), make([]ranked, 0, len(all))
	shown, ranks = rank(all, pattern, shown, ranks)
	if got := testing.AllocsPerRun(50, func() {
		shown, ranks = rank(all, pattern, shown[:0], ranks[:0])
	}); got != 0 {
		t.Errorf("ranking two thousand values allocates %.1f times, want none", got)
	}
}
