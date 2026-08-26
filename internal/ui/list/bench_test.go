package list

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
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

// BenchmarkListSteadyScrollTermed10k is the same scroll with two terms in
// force, which draws the line naming them under the rows.
func BenchmarkListSteadyScrollTermed10k(b *testing.B) {
	scroll(b, termed(b, 10000, 120, 40))
}

// termed is a list narrowed by two chosen values, which is what a user browses
// in after the picker. The terms arrive as the message the picker sends, so the
// search behind them is never run.
func termed(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	m := loaded(tb, n, w, h)
	m.terms = filter.Terms{
		{Facet: filter.FacetAssignee, ID: "acct-ada", Label: "Ada Lovelace"},
		{Facet: filter.FacetStatus, ID: "10203", Label: "Shipped"},
	}
	m.termsGen++
	_ = m.View()
	return m
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
//
// It goes back to the top on reaching the bottom. Without that the cursor stops
// on the last row and every iteration after the ten thousandth is a memo hit, so
// what the benchmark reports is a blend of walking and standing still, weighted
// by how many iterations the machine got through — which is a number about the
// machine and not about the code.
func BenchmarkListWalk10k(b *testing.B) {
	m := loaded(b, 10000, 120, 40)
	var down, top tea.Msg = keyPress("j"), keyPress("home")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
		if m.cursor >= len(m.view)-1 {
			next, _ = m.Update(top)
			m, _ = next.(*Model)
		}
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
