package backlog

import (
	"testing"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
)

func BenchmarkBacklogFirstPaintFromCache(b *testing.B) {
	b.Setenv("SARAL_CACHE_DIR", b.TempDir())
	if err := config.SaveLook(card.Lines.Word()); err != nil {
		b.Fatal(err)
	}
	card.ResetRecall()
	b.Cleanup(card.ResetRecall)

	msg := benchLoaded(0, 50)
	cache := newFakeCache()
	cache.hold("PROJ", msg.config.BoardID, app.BacklogSnapshot{
		Config: msg.config, Sprints: msg.sprints, Field: msg.field, Issues: msg.page.Items,
	}, false)
	d := withCache(stocked(b, 20, 120, 40).deps, cache)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		view, _ := New(d).(*Model)
		next, _ := view.Update(kernel.SizeMsg{Width: 120, Height: 40})
		m, _ := next.(*Model)
		if len(m.rows) == 0 {
			b.Fatal("the stored backlog drew no rows")
		}
		_ = m.View()
	}
}
