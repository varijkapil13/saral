package list

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// loaded builds a list holding n issues at a given size without touching the
// network: the page arrives as the message a search would have produced.
func loaded(tb testing.TB, n, w, h int) *Model {
	return listOf(tb, kernel.Deps{
		Caps:  jira.Capabilities{TimeZone: time.UTC},
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Now:   func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}, n, w, h)
}

// markedList is the same list a running program has: one with a zone manager, so
// every row and every clickable cell in it carries a marker. It is the shape the
// steady-state budget has to hold in, since a real session always has one.
func markedList(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	return listOf(tb, kernel.Deps{
		Caps:  jira.Capabilities{TimeZone: time.UTC},
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Now:   func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
		Zones: mgr,
	}, n, w, h)
}

func listOf(tb testing.TB, d kernel.Deps, n, w, h int) *Model {
	tb.Helper()
	view, ok := New(d).(*Model)
	if !ok {
		tb.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ := next.(*Model)
	next, _ = m.Update(loadedMsg{gen: m.gen, page: jira.NewPage(jiratest.Gen(n), nil)})
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

// BenchmarkListSteadyScroll10k and its twenty-row twin are the pair that
// matters: docs/PERFORMANCE.md asks that a ten-thousand row list and a twenty
// row list cost the same per frame.
func BenchmarkListSteadyScroll10k(b *testing.B) { scroll(b, loaded(b, 10000, 120, 40)) }

func BenchmarkListSteadyScroll20(b *testing.B) { scroll(b, loaded(b, 20, 120, 40)) }

// BenchmarkListSteadyScrollMarked10k is the same scroll with the mouse on. The
// markers live inside the memoized row, so a frame that hits the memo costs what
// it did before there were any.
func BenchmarkListSteadyScrollMarked10k(b *testing.B) { scroll(b, markedList(b, 10000, 120, 40)) }

// BenchmarkListSteadyScrollFiltered10k is the same scroll under a filter that
// has been accepted, which draws a line naming it under the rows.
func BenchmarkListSteadyScrollFiltered10k(b *testing.B) {
	scroll(b, narrowed(b, 10000, 120, 40))
}

// narrowed is a list with a filter typed and accepted, which is what a user
// browses in after pressing enter.
func narrowed(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	m := loaded(tb, n, w, h)
	for _, key := range []tea.Msg{keyPress("/"), tea.KeyPressMsg{Code: 'l', Text: "l"},
		tea.KeyPressMsg{Code: 'o', Text: "o"}, tea.KeyPressMsg{Code: 'g', Text: "g"}, keyPress("enter")} {
		next, _ := m.Update(key)
		m, _ = next.(*Model)
	}
	if m.query == "" || len(m.view) == len(m.issues) {
		tb.Fatalf("the filter left %d of %d rows, so this is not the narrowed state", len(m.view), len(m.issues))
	}
	_ = m.View()
	return m
}

// BenchmarkListWalk10k walks a fresh row into view on every frame, which is the
// worst case: every frame misses the memo by construction.
func BenchmarkListWalk10k(b *testing.B) {
	m := loaded(b, 10000, 120, 40)
	var down tea.Msg = keyPress("j")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkListRedraw200x60(b *testing.B) {
	m := loaded(b, 10000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkRowMemoHit(b *testing.B) {
	m := loaded(b, 10000, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.row(0, false)
	}
}

func BenchmarkRowRender(b *testing.B) {
	issues := jiratest.Gen(64)
	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	st := newStyles(theme)
	lay := planLayout(120, 8)
	now := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_ = renderRow(&issues[i%len(issues)], lay, i%7 == 0, st, theme, time.UTC, now, widget.Zoner{})
	}
}

func BenchmarkRowRenderMarked(b *testing.B) {
	issues := jiratest.Gen(64)
	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	st := newStyles(theme)
	lay := planLayout(120, 8)
	now := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	mgr := zone.New()
	b.Cleanup(mgr.Close)
	z := widget.NewZoner(mgr)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_ = renderRow(&issues[i%len(issues)], lay, i%7 == 0, st, theme, time.UTC, now, z)
	}
}

func BenchmarkFilterKeystroke10k(b *testing.B) {
	m := loaded(b, 10000, 120, 40)
	next, _ := m.Update(keyPress("/"))
	m, _ = next.(*Model)
	for _, r := range "log" {
		next, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m, _ = next.(*Model)
	}
	keys := []tea.Msg{tea.KeyPressMsg{Code: 'i', Text: "i"}, tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// BenchmarkQueryPromptKeystroke10k is the other prompt this view draws under
// the rows. It costs what the filter does not: nothing is re-matched, so the
// rows behind it are a memo hit and the line itself is all that is rebuilt.
func BenchmarkQueryPromptKeystroke10k(b *testing.B) {
	m := loaded(b, 10000, 120, 40)
	next, _ := m.Update(keyPress("e"))
	m, _ = next.(*Model)
	keys := []tea.Msg{tea.KeyPressMsg{Code: 'x', Text: "x"}, tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func TestScrolling_CostsTheSameOnTenThousandRowsAsOnTwenty(t *testing.T) {
	t.Parallel()

	big := testing.Benchmark(BenchmarkListSteadyScroll10k)
	small := testing.Benchmark(BenchmarkListSteadyScroll20)

	bigAllocs := big.AllocsPerOp()
	smallAllocs := small.AllocsPerOp()
	if bigAllocs > smallAllocs {
		t.Errorf("a 10k-row list allocates %d per frame against %d for a 20-row list; the render is not virtualized",
			bigAllocs, smallAllocs)
	}
	// The one allocation left is the frame string itself, which View has to
	// return; everything behind it is memoized.
	if bigAllocs > 1 {
		t.Errorf("a steady-state frame allocates %d times, want the memo to carry all but the frame itself", bigAllocs)
	}
}

// The clickable cells are the reason to check this twice: a marked row is a
// longer string built out of more pieces, and if the marks were applied outside
// the memo the whole window would be rebuilt on every frame.
func TestScrolling_CostsTheSameWithTheMouseOn(t *testing.T) {
	t.Parallel()

	marked := testing.Benchmark(BenchmarkListSteadyScrollMarked10k)
	if got := marked.AllocsPerOp(); got > 1 {
		t.Errorf("a steady-state frame with the mouse on allocates %d times, want the memo to carry all but the frame itself", got)
	}
}

// The line that names an accepted filter is on every frame it is on, so it is
// memoized the way the summary is.
func TestScrolling_CostsTheSameUnderAFilterThatHasBeenAccepted(t *testing.T) {
	t.Parallel()

	narrow := testing.Benchmark(BenchmarkListSteadyScrollFiltered10k)
	if got := narrow.AllocsPerOp(); got > 1 {
		t.Errorf("a steady-state frame under a kept filter allocates %d times, want the frame string and nothing else", got)
	}
}

func TestRowRendering_CostsNothingOnceMemoized(t *testing.T) {
	m := loaded(t, 10000, 120, 40)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0, false) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}

func TestKeystrokeToFrame_StaysUnderTheBudgetAtTenThousandRows(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkListWalk10k)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s at 10k rows, want under 16ms", per)
	}
}

func TestFullRedraw_StaysUnderTheBudgetAt200x60(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkListRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under 4ms", per)
	}
}
