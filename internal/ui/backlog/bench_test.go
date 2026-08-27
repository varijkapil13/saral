package backlog

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// stocked builds a backlog holding n issues at a given size without touching the
// network: the board, its sprints and the page all arrive as the message a read
// would have produced.
//
// It has a zone manager, which is the shape a running program has: the markers
// live inside the memoized row, so a frame that hits the memo costs what it did
// before there were any.
func stocked(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	d := kernel.Deps{
		Caps:    jira.Capabilities{TimeZone: time.UTC},
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Zones:   mgr,
		Now:     func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	view, ok := New(d).(*Model)
	if !ok {
		tb.Fatal("New did not return a *Model")
	}
	next, _ := view.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ := next.(*Model)
	next, _ = m.Update(benchLoaded(m.gen, n))
	m, _ = next.(*Model)
	_ = m.View()
	if len(m.rows) == 0 {
		tb.Fatalf("a backlog stocked with %d issues drew no rows", n)
	}
	return m
}

// benchLoaded is the answer a read of a scrum board with one active and one
// future sprint gives, with the issues spread across the three sections.
func benchLoaded(gen, n int) loadedMsg {
	field := jira.FieldRef{ID: "customfield_13402", Name: "Sprint"}
	issues := jiratest.Gen(n)
	for i := range issues {
		// Every third issue is in a sprint, so the sections are all populated
		// and the frame draws heads as well as rows.
		if i%3 == 0 {
			id := int64(1001 + i%2)
			issues[i].Fields = issues[i].Fields.With(field, jira.FieldValue{
				Kind:    jira.KindOptions,
				Options: []jira.Option{{ID: strconv.FormatInt(id, 10), Label: "Sprint " + strconv.FormatInt(id, 10)}},
			})
		}
	}
	return loadedMsg{
		gen:    gen,
		boards: []jira.Board{{ID: 10, Name: "PROJ board", Type: jira.BoardScrum, ProjectKey: "PROJ"}},
		config: jira.BoardConfig{
			BoardID: 10, Name: "PROJ board", Type: jira.BoardScrum,
			RankFieldID: "customfield_13404",
		},
		sprints: []jira.Sprint{
			{ID: 1001, BoardID: 10, Name: "Sprint 1001", State: jira.SprintActive},
			{ID: 1002, BoardID: 10, Name: "Sprint 1002", State: jira.SprintFuture},
		},
		field: field,
		page:  jira.NewPage(issues, nil),
	}
}

// scroll is the steady state: the cursor moving one row and back inside the
// window, so every frame is a memo hit.
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

// BenchmarkBacklogSteadyScroll10k and its twenty-row twin are the pair that
// matters: docs/PERFORMANCE.md asks that a ten-thousand row list and a twenty
// row list cost the same per frame.
func BenchmarkBacklogSteadyScroll10k(b *testing.B) { scroll(b, stocked(b, 10000, 120, 40)) }

func BenchmarkBacklogSteadyScroll20(b *testing.B) { scroll(b, stocked(b, 20, 120, 40)) }

// BenchmarkBacklogWalk10k walks a fresh row into view on every frame, which is
// the worst case: every frame misses the memo by construction.
//
// It goes back to the top on reaching the bottom. Without that the cursor stops
// on the last row and every iteration after the ten thousandth is a memo hit, so
// what the benchmark reports is a blend of walking and standing still, weighted
// by how many iterations the machine got through.
func BenchmarkBacklogWalk10k(b *testing.B) {
	m := stocked(b, 10000, 120, 40)
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

// BenchmarkBacklogPickAndFrame is a pick and the frame after it, on one row and
// with the cursor standing still.
//
// It stands still on purpose. A pick that walked down the list would spend its
// first screenful missing the memo and every iteration after that hitting it, so
// what it reported would be the two states averaged by however many iterations
// the machine managed — which is a number about the machine. Both renderings of
// one row are memoized after two iterations, so this is the cost of a pick and
// nothing else: if it ever clears the whole memo, the figure is a screenful.
func BenchmarkBacklogPickAndFrame(b *testing.B) {
	m := stocked(b, 10000, 120, 40)
	at := 0
	for i := range m.rows {
		if !m.rows[i].head {
			at = i
			break
		}
	}
	m.cursor = at
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.pick()
		_ = m.View()
	}
}

func BenchmarkBacklogRedraw200x60(b *testing.B) {
	m := stocked(b, 10000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkBacklogRegroup10k is the work a move does after the site accepts a
// chunk: every issue is placed into its section again and every section is put
// back in rank order.
func BenchmarkBacklogRegroup10k(b *testing.B) {
	m := stocked(b, 10000, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.regroup()
	}
}
