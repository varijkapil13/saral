package sprint

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	// marker is the gutter the selected row's arrow sits in.
	marker = 2
	gap    = 2
	// headHeight is the head line and the rule under it.
	headHeight = 2
	// stateWidth holds the longest word this view has for a state, and a state
	// the site reports that this build has no word for is truncated into it.
	stateWidth = 8
	// datesWidth holds two dates and the arrow between them.
	datesWidth = 24
	goalWidth  = 30
	boardWidth = 16
	minName    = 16
	// maxName keeps the rest of a row beside the name rather than at the far
	// edge of a wide terminal.
	maxName  = 40
	minWidth = marker + stateWidth + gap + minName
	// reasonLines is how many lines of a refusal the pane wraps before it stops.
	reasonLines = 4
	// formLabel is the column the field names sit in, and formGutter the space
	// after it.
	formLabel  = 7
	formGutter = 2
	// rowMemoLimit holds the visible window and its overscan several relayouts
	// deep, in both selected and unselected forms. Past it the map is cleared
	// rather than evicted one row at a time, because a scroll invalidates a
	// screenful at once anyway.
	rowMemoLimit = 256
)

// zones are the click targets this view marks. Each is prefixed per instance so
// that two of these views cannot answer for each other.
const (
	zoneConfirm = "confirm"
	zoneRefuse  = "refuse"
	zoneSend    = "send"
	zoneCancel  = "cancel"
	zoneSprint  = "sprint:"
	zoneField   = "field:"
)

func (m *Model) zoneOf(at int) string {
	if at < 0 || at >= len(m.sprints) {
		return ""
	}
	return zoneSprint + strconv.FormatInt(m.sprints[at].ID, 10)
}

func fieldZone(at field) string { return zoneField + at.label() }

// styles are this view's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	selected lipgloss.Style
	base     lipgloss.Style
	muted    lipgloss.Style
	accent   lipgloss.Style
	danger   lipgloss.Style
	warning  lipgloss.Style
	rule     lipgloss.Style
	// states is one style per state rank, so a row's badge is looked up rather
	// than branched on inside the render.
	states [4]lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		selected: t.Selected,
		base:     t.Base,
		muted:    t.Muted,
		accent:   t.Accent,
		danger:   t.Danger,
		warning:  t.Warning,
		rule:     t.Muted,
		states:   [4]lipgloss.Style{t.Accent, t.Base, t.Muted, t.Warning},
	}
}

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width int
	state int
	name  int
	dates int
	goal  int
	board int
	pad   int
}

// planLayout drops the board, then the goal, then the dates, before the name
// loses its room: the name is the only part of a row that says which sprint it
// is. The board column is only there when there is more than one board, because
// then it is the difference between two rows that otherwise read the same.
func planLayout(width, boards int) layout {
	lay := layout{width: max(width, minWidth), state: stateWidth, dates: datesWidth, goal: goalWidth}
	if boards > 1 {
		lay.board = boardWidth
	}
plan:
	for {
		lay.name = lay.width - marker - lay.state - gap - optionalWidth(lay)
		if lay.name >= minName {
			break
		}
		switch {
		case lay.board > 0:
			lay.board = 0
		case lay.goal > 0:
			lay.goal = 0
		case lay.dates > 0:
			lay.dates = 0
		default:
			break plan
		}
	}
	lay.name = max(lay.name, 1)
	if lay.name > maxName {
		lay.pad, lay.name = lay.name-maxName, maxName
	}
	return lay
}

func optionalWidth(lay layout) int {
	out := 0
	for _, w := range [...]int{lay.dates, lay.goal, lay.board} {
		if w > 0 {
			out += gap + w
		}
	}
	return out
}

// rowKey is what makes two renderings of a row the same rendering.
//
// The dates and the board are in it as the numbers behind them rather than as
// the words drawn from them: a key is built on every frame to look the memo up
// with, and formatting two dates to do it would put three allocations a row
// under every keystroke.
type rowKey struct {
	id         int64
	name       string
	goal       string
	state      string
	start, end int64
	boardID    int64
	lay        layout
	selected   bool
	gen        int
}

