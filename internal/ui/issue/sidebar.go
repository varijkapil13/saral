package issue

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

// The two cells a gutter is drawn from.
const (
	railTrack = 0
	railThumb = 1
)

// railRun is where a region's scrollbar thumb sits: the rows it covers, and
// whether the region has the keyboard. A region whose content fits has no thumb,
// which is how "there is more below" is said without spending a word or a row on
// it — and in the no-colour theme the thumb is what carries the meaning, since
// the rail itself is only faint against bold there.
type railRun struct {
	from, to int
	focused  bool
}

func railFor(h, total, top int, focused bool) railRun {
	if h <= 0 || total <= h {
		return railRun{from: -1, to: -1, focused: focused}
	}
	size := max(h*h/total, 1)
	at := top * (h - size) / (total - h)
	return railRun{from: at, to: at + size, focused: focused}
}

// cell is the gutter's glyph for one row.
func (r railRun) cell(s *styles, at int) string {
	part := railTrack
	if at >= r.from && at < r.to {
		part = railThumb
	}
	if r.focused {
		return s.railOn[part]
	}
	return s.railOff[part]
}

// marker is the zone sequence a region opens and closes with. A region is drawn
// row by row beside another one, so it cannot be marked as a block of lines: the
// sequence goes where the region starts and again where it ends, and the
// rectangle between them is what bubblezone records. It is empty when the mouse
// is off, and then nothing is written into the frame at all.
func marker(z widget.Zoner, name string) string {
	const probe = "\x00"
	marked := z.Mark(name, probe)
	at := strings.Index(marked, probe)
	if at <= 0 {
		return ""
	}
	return marked[:at]
}

// appendRegion writes one row of one region: the zone marker where the region
// opens and closes, its gutter cell, then the line the region's own offset puts
// there. Every row is exactly the box's width, so composing a frame is
// concatenation rather than measurement.
func (m *Model) appendRegion(buf []byte, r region, row int) []byte {
	b := m.lay.boxes[r]
	if !b.drawn() {
		return buf
	}
	if row == 0 {
		buf = append(buf, m.marks[r]...)
	}
	w := b.content()
	buf = append(buf, m.rails[r].cell(m.styles, row)...)
	lines, widths := m.rendered(r)
	at := row
	pan := 0
	if r != regionComments {
		at += m.tops[r]
		pan = m.pans[r]
	}
	switch {
	case at < 0 || at >= len(lines):
		buf = append(buf, m.blank[:w]...)
	case pan == 0 && widths[at] <= w:
		buf = append(buf, lines[at]...)
		buf = append(buf, m.blank[:w-widths[at]]...)
	default:
		buf = m.appendWindow(buf, lines[at], widths[at], pan, w)
	}
	if row == b.h-1 {
		buf = append(buf, m.marks[r]...)
	}
	return buf
}

// appendDivider writes the column between the description and the sidebar, with
// the zone marker at the top of it and again at the bottom so that the rectangle
// bubblezone records is the whole boundary rather than one cell of it.
//
// The column itself stays blank. A rule drawn here would stand next to the
// sidebar's own gutter, and two vertical lines in adjacent columns say less than
// one — so what says the boundary can be moved is the ? overlay, the palette and
// docs/UX.md's gesture table, not a glyph.
func (m *Model) appendDivider(buf []byte, row int) []byte {
	if row == 0 {
		buf = append(buf, m.dividerMark...)
	}
	buf = append(buf, ' ')
	if row == m.lay.paneH-1 {
		buf = append(buf, m.dividerMark...)
	}
	return buf
}

// appendWindow writes the w cells of a line that start at pan, with the theme's
// ellipsis where there is still more of it to the right. Cutting is done here and
// never at build time, because the same rendered line is drawn at every pan.
func (m *Model) appendWindow(buf []byte, line string, width, pan, w int) []byte {
	cut := line
	if pan > 0 {
		cut = ansi.Cut(line, pan, pan+w)
	}
	if width-pan > w {
		cut = ansi.Truncate(cut, w, m.deps.Theme.Glyphs.Ellipsis)
	}
	got := ansi.StringWidth(cut)
	buf = append(buf, cut...)
	if got < w {
		buf = append(buf, m.blank[:w-got]...)
	}
	return buf
}

// tell hands a message to the thread this pane holds. The thread is a child
// rather than an entry on the kernel's stack, so nothing else delivers to it.
func (m *Model) tell(msg tea.Msg) tea.Cmd {
	if m.thread == nil {
		return nil
	}
	view, cmd := m.thread.Update(msg)
	m.thread = view
	return cmd
}

// sizeThread gives the thread the sidebar box rather than the pane's own. While
// it is full screen the kernel owns its size, and this leaves it alone.
func (m *Model) sizeThread() tea.Cmd {
	if m.thread == nil || m.pushed {
		return nil
	}
	w, h := 0, 0
	if b := m.lay.boxes[regionComments]; b.drawn() {
		w, h = b.content(), b.h
	}
	if w == m.threadAt.w && h == m.threadAt.h {
		return nil
	}
	m.threadAt.w, m.threadAt.h = w, h
	return m.tell(kernel.SizeMsg{Width: w, Height: h})
}

// readThread takes the thread's own frame apart into the rows the sidebar draws.
// The thread decides what those rows say — the scroll, the selection and the
// count line are all its own — so the only thing kept here is the split, and only
// while the box it fills is on screen.
func (m *Model) readThread() {
	if !m.lay.shows(regionComments) || !m.lay.boxes[regionComments].drawn() {
		m.rows, m.rowWidths = m.rows[:0], m.rowWidths[:0]
		return
	}
	raw := m.thread.View()
	if raw == m.threadRaw && len(m.rows) > 0 {
		return
	}
	m.threadRaw = raw
	m.rows, m.rowWidths = m.rows[:0], m.rowWidths[:0]
	for rest := raw; ; {
		at := strings.IndexByte(rest, '\n')
		if at < 0 {
			m.rows = append(m.rows, rest)
			m.rowWidths = append(m.rowWidths, ansi.StringWidth(rest))
			return
		}
		m.rows = append(m.rows, rest[:at])
		m.rowWidths = append(m.rowWidths, ansi.StringWidth(rest[:at]))
		rest = rest[at+1:]
	}
}
