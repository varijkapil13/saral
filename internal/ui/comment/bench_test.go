package comment

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// thread builds a view holding n comments at a given size without touching the
// network: the page arrives as the message a read would have produced.
func thread(tb testing.TB, n, w, h int) *Model {
	tb.Helper()

	d := kernel.Deps{
		Caps:  jira.Capabilities{TimeZone: time.UTC},
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Site:  "bench.example.atlassian.net",
		Now:   func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	m := build(d, "PROJ-1")
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)

	at := time.Date(2026, time.February, 11, 9, 38, 0, 0, time.UTC)
	comments := make([]jira.Comment, 0, n)
	for i := range n {
		comments = append(comments, jira.Comment{
			ID:      strconv.Itoa(20000 + i),
			Author:  jira.User{DisplayName: "Another User"},
			Body:    adf.NewDoc(adf.NewNode("paragraph", adf.NewText("comment number "+strconv.Itoa(i)))),
			Created: at.Add(time.Duration(i) * time.Minute),
			Updated: at.Add(time.Duration(i) * time.Minute),
		})
	}
	next, _ = m.Update(loadedMsg{gen: m.gen, page: jira.NewPage(comments, nil)})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

// wideThread is a thread of comments whose code blocks are all wider than the
// box, so that every line on screen is one the pane has to cut.
func wideThread(tb testing.TB, w, h int) *Model {
	tb.Helper()

	m := thread(tb, 200, w, h)
	code := "func total(rows []Row) Money { return sum(rows).Round(2) } // the rounding that lies"
	for i := range m.comments {
		m.comments[i].Body = adf.NewDoc(
			adf.NewNode("paragraph", adf.NewText("comment number "+strconv.Itoa(i))),
			adf.NewNode("codeBlock", adf.NewText(code)).WithAttrs(adf.Attrs{"language": "go"}),
		)
	}
	m.blocks.reset()
	_ = m.View()
	return m
}

func scroll(b *testing.B, m *Model) {
	b.Helper()

	var down, up tea.Msg = keyPress("j"), keyPress("k")
	// The first press of each key renders the two comments the selection moves
	// between; every frame after that is the memo, which is the steady state this
	// claims to measure.
	for _, key := range []tea.Msg{down, up} {
		next, _ := m.Update(key)
		m, _ = next.(*Model)
		_ = m.View()
	}
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

// BenchmarkThreadSteadyScroll10k and its twenty-comment twin are the pair that
// matters: docs/PERFORMANCE.md asks that a long thread and a short one cost the
// same per frame.
func BenchmarkThreadSteadyScroll10k(b *testing.B) { scroll(b, thread(b, 10000, 120, 40)) }

func BenchmarkThreadSteadyScroll20(b *testing.B) { scroll(b, thread(b, 20, 120, 40)) }

// BenchmarkThreadWalk10k walks a fresh comment into view on every frame, which
// is the worst case: every frame misses the memo by construction.
func BenchmarkThreadWalk10k(b *testing.B) {
	m := thread(b, 10000, 120, 40)
	next, _ := m.Update(keyPress("g"))
	m, _ = next.(*Model)
	next, _ = m.Update(keyPress("g"))
	m, _ = next.(*Model)

	down := keyPress("j")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
		if m.cursor >= len(m.comments)-1 {
			next, _ = m.Update(keyPress("g"))
			m, _ = next.(*Model)
			next, _ = m.Update(keyPress("g"))
			m, _ = next.(*Model)
		}
	}
}

func BenchmarkThreadRedraw200x60(b *testing.B) {
	m := thread(b, 10000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkThreadRedrawSidebar34x24 is the sidebar: the same instance at a third
// of the width, with every comment laid out for it.
func BenchmarkThreadRedrawSidebar34x24(b *testing.B) {
	m := thread(b, 10000, 34, 24)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkThreadCompose is a frame with the composer open, which is a second
// layout over the same thread and must not cost a render of it.
func BenchmarkThreadCompose(b *testing.B) {
	m := thread(b, 10000, 34, 24)
	next, _ := m.Update(WriteMsg{})
	m, _ = next.(*Model)
	for _, r := range "A reply long enough to wrap in a sidebar twice over." {
		next, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m, _ = next.(*Model)
	}
	_ = m.View()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkThreadPan is the window cut out of the lines too wide for the box.
// Panning re-cuts a screenful, so it is the one gesture that cannot be answered
// out of the memo — but it is memoized per pan, so holding the key is not a cut
// per frame.
func BenchmarkThreadPan(b *testing.B) {
	m := wideThread(b, 34, 24)
	right, left := keyPress("l"), keyPress("h")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		key := right
		if i%2 == 1 {
			key = left
		}
		next, _ := m.Update(key)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// BenchmarkThreadRenderBlock is the per-comment cost the memo protects.
func BenchmarkThreadRenderBlock(b *testing.B) {
	m := thread(b, 20, 120, 40)
	c := &m.comments[0]
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = renderBlock(c, 120, false, m.styles, m.deps.Theme, time.UTC)
	}
}

// The composer is a second thing in the box and not a second layout of it, so a
// frame with it open costs no render of the thread above it. What a composing
// frame does cost is one render of the editor widget, which is the library's own
// and is not memoized here: its key would have to include the cursor, the scroll
// offset and the placeholder, and a memo that misses one of those draws the
// wrong text at the wrong width.
func TestComposing_RendersNoCommentPerFrame(t *testing.T) {
	t.Parallel()

	m := thread(t, 200, 34, 24)
	next, _ := m.Update(WriteMsg{})
	m, _ = next.(*Model)
	_ = m.View()

	before := len(m.blocks.made)
	for range 50 {
		_ = m.View()
	}
	if got := len(m.blocks.made); got != before {
		t.Errorf("fifty composing frames rendered %d comments", got-before)
	}
}

// Panning cuts the lines again — it has to, the window into them moved — but it
// never lays a comment out again: the cut is over lines the memo already holds.
func TestPanning_CutsTheLinesAgainAndRendersNoComment(t *testing.T) {
	t.Parallel()

	m := wideThread(t, 34, 24)
	before := len(m.blocks.made)
	for range 10 {
		next, _ := m.Update(keyPress("l"))
		m, _ = next.(*Model)
		_ = m.View()
	}
	if m.pan == 0 {
		t.Fatal("nothing panned, so this measured the wrong thing")
	}
	if got := len(m.blocks.made); got != before {
		t.Errorf("panning rendered %d comments again", got-before)
	}
}

func TestBlockRendering_CostsNothingOnceMemoized(t *testing.T) {
	t.Parallel()

	m := thread(t, 200, 120, 40)
	before := len(m.blocks.made)
	for range 50 {
		_ = m.blockLines(m.cursor)
	}
	if got := len(m.blocks.made); got != before {
		t.Errorf("drawing one comment fifty times rendered it %d more times", got-before)
	}
}
