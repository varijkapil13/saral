package palette

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

const (
	// marker is the gutter the selected row's arrow sits in.
	marker = 2
	gap    = 2
	// headHeight is the filter line and the rule under it.
	headHeight = 2
	// inputPrompt is what the filter's own prompt costs it.
	inputPrompt = 2
	minTitle    = 20
	// maxTitle keeps the group beside the title rather than at the far edge of a
	// wide terminal, with the slack between the group and the keys.
	maxTitle    = 44
	groupWidth  = 16
	maxKeyWidth = 12
	// refusalLines is how many reasons the empty state names. More than this is
	// a wall of text where a list of commands should be.
	refusalLines = 3
	// rowMemoLimit holds the visible window and its overscan several relayouts
	// deep. A build's commands are fewer than this; the cached issues a session
	// types its way through are not, and past it the map is cleared.
	rowMemoLimit = 256
)

// styles are the palette's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	selected lipgloss.Style
	title    lipgloss.Style
	group    lipgloss.Style
	keys     lipgloss.Style
	muted    lipgloss.Style
	rule     lipgloss.Style
	stale    lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		selected: t.Selected,
		title:    t.Base,
		group:    t.Muted,
		keys:     t.HintKey,
		muted:    t.Muted,
		rule:     t.Muted,
		// The cell is already padded to the column, and the badge's own padding
		// would push the row past it.
		stale: t.StaleBadge.Padding(0, 0),
	}
}

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width int
	title int
	group int
	keys  int
	// slack is the empty run that holds the key column against the right edge.
	slack int
}

// planLayout drops the group and then the key column until the title has room.
// The title is the only part of a row that says what the command does.
func planLayout(width, keyWidth int) layout {
	lay := layout{
		width: max(width, marker+minTitle),
		group: groupWidth,
		keys:  min(keyWidth, maxKeyWidth),
	}
	for _, drop := range []*int{&lay.group, &lay.keys} {
		lay.title = lay.width - marker - optionalWidth(lay)
		if lay.title >= minTitle {
			break
		}
		*drop = 0
	}
	lay.title = max(lay.width-marker-optionalWidth(lay), 1)
	if lay.title > maxTitle {
		lay.slack, lay.title = lay.title-maxTitle, maxTitle
	}
	return lay
}

func optionalWidth(lay layout) int {
	total := 0
	for _, w := range [...]int{lay.group, lay.keys} {
		if w > 0 {
			total += gap + w
		}
	}
	return total
}

// widestKey is how much room the key column needs. Zero means no command in
// this build has a key, and the column is not drawn at all.
func widestKey(rows []row) int {
	widest := 0
	for i := range rows {
		widest = max(widest, ansi.StringWidth(rows[i].keys))
	}
	return widest
}

// rowKey is what makes two renderings of a row the same rendering: what the row
// says, the column plan, whether it is selected and the theme it was drawn in. A
// command's title never moves under its ID, so an ID is the whole of it; an
// issue's title and the age of the copy it came from both do.
type rowKey struct {
	id       string
	text     string
	age      string
	heading  string
	lay      layout
	selected bool
	gen      int
}

// rowCache is a bounded memo of rendered rows. Past its limit it is emptied
// rather than evicted one at a time, because a scroll invalidates a screenful
// at once anyway and clearing keeps the map's capacity.
type rowCache struct {
	rows  map[rowKey]string
	limit int
}

func newRowCache(limit int) *rowCache {
	return &rowCache{rows: make(map[rowKey]string, limit), limit: limit}
}

func (c *rowCache) get(k rowKey) (string, bool) {
	s, ok := c.rows[k]
	return s, ok
}

func (c *rowCache) put(k rowKey, s string) {
	if len(c.rows) >= c.limit {
		clear(c.rows)
	}
	c.rows[k] = s
}

func (c *rowCache) reset() { clear(c.rows) }

// renderRow draws one command to exactly lay.width columns.
func renderRow(r *row, lay layout, sel bool, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(lay.width + 32)

	writeMarker(&b, sel, t)
	title := padTruncate(r.cmd.Title, lay.title, ell)
	if sel {
		b.WriteString(title)
	} else {
		b.WriteString(st.title.Render(title))
	}
	if lay.group > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		cell := padTruncate(r.cmd.Group, lay.group, ell)
		if sel {
			b.WriteString(cell)
		} else {
			b.WriteString(st.group.Render(cell))
		}
	}
	if lay.slack > 0 {
		b.WriteString(strings.Repeat(" ", lay.slack))
	}
	if lay.keys > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		cell := padLeft(r.keys, lay.keys, ell)
		if sel {
			b.WriteString(cell)
		} else {
			b.WriteString(st.keys.Render(cell))
		}
	}
	if sel {
		return st.selected.Render(b.String())
	}
	return b.String()
}

