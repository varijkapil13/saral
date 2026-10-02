package issue

import (
	"strconv"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/sortpick"
	"github.com/varijkapil13/saral/pkg/jira"
)

func sortableChildren(n int) []jira.Issue {
	out := manyChildren(n)
	names := [...]string{"Urgent", "Normal", "Whenever", "Soon", "Later"}
	for i := range out {
		at := (i * 7) % len(names)
		out[i].Priority = &jira.Priority{ID: strconv.Itoa(10401 + at), Name: names[at]}
		out[i].Summary = "Child " + strconv.Itoa((i*37)%n) + " of the billing rewrite"
		out[i].Created = time.Date(2025, time.January, 1+i%28, 9, 0, 0, 0, time.UTC)
	}
	return out
}

func benchSortSheet(b *testing.B) *sheet {
	b.Helper()

	prev := childSortNow.Load()
	zero := sortpick.Choice{}
	childSortNow.Store(&zero)
	b.Cleanup(func() { childSortNow.Store(prev) })

	d := kernel.Deps{
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Now:   func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	kind := &childrenKind{seed: &childSeed{issues: sortableChildren(500)}}
	s := newSheet(d, jira.Issue{Key: "PROJ-3", Summary: "Billing rewrite"}, kind)
	s.Update(kernel.SizeMsg{Width: 120, Height: 30})
	_ = s.Init()
	_ = s.View()
	return s
}

func BenchmarkChildSort500(b *testing.B) {
	issues := sortableChildren(500)
	o := &childOrder{prio: map[string]int{"10401": 0, "10402": 1, "10403": 2, "10404": 3, "10405": 4}}
	choices := [...]sortpick.Choice{{Field: fieldPriority}, {Field: "summary", Desc: true}, {Field: "key"}}
	idx := make([]int, 0, len(issues))
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		idx = orderIndex(issues, choices[i%len(choices)], o, idx)
	}
}

func BenchmarkChildSortChoose500(b *testing.B) {
	s := benchSortSheet(b)
	open, right, enter := keyPress("s"), keyPress("right"), keyPress("enter")
	_, _ = s.Update(open)
	for range 4 {
		_, _ = s.Update(right)
	}
	_, _ = s.Update(enter)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = s.Update(open)
		_, _ = s.Update(enter)
		_ = s.View()
	}
}

func BenchmarkChildSortPickerKey(b *testing.B) {
	s := benchSortSheet(b)
	_, _ = s.Update(keyPress("s"))
	_ = s.View()
	right, left := keyPress("right"), keyPress("left")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		press := right
		if i%2 == 1 {
			press = left
		}
		_, _ = s.Update(press)
		_ = s.View()
	}
}