// unixOr is a date as a number, and zero for a date that is not set.
func unixOr(at *time.Time) int64 {
	if at == nil {
		return 0
	}
	return at.UnixNano()
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

func (m *Model) row(at int) string {
	sp := m.sprints[at]
	k := rowKey{
		id: sp.ID, name: sp.Name, goal: sp.Goal, state: stateWord(sp.State),
		start: unixOr(sp.Start), end: unixOr(sp.End), boardID: sp.BoardID,
		lay: m.lay, selected: at == m.cursor, gen: m.styles.gen,
	}
	if s, ok := m.memo.get(k); ok {
		return s
	}
	s := m.zones.Mark(m.zoneOf(at), renderRow(k, m.datesOf(sp), m.boardOf(sp),
		m.styles, m.deps.Theme, rankState(sp.State)))
	m.memo.put(k, s)
	return s
}

// datesOf is the two dates as a sprint has them, in the account's timezone
// rather than the machine's. A sprint with no dates says so: it is the state a
// planned sprint sits in until somebody gives it one, and it is why starting it
// would be refused.
func (m *Model) datesOf(sp jira.Sprint) string {
	loc := m.deps.Caps.Location()
	start, end := writeDate(sp.Start, loc), writeDate(sp.End, loc)
	arrow := m.deps.Theme.Glyphs.Arrow
	switch {
	case start != "" && end != "":
		return start + " " + arrow + " " + end
	case start != "":
		return "from " + start
	case end != "":
		return "until " + end
	}
	return "no dates yet"
}

// renderRow draws one row to exactly lay.width columns.
func renderRow(k rowKey, dates, board string, st *styles, t *kernel.Theme, rank int) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(k.lay.width + 48)

	if k.selected {
		b.WriteString(t.Glyphs.Collapsed)
		b.WriteString(strings.Repeat(" ", max(marker-ansi.StringWidth(t.Glyphs.Collapsed), 0)))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	state := padTruncate(k.state, k.lay.state, ell)
	if !k.selected {
		state = st.states[min(rank, len(st.states)-1)].Render(state)
	}
	b.WriteString(state)
	b.WriteString(strings.Repeat(" ", gap))

	name := padTruncate(nameOr(k.name), k.lay.name, ell)
	if k.selected {
		b.WriteString(name)
	} else {
		b.WriteString(st.base.Render(name))
	}
	for _, cell := range [...]struct {
		width int
		text  string
	}{{k.lay.dates, dates}, {k.lay.goal, k.goal}, {k.lay.board, board}} {
		if cell.width == 0 {
			continue
		}
		b.WriteString(strings.Repeat(" ", gap))
		text := padTruncate(cell.text, cell.width, ell)
		if k.selected {
			b.WriteString(text)
		} else {
			b.WriteString(st.muted.Render(text))
		}
	}
	if k.lay.pad > 0 {
		b.WriteString(strings.Repeat(" ", k.lay.pad))
	}
	if k.selected {
		return st.selected.Render(b.String())
	}
	return b.String()
}

// nameOr is what a row says when the site gave the sprint no name, which is
// legal and does happen.
func nameOr(name string) string {
	if strings.TrimSpace(name) == "" {
		return "(unnamed)"
	}
	return name
}

// chromeKey is everything the two lines above the body are built from, so that
// they are rebuilt when one of them moves and never once per frame.
type chromeKey struct {
	board    string
	boards   int
	more     int
	showAll  bool
	rows     int
	width    int
	gen      int
	loading  bool
	loaded   bool
	failed   bool
	state    state
	inflight op
}

func (m *Model) chromeKey() chromeKey {
	board := ""
	if len(m.boards) == 1 {
		board = m.boards[0].Name
	}
	return chromeKey{
		board: board, boards: len(m.boards), more: m.more, showAll: m.showAll,
		rows: m.rowCount(), width: m.width, gen: m.styles.gen,
		loading: m.loading, loaded: m.loaded, failed: m.failure != nil,
		state: m.state, inflight: m.inflight,
	}
}

// chromeLines is the head and the rule under it, memoized together because they
// are built from the same key.
func (m *Model) chromeLines() (head, rule string) {
	key := m.chromeKey()
	if m.chrome[0] != "" && key == m.chromeAt {
		return m.chrome[0], m.chrome[1]
	}
	ell := m.deps.Theme.Glyphs.Ellipsis
	head = m.styles.muted.Render(ansi.Truncate("  "+headWords(key), max(m.width, 8), ell))
	count := countLabel(key)
	dashes := max(m.width-ansi.StringWidth(count)-1, 0)
	rule = m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) +
		" " + m.styles.muted.Render(count)
	m.chrome, m.chromeAt = [2]string{head, rule}, key
	return head, rule
}

// headWords says which board's sprints these are and which states were asked
// for. Both are what the list is, and the second is why a sprint somebody
// expected is not on it.
func headWords(key chromeKey) string {
	var b strings.Builder
	switch {
	case key.boards == 0:
		b.WriteString("no board")
	case key.boards == 1 && key.board != "":
		b.WriteString(key.board)
	default:
		b.WriteString(strconv.Itoa(key.boards) + " boards")
	}
	if key.more > 0 {
		b.WriteString(" (" + strconv.Itoa(key.more) + " more not read)")
	}
	b.WriteString(" · ")
	if key.showAll {
		b.WriteString("running, planned and closed")
		return b.String()
	}
	b.WriteString("running and planned")
	return b.String()
}

