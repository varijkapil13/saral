package settings

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// opened builds the settings screen over the sample settings, drawn once,
// which is the state a keystroke arrives in.
func opened(tb testing.TB, w, h int) *Model {
	tb.Helper()
	st := &fakeState{theme: "dark", scheme: "default", mouse: true}
	all, sections := sampleSettings(st)
	m := build(settingsDeps(defaultTheme()), all, sections)
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

// BenchmarkSettingsRedraw is the steady-state repaint: nothing changed, and
// every row's own memoized string is reused.
func BenchmarkSettingsRedraw(b *testing.B) {
	m := opened(b, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkSettingsMoveCursor is the keystroke path: the cursor moves one row
// down and back up, and everything but the two rows whose selection changed
// is a memo hit — the theme generation and the current value are both part of
// rowKey, so a radio that moved would show up here as a miss rather than a
// stale frame.
func BenchmarkSettingsMoveCursor(b *testing.B) {
	m := opened(b, 120, 40)
	var down, up = stroke("down"), stroke("up")
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

// BenchmarkSettingsRowRender is one row's own cost with nothing memoized,
// which is what a value moving pays.
func BenchmarkSettingsRowRender(b *testing.B) {
	st := &fakeState{theme: "dark", scheme: "default", mouse: true}
	all, _ := sampleSettings(st)
	d := settingsDeps(defaultTheme())
	lay := planLayout(120)
	s := all[0]
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		st.theme = []string{"auto", "dark", "light", "no-color"}[i%4]
		_ = renderControl(s, shapeRadios, d, st.theme, i%2 == 0, lay, newStyles(d.Theme))
	}
}
