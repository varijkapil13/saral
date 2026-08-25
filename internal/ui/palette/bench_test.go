package palette

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// opened builds a palette over n commands at a given size, drawn once, which is
// the state a keystroke arrives in.
func opened(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	m := build(paletteDeps(), manyCommands(n), memoryTable())
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

// manyCommands is a registry far larger than any build will have, which is how
// the drawing is held to the visible window rather than to the whole list.
func manyCommands(n int) []kernel.Command {
	run := func(kernel.Deps) tea.Cmd { return nil }
	out := make([]kernel.Command, 0, n)
	for i := range n {
		id := strconv.Itoa(i)
		out = append(out, kernel.Command{
			ID:    "group." + id,
			Title: "Do the thing that is number " + id,
			Group: "Group " + strconv.Itoa(i%9),
			Keys:  []string{"g" + strconv.Itoa(i%9)},
			Run:   run,
		})
	}
	return out
}

func redraw(b *testing.B, m *Model) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkPaletteRedraw2000 and its twenty-command twin are the pair that
// matters: docs/PERFORMANCE.md asks that a long list and a short one cost the
// same per frame.
func BenchmarkPaletteRedraw2000(b *testing.B) { redraw(b, opened(b, 2000, 120, 40)) }

func BenchmarkPaletteRedraw20(b *testing.B) { redraw(b, opened(b, 20, 120, 40)) }

// BenchmarkPaletteKeystroke2000 is the budgeted path: a character into the
// filter, the whole list ranked again, then a frame.
func BenchmarkPaletteKeystroke2000(b *testing.B) {
	m := opened(b, 2000, 120, 40)
	keys := []tea.Msg{tea.KeyPressMsg{Code: 't', Text: "t"}, tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// BenchmarkPaletteOpen64 is ctrl+k itself: the palette is built fresh on every
// press, so this is what the keypress costs before anything is drawn. Sixty-four
// commands is several times what a build registers; the 2000 twin is there for
// the scaling and is not held to the keystroke budget, which a race-instrumented
// run of it cannot meet and no registry will ever ask it to.
func BenchmarkPaletteOpen64(b *testing.B) { open(b, 64) }

func BenchmarkPaletteOpen2000(b *testing.B) { open(b, 2000) }

func open(b *testing.B, n int) {
	b.Helper()
	cmds := manyCommands(n)
	freq := memoryTable()
	d := paletteDeps()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m := build(d, cmds, freq)
		next, _ := m.Update(kernel.SizeMsg{Width: 120, Height: 40})
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkPaletteRowRender(b *testing.B) {
	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	st := newStyles(theme)
	rows := make([]row, 0, 64)
	for _, cmd := range manyCommands(64) {
		rows = append(rows, row{cmd: cmd, keys: cmd.Keys[0]})
	}
	lay := planLayout(120, widestKey(rows))
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_ = renderRow(&rows[i%len(rows)], lay, i%7 == 0, st, theme)
	}
}

func BenchmarkMatch(b *testing.B) {
	c := newCandidate("Move issues between projects")
	query := []rune("mvpr")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = c.match(query)
	}
}

func TestDrawing_CostsTheSameOnTwoThousandCommandsAsOnTwenty(t *testing.T) {
	t.Parallel()

	long := testing.Benchmark(BenchmarkPaletteRedraw2000)
	short := testing.Benchmark(BenchmarkPaletteRedraw20)
	if long.AllocsPerOp() > short.AllocsPerOp() {
		t.Errorf("2000 commands allocate %d per frame against %d for twenty; the drawing is not virtualized",
			long.AllocsPerOp(), short.AllocsPerOp())
	}
}

func TestKeystrokeToFrame_StaysUnderTheBudget(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkPaletteKeystroke2000)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("a keystroke into the filter took %s over 2000 commands, want under 16ms", per)
	}
}

// ctrl+k builds the palette from scratch, so opening it is on the keystroke
// budget too rather than on a start-up one.
func TestOpening_StaysUnderTheKeystrokeBudget(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkPaletteOpen64)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("building the palette over 64 commands took %s, want under 16ms", per)
	}
}

func TestRowRendering_CostsNothingOnceMemoized(t *testing.T) {
	m := opened(t, 2000, 120, 40)
	if got := testing.AllocsPerRun(200, func() { _ = m.row(0) }); got != 0 {
		t.Errorf("a memoized row allocates %.1f times, want none", got)
	}
}
