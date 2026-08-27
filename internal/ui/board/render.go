package board

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	gap = 1
	// minCell is the narrowest a column can be drawn and still say what is in
	// it: an issue key, a space and a few letters of the summary.
	minCell = 16
	// chromeLines are the three lines the grid sits between: the line naming the
	// board, the column captions and the rule that closes the columns off.
	chromeLines = 3
	// minSummary is how much of a card has to be left for the summary before the
	// estimate is given up for it.
	minSummary = 10
)

// zones are the click targets this view marks. Each is prefixed per instance so
// that two boards on one screen cannot answer for each other.
const (
	zoneCard   = "card:"
	zoneColumn = "col:"
)

// cardZone is the target one card is marked with, named by the issue rather than
// by where it happens to be drawn, so that scrolling does not mint a new id for
// a card that has already been drawn once. Zone ids are never freed.
func cardZone(key string) string { return zoneCard + key }

// colZone is the target one column strip is marked with. The strip runs from the
// caption to the rule under the grid, which is what makes a drop anywhere in an
// empty column land in it.
func colZone(at int) string { return zoneColumn + strconv.Itoa(at) }

// layout is the plan for one width and height. It is comparable so that a card
// or a line memoized under it is invalidated by any relayout, not only by a
// resize.
type layout struct {
	width int
	// cols is how many columns are drawn. A board with more of them scrolls
	// sideways rather than squeezing every one of them past legibility.
	cols int
	cell int
	rows int
}

// planLayout fits as many whole columns as the width allows, all the same width.
// Sharing the remainder out would make two columns of one board different widths
// and put the width into every card's memo key for a cell of padding.
func planLayout(width, rows, columns int) layout {
	if width <= 0 || rows <= 0 || columns <= 0 {
		return layout{width: max(width, 0), rows: max(rows, 0)}
	}
	fit := max((width+gap)/(minCell+gap), 1)
	cols := min(fit, columns)
	cell := max((width-gap*(cols-1))/cols, 1)
	return layout{width: width, cols: cols, cell: cell, rows: rows}
}

// rowsHeight is how many grid rows fit: the box, less the three chrome lines and
// the prompt a gesture in progress puts under them.
func (m *Model) rowsHeight() int {
	h := m.height - chromeLines
	if m.card != nil || m.moving {
		h--
	}
	return max(h, 1)
}

// styles are the board's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a card.
type styles struct {
	gen        int
	selected   lipgloss.Style
	held       lipgloss.Style
	aimed      lipgloss.Style
	key        lipgloss.Style
	base       lipgloss.Style
	muted      lipgloss.Style
	title      lipgloss.Style
	danger     lipgloss.Style
	warning    lipgloss.Style
	categories [4]lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	s := &styles{
		gen:      t.Gen,
		selected: t.Selected,
		held:     t.Warning,
		aimed:    t.Accent,
		key:      t.Accent,
		base:     t.Base,
		muted:    t.Muted,
		title:    t.Title,
		danger:   t.Danger,
		warning:  t.Warning,
	}
	s.categories = [4]lipgloss.Style{
		jira.CategoryUnknown:    t.Muted,
		jira.CategoryToDo:       t.Base,
		jira.CategoryInProgress: t.Accent,
		jira.CategoryDone:       t.Success,
	}
	return s
}

// cardKey is what makes two renderings of a card the same rendering: the tuple
// docs/PERFORMANCE.md asks for — updated, width, selected, theme generation —
// widened to the issue's identity, since one memo serves every card, and to
// whether it is the card in hand.
type cardKey struct {
	key      string
	updated  int64
	cell     int
	selected bool
	held     bool
	gen      int
}

type cardCache struct {
	cards map[cardKey]string
	limit int
}

func newCardCache(limit int) *cardCache {
	return &cardCache{cards: make(map[cardKey]string, limit), limit: limit}
}

func (c *cardCache) get(k cardKey) (string, bool) {
	s, ok := c.cards[k]
	return s, ok
}

func (c *cardCache) put(k cardKey, s string) {
	if len(c.cards) >= c.limit {
		clear(c.cards)
	}
	c.cards[k] = s
}

func (c *cardCache) reset() { clear(c.cards) }

// lineKey is what makes two renderings of a grid line the same rendering. The
// cursor and the card in hand are held as the column they are in on this line,
// or -1, so that moving the cursor rebuilds the two lines it moved between and
// not the screen.
type lineKey struct {
	row    int
	lay    layout
	colTop int
	sel    int
	held   int
	gen    int
}