func countLabel(key chromeKey) string {
	switch {
	case key.inflight != opNone:
		return "asking the site"
	case key.state == filling:
		return "editing"
	case key.state == confirming:
		return "waiting for an answer"
	case key.loading && !key.loaded:
		return "asking the site"
	case key.failed && key.rows == 0:
		return "no answer"
	case key.rows == 1:
		return "1 sprint"
	}
	return strconv.Itoa(key.rows) + " sprints"
}

// bodyHeight is what is left under the head for whichever of the three screens
// is up.
func (m *Model) bodyHeight() int { return max(m.height-headHeight, 1) }

// rowsHeight is how many rows fit, less the line a refusal keeps under them.
func (m *Model) rowsHeight() int {
	h := m.bodyHeight()
	if m.refused() {
		h--
	}
	return max(h, 1)
}

// refused reports that the site said no and there are still rows to draw, which
// is the one state where the reason cannot be the pane's whole answer.
func (m *Model) refused() bool { return m.failure != nil && m.rowCount() > 0 }

// View draws the head, the rule, and whichever of the list, the form and the
// confirm is up. Only the visible rows are built, so a board with two hundred
// closed sprints costs what one with two costs.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	head, rule := m.chromeLines()
	lines = append(lines, head, rule)
	h := m.bodyHeight()
	switch m.state {
	case filling:
		lines = m.appendBlock(lines, m.formLines(), h)
	case confirming:
		lines = m.appendBlock(lines, m.confirmLines(), h)
	case browsing:
		lines = m.appendRows(lines, h)
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

func (m *Model) appendRows(lines []string, h int) []string {
	at := len(lines)
	if m.rowCount() == 0 {
		return m.appendEmpty(lines, h)
	}
	rows := m.rowsHeight()
	end := min(m.top+rows, m.rowCount())
	for i := m.top; i < end; i++ {
		lines = append(lines, m.row(i))
	}
	for len(lines)-at < rows {
		lines = append(lines, "")
	}
	if m.refused() {
		lines = append(lines, m.refusalLine())
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// appendEmpty says which kind of empty this is. There are five, and a reader
// cannot act on the difference unless the screen names it: no connection,
// nothing asked yet, a read in flight, a read that was refused, and an answer
// with no sprint in it — which is two answers, since a project with no board
// has nowhere for one and a board can simply have none in these states.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	say := func(text string) {
		lines = append(lines, m.styles.muted.Render(ansi.Truncate("  "+text, room+marker, ell)))
	}
	switch {
	case m.deps.Jira == nil:
		say("There is no Jira connection in this session.")
	case m.loading && !m.loaded:
		say("Asking the site" + ell)
	case m.failure != nil:
		lines = m.appendFailure(lines, room)
	case !m.loaded:
		say("Nothing has been asked of Jira yet.")
	case len(m.boards) == 0:
		say("No board of this project is readable in this session, so it has no sprints here.")
		say("A sprint lives on a board, and this session found none.")
	case m.showAll:
		say("This board has no sprints at all.")
		say(m.keys.New.Help().Key + " plans the first one.")
	default:
		say("No sprint here is running or planned.")
		say(m.keys.Closed.Help().Key + " shows the closed ones too, and " +
			m.keys.New.Help().Key + " plans a new one.")
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
	lines = append(lines, m.styles.danger.Render("  The site refused: "+m.failedOp.word()+"."))
	said := strings.Split(ansi.Wrap(reason, room, ""), "\n")
	for _, line := range said[:min(len(said), reasonLines)] {
		lines = append(lines, m.styles.muted.Render("  "+line))
	}
	return append(lines, "", m.styles.muted.Render("  "+retryHint))
}

// retryHint names the kernel's own refresh, which this view registers nothing
// for and must not spell out for itself.
var retryHint = kernel.DefaultGlobalKeys().Refresh.Help().Key + " asks again."

func (m *Model) refusalLine() string {
	reason, _ := jira.Reason(m.failure)
	return m.styles.danger.Render(
		ansi.Truncate("  "+reason, max(m.width, 8), m.deps.Theme.Glyphs.Ellipsis))
}

// appendBlock puts a screen of prose under the head, cut to the room there is
// and padded out to it so that the frame is exactly the box the kernel gave.
func (m *Model) appendBlock(lines, block []string, h int) []string {
	at := len(lines)
	lines = append(lines, block[:min(len(block), h)]...)
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// formLines is the create-or-edit screen: what is being filled in, one field a
// line with what is wrong with it beside it, and the two keys that end it.
func (m *Model) formLines() []string {
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	out := make([]string, 0, int(fieldCount)+6)
	out = append(out, m.styles.accent.Render(ansi.Truncate("  "+m.formTitle(), room, ell)), "")
	for at := field(0); at < fieldCount; at++ {
		out = append(out, m.fieldLine(at, room))
		if problem := m.form.problems[at]; problem != "" {
			out = append(out, m.styles.danger.Render(ansi.Truncate("  "+strings.Repeat(" ", formLabel)+problem, room, ell)))
		}
	}
	if m.form.notice != "" {
		out = append(out, "", m.styles.warning.Render(ansi.Truncate("  "+m.form.notice, room, ell)))
	}
	return append(out, "", "  "+m.styles.muted.Render(m.zones.Mark(zoneSend, m.keys.Save.Help().Key+" sends it"))+
		m.styles.muted.Render(" · ")+
		m.styles.muted.Render(m.zones.Mark(zoneCancel, m.keys.Discard.Help().Key+" discards it")))
}

func (m *Model) formTitle() string {
	if m.form.mode == formCreate {
		if name := strings.TrimSpace(m.form.board.Name); name != "" {
			return "A new sprint on " + name + ", planned and not running"
		}
		return "A new sprint, planned and not running"
	}
	return named(m.form.sprint) + ", which is " + stateWord(m.form.sprint.State)
}

// fieldLine is one field: its name, what is in it, and nothing about where it
// is on screen — the line is marked where it is drawn so a click resolves to it.
func (m *Model) fieldLine(at field, room int) string {
	label := padTruncate(at.label(), formLabel, m.deps.Theme.Glyphs.Ellipsis)
	if at == m.form.at {
		label = m.styles.accent.Render(label)
	} else {
		label = m.styles.muted.Render(label)
	}
	value := m.form.inputs[at].View()
	if m.form.locked() && (at == fieldStart || at == fieldEnd) {
		value = m.styles.muted.Render(padTruncate(m.form.value(at), min(room-formLabel, datesWidth), m.deps.Theme.Glyphs.Ellipsis))
	}
	return "  " + m.zones.Mark(fieldZone(at), label+strings.Repeat(" ", formGutter)+value)
}

// confirmLines is the question in front of a move that cannot be undone. It
// names the sprint, what the move does to it, and what happens to the issues in
// it, because those are the three things a reader needs and none of them is on
// the row.
func (m *Model) confirmLines() []string {
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	sp := m.pending.sprint
	out := make([]string, 0, 10)
	out = append(out, m.styles.warning.Render(ansi.Truncate("  "+m.confirmQuestion(), room, ell)), "")
	for _, line := range m.confirmProse(sp) {
		for _, wrapped := range strings.Split(ansi.Wrap(line, room, ""), "\n") {
			out = append(out, m.styles.muted.Render("  "+wrapped))
		}
	}
	return append(out, "", "  "+m.styles.base.Render(m.zones.Mark(zoneConfirm, m.keys.Yes.Help().Key+" goes ahead"))+
		m.styles.muted.Render(" · ")+
		m.styles.muted.Render(m.zones.Mark(zoneRefuse, m.keys.No.Help().Key+" leaves it alone")))
}

func (m *Model) confirmQuestion() string {
	sp := m.pending.sprint
	if m.pending.op == opStart {
		return "Start " + named(sp) + "?"
	}
	return "Complete " + named(sp) + "?"
}

// confirmProse is what the move does, in sentences. The count of what is still
// open is deliberately absent and said to be absent: the port has no read for
// the issues in a sprint, and a number this program cannot get is not one it
// may imply.
func (m *Model) confirmProse(sp jira.Sprint) []string {
	board := m.pending.board
	where := ""
	if board != "" {
		where = " on " + board
	}
	if m.pending.op == opStart {
		return []string{
			"It runs " + m.datesOf(sp) + where + ".",
			"A sprint that has started cannot be made a planned one again, and the dates go to " +
				"everyone looking at the board.",
		}
	}
	return []string{
		"Closing a sprint" + where + " cannot be undone.",
		"Every issue in it that is not done leaves the sprint: Jira puts them back in the backlog.",
		"This session cannot say how many that is — nothing in the port reads the issues in a sprint — " +
			"so check the board first if it matters.",
	}
}

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes: a sprint name is whatever anybody typed, and a
// goal is a sentence in whatever language they wrote it in.
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
