package palette

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
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

// BenchmarkPaletteScroll2000 and its twenty-command twin are the scroll path:
// the cursor moves, the window under it moves, and every row it lands on has
// already been rendered.
func BenchmarkPaletteScroll2000(b *testing.B) { scrollOver(b, 2000) }

func BenchmarkPaletteScroll20(b *testing.B) { scrollOver(b, 20) }

func scrollOver(b *testing.B, n int) {
	m := opened(b, n, 120, 40)
	var down, up tea.Msg = stroke("down"), stroke("up")
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

// cachedIssues is a cache holding what the real one is bounded to, so the
// keystroke measured below is the worst a session can present.
func cachedIssues(n int) *fakeCache {
	c := newFakeCache()
	for i := range n {
		id := strconv.Itoa(i)
		c.hold("PROJ-"+id, "Fix the login flow before release "+id, clockAt.Add(-time.Duration(i)*time.Minute))
	}
	return c
}

// BenchmarkPaletteKeystrokeCached is the budgeted path with both halves of the
// list answering: a character into the filter, every command ranked again, every
// cached issue ranked against it, then a frame. The pattern matches all of them,
// which is the worst case rather than the usual one.
func BenchmarkPaletteKeystrokeCached(b *testing.B) {
	d := paletteDeps()
	d.Cache = cachedIssues(app.DefaultIssueBound)
	m := build(d, manyCommands(64), memoryTable())
	next, _ := m.Update(kernel.SizeMsg{Width: 120, Height: 40})
	m, _ = next.(*Model)
	for _, r := range "releas" {
		next, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m, _ = next.(*Model)
	}
	_ = m.View()

	keys := []tea.Msg{tea.KeyPressMsg{Code: 'e', Text: "e"}, tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
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

// BenchmarkMatch is one command scored against one pattern: the title, the
// group and the ID, which is what a keystroke pays per row.
func BenchmarkMatch(b *testing.B) {
	r := row{cmd: kernel.Command{
		ID: "issue.move", Title: "Move issues between projects", Group: "Issue",
	}}
	pattern := app.NewPattern("mvpr")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = r.match(pattern)
	}
}

// scopesFound is more projects than a read of one page can turn up, so the
// picker's keystroke is measured against more than it will ever hold.
func scopesFound(n int) []project {
	out := make([]project, 0, n)
	for i := range n {
		id := strconv.Itoa(i)
		out = append(out, project{key: "OPS" + id, name: "Operations " + id})
	}
	return out
}

// BenchmarkProjectKeystroke is the picker's budgeted path: a character into the
// filter, every scope ranked again, then a frame.
func BenchmarkProjectKeystroke(b *testing.B) {
	m := buildProject(paletteDeps(), memoryTable())
	next, _ := m.Update(kernel.SizeMsg{Width: 120, Height: 40})
	m, _ = next.(*projectModel)
	next, _ = m.Update(projectsFoundMsg{found: scopesFound(suggestionLimit)})
	m, _ = next.(*projectModel)
	_ = m.View()

	keys := []tea.Msg{tea.KeyPressMsg{Code: 'o', Text: "o"}, tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*projectModel)
		_ = m.View()
	}
}
