package search

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func never(time.Duration, func() tea.Msg) tea.Cmd { return nil }

func benchDeps(tb testing.TB) kernel.Deps {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	return kernel.Deps{
		Caps:    jira.Capabilities{TimeZone: time.UTC},
		Theme:   kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Zones:   mgr,
		Project: "PROJ",
		Now:     func() time.Time { return testNow },
	}
}

func resultsOf(n int) appquery.Result {
	return appquery.Result{Page: jira.NewPage(jiratest.Gen(n), nil)}
}

func land(tb testing.TB, m *Model, n int) {
	tb.Helper()
	m.input.SetValue("log")
	m.typed = "log"
	m.tq = jira.ParseText("log")
	m.words, m.jql = m.tq.Words(), "text ~ \"log*\""
	m.gen++
	next, _ := m.Update(searchMsg{gen: m.gen, res: resultsOf(n)})
	if got := len(next.(*Model).rows); got != n {
		tb.Fatalf("%d rows landed, want %d", got, n)
	}
}

func searchOf(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	m := New(benchDeps(tb), Seed{}, withAfter(never))
	m.Update(kernel.SizeMsg{Width: w, Height: h})
	m.Update(kernel.FocusMsg{Focused: true})
	land(tb, m, n)
	_ = m.View()
	return m
}

func browsing(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	m := searchOf(tb, n, w, h)
	next, _ := m.Update(keyPress("down"))
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

func BenchmarkSearchKeystroke200(b *testing.B) {
	m := searchOf(b, 200, 120, 40)
	letters := [...]tea.Msg{keyPress("x"), tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(letters[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
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

func BenchmarkSearchScroll200(b *testing.B) { scroll(b, browsing(b, 200, 120, 40)) }

func BenchmarkSearchScroll20(b *testing.B) { scroll(b, browsing(b, 20, 120, 40)) }

func BenchmarkSearchSteadyFrame200(b *testing.B) {
	m := browsing(b, 200, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkSearchFullRedraw200x60(b *testing.B) {
	m := browsing(b, 200, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// Each pass ages the selected row by a second, so its memo key is new and the
// frame has exactly one row to render.
func BenchmarkSearchWalk200(b *testing.B) {
	m := browsing(b, 200, 120, 40)
	m.cursor = 5
	_ = m.View()
	r := m.rows[m.cursor]
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.iss.Updated = r.iss.Updated.Add(time.Second)
		_ = m.View()
	}
}

func BenchmarkSearchResultsLand200(b *testing.B) {
	m := searchOf(b, 200, 120, 40)
	msg := searchMsg{res: resultsOf(200)}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.gen++
		msg.gen = m.gen
		next, _ := m.Update(msg)
		m, _ = next.(*Model)
		_ = m.View()
	}
}
