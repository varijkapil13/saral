package filter

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	// marker is the gutter the selected row's arrow sits in, and chosen is the
	// one beside it that says a value is already in force.
	marker = 2
	chosen = 2
	gap    = 2
	// headHeight is the head line and the rule under it.
	headHeight = 2
	// inputChrome is what the needle's line costs beyond the text itself: its
	// two-cell prompt and the cell the cursor sits in past the last rune.
	inputChrome = 3
	minName     = 16
	// maxName keeps the note beside the name rather than at the far edge of a
	// wide terminal.
	maxName   = 44
	noteWidth = 30
	// reasonLines is how many lines of a refusal the pane wraps before it stops.
	reasonLines = 4
	// rowMemoLimit holds the visible window and its overscan several relayouts
	// deep. A facet list is smaller than this; a site's labels are not, and past
	// it the map is cleared rather than evicted one row at a time.
	rowMemoLimit = 256
)

// styles are the picker's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	selected lipgloss.Style
	name     lipgloss.Style
	note     lipgloss.Style
	muted    lipgloss.Style
	rule     lipgloss.Style
	prompt   lipgloss.Style
	danger   lipgloss.Style
	mark     lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		selected: t.Selected,
		name:     t.Base,
		note:     t.Muted,
		muted:    t.Muted,
		rule:     t.Muted,
		prompt:   t.Accent,
		danger:   t.Danger,
		mark:     t.Accent,
	}
}

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width int
	name  int
	note  int
	pad   int
}

// planLayout drops the note column before the name loses its room: the name is
// the only part of a row that says what the value is.
func planLayout(width int) layout {
	lay := layout{width: max(width, marker+chosen+minName), note: noteWidth}
	for {
		lay.name = lay.width - marker - chosen - optionalWidth(lay)
		if lay.name >= minName || lay.note == 0 {
			break
		}
		lay.note = 0
	}
	lay.name = max(lay.name, 1)
	if lay.name > maxName {
		lay.pad, lay.name = lay.name-maxName, maxName
	}
	return lay
}

func optionalWidth(lay layout) int {
	if lay.note == 0 {
		return 0
	}
	return gap + lay.note
}

