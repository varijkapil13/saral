package timeline

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	marker     = 2
	gap        = 2
	keyMin     = 6
	keyMax     = 14
	summaryMin = 16
	summaryMax = 40
	whyWidth   = 9
	minChart   = 20

	rowCacheLimit = 1024
)

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width   int
	key     int
	summary int
	why     int
	chart   int
}

// planLayout gives the chart what is left after a readable key and summary, and
// gives up the source column before it gives up the chart: a timeline with
// twenty cells of calendar in it is not a timeline.
func planLayout(width, keyWidth int) layout {
	lay := layout{width: width, key: min(max(keyWidth, keyMin), keyMax)}
	for _, plan := range [...]struct{ why, sumMin int }{{whyWidth, summaryMin}, {0, summaryMin}, {0, 1}} {
		lay.why = plan.why
		fixed := marker + lay.key + 2*gap
		if plan.why > 0 {
			fixed += plan.why + gap
		}
		room := max(width-fixed, 2)
		lay.summary = min(min(max(room/3, plan.sumMin), summaryMax), room-1)
		lay.chart = room - lay.summary
		if lay.chart >= minChart {
			return lay
		}
	}
	return lay
}

func (lay layout) prefix() int { return lay.width - lay.chart }

// chartGlyphs are the one-cell characters a chart cell can hold. Each is checked
// for width rather than taken on trust: the ASCII diamond is two cells, and a
// two-cell glyph in a one-cell column moves every column right of it.
type chartGlyphs struct {
	bar       string
	faded     string
	rollup    string
	milestone string
	today     string
	rule      string
	tick      string
	mark      string
}

func newChartGlyphs(g kernel.Glyphs) chartGlyphs {
	one := func(pick ...string) string {
		for _, s := range pick {
			if ansi.StringWidth(s) == 1 {
				return s
			}
		}
		return "?"
	}
	return chartGlyphs{
		bar:       one(g.ProgressOn, "#"),
		faded:     one(g.ProgressNo, "-"),
		rollup:    one(g.Dot, "."),
		milestone: one(g.Diamond, g.Bullet, "*"),
		today:     one(g.VLine, "|"),
		rule:      one(g.HLine, "-"),
		tick:      one(g.VLine, "|"),
		mark:      one(g.Expanded, g.Bullet, "*"),
	}
}

// styles are this view's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	glyphs   chartGlyphs
	selected lipgloss.Style
	key      lipgloss.Style
	base     lipgloss.Style
	muted    lipgloss.Style
	title    lipgloss.Style
	danger   lipgloss.Style
	bar      lipgloss.Style
	faded    lipgloss.Style
	rollup   lipgloss.Style
	stone    lipgloss.Style
	today    lipgloss.Style
	mark     lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		glyphs:   newChartGlyphs(t.Glyphs),
		selected: t.Selected,
		key:      t.Accent,
		base:     t.Base,
		muted:    t.Muted,
		title:    t.Title,
		danger:   t.Danger,
		bar:      t.Accent,
		faded:    t.Muted,
		rollup:   t.Muted,
		stone:    t.Success,
		today:    t.Warning,
		mark:     t.Warning,
	}
}

// rowKey is what makes two renderings of a row the same rendering: the bar, the
// window it is drawn into and the theme it is drawn with.
type rowKey struct {
	key      string
	summary  string
	start    jira.Date
	end      jira.Date
	from     app.Provenance
	absent   app.Absence
	lay      layout
	ax       axis
	left     int
	today    int
	selected bool
	gen      int
}

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

// put empties the memo past its limit rather than evicting one at a time,
// because a scroll or a pan invalidates a screenful at once anyway and clearing
// keeps the map's capacity.
func (c *rowCache) put(k rowKey, s string) {
	if len(c.rows) >= c.limit {
		clear(c.rows)
	}
	c.rows[k] = s
}

func (c *rowCache) reset() { clear(c.rows) }

// A row's zone id is prefixed per instance, so two of these views on one screen
// cannot answer for each other.
func rowZone(key string) string { return "row:" + key }

// chrome is which of the lines around the bars a box has room for. A short box
// gives them up from the least useful inward rather than overflowing, and the
// summary line is never given up: it is the only one that names what is on
// screen.
type chrome struct {
	heading  bool
	ruler    bool
	versions bool
	sprints  bool
	detail   bool
	bar      bool
	note     bool
	rows     int
}