// renderHit draws one cached issue in the same columns as a command: its key and
// what the copy on disk says its title is, then how old that copy is where a
// command shows its group.
func renderHit(h *hit, lay layout, sel bool, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(lay.width + 32)

	writeMarker(&b, sel, t)
	text := padTruncate(h.text, lay.title, ell)
	if sel {
		b.WriteString(text)
	} else {
		b.WriteString(st.title.Render(text))
	}
	if lay.group > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		cell := padTruncate(h.age, lay.group, ell)
		switch {
		case sel:
			b.WriteString(cell)
		case h.stale:
			b.WriteString(st.stale.Render(cell))
		default:
			b.WriteString(st.group.Render(cell))
		}
	}
	if lay.slack > 0 {
		b.WriteString(strings.Repeat(" ", lay.slack))
	}
	// No key reaches one issue, so the column stays blank rather than moving.
	if lay.keys > 0 {
		b.WriteString(strings.Repeat(" ", gap+lay.keys))
	}
	if sel {
		return st.selected.Render(b.String())
	}
	return b.String()
}

// renderHeading draws a group name on its own line. No marker, no click zone.
func renderHeading(group string, lay layout, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	return strings.Repeat(" ", marker) + st.group.Render(padTruncate(group, max(lay.width-marker, 0), ell))
}

func writeMarker(b *strings.Builder, sel bool, t *kernel.Theme) {
	if !sel {
		b.WriteString(strings.Repeat(" ", marker))
		return
	}
	b.WriteString(t.Glyphs.Collapsed)
	b.WriteString(strings.Repeat(" ", max(marker-ansi.StringWidth(t.Glyphs.Collapsed), 0)))
}

func (m *Model) row(at int) string {
	if row := m.shown[at]; !row.selectable() {
		k := rowKey{heading: row.heading, lay: m.lay, gen: m.styles.gen}
		if s, ok := m.memo.get(k); ok {
			return s
		}
		s := renderHeading(row.heading, m.lay, m.styles, m.deps.Theme)
		m.memo.put(k, s)
		return s
	}
	sel := at == m.cursor
	if row := m.shown[at]; row.issue {
		h := &m.hits[row.at]
		k := rowKey{id: h.key, text: h.text, age: h.age, lay: m.lay, selected: sel, gen: m.styles.gen}
		if s, ok := m.memo.get(k); ok {
			return s
		}
		s := renderHit(h, m.lay, sel, m.styles, m.deps.Theme)
		if m.deps.Zones != nil {
			s = m.deps.Zones.Mark(m.zonePrefix+zoneHit+h.key, s)
		}
		m.memo.put(k, s)
		return s
	}
	r := &m.rows[m.shown[at].at]
	k := rowKey{id: r.cmd.ID, lay: m.lay, selected: sel, gen: m.styles.gen}
	if s, ok := m.memo.get(k); ok {
		return s
	}
	s := renderRow(r, m.lay, sel, m.styles, m.deps.Theme)
	if m.deps.Zones != nil {
		s = m.deps.Zones.Mark(m.zonePrefix+zoneRow+r.cmd.ID, s)
	}
	m.memo.put(k, s)
	return s
}

// headKey is everything the rule line is built from, so that it is rebuilt when
// one of them moves and never once per frame.
type headKey struct {
	width int
	gen   int
	shown int
	hits  int
	// registered is every command the build put in the registry and total is the
	// ones this site allows. Both, because a build with nothing in it and a build
	// whose every command is refused are different answers.
	registered int
	total      int
	filtered   bool
}

// rule is the line under the filter, with the count at its right end.
func (m *Model) rule() string {
	key := headKey{
		width: m.width, gen: m.styles.gen,
		shown: m.shownCmds, hits: len(m.hits),
		registered: len(m.rows), total: m.offered(), filtered: m.query != "",
	}
	if m.head != "" && key == m.headAt {
		return m.head
	}
	count := m.countLabel(key)
	dashes := max(m.width-ansi.StringWidth(count)-1, 0)
	m.head = m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) +
		" " + m.styles.muted.Render(count)
	m.headAt = key
	return m.head
}

