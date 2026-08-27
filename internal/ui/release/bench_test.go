package release

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// stocked is a list already holding versions, without a site behind it: they
// arrive as the message a read would have produced, which is what keeps a
// benchmark off the network and off a fake's locks.
//
// It has a zone manager, because a running session always has one and a frame
// with every row marked is the shape the budget has to hold in.
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
	next, _ = m.Update(versionsMsg{gen: m.gen, versions: benchVersions(n)})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

// benchVersions is a project's worth of versions, half of them counted so that
// both forms of the open column are drawn.
func benchVersions(n int) []jira.Version {
	out := make([]jira.Version, 0, n)
	for i := range n {
		v := jira.Version{
			ID:          strconv.Itoa(60000 + i),
			Name:        "release-1." + strconv.Itoa(i),
			Description: "the release that carries the work of week " + strconv.Itoa(i%52),
			Released:    i%4 == 0,
			StartDate:   jira.Date{Year: 2026, Month: time.January, Day: i%28 + 1},
			ReleaseDate: jira.Date{Year: 2026, Month: time.March, Day: i%28 + 1},
		}
		if i%2 == 0 {
			open := i % 17
			v.Unresolved = &open
		}
		out = append(out, v)
	}
	return out
}

// BenchmarkReleasesWalk walks a fresh row into view on every frame, which is the
// worst case: every frame misses the memo by construction.
//
// It goes back to the top on reaching the bottom. Without that the cursor stops
// on the last row and every iteration after the last is a memo hit, so what the
// benchmark reports is a blend of walking and standing still, weighted by how
// many iterations the machine got through.
func BenchmarkReleasesWalk(b *testing.B) {
	m := stocked(b, 2000, 120, 40)
	var down, top tea.Msg = keyPress("j"), keyPress("home")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
		if m.cursor >= len(m.versions)-1 {
			next, _ = m.Update(top)
			m, _ = next.(*Model)
		}
	}
}

// BenchmarkReleasesKeystroke is a rune typed into the editor and drawn, which is
// the path a name is typed on.
func BenchmarkReleasesKeystroke(b *testing.B) {
	m := stocked(b, 2000, 120, 40)
	next, _ := m.Update(keyPress("n"))
	m, _ = next.(*Model)
	keys := []tea.Msg{tea.KeyPressMsg{Code: 'r', Text: "r"}, tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkReleasesRedraw200x60(b *testing.B) {
	m := stocked(b, 2000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkRowRender(b *testing.B) {
	m := stocked(b, 40, 120, 40)
	k := m.rowKeyOf(0, true)
	theme := m.deps.Theme
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = renderRow(k, m.styles, theme)
	}
}

// scrollOver is the steady state: the window moves and every row it lands on is
// already rendered.
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

func BenchmarkReleasesSteadyScroll2000(b *testing.B) { scrollOver(b, 2000) }
func BenchmarkReleasesSteadyScroll20(b *testing.B)   { scrollOver(b, 20) }

// stockedFlow is a release screen with somewhere to move the open issues to, and
// no site behind it.
func stockedFlow(tb testing.TB, targets, w, h int) *Flow {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	d := kernel.Deps{
		Caps:  fullCaps(),
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Zones: mgr,
		Now:   func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	f, ok := NewFlow(d, jira.Version{ID: twoOh, Name: "2.0"}, 12, benchVersions(targets)).(*Flow)
	if !ok {
		tb.Fatal("NewFlow did not return a *Flow")
	}
	next, _ := f.Update(kernel.SizeMsg{Width: w, Height: h})
	f, _ = next.(*Flow)
	_ = f.View()
	return f
}

func BenchmarkFlowRedraw200x60(b *testing.B) {
	f := stockedFlow(b, 200, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = f.View()
	}
}

func flowScrollOver(b *testing.B, targets int) {
	f := stockedFlow(b, targets, 120, 40)
	next, _ := f.Update(keyPress("j"))
	f, _ = next.(*Flow)
	next, _ = f.Update(keyPress("enter"))
	f, _ = next.(*Flow)
	var down, up tea.Msg = keyPress("down"), keyPress("up")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		key := down
		if i%2 == 1 {
			key = up
		}
		next, _ := f.Update(key)
		f, _ = next.(*Flow)
		_ = f.View()
	}
}

func BenchmarkFlowSteadyScroll2000(b *testing.B) { flowScrollOver(b, 2000) }
func BenchmarkFlowSteadyScroll20(b *testing.B)   { flowScrollOver(b, 20) }