func (c chrome) lines() int {
	n := 0
	for _, on := range [...]bool{c.heading, c.ruler, c.versions, c.sprints, c.detail, c.bar, c.note} {
		if on {
			n++
		}
	}
	return n
}

func (m *Model) chrome() chrome {
	_, _, hasVersions, hasSprints := m.markerLines()
	c := chrome{
		heading: true, ruler: true,
		versions: hasVersions, sprints: hasSprints,
		detail: m.selected() != nil, bar: len(m.terms) > 0, note: m.hasNoteCount(),
	}
	spare := max(m.height-1, 0)
	for spare-c.lines() < 1 {
		switch {
		case c.note:
			c.note = false
		case c.sprints:
			c.sprints = false
		case c.versions:
			c.versions = false
		case c.detail:
			c.detail = false
		case c.bar:
			c.bar = false
		case c.ruler:
			c.ruler = false
		case c.heading:
			c.heading = false
		default:
			return chrome{rows: spare}
		}
	}
	c.rows = spare - c.lines()
	return c
}

// View draws the visible window of rows over the visible window of calendar.
// Neither the number of issues nor the length of the span is in the cost of a
// frame: only the rows that fit are built, and only the columns that fit.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	lines = append(lines, m.summaryLine())
	if m.notes {
		m.lines = m.appendNotes(lines, m.height-1)
		return strings.Join(m.lines, "\n")
	}
	c := m.chrome()
	if c.heading {
		lines = append(lines, m.headingLine())
	}
	if c.ruler {
		lines = append(lines, m.rulerLine())
	}
	versions, sprints, _, _ := m.markerLines()
	if c.versions {
		lines = append(lines, versions)
	}
	if c.sprints {
		lines = append(lines, sprints)
	}

	if len(m.rows) == 0 {
		lines = m.appendEmpty(lines, c.rows)
	} else {
		end := min(m.top+c.rows, len(m.rows))
		for i := m.top; i < end; i++ {
			lines = append(lines, m.row(i, i == m.cursor))
		}
		for i := end - m.top; i < c.rows; i++ {
			lines = append(lines, "")
		}
		m.warm(end)
	}
	if c.detail {
		lines = append(lines, m.detailLine())
	}
	if c.bar {
		lines = append(lines, m.filterBar())
	}
	if c.note {
		lines = append(lines, m.noteCountLine())
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

// filterBar draws the chip line naming the terms in force.
func (m *Model) filterBar() string {
	return m.bar.Render(m.terms, m.width, m.deps.Theme, clearFilterKey, m.termsGen)
}

// clearFilterKey is built once rather than read off a fresh defaultKeys() on
// every frame.
var clearFilterKey = defaultKeys().Unfilter.Help().Key

// warm renders the overscan into the memo so the next scroll step is a hit
// rather than a row build. It draws nothing.
func (m *Model) warm(end int) {
	const overscan = 4
	for i := max(m.top-overscan, 0); i < min(end+overscan, len(m.rows)); i++ {
		if i < m.top || i >= end {
			m.row(i, false)
		}
	}
}

func (m *Model) row(at int, selected bool) string {
	r := &m.rows[at]
	k := rowKey{
		key: r.key, summary: r.summary,
		start: r.rng.Start, end: r.rng.End, from: r.rng.From, absent: r.rng.Absent,
		lay: m.lay, ax: m.ax, left: m.left, today: m.todayCol(), selected: selected, gen: m.styles.gen,
	}
	if s, ok := m.memo.get(k); ok {
		return s
	}
	s := m.zones.Mark(rowZone(r.key), renderRow(r, k, m.styles, m.deps.Theme))
	m.memo.put(k, s)
	return s
}

func renderRow(r *barRow, k rowKey, st *styles, t *kernel.Theme) string {
	lay, ell := k.lay, t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(lay.width + 64)

	if k.selected {
		b.WriteString(t.Glyphs.Collapsed)
		b.WriteString(strings.Repeat(" ", max(marker-ansi.StringWidth(t.Glyphs.Collapsed), 0)))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	b.WriteString(st.key.Render(pad(r.key, lay.key, ell)))
	b.WriteString("  ")
	b.WriteString(st.base.Render(pad(r.summary, lay.summary, ell)))
	if lay.why > 0 {
		b.WriteString("  ")
		b.WriteString(st.muted.Render(pad(whyLabel(r.rng), lay.why, ell)))
	}
	b.WriteString("  ")
	writeBar(&b, r.rng, k, st)

	if k.selected {
		return st.selected.Render(b.String())
	}
	return b.String()
}

// writeBar draws one row's chart cells: a solid run for dates somebody set, a
// faint one for the pair the cascade guessed, dots for a range rolled up off the
// children, and one glyph where only a single date resolved.
func writeBar(b *strings.Builder, rng app.Range, k rowKey, st *styles) {
	width := k.lay.chart
	if width <= 0 {
		return
	}
	from, to, ok := columnsOf(rng, k.ax)
	if !ok {
		writeBlank(b, 0, width, k, st)
		return
	}
	start, end := max(from-k.left, 0), min(to-k.left, width-1)
	if start > width-1 || end < 0 {
		writeBlank(b, 0, width, k, st)
		return
	}
	writeBlank(b, 0, start, k, st)
	glyph, style := st.glyphs.bar, st.bar
	switch {
	case rng.From.Milestone():
		glyph, style = st.glyphs.milestone, st.stone
	case rng.From.Rollup():
		glyph, style = st.glyphs.rollup, st.rollup
	case rng.From.Faded():
		glyph, style = st.glyphs.faded, st.faded
	}
	b.WriteString(style.Render(strings.Repeat(glyph, end-start+1)))
	writeBlank(b, end+1, width, k, st)
}

// writeBlank fills cells with space, putting the today line through whichever of
// them it falls in.
func writeBlank(b *strings.Builder, from, to int, k rowKey, st *styles) {
	if to <= from {
		return
	}
	at := k.today - k.left
	if k.today < 0 || at < from || at >= to {
		b.WriteString(strings.Repeat(" ", to-from))
		return
	}
	b.WriteString(strings.Repeat(" ", at-from))
	b.WriteString(st.today.Render(st.glyphs.today))
	b.WriteString(strings.Repeat(" ", to-at-1))
}

// columnsOf is the pair of chart columns a range covers, earliest first. A range
// recorded the wrong way round is drawn over the days it actually spans;
// app.Resolution.Warnings is what says it is the wrong way round.
func columnsOf(rng app.Range, ax axis) (from, to int, ok bool) {
	if !rng.OK() || ax.empty() {
		return 0, 0, false
	}
	from, to = ax.col(rng.Start), ax.col(rng.End)
	if to < from {
		from, to = to, from
	}
	return from, to, true
}

// whyLabel is the cascade rule in the width a column has. The whole sentence is
// on the detail line under the rows, which is where a bar in the wrong place is
// actually diagnosed.
func whyLabel(rng app.Range) string {
	switch {
	case !rng.OK():
		return "no dates"
	case rng.From.Rollup():
		return "children"
	case rng.From.Milestone():
		return "one date"
	case rng.From.Faded():
		return "guessed"
	}
	switch rng.From {
	case app.FromConfiguredFields:
		return "config"
	case app.FromTargetDates:
		return "target"
	case app.FromStartAndDue:
		return "start/due"
	case app.FromSprint:
		return "sprint"
	default:
		return ""
	}
}

type axisKey struct {
	ax    axis
	lay   layout
	left  int
	today int
	gen   int
}

func (m *Model) axisKey() axisKey {
	return axisKey{ax: m.ax, lay: m.lay, left: m.left, today: m.todayCol(), gen: m.styles.gen}
}

func (m *Model) headingLine() string {
	key := m.axisKey()
	if m.heading != "" && key == m.axisAt {
		return m.heading
	}
	m.axisAt = key
	var b strings.Builder
	b.Grow(m.lay.width + 32)
	b.WriteString(strings.Repeat(" ", marker))
	b.WriteString(pad("KEY", m.lay.key, ""))
	b.WriteString("  ")
	b.WriteString(pad("SUMMARY", m.lay.summary, ""))
	if m.lay.why > 0 {
		b.WriteString("  ")
		b.WriteString(pad("SOURCE", m.lay.why, ""))
	}
	b.WriteString("  ")
	b.WriteString(m.periods())
	m.heading = m.styles.muted.Render(b.String())
	return m.heading
}

// periods writes a calendar period's name from the column it starts in, and
// leaves it out where the next period starts before the name would end: a label
// cut to one letter says less than the gap it would have filled, and the summary
// line names the window's dates in full either way.
func (m *Model) periods() string {
	var b strings.Builder
	b.Grow(m.lay.chart)
	skip := 0
	for i := 0; i < m.lay.chart; i++ {
		col := m.left + i
		switch {
		case skip > 0:
			skip--
			continue
		case col < 0 || col >= m.ax.cols || !m.ax.heads(col):
			b.WriteByte(' ')
			continue
		}
		text := m.ax.heading(col)
		if width := ansi.StringWidth(text); width > m.room(i, width) {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(text)
		skip = ansi.StringWidth(text) - 1
	}
	return b.String()
}

// room is how many cells a label starting at chart position i may use before the
// next period starts, looking no further than the label needs.
func (m *Model) room(i, want int) int {
	limit := min(want, m.lay.chart-i)
	for j := 1; j <= limit; j++ {
		if col := m.left + i + j; col < m.ax.cols && m.ax.heads(col) {
			return j
		}
	}
	return limit
}

func (m *Model) rulerLine() string {
	key := m.axisKey()
	if m.ruler != "" && key == m.rulerAt {
		return m.ruler
	}
	m.rulerAt = key
	g := m.styles.glyphs
	var b strings.Builder
	b.Grow(m.lay.width + 32)
	b.WriteString(m.styles.muted.Render(strings.Repeat(g.rule, m.lay.prefix())))
	today := m.todayCol()
	run := strings.Builder{}
	run.Grow(m.lay.chart)
	flush := func() {
		if run.Len() > 0 {
			b.WriteString(m.styles.muted.Render(run.String()))
			run.Reset()
		}
	}
	for i := 0; i < m.lay.chart; i++ {
		col := m.left + i
		switch {
		case col == today:
			flush()
			b.WriteString(m.styles.today.Render(g.mark))
		case col >= 0 && col < m.ax.cols && m.ax.heads(col):
			run.WriteString(g.tick)
		default:
			run.WriteString(g.rule)
		}
	}
	flush()
	m.ruler = b.String()
	return m.ruler
}

type markerKey struct {
	ax   axis
	lay  layout
	left int
	gen  int
	// marks counts the changes to the marker sets, because a slice cannot be
	// part of a comparable key.
	marks int
}

func (m *Model) markerKey() markerKey {
	return markerKey{ax: m.ax, lay: m.lay, left: m.left, gen: m.styles.gen, marks: m.marksGen}
}

// markerLines are the two lines above the bars: the versions releasing inside
// the window, and the sprints starting or ending in it. Both are built together
// because both are invalidated by the same things.
func (m *Model) markerLines() (versions, sprints string, hasVersions, hasSprints bool) {
	key := m.markerKey()
	if key != m.marksAt || !m.marksBuilt {
		m.marksAt, m.marksBuilt = key, true
		diamond, tick := m.styles.glyphs.milestone, m.styles.glyphs.tick
		labels := make([]markLabel, 0, len(m.versionMarks))
		for _, v := range m.versionMarks {
			labels = append(labels, markLabel{col: m.ax.col(v.on), tick: diamond, text: diamond + v.name})
		}
		m.versions, m.hasVersions = m.markLine(labels, m.styles.mark)
		labels = labels[:0]
		for _, s := range m.sprintMarks {
			labels = append(labels, markLabel{col: m.ax.col(s.from), tick: tick, text: tick + s.name})
			if s.to != s.from {
				labels = append(labels, markLabel{col: m.ax.col(s.to), tick: tick, text: tick})
			}
		}
		m.sprints, m.hasSprints = m.markLine(labels, m.styles.muted)
	}
	return m.versions, m.sprints, m.hasVersions, m.hasSprints
}

// markLabel is one boundary on a marker line: the glyph alone, and the glyph
// with the name after it for where there is room for both.
type markLabel struct {
	col  int
	tick string
	text string
}

// markLine writes labels across the chart, earliest first, each starting at its
// own column and giving way to whatever comes after it. It reports false when
// nothing lands in the window, which is what keeps the line off the frame.
func (m *Model) markLine(labels []markLabel, style lipgloss.Style) (string, bool) {
	if len(labels) == 0 || m.lay.chart <= 0 {
		return "", false
	}
	slices.SortFunc(labels, func(a, b markLabel) int { return a.col - b.col })
	// How far each label may run before the next one starts, taken off the
	// sorted list so that a board with two hundred sprints on it costs one walk
	// rather than one per label.
	nextCol := make(map[int]int, len(labels))
	next := labels[len(labels)-1].col + m.lay.chart + 1
	for i := len(labels) - 1; i >= 0; i-- {
		if labels[i].col != next {
			nextCol[labels[i].col] = next
			next = labels[i].col
		}
	}
	cells := make([]string, m.lay.chart)
	filled := 0
	for _, label := range labels {
		at := label.col - m.left
		if at < 0 || at >= m.lay.chart || cells[at] != "" {
			continue
		}
		room := min(nextCol[label.col]-label.col, m.lay.chart-at)
		text := label.text
		if ansi.StringWidth(text) > room {
			// A name cut to a letter or two says nothing; the boundary itself
			// still does, so what is left is the tick.
			text = ansi.Truncate(label.tick, room, "")
		}
		width := ansi.StringWidth(text)
		if width == 0 {
			continue
		}
		cells[at] = text
		for i := at + 1; i < at+width && i < m.lay.chart; i++ {
			cells[i] = "\x00"
		}
		filled++
	}
	if filled == 0 {
		return "", false
	}
	var b strings.Builder
	b.Grow(m.lay.width + 32)
	b.WriteString(strings.Repeat(" ", m.lay.prefix()))
	for _, cell := range cells {
		switch cell {
		case "":
			b.WriteString(" ")
		case "\x00":
		default:
			b.WriteString(style.Render(cell))
		}
	}
	return b.String(), true
}

// summaryKey is everything the summary line is built from, so that the line is
// rebuilt when one of them moves and never otherwise.
type summaryKey struct {
	title           string
	width, gen      int
	rows, resolved  int
	zoom            Zoom
	from, to        jira.Date
	loading, loaded bool
	failed          bool
	badge           string
	checked         int64
}

func (m *Model) summaryKey() summaryKey {
	from, to := m.window()
	return summaryKey{
		title: m.title, width: m.width, gen: m.styles.gen,
		rows: len(m.rows), resolved: m.resolvedShown, zoom: m.zoom,
		from: from, to: to,
		loading: m.loading, loaded: m.loaded, failed: m.failure != nil,
		badge: m.badge, checked: m.checked.UnixNano(),
	}
}

func (m *Model) window() (from, to jira.Date) {
	if m.ax.empty() {
		return jira.Date{}, jira.Date{}
	}
	return m.ax.start(m.left), m.ax.start(min(m.left+max(m.lay.chart, 1)-1, m.ax.cols-1))
}

func (m *Model) summaryLine() string {
	key := m.summaryKey()
	if m.summary != "" && key == m.sumKey {
		return m.summary
	}
	m.sumKey = key
	right := m.countLabel()
	badge := ""
	if m.badge != "" {
		badge = m.deps.Theme.StaleBadge.Render(m.badge)
	}
	stamp := m.checkedLabel()
	tail := ansi.StringWidth(stamp) + ansi.StringWidth(badge) + ansi.StringWidth(right)
	title := ansi.Truncate(m.title, max(m.width-tail-1, 1), m.deps.Theme.Glyphs.Ellipsis)
	pad := max(m.width-ansi.StringWidth(title)-tail, 1)
	m.summary = m.styles.title.Render(title) + strings.Repeat(" ", pad) + stamp + badge + m.styles.muted.Render(right)
	return m.summary
}

func (m *Model) checkedLabel() string {
	if m.checked.IsZero() {
		return ""
	}
	return m.styles.muted.Render("checked " + m.checked.In(m.deps.Caps.Location()).Format("15:04") + "  ")
}

// wideSummary is whether the summary line has room for the window's dates as
// well as the counts. Below it the title is what a reader needs most, and a
// title cut to four letters says less than the dates do.
const wideSummary = 110

// countLabel says how much of the search resolved to a bar and how much
// calendar one column is, both of which a reader needs to trust the picture.
func (m *Model) countLabel() string {
	var b strings.Builder
	switch {
	case !m.loaded && m.loading:
		b.WriteString("reading")
	case m.failure != nil:
		b.WriteString("no answer")
	default:
		b.WriteString(strconv.Itoa(m.resolvedShown))
		b.WriteString(" of ")
		b.WriteString(strconv.Itoa(len(m.rows)))
		b.WriteString(" dated")
		if m.filteredOut > 0 {
			b.WriteString(", ")
			b.WriteString(strconv.Itoa(m.filteredOut))
			b.WriteString(" hidden by filter")
		}
	}
	if from, to := m.window(); !from.IsZero() && m.width >= wideSummary {
		b.WriteString("  ")
		b.WriteString(from.String())
		b.WriteString(" ")
		b.WriteString(m.deps.Theme.Glyphs.Arrow)
		b.WriteString(" ")
		b.WriteString(to.String())
	}
	b.WriteString("  ")
	if m.width >= wideSummary {
		b.WriteString("one column: ")
	}
	b.WriteString(m.zoom.String())
	if m.loaded && m.loading {
		b.WriteString(" ")
		b.WriteString(m.deps.Theme.Glyphs.Stale)
	}
	return b.String()
}

type detailKey struct {
	key    string
	start  jira.Date
	end    jira.Date
	from   app.Provenance
	absent app.Absence
	source string
	width  int
	gen    int
}

// detailLine is where the whole of a bar's provenance goes: the dates, the rule
// that produced them and the things they came out of. It stays on screen,
// because a status line is overwritten by the next thing that happens and the
// question "why is this bar here" comes back.
func (m *Model) detailLine() string {
	r := m.selected()
	if r == nil {
		return ""
	}
	key := detailKey{
		key: r.key, start: r.rng.Start, end: r.rng.End, from: r.rng.From,
		absent: r.rng.Absent, source: r.rng.Source, width: m.width, gen: m.styles.gen,
	}
	if m.detail != "" && key == m.detailAt {
		return m.detail
	}
	m.detailAt = key
	var b strings.Builder
	b.WriteString(r.key)
	b.WriteString("  ")
	switch {
	case !r.rng.OK():
		b.WriteString("no dates")
		if said := r.rng.Absent.String(); said != "" {
			b.WriteString(" — ")
			b.WriteString(said)
		}
	case r.rng.From.Milestone():
		b.WriteString(r.rng.Start.String())
		b.WriteString("  ")
		b.WriteString(r.rng.From.String())
		b.WriteString(" (")
		b.WriteString(r.rng.Source)
		b.WriteString(")")
	default:
		b.WriteString(r.rng.Start.String())
		b.WriteString(" ")
		b.WriteString(m.deps.Theme.Glyphs.Arrow)
		b.WriteString(" ")
		b.WriteString(r.rng.End.String())
		b.WriteString("  ")
		b.WriteString(r.rng.From.String())
		b.WriteString(" (")
		b.WriteString(r.rng.Source)
		b.WriteString(")")
	}
	m.detail = m.styles.muted.Render(ansi.Truncate(b.String(), m.width, m.deps.Theme.Glyphs.Ellipsis))
	return m.detail
}

type noteCountKey struct {
	width, gen     int
	rows, resolved int
	notes          int
	loaded         bool
}

// noteCountLine says that something did not add up, and — the state this view
// exists to make diagnosable — that a search came back with issues no rule could
// date. Both persist, because both are still true after the next keypress.
func (m *Model) noteCountLine() string {
	key := noteCountKey{
		width: m.width, gen: m.styles.gen, rows: len(m.rows),
		resolved: m.resolvedShown, notes: len(m.noteLines), loaded: m.loaded,
	}
	if m.noteCountSet && key == m.noteCountAt {
		return m.noteCount
	}
	m.noteCountAt, m.noteCountSet = key, true
	ell := m.deps.Theme.Glyphs.Ellipsis
	switch {
	case m.loaded && len(m.rows) > 0 && m.resolvedShown == 0:
		m.noteCount = m.styles.danger.Render(ansi.Truncate("none of these "+strconv.Itoa(len(m.rows))+
			" issues carries a date this site could be read from — "+notesHint, m.width, ell))
	case len(m.noteLines) == 1:
		m.noteCount = m.styles.muted.Render(ansi.Truncate("one thing worth knowing about these dates — "+
			notesHint, m.width, ell))
	case len(m.noteLines) > 1:
		m.noteCount = m.styles.muted.Render(ansi.Truncate(strconv.Itoa(len(m.noteLines))+
			" things worth knowing about these dates — "+notesHint, m.width, ell))
	default:
		m.noteCount = ""
	}
	return m.noteCount
}

// The two sentences that name a key, spelt from the binding rather than written
// out. The retry names the kernel's own refresh, which this view registers
// nothing for.
var (
	notesHint = defaultKeys().Notes.Help().Key + " says what they are"
	retryHint = kernel.DefaultGlobalKeys().Refresh.Help().Key + " tries the search again."
)

// appendEmpty says which kind of empty this is. A wrong project, a bad JQL, a
// dead host and a rate limit are four different screens, not one that looks like
// a hang.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	switch {
	case m.search == nil:
		lines = append(lines, m.styles.muted.Render("  No Jira connection in this session yet."))
	case m.loading && !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Reading the fields these dates come from"+m.deps.Theme.Glyphs.Ellipsis))
	case m.failure != nil:
		lines = m.appendFailure(lines, h)
	case !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Nothing has been asked of Jira yet."))
	default:
		lines = append(lines, m.styles.muted.Render("  Nothing matches this search."),
			m.styles.muted.Render("  "+ansi.Truncate(m.jql, max(m.width-2, 8), m.deps.Theme.Glyphs.Ellipsis)))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// appendFailure keeps the refusal in the pane: the reason in the site's own
// words, wrapped rather than cut, then the query and the key that runs it again.
func (m *Model) appendFailure(lines []string, h int) []string {
	reason, _ := jira.Reason(m.failure)
	lines = append(lines, m.styles.danger.Render("  The timeline could not be read."))
	room := max(m.width-2, 8)
	said := strings.Split(ansi.Wrap(reason, room, ""), "\n")
	for _, line := range said[:min(len(said), max(h-3, 1))] {
		lines = append(lines, m.styles.muted.Render("  "+line))
	}
	return append(lines,
		m.styles.muted.Render("  "+ansi.Truncate(m.jql, room, m.deps.Theme.Glyphs.Ellipsis)),
		"",
		m.styles.muted.Render("  "+retryHint))
}

// appendNotes draws the pane that names every field the cascade could not
// resolve, every marker it could not read and everything the pass warned about.
func (m *Model) appendNotes(lines []string, h int) []string {
	at := len(lines)
	if len(m.noteLines) == 0 {
		lines = append(lines, m.styles.muted.Render("  Nothing needed saying about these dates."))
		for len(lines)-at < h {
			lines = append(lines, "")
		}
		return lines[:at+h]
	}
	room := max(m.width-4, 8)
	body := make([]string, 0, len(m.noteLines))
	for _, note := range m.noteLines {
		for _, line := range strings.Split(ansi.Wrap(note, room, ""), "\n") {
			body = append(body, m.styles.muted.Render("  "+line))
		}
	}
	visible, top := widget.Window(body, m.noteTop, h, -1)
	m.noteTop = top
	lines = append(lines, visible...)
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// pad makes a string exactly width columns wide, counting grapheme clusters
// rather than bytes: a summary is whatever anybody typed.
func pad(s string, width int, ellipsis string) string {
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
	if extra := width - ansi.StringWidth(out); extra > 0 {
		out += strings.Repeat(" ", extra)
	}
	return out
}

func (m *Model) todayCol() int {
	if m.ax.empty() {
		return -1
	}
	return m.ax.col(m.today())
}

func (m *Model) today() jira.Date {
	if got := m.res.Today(); !got.IsZero() {
		return got
	}
	return jira.DateOf(m.now().In(m.deps.Caps.Location()))
}

func (m *Model) now() time.Time {
	if m.deps.Now == nil {
		return time.Time{}
	}
	return m.deps.Now()
}
