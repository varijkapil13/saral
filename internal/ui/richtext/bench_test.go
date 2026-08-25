package richtext

import (
	"fmt"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// long is a document of the size a real description reaches: the kitchen sink
// over and over, which is 40 blocks a copy.
func long(t testing.TB, copies int) adf.Doc {
	t.Helper()
	one := load(t, "kitchen.json")
	nodes := make([]adf.Node, 0, len(one.Content)*copies)
	for range copies {
		nodes = append(nodes, one.Content...)
	}
	return adf.NewDoc(nodes...)
}

func BenchmarkRender(b *testing.B) {
	for _, width := range []int{120, 80, 40} {
		d := load(b, "kitchen.json")
		opt := options(width)
		opt.Styles = NewStyles(colourPalette())
		b.Run(fmt.Sprint(width), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				Render(d, opt)
			}
		})
	}
}

func BenchmarkRenderLong(b *testing.B) {
	d := long(b, 5)
	opt := options(80)
	opt.Styles = NewStyles(colourPalette())
	b.ReportAllocs()
	for b.Loop() {
		Render(d, opt)
	}
}

func BenchmarkRenderNoColor(b *testing.B) {
	d := load(b, "kitchen.json")
	opt := options(80)
	b.ReportAllocs()
	for b.Loop() {
		Render(d, opt)
	}
}

func BenchmarkSummary(b *testing.B) {
	d := load(b, "kitchen.json")
	b.ReportAllocs()
	for b.Loop() {
		Summary(d, 80)
	}
}
