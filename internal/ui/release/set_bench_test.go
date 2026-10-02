package release

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func benchSet(n, groups int) Set {
	owners := []Owner{owner("EX"), owner("OPS"), owner("WEB"), owner("APP"), owner("DOC")}
	versions := benchVersions(n)
	s := Set{Title: "Delivery", Excluded: map[string]bool{}}
	for i := range versions {
		s.Members = append(s.Members, Member{Version: versions[i], Project: owners[i%len(owners)]})
		if i%25 == 24 {
			s.Excluded[versions[i].ID] = true
		}
	}
	per := max(n/groups, 1)
	for g := range groups {
		ids := make([]string, 0, per)
		for i := g * per; i < min((g+1)*per, n); i++ {
			ids = append(ids, versions[i].ID)
		}
		s.Groups = append(s.Groups, Group{Name: "launch-" + strconv.Itoa(g), VersionIDs: ids})
	}
	return s
}

func stockedSet(tb testing.TB, n, groups, w, h int) *Model {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	d := kernel.Deps{
		Caps:  fullCaps(),
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Zones: mgr,
		Now:   func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	m := newSetModel(d, benchSet(n, groups))
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

func setScrollOver(b *testing.B, n, groups int) {
	m := stockedSet(b, n, groups, 120, 40)
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

func BenchmarkReleaseSetSteadyScroll2000(b *testing.B) { setScrollOver(b, 2000, 200) }
func BenchmarkReleaseSetSteadyScroll20(b *testing.B)   { setScrollOver(b, 20, 2) }

// The letter matches nearly every version, which is the filter's worst case.
func BenchmarkReleaseSetFindKeystroke(b *testing.B) {
	m := stockedSet(b, 2000, 200, 120, 40)
	next, _ := m.Update(keyPress("/"))
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

func BenchmarkReleaseSetRegroupKeystroke(b *testing.B) {
	m := stockedSet(b, 2000, 200, 120, 40)
	var v tea.Msg = keyPress("v")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(v)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkReleaseSetRedraw200x60(b *testing.B) {
	m := stockedSet(b, 2000, 200, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}