// rowKey is what makes two renderings of a row the same rendering.
type rowKey struct {
	facets   bool
	id       string
	name     string
	note     string
	lay      layout
	selected bool
	inForce  bool
	refused  bool
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

// zoneOf is the click target one row is marked with. A facet is named by its
// own label and a value by the id it is held under, both stable for the life of
// the picker.
func (m *Model) zoneOf(at int) string {
	if m.state == pickFacet {
		if at < 0 || at >= len(m.facets) {
			return ""
		}
		return "facet:" + m.facets[at].facet.Label()
	}
	if at < 0 || at >= len(m.shown) {
		return ""
	}
	v := &m.all[m.shown[at]]
	return "value:" + v.term.Facet.Label() + ":" + v.term.ID
}

func (m *Model) row(at int) string {
	sel := at == m.cursor
	if m.state == pickFacet {
		row := m.facets[at]
		k := rowKey{
			facets: true, id: row.facet.Label(), name: row.facet.Label(), note: m.facetNote(row),
			lay: m.lay, selected: sel, inForce: m.terms.Count(row.facet) > 0,
			refused: row.reason != "", gen: m.styles.gen,
		}
		if s, ok := m.memo.get(k); ok {
			return s
		}
		s := m.zones.Mark(m.zoneOf(at), renderRow(k, m.styles, m.deps.Theme))
		m.memo.put(k, s)
		return s
	}
	v := &m.all[m.shown[at]]
	k := rowKey{
		id: v.term.ID, name: v.term.Label, note: v.note, lay: m.lay,
		selected: sel, inForce: m.terms.Has(v.term), gen: m.styles.gen,
	}
	if s, ok := m.memo.get(k); ok {
		return s
	}
	s := m.zones.Mark(m.zoneOf(at), renderRow(k, m.styles, m.deps.Theme))
	m.memo.put(k, s)
	return s
}

// facetNote is the second column of a facet row: why this site cannot answer
// for it, or how many of its values are already in force.
func (m *Model) facetNote(row facetRow) string {
	if row.reason != "" {
		return row.reason
	}
	switch n := m.terms.Count(row.facet); n {
	case 0:
		return ""
	case 1:
		return "1 in force"
	default:
		return strconv.Itoa(n) + " in force"
	}
}

// renderRow draws one row to exactly lay.width columns.
func renderRow(k rowKey, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(k.lay.width + 32)

	if k.selected {
		b.WriteString(t.Glyphs.Collapsed)
		b.WriteString(strings.Repeat(" ", max(marker-ansi.StringWidth(t.Glyphs.Collapsed), 0)))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	switch {
	case !k.inForce:
		b.WriteString(strings.Repeat(" ", chosen))
	case k.selected:
		b.WriteString(padTruncate(t.Glyphs.Check, chosen, ell))
	default:
		b.WriteString(st.mark.Render(padTruncate(t.Glyphs.Check, chosen, ell)))
	}

	name := padTruncate(k.name, k.lay.name, ell)
	switch {
	case k.selected:
		b.WriteString(name)
	case k.refused:
		b.WriteString(st.muted.Render(name))
	default:
		b.WriteString(st.name.Render(name))
	}
	if k.lay.note > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		// A refusal is a sentence rather than a badge, so it takes the slack the
		// other rows leave against the right edge.
		room := k.lay.note
		if k.refused {
			room += k.lay.pad
		}
		note := padTruncate(k.note, room, ell)
		if k.selected {
			b.WriteString(note)
		} else {
			b.WriteString(st.note.Render(note))
		}
	}
	if k.lay.pad > 0 && (!k.refused || k.lay.note == 0) {
		b.WriteString(strings.Repeat(" ", k.lay.pad))
	}
	if k.selected {
		return st.selected.Render(b.String())
	}
	return b.String()
}

// headKey is everything the two lines above the rows are built from, so that
// they are rebuilt when one of them moves and never once per frame.
type headKey struct {
	facets  bool
	facet   Facet
	width   int
	gen     int
	shown   int
	total   int
	terms   int
	typed   bool
	loading bool
	failed  bool
}

func (m *Model) headKey() headKey {
	return headKey{
		facets: m.state == pickFacet, facet: m.facet, width: m.width, gen: m.styles.gen,
		shown: m.rowCount(), total: len(m.all), terms: len(m.terms),
		typed: strings.TrimSpace(m.query) != "", loading: m.loading, failed: m.failure != nil,
	}
}

// needleKey is everything the needle's line is built from. Its blink is dropped
// — the picker never returns the input's own command — so between two keys it
// draws the same thing twice, and drawing it twice is most of what a frame
// costs when the rows are memoized.
type needleKey struct {
	facet Facet
	value string
	at    int
	width int
	gen   int
}

// headLine says what is being filtered by, or takes the needle. The facets have
// no needle of their own: six rows are read rather than searched, and every
// letter left is one somebody types into the value that follows.
func (m *Model) headLine() string {
	if m.state == pickValue {
		// The facet is in the key because the placeholder names it, and an empty
		// needle for two facets is otherwise the same line twice.
		key := needleKey{
			facet: m.facet, value: m.input.Value(), at: m.input.Position(),
			width: m.width, gen: m.styles.gen,
		}
		if m.needle != "" && key == m.needleAt {
			return m.needle
		}
		m.needle, m.needleAt = m.input.View(), key
		return m.needle
	}
	ell := m.deps.Theme.Glyphs.Ellipsis
	if len(m.terms) == 0 {
		return m.styles.muted.Render(ansi.Truncate("  nothing is being filtered out", m.width, ell))
	}
	return m.styles.prompt.Render(ansi.Truncate("  "+m.terms.Words(), m.width, ell))
}

// rule is the line under the head, with the count at its right end.
func (m *Model) rule() string {
	key := m.headKey()
	if m.head != "" && key == m.headAt {
		return m.head
	}
	count := countLabel(key)
	dashes := max(m.width-ansi.StringWidth(count)-1, 0)
	m.head = m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) +
		" " + m.styles.muted.Render(count)
	m.headAt = key
	return m.head
}

