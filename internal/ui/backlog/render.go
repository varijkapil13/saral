package backlog

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	gap         = 2
	marker      = 2
	box         = 2
	minSummary  = 24
	minKeyWidth = 6
	maxKeyWidth = 14
	statusWidth = 12
	userWidth   = 16
)

// zones are the click targets this view marks. Each is prefixed per instance so
// that two of these views on one screen cannot answer for each other.
const (
	zoneBoard   = "board"
	zoneConfirm = "confirm"
	zoneCancel  = "cancel"
)

func destZone(at int) string { return "dest:" + strconv.Itoa(at) }

// zoneOf is the click target one row is marked with: an issue by its key, a
// section by its position, both stable for as long as the board is on screen.
func (m *Model) zoneOf(at int) string {
	if at < 0 || at >= len(m.rows) {
		return ""
	}
	if m.rows[at].head {
		return "head:" + strconv.Itoa(m.rows[at].group)
	}
	return "row:" + m.issues[m.rows[at].issue].Key
}

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width   int
	key     int
	summary int
	status  int
	who     int
}

// planLayout drops columns from the right until the summary has room. A summary
// squeezed to nothing is worse than no assignee column, because the summary is
// the only part of a row that says what the issue is.
func planLayout(width, keyWidth int) layout {
	keyWidth = min(max(keyWidth, minKeyWidth), maxKeyWidth)
	lay := layout{
		width: max(width, marker+box+minKeyWidth+minSummary),
		key:   keyWidth, status: statusWidth, who: userWidth,
	}
	drop := []*int{&lay.who, &lay.status}
	for {
		lay.summary = lay.width - marker - box - lay.key - gap - optionalWidth(lay)
		if lay.summary >= minSummary || len(drop) == 0 {
			break
		}
		*drop[0] = 0
		drop = drop[1:]
	}
	lay.summary = max(lay.summary, 1)
	return lay
}

func optionalWidth(lay layout) int {
	total := 0
	for _, w := range [...]int{lay.status, lay.who} {
		if w > 0 {
			total += gap + w
		}
	}
	return total
}

// styles are this view's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen        int
	selected   lipgloss.Style
	key        lipgloss.Style
	base       lipgloss.Style
	muted      lipgloss.Style
	title      lipgloss.Style
	accent     lipgloss.Style
	danger     lipgloss.Style
	warn       lipgloss.Style
	badge      lipgloss.Style
	categories [4]lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	s := &styles{
		gen:      t.Gen,
		selected: t.Selected,
		key:      t.Accent,
		base:     t.Base,
		muted:    t.Muted,
		title:    t.Title,
		accent:   t.Accent,
		danger:   t.Danger,
		warn:     t.Warning,
		badge:    t.Badge,
	}
	s.categories = [4]lipgloss.Style{
		jira.CategoryUnknown:    t.Muted,
		jira.CategoryToDo:       t.Base,
		jira.CategoryInProgress: t.Accent,
		jira.CategoryDone:       t.Success,
	}
	return s
}

// rowKey is what makes two renderings of a row the same rendering: the tuple
// docs/PERFORMANCE.md asks for — updated, width, selected, theme generation —
// widened to the column plan, to whether the row is picked, and to what a
// section head is built from.
type rowKey struct {
	head     bool
	name     string
	updated  int64
	count    int
	state    string
	lay      layout
	selected bool
	picked   bool
	gen      int
}

// rowCache is a bounded memo of rendered rows. Past its limit it is emptied
// rather than evicted one at a time, because a scroll invalidates a screenful at
// once anyway and clearing keeps the map's capacity.
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

// headKey is everything the head line is built from, so that it is rebuilt when
// one of them moves and never otherwise.
type headKey struct {
	board   string
	boards  int
	width   int
	gen     int
	issues  int
	shown   int
	sprints int
	more    bool
	loading bool
}