func (m *Model) countLabel(key headKey) string {
	if key.hits == 0 {
		return commandCount(key)
	}
	return commandCount(key) + " " + m.deps.Theme.Glyphs.Separator + " " + issueCount(key.hits)
}

func commandCount(key headKey) string {
	switch {
	case key.registered == 0:
		return "nothing registered"
	case key.total == 0:
		return "none you can run here"
	case key.filtered:
		return strconv.Itoa(key.shown) + " of " + strconv.Itoa(key.total)
	case key.total == 1:
		return "1 command"
	default:
		return strconv.Itoa(key.total) + " commands"
	}
}

// issueCount reads 20+ at the bound, because the index was asked for that many
// and stopped: the cache may hold more matches and there is no number to give.
func issueCount(hits int) string {
	if hits >= hitLimit {
		return strconv.Itoa(hitLimit) + "+ issues"
	}
	if hits == 1 {
		return "1 issue"
	}
	return strconv.Itoa(hits) + " issues"
}

// offered is how many commands this site allows, which is the number the count
// is out of: a command the token cannot run is not one of them.
func (m *Model) offered() int {
	n := 0
	for i := range m.rows {
		if m.rows[i].offered() {
			n++
		}
	}
	return n
}

// View draws the filter, the rule and the window of commands under it.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	lines = append(lines, m.input.View(), m.rule())
	h := m.rowsHeight()
	if len(m.shown) == 0 {
		lines = m.appendEmpty(lines, h)
	} else {
		end := min(m.top+h, len(m.shown))
		for i := m.top; i < end; i++ {
			lines = append(lines, m.row(i))
		}
		for i := end - m.top; i < h; i++ {
			lines = append(lines, "")
		}
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

// appendEmpty says why there is nothing to run. A filter that matched only
// commands this site refuses answers with the probe's own words rather than
// with "nothing matches", which would be a lie about a command that exists.
//
// A build with nothing registered, a build whose every command this site refuses
// and a filter that matched none of the ones it allows are three answers, and
// only the first is about the build.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	switch {
	case len(m.rows) == 0:
		lines = append(lines, m.styles.muted.Render("  Nothing has registered a command in this build."))
	case m.offered() == 0:
		lines = append(lines, m.styles.muted.Render("  No command in this build can be run on this site."))
		shown := 0
		for i := range m.rows {
			if m.rows[i].offered() || shown == refusalLines {
				continue
			}
			lines, shown = append(lines, m.refusalLine(i, room, ell)), shown+1
		}
	case len(m.refused) > 0:
		lines = append(lines, m.styles.muted.Render("  Nothing you can run here matches that."))
		for _, i := range m.refused[:min(len(m.refused), refusalLines)] {
			lines = append(lines, m.refusalLine(i, room, ell))
		}
	default:
		lines = append(lines, m.styles.muted.Render(
			ansi.Truncate("  Nothing matches "+strconv.Quote(m.query)+".", m.width, ell)))
		if why := m.noIssues(); why != "" {
			lines = append(lines, "  "+m.styles.muted.Render(ansi.Truncate(why, room, ell)))
		}
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// refusalLine is one refused command and why, in the probe's own words.
func (m *Model) refusalLine(at, room int, ellipsis string) string {
	r := &m.rows[at]
	return "  " + m.styles.muted.Render(
		ansi.Truncate(r.cmd.Title+" "+m.deps.Theme.Glyphs.Separator+" "+r.reason, room, ellipsis))
}

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes so that an emoji or a CJK title does not shift
// every column to its right.
func padTruncate(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	got := ansi.StringWidth(s)
	switch {
	case got == width:
		return s
	case got < width:
		return s + strings.Repeat(" ", width-got)
	}
	out := ansi.Truncate(s, width, ellipsis)
	if pad := width - ansi.StringWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

// padLeft is padTruncate for a cell that reads better against the right edge,
// which is where a key belongs.
func padLeft(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	got := ansi.StringWidth(s)
	if got < width {
		return strings.Repeat(" ", width-got) + s
	}
	return padTruncate(s, width, ellipsis)
}