type lineCache struct {
	lines map[lineKey]string
	limit int
}

func newLineCache(limit int) *lineCache {
	return &lineCache{lines: make(map[lineKey]string, limit), limit: limit}
}

func (c *lineCache) get(k lineKey) (string, bool) {
	s, ok := c.lines[k]
	return s, ok
}

func (c *lineCache) put(k lineKey, s string) {
	if len(c.lines) >= c.limit {
		clear(c.lines)
	}
	c.lines[k] = s
}

func (c *lineCache) reset() { clear(c.lines) }

// chromeKey is everything the caption row and the rule are built from, so that
// both are rebuilt when one of them moves and never otherwise.
type chromeKey struct {
	lay     layout
	colTop  int
	gen     int
	dataGen int
	aimed   int
}

// View draws the line naming the board, the column captions, the window of grid
// rows under them and the rule that closes the columns. Only the rows and the
// columns that fit are built, so a board of six thousand cards costs what a
// board of six costs.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	m.relayout()
	lines := m.lines[:0]
	lines = append(lines, m.summaryLine())
	if !m.drawable() {
		lines = m.appendEmpty(lines, m.height-1)
		m.lines = lines
		return strings.Join(lines, "\n")
	}
	head, rule := m.chrome()
	lines = append(lines, head)
	h := m.rowsHeight()
	if m.gridRows() == 0 {
		lines = m.appendEmpty(lines, h)
	} else {
		end := min(m.rowTop+h, m.gridRows())
		for row := m.rowTop; row < end; row++ {
			lines = append(lines, m.line(row))
		}
		for i := end - m.rowTop; i < h; i++ {
			lines = append(lines, m.line(-1))
		}
	}
	lines = append(lines, rule)
	if m.card != nil || m.moving {
		lines = append(lines, m.prompt())
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

// drawable reports whether there are columns to draw. Everything else — no
// connection, no board, a read in flight, a refusal — is a sentence rather than
// a grid, and appendEmpty is where they are told apart.
func (m *Model) drawable() bool {
	return m.ready && len(m.plan.columns) > 0 && m.failure == nil && m.lay.cols > 0
}

// line is one row of the grid across every visible column, memoized so that a
// frame nothing has changed on costs the frame string and nothing behind it.
// A row of -1 is the padding under a short grid.
func (m *Model) line(row int) string {
	k := lineKey{row: row, lay: m.lay, colTop: m.colTop, sel: -1, held: -1, gen: m.styles.gen}
	if row >= 0 && row == m.curRow {
		k.sel = m.curCol
	}
	if m.card != nil && row == m.card.row {
		k.held = m.card.from
	}
	if s, ok := m.rows.get(k); ok {
		return s
	}
	var b strings.Builder
	b.Grow(m.lay.width + 32)
	for c := m.colTop; c < min(m.colTop+m.lay.cols, len(m.cols)); c++ {
		if c > m.colTop {
			b.WriteString(strings.Repeat(" ", gap))
		}
		b.WriteString(m.cell(c, row))
	}
	s := b.String()
	if pad := m.lay.width - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	m.rows.put(k, s)
	return s
}

// cell is one column's part of one grid line: the card there, or the blank that
// keeps the columns to the right where they are.
func (m *Model) cell(col, row int) string {
	iss := m.issueAt(col, row)
	if iss == nil {
		return m.blank
	}
	selected := col == m.curCol && row == m.curRow && m.card == nil
	inHand := m.card != nil && m.card.key == iss.Key
	k := cardKey{
		key: iss.Key, updated: iss.Updated.UnixNano(), cell: m.lay.cell,
		selected: selected, held: inHand, gen: m.styles.gen,
	}
	if s, ok := m.cards.get(k); ok {
		return s
	}
	s := m.zones.Mark(cardZone(iss.Key), renderCard(iss, m.lay.cell, selected, inHand, m.styles, m.deps.Theme, m.plan))
	m.cards.put(k, s)
	return s
}

// renderCard draws one card to exactly cell columns: a marker saying whether it
// is the one under the cursor or the one in hand, the issue key, as much of the
// summary as is left, and the board's estimate for it where the board has one.
func renderCard(iss *jira.Issue, cell int, selected, inHand bool, st *styles, t *kernel.Theme, p plan) string {
	ell := t.Glyphs.Ellipsis
	mark := " "
	switch {
	case inHand:
		mark = t.Glyphs.Diamond
	case selected:
		mark = t.Glyphs.Collapsed
	}
	room := max(cell-ansi.StringWidth(mark), 0)
	estimate := ""
	if p.estimates {
		if n, ok := iss.Fields.Number(p.estimate); ok {
			estimate = " " + trimNumber(n)
		}
	}
	// A summary squeezed to nothing is worse than no estimate: the summary is
	// the only part of a card that says what the issue is.
	if left := room - ansi.StringWidth(estimate); left < minSummary {
		estimate = ""
	}
	body := iss.Key
	if left := room - ansi.StringWidth(estimate) - ansi.StringWidth(body) - 1; left > 0 {
		body += " " + ansi.Truncate(iss.Summary, left, ell)
	}
	out := mark + padTruncate(body, max(room-ansi.StringWidth(estimate), 0), ell) + estimate
	switch {
	case inHand:
		return st.held.Render(out)
	case selected:
		return st.selected.Render(out)
	default:
		return out
	}
}

// chrome is the caption row and the rule under the grid. They are built as a
// pair because one zone spans both: the column's id opens on its caption and
// closes at the end of its rule, so the rectangle a click resolves against is
// the whole strip rather than the caption alone.
func (m *Model) chrome() (head, rule string) {
	k := chromeKey{lay: m.lay, colTop: m.colTop, gen: m.styles.gen, dataGen: m.dataGen, aimed: m.aimedAt()}
	if m.head != "" && k == m.chromeAt {
		return m.head, m.rule
	}
	var hb, rb strings.Builder
	hb.Grow(m.lay.width + 64)
	rb.Grow(m.lay.width + 64)
	for c := m.colTop; c < min(m.colTop+m.lay.cols, len(m.plan.columns)); c++ {
		if c > m.colTop {
			hb.WriteString(strings.Repeat(" ", gap))
			rb.WriteString(strings.Repeat(" ", gap))
		}
		pair := m.zones.MarkLines(colZone(c), []string{m.caption(c), m.ruleCell(c)})
		hb.WriteString(pair[0])
		rb.WriteString(pair[1])
	}
	m.head, m.rule, m.chromeAt = hb.String(), rb.String(), k
	return m.head, m.rule
}

// aimedAt is the column the card in hand is pointed at, or -1. The captions are
// drawn from it, which is what says where the card will land before anything is
// asked of the site.
func (m *Model) aimedAt() int {
	if m.card == nil {
		return -1
	}
	return m.card.target
}

// caption names a column and says how many cards are in it, with the count in
// the warning style when the board's own limit for that column is breached. Min
// and Max are pointers because a column may have neither.
func (m *Model) caption(col int) string {
	c := m.plan.columns[col]
	n := m.columnLen(col)
	count := strconv.Itoa(n)
	room := max(m.lay.cell-ansi.StringWidth(count)-1, 1)
	name := padTruncate(c.name, room, m.deps.Theme.Glyphs.Ellipsis)
	style := m.styles.muted
	if col == m.aimedAt() {
		style = m.styles.aimed
	}
	numbers := m.styles.muted
	if breached(c, n) {
		numbers = m.styles.warning
	}
	return padCells(style.Render(name)+" "+numbers.Render(count), m.lay.cell,
		m.deps.Theme.Glyphs.Ellipsis)
}

// breached reports that a column holds fewer or more cards than the board says
// it should.
func breached(c planColumn, n int) bool {
	return (c.min != nil && n < *c.min) || (c.max != nil && n > *c.max)
}

// ruleCell closes a column off, and carries its estimate total when the board
// estimates at all. A board that does not estimate draws a plain rule rather
// than a zero.
func (m *Model) ruleCell(col int) string {
	line := m.deps.Theme.Glyphs.HLine
	if !m.plan.estimates {
		return m.styles.muted.Render(repeatTo(line, m.lay.cell))
	}
	total := trimNumber(m.estimateOf(col))
	room := m.lay.cell - ansi.StringWidth(total) - 1
	if room < 1 {
		return m.styles.muted.Render(repeatTo(line, m.lay.cell))
	}
	return m.styles.muted.Render(repeatTo(line, room) + " " + total)
}

func (m *Model) estimateOf(col int) float64 {
	total := 0.0
	for _, at := range m.cols[col] {
		if n, ok := m.issues[at].Fields.Number(m.plan.estimate); ok {
			total += n
		}
	}
	return total
}

// summaryKey is everything the top line is built from, so that the line is
// rebuilt when one of them moves and never otherwise.
type summaryKey struct {
	board      string
	width, gen int
	columns    int
	cards      int
	unmapped   int
	shown      int
	boards     int
	more       bool
	loading    bool
	loaded     bool
	failed     bool
	ordering   jira.Ordering
	estimates  bool
	checked    int64
}

func (m *Model) summaryKey() summaryKey {
	return summaryKey{
		board: m.boardName(), width: m.width, gen: m.styles.gen,
		columns: len(m.plan.columns), cards: len(m.issues), unmapped: m.unmapped,
		shown: m.lay.cols, boards: len(m.all), more: m.more,
		loading: m.loading, loaded: m.loaded, failed: m.failure != nil,
		ordering: m.plan.ordering, estimates: m.plan.estimates,
		checked: m.checked.UnixNano(),
	}
}

// summaryLine names the board and says what kind of board it is: how many
// columns, how it is ordered, and how many of its cards are in hand. Everything
// on it is the site's answer rather than an assumption.
func (m *Model) summaryLine() string {
	key := m.summaryKey()
	if m.summary != "" && key == m.sumKey {
		return m.summary
	}
	m.summary = m.twoCells(m.boardTitle(), m.counts(), m.styles.title, m.styles.muted)
	m.sumKey = key
	return m.summary
}

// twoCells lays a line out as something on the left, something on the right and
// the space between them, exactly the width of the box. The left is what gives
// way: a board's name is worth less than the count of what is on it, and a hint
// naming a key is worth more than the sentence in front of it.
func (m *Model) twoCells(left, right string, ls, rs lipgloss.Style) string {
	ell := m.deps.Theme.Glyphs.Ellipsis
	if got := ansi.StringWidth(right); got > m.width {
		right = ansi.Truncate(right, m.width, ell)
	}
	room := max(m.width-ansi.StringWidth(right)-1, 0)
	if room == 0 {
		return padCells(rs.Render(right), m.width, ell)
	}
	left = ansi.Truncate(left, room, ell)
	pad := max(m.width-ansi.StringWidth(left)-ansi.StringWidth(right), 0)
	return ls.Render(left) + strings.Repeat(" ", pad) + rs.Render(right)
}

func (m *Model) boardTitle() string {
	name := m.boardName()
	if name == "" {
		return "Board"
	}
	if len(m.all) > 1 {
		return name + " (" + strconv.Itoa(m.at+1) + " of " + strconv.Itoa(len(m.all)) + ")"
	}
	return name
}

// counts is the right-hand half of the top line: what the site said, in its own
// terms. The plus is not decoration — a search reports no total, so a number
// without one would be a number the user could not trust.
func (m *Model) counts() string {
	if !m.ready {
		return ""
	}
	cards := strconv.Itoa(len(m.issues))
	if m.more {
		cards += "+"
	}
	// The order is what is given up first: the stamp goes before the count of
	// what is on the board, because a board with no cards on it is the question
	// and when it was last read is the footnote.
	parts := []string{strconv.Itoa(len(m.plan.columns)) + " columns", cards + " cards"}
	if m.lay.cols > 0 && m.lay.cols < len(m.plan.columns) {
		parts[0] += " (" + strconv.Itoa(m.lay.cols) + " shown)"
	}
	if m.unmapped > 0 {
		parts = append(parts, strconv.Itoa(m.unmapped)+" in no column")
	}
	parts = append(parts, m.plan.orderWords())
	if !m.checked.IsZero() {
		parts = append(parts, "checked "+m.checked.In(m.deps.Caps.Location()).Format("15:04"))
	}
	sep := " " + m.deps.Theme.Glyphs.Separator + " "
	room := max(m.width/2, 12)
	for len(parts) > 1 && ansi.StringWidth(strings.Join(parts, sep)) > room {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, sep)
}

// prompt is the line a gesture in progress puts under the grid: which issue is
// in hand, where it is going and what the two keys do. It is the named
// confirmation a move gets — the words say what will change before either
// gesture commits it.
func (m *Model) prompt() string {
	ell := m.deps.Theme.Glyphs.Ellipsis
	if m.card == nil {
		if !m.moving {
			return strings.Repeat(" ", m.width)
		}
		return padCells(m.styles.muted.Render("  asking the site for the move"+ell), m.width, ell)
	}
	said := "move " + m.card.key + " from " + m.card.status + " to " +
		m.plan.columns[m.card.target].name
	hint := dropHint
	if m.moving {
		hint = m.deps.Theme.Glyphs.Stale + " asking the site"
	}
	if ansi.StringWidth(hint) > m.width/2 {
		hint = defaultKeys().Drop.Help().Key
	}
	return m.twoCells(said, hint+"  ", m.styles.aimed, m.styles.muted)
}

// appendEmpty says which kind of empty this is, and keeps saying it. Six are
// told apart here, because a user cannot act on the difference between a token
// that may not see boards, a project with no board, a read still out and a read
// that was refused unless the screen names it.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	switch {
	case m.deps.Jira == nil:
		lines = append(lines, m.say("  No Jira connection in this session yet."))
	case !m.deps.Caps.Allows(jira.CapBoards):
		lines = append(lines, m.warn("  Boards are not available here."),
			m.say("  "+m.deps.Caps.Capability(jira.CapBoards).Reason))
	case m.failure != nil:
		lines = m.appendFailure(lines, h)
	case m.loading:
		lines = append(lines, m.say("  "+asking[m.step]+m.deps.Theme.Glyphs.Ellipsis))
	case !m.loaded:
		lines = append(lines, m.say("  Nothing has been asked of Jira yet."))
	case len(m.all) == 0:
		lines = append(lines, m.say("  No board draws on "+m.deps.Project+"."),
			m.say("  A project without one is ordinary; the issue list is where its work is."))
	case !m.ready || len(m.plan.columns) == 0:
		lines = append(lines, m.say("  "+m.boardName()+" has no columns mapped."),
			m.say("  A board with no status in any column has nothing to draw."))
	default:
		lines = append(lines, m.say("  No issue in "+m.deps.Project+" is in a status this board maps."))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// asking is what each of the three reads is called while it is outstanding. One
// shared "Loading…" would make a wrong project key, a board that cannot be read
// and a refused search into one screen that looks like a hang.
var asking = [...]string{
	stepIdle:   "Reading this board",
	stepBoards: "Asking which boards draw on this project",
	stepConfig: "Reading this board's columns",
	stepIssues: "Searching for the issues on this board",
}

// appendFailure is what the pane says instead of a grid: the reason in the
// error's own words, which of the three questions went unanswered, and the key
// that asks it again. The reason is wrapped rather than cut, since a transport
// failure names a host and a port before it says what is wrong with them.
func (m *Model) appendFailure(lines []string, h int) []string {
	reason, _ := jira.Reason(m.failure)
	lines = append(lines, m.warn("  "+failedAt[m.failStep]))
	room := max(m.width-2, 8)
	said := strings.Split(ansi.Wrap(reason, room, ""), "\n")
	for _, line := range said[:min(len(said), max(h-3, 1))] {
		lines = append(lines, m.say("  "+line))
	}
	return append(lines, "", m.say("  "+retryHint))
}

var failedAt = [...]string{
	stepIdle:   "This board could not be read.",
	stepBoards: "The boards on this project could not be read.",
	stepConfig: "This board's columns could not be read.",
	stepIssues: "The issues on this board could not be read.",
}

func (m *Model) say(s string) string  { return m.styles.muted.Render(s) }
func (m *Model) warn(s string) string { return m.styles.danger.Render(s) }

// The two sentences that name a key, spelt from the bindings rather than written
// out. The retry names the kernel's own refresh, which this view registers
// nothing for.
var (
	retryHint = kernel.DefaultGlobalKeys().Refresh.Help().Key + " tries again."
	dropHint  = defaultKeys().Drop.Help().Key + " moves it, " +
		defaultKeys().Cancel.Help().Key + " puts it back"
)

// repeatTo draws a glyph out to a width, counting cells rather than bytes: the
// rule is a theme glyph and an ASCII fallback is one byte where a box-drawing
// character is three.
func repeatTo(glyph string, width int) string {
	w := ansi.StringWidth(glyph)
	if width <= 0 || w <= 0 {
		return strings.Repeat(" ", max(width, 0))
	}
	out := strings.Repeat(glyph, width/w)
	if pad := width - ansi.StringWidth(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

// padCells makes an already-styled line exactly width columns wide. It is
// separate from padTruncate because what it is handed carries escape sequences
// and must keep them.
func padCells(s string, width int, ellipsis string) string {
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

// trimNumber spells an estimate without a decimal point it does not need: a
// board measuring in points holds 3 far more often than 3.5, and "3.0" in every
// cell of a narrow column is a column of noise.
func trimNumber(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }
