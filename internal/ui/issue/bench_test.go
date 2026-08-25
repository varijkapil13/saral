package issue

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// benchPane is the pane as a reader meets it: an issue read in full, a long
// description, a thread beside it, and one frame already drawn so the memos are
// warm.
func benchPane(tb testing.TB, w, h int) *Model {
	tb.Helper()

	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(20)))
	full, err := f.Issue(tb.Context(), "PROJ-12")
	if err != nil {
		tb.Fatal(err)
	}
	full.Requested = jira.AllFields()
	full.Description = longDoc(80)
	for i := range 20 {
		body := adf.NewDoc(adf.NewNode("paragraph", adf.NewText(
			"Comment "+strconv.Itoa(i+1)+", worth a couple of lines of somebody's day.")))
		if _, err := f.AddComment(tb.Context(), "PROJ-12", body); err != nil {
			tb.Fatal(err)
		}
	}

	d := kernel.Deps{
		Jira:  f,
		Caps:  jira.Capabilities{TimeZone: time.UTC},
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Now:   func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	view, ok := New(d, full).(*Model)
	if !ok {
		tb.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ := next.(*Model)
	m = settle(tb, m, m.Init())
	next, _ = m.Update(loadedMsg{gen: m.gen, issue: full})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

// settle runs the commands a pane returned until nothing is left, which is what
// gets the thread read before anything is measured.
func settle(tb testing.TB, m *Model, cmd tea.Cmd) *Model {
	tb.Helper()

	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 2000 {
			tb.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if _, isStatus := msg.(kernel.StatusMsg); isStatus {
			continue
		}
		view, follow := m.Update(msg)
		m, _ = view.(*Model)
		queue = append(queue, follow)
	}
	return m
}

// benchDrag is the pane with its boundary held, which is the state a stream of
// motion messages arrives in. The press is made on the widget directly, because
// a benchmark has no zone manager for a hit test to resolve through.
func benchDrag(tb testing.TB, w, h int) (m *Model, boundaryX int) {
	tb.Helper()

	m = benchPane(tb, w, h)
	m.dragFrom, m.dragSide = m.split, sideWidth(m.width, m.split)
	boundaryX = m.width - m.dragSide - divider
	if !m.drag.Start(dividerZone, pressAt(boundaryX, headerHeight)) {
		tb.Fatal("the press started no drag")
	}
	return m, boundaryX
}

func BenchmarkIssueView(b *testing.B) {
	m := benchPane(b, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkIssueViewNarrow(b *testing.B) {
	m := benchPane(b, 80, 20)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkIssueRedraw200x60(b *testing.B) {
	m := benchPane(b, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkIssueScroll is the steady state a held-down j reaches: a keypress and
// the frame it produces, with every memo warm.
func BenchmarkIssueScroll(b *testing.B) {
	m := benchPane(b, 120, 40)
	down, up := keyPress("j"), keyPress("k")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		press := down
		if i%2 == 1 {
			press = up
		}
		next, _ := m.Update(press)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// BenchmarkIssueDragFrame is a frame drawn while the boundary is held. Holding
// one is not a state the pane draws differently, so this is the frame at rest
// with a gesture under way over it.
func BenchmarkIssueDragFrame(b *testing.B) {
	m, _ := benchDrag(b, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkIssueDragHold is the half of a drag that moves the pointer without
// moving the boundary — up and down the column it grabbed. Most of the messages
// a real gesture sends are these, and none of them may cost a render.
func BenchmarkIssueDragHold(b *testing.B) {
	m, x := benchDrag(b, 120, 40)
	up, down := motionAt(x, headerHeight+1), motionAt(x, headerHeight+2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		at := up
		if i%2 == 1 {
			at = down
		}
		next, _ := m.Update(at)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// BenchmarkIssueDragMove is the other half: a motion that really does move the
// boundary, which gives both panes a width they have not been rendered at. That
// is a resize of two regions and costs what one costs, which is what
// BenchmarkIssueResize is here to be compared against.
func BenchmarkIssueDragMove(b *testing.B) {
	m, x := benchDrag(b, 120, 40)
	here, over := motionAt(x, headerHeight+1), motionAt(x-1, headerHeight+1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		at := here
		if i%2 == 1 {
			at = over
		}
		next, _ := m.Update(at)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkIssueResize(b *testing.B) {
	m := benchPane(b, 120, 40)
	wide, narrow := kernel.SizeMsg{Width: 120, Height: 40}, kernel.SizeMsg{Width: 118, Height: 40}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		to := wide
		if i%2 == 1 {
			to = narrow
		}
		next, _ := m.Update(to)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func TestFullRedraw_StaysUnderTheBudgetAt200x60(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkIssueRedraw200x60)
	if per := time.Duration(res.NsPerOp()); per > 4*time.Millisecond {
		t.Errorf("a full redraw at 200x60 took %s, want under 4ms", per)
	}
}

func TestKeystrokeToFrame_StaysUnderTheBudget(t *testing.T) {
	t.Parallel()

	res := testing.Benchmark(BenchmarkIssueScroll)
	if per := time.Duration(res.NsPerOp()); per > 16*time.Millisecond {
		t.Errorf("keystroke to frame took %s, want under 16ms", per)
	}
}
