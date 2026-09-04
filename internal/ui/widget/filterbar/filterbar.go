// Package filterbar draws the strip of chips a list-shaped view puts under its
// rows to say what it is narrowed by: one chip per facet naming its values,
// not one per value — the grouping filter.Terms already promises. Every
// gesture on it goes through filter.Terms.Toggle and filter.Terms.Without, so
// the keyboard, the chips and the picker cannot disagree about what is in
// force.
package filterbar

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

// Bar draws the line and resolves clicks on it, the way any other view holds
// widget.Zoner-backed chrome. A view keeps one field of this type; nothing
// here renders on its own.
type Bar struct {
	zones widget.Zoner

	line string
	at   renderKey
	sty  *styles
}

// New builds a bar that marks its zones through z. A zero Zoner marks nothing,
// which is what a bar built for a benchmark or a headless test gets.
func New(z widget.Zoner) *Bar { return &Bar{zones: z} }

type styles struct {
	gen    int
	muted  lipgloss.Style
	prompt lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{gen: t.Gen, muted: t.Muted, prompt: t.Accent}
}

// renderKey is everything a rendered line depends on. gen is the caller's own
// counter over the terms: filter.Terms is a slice and cannot sit in a
// comparable key, so a view that changes the terms bumps its own generation
// the way list.Model already counted them before this widget existed.
type renderKey struct {
	gen, width, styleGen int
	mouse                bool
}

// FacetZone is the mouse zone over the × that drops one facet's whole clause,
// exported so a consuming view's own tests can resolve a click without
// guessing the format one landed on.
func FacetZone(f filter.Facet) string { return "facet:" + f.Label() }

// ValueZone is the mouse zone over one value's name inside its chip.
func ValueZone(t filter.Term) string { return "value:" + t.Facet.Label() + ":" + t.ID }

// Render draws the line: one chip per facet, its values joined by a comma, the
// × that drops the facet, and clearKey at the right end to drop every term at
// once. gen is the caller's own generation counter over term changes, so a
// scroll that changes nothing about the terms costs a cache hit rather than a
// rebuild.
//
// It draws nothing when there is nothing in force, which is what keeps a view
// with no filter from losing a row to a bar it never needed.
func (b *Bar) Render(terms filter.Terms, width int, t *kernel.Theme, clearKey string, gen int) string {
	if len(terms) == 0 {
		return ""
	}
	key := renderKey{gen: gen, width: width, styleGen: t.Gen, mouse: b.zones.Enabled()}
	if b.line != "" && key == b.at {
		return b.line
	}
	if b.sty == nil || b.sty.gen != t.Gen {
		b.sty = newStyles(t)
	}
	hint := "  " + clearKey + " clears everything"
	room, used := max(width-ansi.StringWidth(hint), 8), 0
	ell, cross := t.Glyphs.Ellipsis, t.Glyphs.Cross
	var out strings.Builder
	for i := 0; i < len(terms); {
		j := i
		for j < len(terms) && terms[j].Facet == terms[i].Facet {
			j++
		}
		group := terms[i:j]
		sep := ""
		if i > 0 {
			sep = "  " + t.Glyphs.Separator + " "
		}
		slack := room - used
		if slack <= 0 {
			break
		}
		left := slack - ansi.StringWidth(sep)
		if left <= 0 {
			out.WriteString(b.sty.muted.Render(ansi.Truncate(" "+ell, slack, "")))
			break
		}
		out.WriteString(b.sty.muted.Render(sep))
		plain := group[0].Facet.Label() + ": " + strings.Join(labelsOf(group), ", ") + " " + cross
		if ansi.StringWidth(plain) > left {
			trimmed := ansi.Truncate(plain, left, ell)
			out.WriteString(b.zones.Mark(FacetZone(group[0].Facet), b.sty.prompt.Render(trimmed)))
			used += ansi.StringWidth(sep) + ansi.StringWidth(trimmed)
			i = j
			continue
		}
		out.WriteString(b.sty.muted.Render(group[0].Facet.Label() + ": "))
		for vi, term := range group {
			if vi > 0 {
				out.WriteString(b.sty.muted.Render(", "))
			}
			out.WriteString(b.zones.Mark(ValueZone(term), b.sty.prompt.Render(term.Label)))
		}
		out.WriteString(b.sty.muted.Render(" "))
		out.WriteString(b.zones.Mark(FacetZone(group[0].Facet), b.sty.muted.Render(cross)))
		used += ansi.StringWidth(sep) + ansi.StringWidth(plain)
		i = j
	}
	out.WriteString(b.sty.muted.Render(hint))
	b.line, b.at = out.String(), key
	return b.line
}

func labelsOf(group []filter.Term) []string {
	out := make([]string, len(group))
	for i, term := range group {
		out[i] = term.Label
	}
	return out
}

// Click resolves a mouse click against the zones the last Render call marked:
// a click on a value's name answers with just that value, and a click on the ×
// answers with the whole facet it belongs to. Neither drops anything itself —
// the caller runs filter.Terms.Toggle or filter.Terms.Without and re-runs its
// search, the way the terms it drew came from the caller in the first place.
func (b *Bar) Click(msg tea.MouseClickMsg, terms filter.Terms) (dropFacet filter.Facet, dropValue filter.Term, ok bool) {
	for _, term := range terms {
		if b.zones.Hit(ValueZone(term), msg) {
			return filter.FacetNone, term, true
		}
	}
	seen := make(map[filter.Facet]bool, len(filter.Facets))
	for _, term := range terms {
		if seen[term.Facet] {
			continue
		}
		seen[term.Facet] = true
		if b.zones.Hit(FacetZone(term.Facet), msg) {
			return term.Facet, filter.Term{}, true
		}
	}
	return filter.FacetNone, filter.Term{}, false
}
