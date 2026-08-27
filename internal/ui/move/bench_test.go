package move

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// stocked is a wizard already on its confirm screen over n issues, without a site
// behind it: the vocabulary arrives as the message a read would have produced,
// which is what keeps a benchmark off the network and off a fake's locks.
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
	m, ok := New(d, WithIssues(benchIssues(n))).(*Model)
	if !ok {
		tb.Fatal("New did not return a *Model")
	}
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)
	m.target = "OTHER"
	next, _ = m.Update(vocabularyMsg{gen: m.gen, project: "OTHER", types: benchVocabulary()})
	m, _ = next.(*Model)
	m.remaps = defaultRemap(sourceStatuses(m.issues), m.targetStatuses())
	m.schema = true
	m.step = stepConfirm
	m.forget()
	_ = m.View()
	return m
}

func benchIssues(n int) []jira.Issue {
	statuses := benchStatuses()
	out := make([]jira.Issue, 0, n)
	for i := range n {
		out = append(out, jira.Issue{
			Key:     "PROJ-" + strconv.Itoa(i+1),
			Summary: "a summary long enough to need truncating in a narrow pane " + strconv.Itoa(i+1),
			Project: jira.ProjectRef{Key: "PROJ"},
			Status:  statuses[i%len(statuses)],
		})
	}
	return out
}

func benchStatuses() []jira.Status {
	return []jira.Status{
		{ID: "10201", Name: "Triage", Category: jira.CategoryToDo},
		{ID: "10202", Name: "Building", Category: jira.CategoryInProgress},
		{ID: "10203", Name: "Shipped", Category: jira.CategoryDone},
	}
}

func benchVocabulary() []jira.IssueTypeStatuses {
	return []jira.IssueTypeStatuses{
		{Type: jira.IssueType{ID: "10301", Name: "Story"}, Statuses: benchStatuses()},
		{Type: jira.IssueType{ID: "10302", Name: "Defect"}, Statuses: benchStatuses()},
	}
}

// scrollOver walks the confirm screen's list of keys and draws a frame each time,
// going back to the top on reaching the bottom so that every iteration is a memo
// miss rather than an average of two states.
func scrollOver(b *testing.B, issues int) {
	m := stocked(b, issues, 120, 30)
	down := tea.KeyPressMsg{Code: 'j', Text: "j"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		if m.top >= max(m.rowCount()-m.rowsHeight(), 0) {
			m.top = 0
		}
		_ = m.View()
	}
}

func BenchmarkMoveConfirmScroll1000(b *testing.B) { scrollOver(b, 1000) }
func BenchmarkMoveConfirmScroll20(b *testing.B)   { scrollOver(b, 20) }

// BenchmarkMoveKeystroke is the path a reader spends time on: a key that moves
// the window over a thousand keys, and the frame it draws.
func BenchmarkMoveKeystroke(b *testing.B) {
	m := stocked(b, 1000, 120, 40)
	keys := []tea.Msg{
		tea.KeyPressMsg{Code: 'j', Text: "j"},
		tea.KeyPressMsg{Code: 'k', Text: "k"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkMoveRedraw200x60(b *testing.B) {
	m := stocked(b, 1000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

// BenchmarkMoveRemapKeystroke is the other keystroke path: cycling a target
// status, which rebuilds the row and the mapping above it.
func BenchmarkMoveRemapKeystroke(b *testing.B) {
	m := stocked(b, 1000, 120, 40)
	m.step, m.cursor = stepStatus, 0
	m.forget()
	_ = m.View()
	keys := []tea.Msg{
		tea.KeyPressMsg{Code: 'l', Text: "l"},
		tea.KeyPressMsg{Code: 'h', Text: "h"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}
