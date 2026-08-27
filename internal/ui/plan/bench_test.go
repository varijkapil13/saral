package plan

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// stocked is a view already holding n plans, with a zone manager on it and no
// site behind it: the plans are the profile's, which is the state the common
// session is in and the one that needs no round trip.
func stocked(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	caps := fullCaps()
	caps.Plans = jira.Capability{Reason: "the Plans API needs Administer Jira"}
	d := kernel.Deps{
		Caps:    caps,
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Zones:   mgr,
		Now:     func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	m, ok := New(d, WithDefined(manyPlans(n))).(*Model)
	if !ok {
		tb.Fatal("New did not return a *Model")
	}
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)
	if len(m.plans) != n {
		tb.Fatalf("the view holds %d plans, want %d", len(m.plans), n)
	}
	_ = m.View()
	return m
}

func manyPlans(n int) []Defined {
	out := make([]Defined, 0, n)
	for i := range n {
		out = append(out, Defined{
			Name:     "team-" + strconv.Itoa(i%37) + "-delivery-" + strconv.Itoa(i),
			Projects: []string{"PROJ"},
			JQL:      "labels = roadmap",
		})
	}
	return out
}

// BenchmarkPlansSteadyScroll is the steady state: the cursor moves between two
// rows and every row the window lands on has already been rendered.
func BenchmarkPlansSteadyScroll2000(b *testing.B) { scrollOver(b, 2000) }

func BenchmarkPlansSteadyScroll20(b *testing.B) { scrollOver(b, 20) }

func scrollOver(b *testing.B, n int) {
	m := stocked(b, n, 120, 40)
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

// BenchmarkPlansWalk walks a fresh row into view on every frame, which is the
// worst case: past the memo's limit every frame misses it by construction.
//
// It goes back to the top on reaching the bottom. Without that the cursor stops
// on the last row and every iteration after the two thousandth is a memo hit, so
// what it reports is a blend of walking and standing still, weighted by how many
// iterations the machine got through.
func BenchmarkPlansWalk(b *testing.B) { walkOver(b, 2000, 120, 40) }

func walkOver(b *testing.B, n, w, h int) {
	m := stocked(b, n, w, h)
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

func BenchmarkPlansRedraw200x60(b *testing.B) {
	m := stocked(b, 2000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkPlansOpen is the fold: one plan's sources flattened into the rows and
// the frame drawn again. It closes what it opened so that every iteration
// measures the same work.
func BenchmarkPlansOpen(b *testing.B) {
	m := stocked(b, 2000, 120, 40)
	enter := tea.Msg(keyPress("enter"))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(enter)
		m, _ = next.(*Model)
		_ = m.View()
		next, _ = m.Update(enter)
		m, _ = next.(*Model)
	}
}