func countLabel(key headKey) string {
	switch {
	case key.facets:
		return "what to filter by"
	case key.loading && key.total == 0:
		return "asking the site"
	case key.failed && key.total == 0:
		return "no answer"
	case key.typed:
		return strconv.Itoa(key.shown) + " of " + strconv.Itoa(key.total)
	case key.total == 1:
		return "1 " + key.facet.Label()
	default:
		return strconv.Itoa(key.total) + " " + key.facet.plural()
	}
}

// appendEmpty says which kind of empty this is. A site that refused says so in
// its own words and keeps saying it, because the status line that said it first
// is gone by the next keypress.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	switch {
	case m.state == pickFacet:
		lines = append(lines, m.styles.muted.Render("  There is nothing to filter by in this build."))
	case m.loading:
		lines = append(lines, m.styles.muted.Render("  Asking the site"+ell))
	case m.failure != nil:
		lines = m.appendFailure(lines, room)
	case len(m.all) == 0:
		lines = append(lines, m.styles.muted.Render(
			ansi.Truncate("  This site has no "+m.facet.Label()+" to offer.", room, ell)))
	default:
		lines = append(lines, m.styles.muted.Render(
			ansi.Truncate("  No "+m.facet.Label()+" here matches "+strconv.Quote(m.query)+".", room, ell)))
		if hint := m.hatch(); hint != "" {
			lines = append(lines, m.styles.muted.Render(ansi.Truncate("  "+hint, room, ell)))
		}
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// appendFailure is the refusal in the words the site used, wrapped rather than
// cut: a transport failure names a host and a port before it says what is wrong
// with them.
func (m *Model) appendFailure(lines []string, room int) []string {
	reason, _ := jira.Reason(m.failure)
	lines = append(lines, m.styles.danger.Render("  The site would not say."))
	said := strings.Split(ansi.Wrap(reason, room, ""), "\n")
	for _, line := range said[:min(len(said), reasonLines)] {
		lines = append(lines, m.styles.muted.Render("  "+line))
	}
	if hint := m.hatch(); hint != "" {
		lines = append(lines, "", m.styles.muted.Render("  "+hint))
	}
	return lines
}

// hatch names the way round a value the picker cannot offer, which is the
// prompt that shows the search and takes an edited one. It is spelt from the
// key the view being filtered actually shows, so it cannot teach a stroke that
// view does not answer.
func (m *Model) hatch() string {
	if m.editKey == "" {
		return ""
	}
	return m.editKey + " on the list edits the search by hand."
}

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes: a person's name is exactly the string that is not
// ASCII, and a label is whatever anybody typed.
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

// View draws the head, the rule and the window of rows under it. Only the
// visible rows are built, so a site with two thousand labels costs what a
// project with four issue types costs.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	lines = append(lines, m.headLine(), m.rule())
	h := m.rowsHeight()
	n := m.rowCount()
	if n == 0 {
		lines = m.appendEmpty(lines, h)
	} else {
		end := min(m.top+h, n)
		for i := m.top; i < end; i++ {
			lines = append(lines, m.row(i))
		}
		for i := end - m.top; i < h; i++ {
			lines = append(lines, "")
		}
	}
	if m.refused() {
		lines = append(lines, m.refusalLine())
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

// refusalLine keeps the site's refusal under whatever is still on offer. The
// status line that said it first is gone by the next keypress, and rows this
// program supplies itself are not an answer to the question the site refused.
func (m *Model) refusalLine() string {
	reason, _ := jira.Reason(m.failure)
	return m.styles.danger.Render(
		ansi.Truncate("  "+reason, max(m.width, 8), m.deps.Theme.Glyphs.Ellipsis))
}
