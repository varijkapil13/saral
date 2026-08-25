// Package widget holds the interaction pieces more than one view needs: the
// zone marking every clickable element does, the double-click Bubble Tea does
// not report, the drag a two-pane view will need, and the window a scrolled
// pane draws through.
//
// Nothing here renders anything on its own. A widget is a helper a view holds,
// not a model it embeds, so a view keeps its own Update and its own frame.
package widget

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

// Zoner is one view instance's slice of the zone manager: the prefix that keeps
// its ids apart from every other view's, and the two calls it makes with them.
//
// The manager is absent in a session with nowhere to put a mouse — a benchmark,
// a test that never clicks — and disabled in one started with mouse = false.
// Both answer the same way: Mark writes nothing into the frame and Hit misses,
// so a view needs no branch of its own for either.
//
// The zero Zoner is usable and marks nothing, which is what a view built with
// no manager gets.
type Zoner struct {
	mgr    *zone.Manager
	prefix string
}

// NewZoner mints one view instance's prefix. Call it once, where the view is
// built: a prefix per frame would put a fresh id in the manager's permanent id
// map on every draw.
func NewZoner(mgr *zone.Manager) Zoner {
	if mgr == nil {
		return Zoner{}
	}
	return Zoner{mgr: mgr, prefix: mgr.NewPrefix()}
}

// Enabled reports whether the mouse is on at all. A view asks when it would
// otherwise draw something only a pointer can reach.
func (z Zoner) Enabled() bool { return z.mgr != nil && z.mgr.Enabled() }

// ID is the manager-wide id a name resolves to, which is what a test looks a
// zone up by.
func (z Zoner) ID(name string) string { return z.prefix + name }

// Mark wraps s so that a click inside it resolves back to name. The markers are
// private escape sequences of zero display width, so a marked string still
// measures and truncates as what it draws.
func (z Zoner) Mark(name, s string) string {
	if z.mgr == nil {
		return s
	}
	return z.mgr.Mark(z.prefix+name, s)
}

// MarkLines marks a block as one zone: the id opens on the first line and
// closes on the last, so the rectangle recorded covers the whole block rather
// than the shape of its last line.
func (z Zoner) MarkLines(name string, lines []string) []string {
	if z.mgr == nil || len(lines) == 0 {
		return lines
	}
	marked := z.Mark(name, strings.Join(lines, "\n"))
	if marked == "" {
		return lines
	}
	return strings.Split(marked, "\n")
}

// Hit reports whether a mouse message landed inside the element marked name.
// This is the only way a view resolves a click: bubblezone records where a
// marked string was actually drawn, and arithmetic on coordinates does not
// survive a resize, a scroll or a truncated cell.
func (z Zoner) Hit(name string, msg tea.MouseMsg) bool {
	if z.mgr == nil {
		return false
	}
	return z.mgr.Get(z.prefix + name).InBounds(msg)
}
