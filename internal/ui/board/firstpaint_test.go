package board

import (
	"strconv"
	"testing"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// BenchmarkBoardFirstPaintFromCache is what kernel.FirstPaint pays for the
// board: building it over a stored board and drawing its first frame.
func BenchmarkBoardFirstPaintFromCache(b *testing.B) {
	cfg := jira.BoardConfig{BoardID: 1, Name: "Ledger", Type: jira.BoardScrum, RankFieldID: "customfield_13404"}
	for i := range 4 {
		cfg.Columns = append(cfg.Columns, jira.Column{
			Name: "Column " + strconv.Itoa(i), StatusIDs: []string{strconv.Itoa(9000 + i)},
		})
	}
	cache := newFakeCache()
	cache.hold("PROJ", cfg.BoardID, appcache.BoardSnapshot{Config: cfg, Issues: manyCards(4, 200), NoSprints: true}, false)
	d := withCache(testDeps(nil), cache)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		view, _ := New(d).(*Model)
		next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 40})
		m, _ := next.(*Model)
		if !m.drawable() {
			b.Fatal("the stored board drew no columns")
		}
		_ = m.View()
	}
}
