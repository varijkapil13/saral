package card

import (
	"fmt"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

var benchCases = []struct {
	look  Look
	width int
}{
	{Roomy, 24}, {Roomy, 120}, {Compact, 24}, {Compact, 120},
}

func benchRender(look Look, width int) func(*testing.B) {
	return func(b *testing.B) {
		theme := kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())
		sty := NewStyles(theme)
		f := listFacts(theme.Glyphs)
		fr := Frame{Width: width, Look: look, Glyphs: theme.Glyphs}
		dst := make([]string, 0, look.Lines())
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			dst = Render(dst[:0], f, State{}, sty, fr)
		}
	}
}

// BenchmarkCardRender is one resting card drawn with nothing memoized: what a
// view's memo miss pays for the card itself.
func BenchmarkCardRender(b *testing.B) {
	for _, c := range benchCases {
		b.Run(fmt.Sprintf("%s/%d", c.look.Word(), c.width), benchRender(c.look, c.width))
	}
}
