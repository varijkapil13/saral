package filterbar

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

var (
	oneFacet = filter.Terms{ada, ben}
	// threeFacets keeps two values on the first facet, so the golden also
	// proves the grouping holds once there is more than one thing on the line.
	threeFacets = filter.Terms{ada, ben, prog, bug}
)

// TestBar_Golden is docs/FILTERS.md's own definition of done: a golden at 80
// and 120 columns, with one facet in force and with three, in both the nerd
// and the ascii glyph sets.
func TestBar_Golden(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		width  int
		glyphs kernel.Glyphs
		terms  filter.Terms
		golden string
	}{
		"one facet, 80 columns, nerd":      {80, kernel.NerdGlyphs(), oneFacet, "one_facet_80_nerd.golden"},
		"one facet, 80 columns, ascii":     {80, kernel.ASCIIGlyphs(), oneFacet, "one_facet_80_ascii.golden"},
		"one facet, 120 columns, nerd":     {120, kernel.NerdGlyphs(), oneFacet, "one_facet_120_nerd.golden"},
		"one facet, 120 columns, ascii":    {120, kernel.ASCIIGlyphs(), oneFacet, "one_facet_120_ascii.golden"},
		"three facets, 80 columns, nerd":   {80, kernel.NerdGlyphs(), threeFacets, "three_facets_80_nerd.golden"},
		"three facets, 80 columns, ascii":  {80, kernel.ASCIIGlyphs(), threeFacets, "three_facets_80_ascii.golden"},
		"three facets, 120 columns, nerd":  {120, kernel.NerdGlyphs(), threeFacets, "three_facets_120_nerd.golden"},
		"three facets, 120 columns, ascii": {120, kernel.ASCIIGlyphs(), threeFacets, "three_facets_120_ascii.golden"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			theme := kernel.NewTheme(kernel.ThemeNoColor, true, tc.glyphs)
			bar := New(widget.Zoner{})
			golden(t, tc.golden, bar.Render(tc.terms, tc.width, theme, "ctrl+g", 1))
		})
	}
}
