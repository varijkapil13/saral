package board

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// marked is the board a running program has: one with a zone manager, so every
// card and every column in it carries a marker. It is the shape the steady-state
// budget has to hold in, since a real session always has one.
//
// The configuration and the cards arrive as the messages a read would have
// produced, which is what keeps a benchmark off the network and off a fake's
// locks.
func marked(tb testing.TB, columns, cards, w, h int) *Model {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	d := kernel.Deps{
		Caps:    jira.Capabilities{Boards: jira.Capability{OK: true}, TimeZone: time.UTC},
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

	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Type: jira.BoardScrum, RankFieldID: "customfield_13404"}
	for i := range columns {
		cfg.Columns = append(cfg.Columns, jira.Column{
			Name: "Column " + strconv.Itoa(i), StatusIDs: []string{strconv.Itoa(9000 + i)},
		})
	}
	next, _ = m.Update(configMsg{gen: m.gen, cfg: cfg})
	m, _ = next.(*Model)
	next, _ = m.Update(issuesMsg{gen: m.gen, issues: manyCards(columns, cards)})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

func manyCards(columns, n int) []jira.Issue {
	out := make([]jira.Issue, 0, n)
	base := time.Date(2026, time.February, 1, 9, 0, 0, 0, time.UTC)
	for i := range n {
		out = append(out, jira.Issue{
			ID:      strconv.Itoa(20000 + i),
			Key:     "PROJ-" + strconv.Itoa(i+1),
			Summary: "Rework the nightly export so that it stops retrying forever",
			Status:  jira.Status{ID: strconv.Itoa(9000 + i%columns), Name: "Column " + strconv.Itoa(i%columns)},
			Updated: base.Add(time.Duration(i) * time.Minute),
		})
	}
	return out
}

// BenchmarkBoardView10k is a frame of a board holding ten thousand cards with
// nothing moving: every line and every card is memoized, so what is left is the
// frame string the view has to return.
func BenchmarkBoardView10k(b *testing.B) {
	m := marked(b, 4, 10000, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkBoardView20 is the same frame over twenty cards, which is what the
// ten-thousand one is compared against: a virtualized board does the same work
// for both.
func BenchmarkBoardView20(b *testing.B) {
	m := marked(b, 4, 20, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkBoardWideView is a board with fifty columns, only a handful of which
// fit. It is compared against a board with four to say that the columns are
// virtualized as well as the rows.
func BenchmarkBoardWideView(b *testing.B) {
	m := marked(b, 50, 10000, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkBoardWalk10k walks a fresh card into view on every frame, which is
// the worst case: every frame misses the memo by construction.
//
// It goes back to the top on reaching the bottom. Without that the cursor stops
// on the last card and every iteration after it is a memo hit, so what the
// benchmark reports is a blend of walking and standing still, weighted by how
// many iterations the machine got through — which is a number about the machine
// and not about the code.
func BenchmarkBoardWalk10k(b *testing.B) {
	m := marked(b, 4, 10000, 120, 40)
	var down, top tea.Msg = keyPress("j"), keyPress("home")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
		if m.curRow >= m.columnLen(m.curCol)-1 {
			next, _ = m.Update(top)
			m, _ = next.(*Model)
		}
	}
}

// BenchmarkBoardAcross walks sideways, which is the axis a list does not have.
// It goes back to the first column rather than stopping at the last, for the
// reason BenchmarkBoardWalk10k does.
func BenchmarkBoardAcross(b *testing.B) {
	m := marked(b, 50, 10000, 120, 40)
	var right, home tea.Msg = keyPress("l"), keyPress("h")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		msg := right
		if i%2 == 1 {
			msg = home
		}
		next, _ := m.Update(msg)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkBoardRedraw200x60(b *testing.B) {
	m := marked(b, 8, 10000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkCardRender is one card built from scratch, which is what a memo miss
// costs and what the frame ceilings are sized against.
func BenchmarkCardRender(b *testing.B) {
	m := marked(b, 4, 200, 120, 40)
	iss := m.issueAt(0, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = renderCard(iss, m.lay.cell, false, false, m.styles, m.deps.Theme, m.plan)
	}
}
