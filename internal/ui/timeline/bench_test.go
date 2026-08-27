package timeline

import (
	"context"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// stocked is a chart already holding n bars over span days of calendar, without
// a site behind it: the issues arrive as the message a read would have produced,
// which is what keeps a benchmark off the network and off a fake's locks.
//
// It is the chart a running program has, with a zone manager, so every row in it
// carries a marker — which is the shape the steady-state budget has to hold in.
func stocked(tb testing.TB, n, span, w, h int) *Model {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	d := kernel.Deps{
		Caps:    fullCaps(),
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Zones:   mgr,
		Now:     func() time.Time { return theDay },
	}
	m, ok := New(d).(*Model)
	if !ok {
		tb.Fatal("New did not return a *Model")
	}
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)

	issues := spread(n, span)
	fields := app.ResolveDateFields(catalogueFor(tb), nil, nil)
	res, err := app.NewDates(fields, app.WithZone(time.UTC, ""), app.WithNow(d.Now)).
		Resolve(context.Background(), issues)
	if err != nil {
		tb.Fatalf("resolving the cascade: %v", err)
	}
	next, _ = m.Update(loadedMsg{gen: m.gen, fields: fields, issues: issues, resolution: res})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

// spread builds n issues whose bars are laid out over span days, so that a
// benchmark can vary the number of rows and the length of the calendar
// independently.
func spread(n, span int) []jira.Issue {
	out := make([]jira.Issue, 0, n)
	base := theDay.AddDate(0, 0, -span/2)
	for i := range n {
		key := "PROJ-" + strconv.Itoa(i+1)
		at := base.AddDate(0, 0, (i*span)/max(n, 1))
		iss := jira.Issue{
			ID:        strconv.Itoa(30000 + i),
			Key:       key,
			Summary:   "Something that has to be done about " + key,
			Project:   jira.ProjectRef{ID: "10000", Key: "PROJ"},
			Type:      jira.IssueType{ID: "10301", Name: "Story"},
			Status:    jira.Status{ID: "10201", Name: "Triage", Category: jira.CategoryToDo},
			Created:   at,
			Requested: jira.AllFields(),
		}
		switch i % 3 {
		case 0:
			iss.FixVersions = []jira.Version{{ID: "v1", Name: "2.0", ReleaseDate: jira.DateOf(at.AddDate(0, 0, 30))}}
		case 1:
			iss.Due = jira.DateOf(at.AddDate(0, 0, 14))
		}
		out = append(out, iss)
	}
	return out
}

func catalogueFor(tb testing.TB) []jira.Field {
	tb.Helper()
	fields, err := jiratest.New().Fields(context.Background())
	if err != nil {
		tb.Fatalf("reading the field catalogue: %v", err)
	}
	return fields
}

// BenchmarkTimelineWalk10k walks a fresh row into view on every frame, which is
// the worst case: every frame misses the memo by construction.
//
// It goes back to the top on reaching the bottom. Without that the cursor stops
// on the last row and every iteration after the ten thousandth is a memo hit, so
// what the benchmark reports is a blend of walking and standing still, weighted
// by how many iterations the machine got through.
func BenchmarkTimelineWalk10k(b *testing.B) {
	m := stocked(b, 10000, 3650, 120, 40)
	var down, top tea.Msg = keyPress("j"), keyPress("home")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
		if m.cursor >= len(m.rows)-1 {
			next, _ = m.Update(top)
			m, _ = next.(*Model)
		}
	}
}

func steadyScroll(b *testing.B, rows, span int) {
	m := stocked(b, rows, span, 120, 40)
	var down, up tea.Msg = tea.MouseWheelMsg{Button: tea.MouseWheelDown}, tea.MouseWheelMsg{Button: tea.MouseWheelUp}
	msgs := [2]tea.Msg{down, up}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(msgs[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// BenchmarkTimelineSteadyScroll10k and its twin are the vertical half of the
// virtualization budget: a scroll that lands on rows the memo already holds.
func BenchmarkTimelineSteadyScroll10k(b *testing.B) { steadyScroll(b, 10000, 3650) }

func BenchmarkTimelineSteadyScroll20(b *testing.B) { steadyScroll(b, 20, 3650) }

// steadyPan pans in one direction, wrapping back to the start of the calendar
// when it runs off the end. Oscillating between two windows would land in the
// memo on the third frame and measure a cache hit; every frame here is a window
// the memo has not seen, which is the repaint the budget is about.
func steadyPan(b *testing.B, rows, span int) {
	m := stocked(b, rows, span, 120, 40)
	later := tea.Msg(keyPress("l"))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(later)
		m, _ = next.(*Model)
		_ = m.View()
		if m.left >= m.ax.cols-m.lay.chart {
			m.left = 0
		}
	}
}

// BenchmarkTimelinePanADecade and its twin are the horizontal half of the
// virtualization budget: the same rows and the same chart over ten years of
// calendar and over a thousand. A pan repaints the window whatever happens —
// every row's bar moves — so what is budgeted is that the repaint does not grow
// with the length of the calendar behind it. This is the view that scrolls in two
// dimensions, and both directions are budgeted.
//
// The second span is absurd on purpose: a render that walked the whole calendar
// would be caught by a factor, and a factor is what survives a runner under
// load.
func BenchmarkTimelinePanADecade(b *testing.B) { steadyPan(b, 400, 3650) }

func BenchmarkTimelinePanAMillennium(b *testing.B) { steadyPan(b, 400, 365000) }

func zoomWalk(b *testing.B, rows int) {
	m := stocked(b, rows, 3650, 120, 40)
	var in, out tea.Msg = keyPress("+"), keyPress("-")
	msgs := [2]tea.Msg{in, out}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(msgs[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// A zoom drops the whole memo, so it repaints — and what is budgeted is that it
// repaints a window rather than a chart.
func BenchmarkTimelineZoom10k(b *testing.B) { zoomWalk(b, 10000) }

func BenchmarkTimelineZoom200(b *testing.B) { zoomWalk(b, 200) }

func BenchmarkTimelineRedraw200x60(b *testing.B) {
	m := stocked(b, 10000, 3650, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkTimelineRowRender(b *testing.B) {
	m := stocked(b, 10000, 3650, 120, 40)
	k := rowKey{
		key: m.rows[0].key, summary: m.rows[0].summary,
		start: m.rows[0].rng.Start, end: m.rows[0].rng.End, from: m.rows[0].rng.From,
		lay: m.lay, ax: m.ax, left: m.left, today: m.todayCol(), gen: m.styles.gen,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = renderRow(&m.rows[0], k, m.styles, m.deps.Theme)
	}
}
