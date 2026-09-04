package filterbar

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

// BenchmarkRenderMemoHit is the steady state a scrolling view actually pays
// for: the terms have not changed since the last frame, so the bar has to
// answer from its own memo rather than rebuild the line.
func BenchmarkRenderMemoHit(b *testing.B) {
	bar := New(widget.Zoner{})
	terms := filter.Terms{ada, ben, prog, bug}
	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	_ = bar.Render(terms, 120, theme, "ctrl+g", 1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = bar.Render(terms, 120, theme, "ctrl+g", 1)
	}
}

// BenchmarkRenderMiss is a fresh build, which is what a term change or a
// resize costs once.
func BenchmarkRenderMiss(b *testing.B) {
	bar := New(widget.Zoner{})
	terms := filter.Terms{ada, ben, prog, bug}
	theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_ = bar.Render(terms, 120, theme, "ctrl+g", i)
	}
}