// View draws the head line and the window of rows under it. Only the visible
// rows are built, so a project with ten thousand issues costs what one with
// twenty costs per frame.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	lines = append(lines, m.headLine())
	h := m.rowsHeight()
	if len(m.rows) == 0 {
		lines = m.appendEmpty(lines, h)
	} else {
		end := min(m.top+h, len(m.rows))
		for i := m.top; i < end; i++ {
			lines = append(lines, m.line(i))
		}
		for i := end - m.top; i < h; i++ {
			lines = append(lines, "")
		}
		m.warm(end)
	}
	if note := m.note(); note != "" {
		lines = append(lines, m.noted.render(note, m.width, m.styles.gen, m.styles.muted, m.deps.Theme.Glyphs.Ellipsis))
	}
	if len(m.picked) > 0 {
		lines = append(lines, m.picks.render(count(len(m.picked), "issue")+" picked",
			m.width, m.styles.gen, m.styles.accent, m.deps.Theme.Glyphs.Ellipsis))
	}
	if m.said != "" {
		lines = append(lines, m.outcome.render(m.said, m.width, m.styles.gen, m.styles.warn, m.deps.Theme.Glyphs.Ellipsis))
	}
	switch m.mode {
	case choosing:
		lines = append(lines, m.chooserLine())
	case confirming:
		lines = append(lines, m.confirmLine())
	case movingIssues:
		lines = append(lines, m.progressLine())
	case browsing:
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

// warm renders the overscan into the memo so that the next scroll step is a
// cache hit rather than a row build. It draws nothing.
func (m *Model) warm(end int) {
	const overscan = 4
	for i := max(m.top-overscan, 0); i < min(end+overscan, len(m.rows)); i++ {
		if i < m.top || i >= end {
			m.line(i)
		}
	}
}

func (m *Model) line(at int) string {
	r := m.rows[at]
	k := rowKey{lay: m.lay, selected: at == m.cursor, gen: m.styles.gen}
	if r.head {
		g := &m.groups[r.group]
		k.head, k.name, k.count, k.state = true, g.name, len(g.issues), string(g.state)
	} else {
		iss := &m.issues[r.issue]
		k.name, k.updated = iss.Key, iss.Updated.UnixNano()
		k.picked = m.picked[iss.Key]
	}
	if s, ok := m.memo.get(k); ok {
		return s
	}
	var s string
	if r.head {
		s = m.renderHead(&m.groups[r.group], k.selected)
	} else {
		s = m.renderRow(&m.issues[r.issue], k.selected, k.picked)
	}
	s = m.zones.Mark(m.zoneOf(at), s)
	m.memo.put(k, s)
	return s
}

// renderHead draws one section head: the sprint, its state and how many issues
// are in it. A sprint with none still has a head, because it is a place issues
// can be dragged into.
func (m *Model) renderHead(g *group, sel bool) string {
	t := m.deps.Theme
	var b strings.Builder
	b.Grow(m.lay.width + 32)
	if sel {
		b.WriteString(padTruncate(t.Glyphs.Collapsed, marker, t.Glyphs.Ellipsis))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	b.WriteString(t.Glyphs.Expanded)
	b.WriteString(" ")
	b.WriteString(g.name)
	if g.state != "" {
		b.WriteString(" ")
		b.WriteString(t.Glyphs.Separator)
		b.WriteString(" ")
		b.WriteString(string(g.state))
	}
	b.WriteString(" ")
	b.WriteString(t.Glyphs.Separator)
	b.WriteString(" ")
	b.WriteString(count(len(g.issues), "issue"))
	line := padTruncate(b.String(), m.lay.width, t.Glyphs.Ellipsis)
	if sel {
		return m.styles.selected.Render(line)
	}
	return m.styles.title.Render(line)
}

// renderRow draws one issue to exactly lay.width columns. The status cell takes
// its colour from the status category, which the port resolved from
// statusCategory rather than from a name a site can translate.
func (m *Model) renderRow(iss *jira.Issue, sel, picked bool) string {
	t := m.deps.Theme
	ell := t.Glyphs.Ellipsis
	lay := m.lay
	var b strings.Builder
	b.Grow(lay.width + 32)

	if sel {
		b.WriteString(padTruncate(t.Glyphs.Collapsed, marker, ell))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	if picked {
		b.WriteString(padTruncate(t.Glyphs.Check, box, ell))
	} else {
		b.WriteString(strings.Repeat(" ", box))
	}
	writeCell(&b, iss.Key, lay.key, ell)
	writeGap(&b)
	writeCell(&b, iss.Summary, lay.summary, ell)
	if lay.status > 0 {
		writeGap(&b)
		cell := iconOrName(iss.Status.Name, t.Glyphs.CategoryGlyph(iss.Status.Category), lay.status, ell)
		if !sel {
			cell = m.styles.categories[categoryIndex(iss.Status.Category)].Render(cell)
		}
		b.WriteString(cell)
	}
	if lay.who > 0 {
		writeGap(&b)
		writeCell(&b, assigneeName(iss), lay.who, ell)
	}
	if sel {
		return m.styles.selected.Render(b.String())
	}
	return b.String()
}

func (m *Model) headLine() string {
	key := m.headKey()
	if m.head != "" && key == m.headOf {
		return m.head
	}
	m.headOf = key
	t := m.deps.Theme
	var b strings.Builder
	b.Grow(m.width + 32)
	b.WriteString("  ")
	name := m.board().Name
	if name == "" {
		name = "no board"
	}
	b.WriteString(m.zones.Mark(zoneBoard, m.styles.title.Render(name)))
	if len(m.boards) > 1 {
		b.WriteString(m.styles.muted.Render(" (" + strconv.Itoa(m.boardAt+1) + " of " +
			strconv.Itoa(len(m.boards)) + ")"))
	}
	b.WriteString(m.styles.muted.Render(" " + t.Glyphs.Separator + " " + count(len(m.sprints), "open sprint")))
	shown := 0
	for i := range m.groups {
		shown += len(m.groups[i].issues)
	}
	tail := " " + t.Glyphs.Separator + " "
	if len(m.issues) > shown {
		// The difference is the finished work, which is neither in a sprint
		// anybody is planning nor waiting to be scheduled.
		tail += strconv.Itoa(shown) + " of " + count(len(m.issues), "issue")
	} else {
		tail += count(shown, "issue")
	}
	if m.page.HasMore() {
		tail += "+"
	}
	if m.loading {
		tail += " " + t.Glyphs.Separator + " reading" + t.Glyphs.Ellipsis
	}
	b.WriteString(m.styles.muted.Render(tail))
	m.head = m.fit(b.String())
	return m.head
}

func (m *Model) headKey() headKey {
	shown := 0
	for i := range m.groups {
		shown += len(m.groups[i].issues)
	}
	return headKey{
		board: m.board().Name, boards: len(m.boards), width: m.width, gen: m.styles.gen,
		issues: len(m.issues), shown: shown, sprints: len(m.sprints),
		more: m.page.HasMore(), loading: m.loading,
	}
}

// note is the one sentence under the rows that is true whatever else is: what
// the order is, and whether the rows can be reordered.
func (m *Model) note() string {
	if len(m.rows) == 0 {
		return ""
	}
	return m.ordering()
}

// lineCache memoizes one of the single lines under the rows. Each of them is
// rebuilt from the same words on every frame otherwise, which is an allocation
// per frame the rows themselves are memoized to avoid.
type lineCache struct {
	text  string
	width int
	gen   int
	out   string
}

func (c *lineCache) render(text string, width, gen int, style lipgloss.Style, ellipsis string) string {
	if c.out != "" && c.text == text && c.width == width && c.gen == gen {
		return c.out
	}
	line := "  " + text
	if width > 0 && ansi.StringWidth(line) > width {
		line = ansi.Truncate(line, width, ellipsis)
	}
	c.text, c.width, c.gen, c.out = text, width, gen, style.Render(line)
	return c.out
}

// chooserLine draws every destination on one line, each a click target of its
// own, with the one under the cursor marked.
func (m *Model) chooserLine() string {
	var b strings.Builder
	b.Grow(m.width + 64)
	b.WriteString("  ")
	b.WriteString(m.styles.base.Render("move " + count(len(m.wanted), "issue") + " to:"))
	for g := range m.groups {
		b.WriteString(" ")
		name := m.groups[g].name
		if g == m.destAt {
			b.WriteString(m.zones.Mark(destZone(g), m.styles.selected.Render("["+name+"]")))
			continue
		}
		b.WriteString(m.zones.Mark(destZone(g), m.styles.muted.Render(" "+name+" ")))
	}
	return m.fit(b.String())
}

// confirmLine is the step a move cannot skip: it names what will change, how
// many issues it is and where they are going.
func (m *Model) confirmLine() string {
	keys := m.wanted
	dest := ""
	if m.destAt >= 0 && m.destAt < len(m.groups) {
		dest = m.groups[m.destAt].name
	}
	var b strings.Builder
	b.Grow(m.width + 64)
	b.WriteString("  ")
	b.WriteString(m.styles.warn.Render("Move " + count(len(keys), "issue") + " into " + dest + "?"))
	if len(keys) > moveChunk {
		b.WriteString(m.styles.muted.Render(" " + strconv.Itoa(batches(len(keys))) +
			" calls, and a refused one leaves the ones before it moved."))
	}
	b.WriteString(" ")
	b.WriteString(m.zones.Mark(zoneConfirm, m.styles.accent.Render("y go ahead")))
	b.WriteString("  ")
	b.WriteString(m.zones.Mark(zoneCancel, m.styles.muted.Render("esc cancel")))
	return m.fit(b.String())
}

// progressLine is the real number the API gives: which batch of the move has
// been accepted, and how many issues are in Jira's hands already.
func (m *Model) progressLine() string {
	if m.mv == nil {
		return ""
	}
	said := "moving " + count(len(m.mv.keys), "issue") + " into " + m.mv.name +
		" " + m.deps.Theme.Glyphs.Separator + " batch " + strconv.Itoa(m.mv.done+1) +
		" of " + strconv.Itoa(m.mv.chunks) + ", " + strconv.Itoa(m.mv.moved) + " moved"
	return m.styles.accent.Render(m.fit("  " + said))
}

func batches(n int) int { return (n + moveChunk - 1) / moveChunk }

// appendEmpty says which kind of empty this is, and keeps saying it: a status
// line is overwritten by the next thing that happens, and a pane that is empty
// because the site said no has to go on saying so.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	switch {
	case m.site == nil:
		lines = append(lines, m.styles.muted.Render("  There is no Jira connection in this session."))
	case m.loading && !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Reading the board"+m.deps.Theme.Glyphs.Ellipsis))
	case m.failure != nil:
		lines = m.appendFailure(lines, h)
	case m.absent != "":
		lines = m.appendWrapped(lines, m.styles.muted, m.absent, h)
	case !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Nothing has been asked of Jira yet."))
	default:
		lines = append(lines, m.styles.muted.Render("  Nothing on this board is waiting to be scheduled."))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// appendFailure is what the pane says instead of rows: the reason in the error's
// own words and the key that runs it again. The reason is wrapped rather than
// cut, since a transport failure names a host and a port before it says what is
// wrong with them.
func (m *Model) appendFailure(lines []string, h int) []string {
	reason, _ := jira.Reason(m.failure)
	lines = append(lines, m.styles.danger.Render("  The board could not be read."))
	lines = m.appendWrapped(lines, m.styles.muted, reason, max(h-4, 1))
	return append(lines, "", m.styles.muted.Render("  "+retryHint))
}

func (m *Model) appendWrapped(lines []string, style lipgloss.Style, text string, h int) []string {
	room := max(m.width-2, 8)
	said := strings.Split(ansi.Wrap(text, room, ""), "\n")
	for _, line := range said[:min(len(said), max(h, 1))] {
		lines = append(lines, style.Render("  "+line))
	}
	return lines
}

// retryHint names the kernel's own refresh, spelt from the binding rather than
// written out.
var retryHint = kernel.DefaultGlobalKeys().Refresh.Help().Key + " reads the board again."

// fit truncates a line to the width the view was given, counting cells rather
// than bytes so that a styled line is not cut through an escape sequence.
func (m *Model) fit(s string) string {
	if m.width <= 0 {
		return s
	}
	if ansi.StringWidth(s) <= m.width {
		return s
	}
	return ansi.Truncate(s, m.width, m.deps.Theme.Glyphs.Ellipsis)
}

// iconOrName drops to an icon only where the name would have been
// truncated anyway, never beside a name that already fits.
func iconOrName(name, icon string, width int, ellipsis string) string {
	if icon == "" || ansi.StringWidth(name) <= width {
		return padTruncate(name, width, ellipsis)
	}
	return padTruncate(icon, width, ellipsis)
}

func categoryIndex(c jira.StatusCategory) int {
	if c < jira.CategoryUnknown || c > jira.CategoryDone {
		return int(jira.CategoryUnknown)
	}
	return int(c)
}

func assigneeName(iss *jira.Issue) string {
	if iss.Assignee == nil || strings.TrimSpace(iss.Assignee.DisplayName) == "" {
		return "unassigned"
	}
	return iss.Assignee.DisplayName
}

func writeGap(b *strings.Builder) { b.WriteString("  ") }

func writeCell(b *strings.Builder, s string, width int, ellipsis string) {
	if width <= 0 {
		return
	}
	b.WriteString(padTruncate(s, width, ellipsis))
}

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes so that an emoji or a CJK summary does not shift
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
