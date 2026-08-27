package attach

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
	// inputChrome is what the path's line costs beyond the text: its two-cell
	// prompt and the cell the cursor sits in past the last rune.
	inputChrome = 3
	sizeWidth   = 9
	whoWidth    = 18
	dateWidth   = 10
	minName     = 16
	// maxName keeps the other columns beside the name rather than at the far edge
	// of a wide terminal.
	maxName = 48
	// maxListRows is how much of the box the list may take. The preview is what
	// the pane is for, and an image squeezed into four rows is not one anybody can
	// look at; z gives it the rest.
	maxListRows    = 8
	minPreviewRows = 3
	// reasonLines is how many lines of a refusal the pane wraps before it stops.
	reasonLines = 4
	barWidth    = 20
	// rowMemoLimit holds the visible window and its overscan several relayouts
	// deep. Past it the map is cleared rather than evicted one row at a time,
	// because a scroll invalidates a screenful at once anyway.
	rowMemoLimit = 256
)

// zones are the click targets this pane marks. Each is prefixed per instance so
// that two panes on one screen cannot answer for each other.
const (
	zonePreview = "preview"
	zoneSend    = "send"
	zoneCancel  = "cancel"
	zoneConfirm = "confirm"
	zoneKeep    = "keep"
	zoneFile    = "file:"
)

// styles are the pane's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	selected lipgloss.Style
	name     lipgloss.Style
	note     lipgloss.Style
	muted    lipgloss.Style
	rule     lipgloss.Style
	accent   lipgloss.Style
	danger   lipgloss.Style
	warning  lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		selected: t.Selected,
		name:     t.Base,
		note:     t.Muted,
		muted:    t.Muted,
		rule:     t.Muted,
		accent:   t.Accent,
		danger:   t.Danger,
		warning:  t.Warning,
	}
}

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width int
	name  int
	size  int
	who   int
	date  int
	pad   int
}

// planLayout drops the date, then the author, then the size before the name loses
// its room: the name is the only part of a row that says which file it is.
func planLayout(width int) layout {
	lay := layout{width: max(width, marker+minName), size: sizeWidth, who: whoWidth, date: dateWidth}
	for {
		lay.name = lay.width - marker - optionalWidth(lay)
		if lay.name >= minName {
			break
		}
		switch {
		case lay.date > 0:
			lay.date = 0
		case lay.who > 0:
			lay.who = 0
		case lay.size > 0:
			lay.size = 0
		}
		if lay.date == 0 && lay.who == 0 && lay.size == 0 {
			lay.name = lay.width - marker
			break
		}
	}
	lay.name = max(lay.name, 1)
	if lay.name > maxName {
		lay.pad, lay.name = lay.name-maxName, maxName
	}
	return lay
}

func optionalWidth(lay layout) int {
	total := 0
	for _, column := range [3]int{lay.size, lay.who, lay.date} {
		if column > 0 {
			total += gap + column
		}
	}
	return total
}

// rowKey is what makes two renderings of a row the same rendering.
//
// The size and the date are the raw values rather than the strings they are drawn
// as. Formatting them to build a key allocates twice a row on every frame, which
// is most of what a memoized frame would then cost.
type rowKey struct {
	id       string
	name     string
	who      string
	size     int64
	created  int64
	loc      *time.Location
	lay      layout
	selected bool
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

// zoneOf is the click target one row is marked with. The attachment id is stable
// for the life of the pane, and zone ids are never freed, so it is the id rather
// than the row number.
func (m *Model) zoneOf(at int) string {
	if at < 0 || at >= len(m.files) {
		return ""
	}
	return zoneFile + m.files[at].ID
}

func (m *Model) row(at int) string {
	att := &m.files[at]
	k := rowKey{
		id: att.ID, name: att.Filename, who: att.Author.DisplayName,
		size: att.Size, created: att.Created.Unix(), loc: m.deps.Caps.Location(),
		lay: m.lay, selected: at == m.cursor, gen: m.styles.gen,
	}
	if s, ok := m.memo.get(k); ok {
		return s
	}
	s := m.zones.Mark(m.zoneOf(at), renderRow(k, m.styles, m.deps.Theme))
	m.memo.put(k, s)
	return s
}

// zeroUnix is what a zero time.Time reports, which is what a read that carried no
// date leaves on an attachment.
var zeroUnix = time.Time{}.Unix()

// dateOf is when the file was attached, in the Jira account's timezone rather
// than the machine's. A zero time is a read that did not carry one, which is a
// blank cell and not the epoch.
func dateOf(k rowKey) string {
	if k.created == zeroUnix {
		return ""
	}
	return time.Unix(k.created, 0).In(k.loc).Format("2006-01-02")
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

	name := padTruncate(k.name, k.lay.name, ell)
	if k.selected {
		b.WriteString(name)
	} else {
		b.WriteString(st.name.Render(name))
	}
	for _, column := range [3]struct {
		width int
		text  string
		right bool
	}{
		{k.lay.size, humanSize(k.size), true},
		{k.lay.who, k.who, false},
		{k.lay.date, dateOf(k), false},
	} {
		if column.width == 0 {
			continue
		}
		b.WriteString(strings.Repeat(" ", gap))
		cell := padTruncate(column.text, column.width, ell)
		if column.right {
			cell = padLeft(column.text, column.width, ell)
		}
		if k.selected {
			b.WriteString(cell)
		} else {
			b.WriteString(st.note.Render(cell))
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

// headKey is everything the two lines above the rows are built from, so that
// they are rebuilt when one of them moves and never once per frame.
type headKey struct {
	width   int
	gen     int
	issue   string
	files   int
	bytes   int64
	loading bool
	loaded  bool
	failed  bool
}

func (m *Model) headKey() headKey {
	var bytes int64
	for i := range m.files {
		bytes += m.files[i].Size
	}
	return headKey{
		width: m.width, gen: m.styles.gen, issue: m.issue, files: len(m.files), bytes: bytes,
		loading: m.loading, loaded: m.loaded, failed: m.failure != nil,
	}
}

func (m *Model) headLine() string {
	key := m.headKey()
	if m.top1 != "" && key == m.headAt {
		return m.top1
	}
	what := "the files attached to " + m.issue
	if m.issue == "" {
		what = "no issue"
	}
	m.top1 = m.styles.accent.Render(
		ansi.Truncate("  "+what, max(m.width, 8), m.deps.Theme.Glyphs.Ellipsis))
	m.head, m.headAt = m.buildRule(key), key
	return m.top1
}

// rule is the line under the head, with the count at its right end. It is built
// beside the head line, because both come from the same key.
func (m *Model) rule() string { return m.head }

func (m *Model) buildRule(key headKey) string {
	count := countLabel(key, m.deps.Theme.Glyphs)
	dashes := max(m.width-ansi.StringWidth(count)-1, 0)
	return m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) +
		" " + m.styles.muted.Render(count)
}

func countLabel(key headKey, g kernel.Glyphs) string {
	switch {
	case key.loading:
		return "asking the site" + g.Ellipsis
	case key.failed && key.files == 0:
		return "no answer"
	case key.files == 0 && !key.loaded:
		return "nothing read yet"
	case key.files == 0:
		return "nothing attached"
	case key.files == 1:
		return "1 file " + g.Dot + " " + humanSize(key.bytes)
	default:
		return strconv.Itoa(key.files) + " files " + g.Dot + " " + humanSize(key.bytes)
	}
}

type divKey struct {
	name  string
	size  int64
	width int
	gen   int
}

// divider captions the preview: which file it is of, and how large that file is.
func (m *Model) divider() string {
	att := m.selected()
	key := divKey{width: m.width, gen: m.styles.gen}
	if att != nil {
		key.name, key.size = att.Filename, att.Size
	}
	if m.div != "" && key == m.divAt {
		return m.div
	}
	line := m.deps.Theme.Glyphs.HLine
	if att == nil {
		m.div, m.divAt = m.styles.rule.Render(strings.Repeat(line, max(m.width, 0))), key
		return m.div
	}
	ell := m.deps.Theme.Glyphs.Ellipsis
	head := strings.Repeat(line, 2) + " " + ansi.Truncate(att.Filename, max(m.width/2, 8), ell) + " "
	tail := " " + humanSize(att.Size) + " " + strings.Repeat(line, 2)
	fill := max(m.width-ansi.StringWidth(head)-ansi.StringWidth(tail), 0)
	m.div, m.divAt = m.styles.rule.Render(head+strings.Repeat(line, fill)+tail), key
	return m.div
}

// split is the box divided between the list and the preview. The preview keeps
// the larger share: it is what the pane exists for, and z takes the rest.
//
// An empty list takes the whole of it. There is nothing to preview, and the
// sentence saying which kind of empty this is runs to several lines when the
// site refused.
func (m *Model) split() (rows, pane int) {
	body := m.height - headHeight - m.promptHeight() - m.refusalHeight()
	switch {
	case body <= 0:
		return 0, 0
	case len(m.files) == 0:
		return body, 0
	case body == 1:
		return 1, 0
	}
	avail := body - 1
	if m.grown {
		return 0, avail
	}
	rows = min(len(m.files), maxListRows)
	if avail-rows < minPreviewRows {
		rows = max(avail-minPreviewRows, 1)
	}
	if rows >= avail {
		return avail, 0
	}
	return rows, avail - rows
}

func (m *Model) promptHeight() int {
	if m.mode == browsing {
		return 0
	}
	return 1
}

// refusalHeight is the line a refusal keeps under the rows. A pane that is empty
// because the site said no says so where the rows would be; one that still has
// rows has to say it somewhere else, because those rows are not an answer to the
// question that was refused.
func (m *Model) refusalHeight() int {
	if m.failure != nil && len(m.files) > 0 {
		return 1
	}
	return 0
}

// rowsHeight is how many rows the list has, which is what a page key moves by.
func (m *Model) rowsHeight() int {
	rows, _ := m.split()
	return max(rows, 1)
}

// previewBox is the room the preview has. It is asked for before a download so
// that the geometry the graphics protocol is told matches the frame it lands in.
func (m *Model) previewBox() previewBox {
	_, pane := m.split()
	return previewBox{width: max(m.width, 0), height: max(pane, 0)}
}

// View draws the head, the list, the caption and the preview under it. Only the
// visible rows are built, so an issue with two hundred files costs what one with
// three costs.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	lines = append(lines, m.headLine(), m.rule())
	rows, pane := m.split()
	if rows > 0 {
		if len(m.files) == 0 {
			lines = m.appendEmpty(lines, rows)
		} else {
			end := min(m.top+rows, len(m.files))
			for i := m.top; i < end; i++ {
				lines = append(lines, m.row(i))
			}
			for i := end - m.top; i < rows; i++ {
				lines = append(lines, "")
			}
		}
	}
	if pane > 0 {
		lines = append(lines, m.divider())
		lines = m.appendPreview(lines, pane)
	}
	if m.refusalHeight() > 0 {
		lines = append(lines, m.refusalLine())
	}
	switch m.mode {
	case typing:
		lines = append(lines, m.promptLine())
	case confirming:
		lines = append(lines, m.confirmLine())
	case browsing:
	}
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	lines = lines[:m.height]
	m.lines = lines
	return strings.Join(lines, "\n")
}

// appendEmpty says which kind of empty this is. A site that refused says so in
// its own words and keeps saying it, because the status line that said it first
// is gone by the next keypress.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	switch {
	case m.issue == "":
		lines = append(lines, m.styles.muted.Render("  There is no issue behind this pane."))
	case m.deps.Jira == nil:
		lines = append(lines, m.styles.muted.Render("  No Jira connection in this session yet."))
	case m.loading:
		lines = append(lines, m.styles.muted.Render("  Reading what is attached"+ell))
	case m.failure != nil:
		lines = m.appendFailure(lines, room)
	case !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Nothing has been asked of Jira yet."))
	default:
		lines = append(lines, m.styles.muted.Render(
			ansi.Truncate("  Nothing is attached to "+m.issue+".", room, ell)))
		if m.canWrite {
			lines = append(lines, m.styles.muted.Render("  "+attachHint))
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
	return append(lines, "", m.styles.muted.Render("  "+retryHint))
}

// refusalLine keeps the site's refusal under the rows. The status line that said
// it first is gone by the next keypress.
func (m *Model) refusalLine() string {
	reason, _ := jira.Reason(m.failure)
	return m.styles.danger.Render(
		ansi.Truncate("  "+reason, max(m.width, 8), m.deps.Theme.Glyphs.Ellipsis))
}

// appendPreview draws whatever this terminal can show of the file the reader
// asked for. Nothing is fetched by moving the cursor: a preview costs a round
// trip, so it is asked for.
func (m *Model) appendPreview(lines []string, h int) []string {
	at := len(lines)
	lines = append(lines, m.previewBody(h)...)
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// paneKey is everything the preview region is built from, marking included, so
// that a steady frame reuses the lines rather than rebuilding and re-marking them
// under every keystroke.
type paneKey struct {
	id       string
	kind     previewKind
	why      string
	fetching bool
	written  int64
	total    int64
	name     string
	size     int64
	mime     string
	width    int
	height   int
	gen      int
}

func (m *Model) paneKey(h int) paneKey {
	key := paneKey{
		fetching: m.asked != "", written: m.written, total: m.total,
		width: m.width, height: h, gen: m.styles.gen,
		id: m.shown.id, kind: m.shown.kind, why: m.shown.why,
	}
	if att := m.selected(); att != nil {
		key.name, key.size, key.mime = att.Filename, att.Size, att.MimeType
		if m.asked == att.ID {
			key.id = att.ID
		}
	}
	return key
}

func (m *Model) previewBody(h int) []string {
	key := m.paneKey(h)
	if m.pane != nil && key == m.paneAt {
		return m.pane
	}
	body := m.previewLines(h)
	if m.zones.Enabled() {
		body = m.zones.MarkLines(zonePreview, body)
	}
	m.pane, m.paneAt = body, key
	return body
}

func (m *Model) previewLines(h int) []string {
	att := m.selected()
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	switch {
	case att == nil:
		return nil
	case m.asked == att.ID:
		return []string{m.styles.muted.Render(ansi.Truncate("  "+m.fetching(*att), room, ell))}
	case m.shown.id != att.ID, m.shown.kind == previewNone:
		return []string{m.styles.muted.Render(ansi.Truncate("  "+m.offer(*att), room, ell))}
	case m.shown.kind == previewInline, m.shown.kind == previewCells:
		if len(m.shown.lines) > h {
			return m.shown.lines[:h]
		}
		return m.shown.lines
	default:
		return m.describe(*att, room, ell)
	}
}

// describe is the last resort: what the file is, how big it is, and why it is not
// a picture. The reason is not decoration — a pane showing a filename where an
// image was expected is otherwise one nobody can tell from a broken one.
func (m *Model) describe(att jira.Attachment, room int, ell string) []string {
	out := []string{
		m.styles.name.Render(ansi.Truncate("  "+att.Filename, room, ell)),
		m.styles.note.Render(ansi.Truncate(
			"  "+humanSize(att.Size)+" "+m.deps.Theme.Glyphs.Dot+" "+mediaType(att), room, ell)),
	}
	if m.shown.why != "" {
		out = append(out, "", m.styles.muted.Render(ansi.Truncate("  "+m.shown.why, room, ell)))
	}
	return append(out, "", m.styles.muted.Render(ansi.Truncate("  "+openHint, room, ell)))
}

// mediaType is what the file says it is. MimeType can be empty on a real site,
// which is a different answer from octet-stream and worth keeping apart.
func mediaType(att jira.Attachment) string {
	if strings.TrimSpace(att.MimeType) == "" {
		return "no media type given"
	}
	return att.MimeType
}

// fetching is the running total, which the port reports in bytes, plus a bar
// where the size is known. docs/UX.md asks for a real number wherever the API
// gives one, and this is one of the two places it does.
func (m *Model) fetching(att jira.Attachment) string {
	if m.total <= 0 {
		return "Fetching " + att.Filename + " " + m.deps.Theme.Glyphs.Ellipsis +
			" " + humanSize(m.written)
	}
	done := min(max(int(int64(barWidth)*m.written/m.total), 0), barWidth)
	bar := strings.Repeat(m.deps.Theme.Glyphs.ProgressOn, done) +
		strings.Repeat(m.deps.Theme.Glyphs.ProgressNo, barWidth-done)
	return "Fetching " + att.Filename + " " + bar + " " +
		humanSize(m.written) + " of " + humanSize(m.total)
}

// offer names the key that shows this file, spelt from the binding rather than
// written out, so it cannot teach a stroke the pane does not answer.
func (m *Model) offer(att jira.Attachment) string {
	if isImage(att) {
		return showHint + " shows " + att.Filename + " here."
	}
	return showHint + " opens " + att.Filename + " in whatever this desktop opens it with."
}

func (m *Model) promptLine() string {
	view := m.input.View()
	if !m.zones.Enabled() {
		return view
	}
	return m.zones.Mark(zoneSend, view)
}

// confirmLine is the confirmation, which names the file and what else the
// deletion breaks. Jira validates a media node against the issue it is on, so a
// description that showed this file is refused whole once the file is gone.
func (m *Model) confirmLine() string {
	name := m.confirm
	if at := m.indexOf(m.confirm); at >= 0 {
		name = m.files[at].Filename
	}
	text := "  Delete " + name + "? Anything that showed it will stop saving. "
	yes := m.zones.Mark(zoneConfirm, m.styles.danger.Render(deleteHint))
	no := m.zones.Mark(zoneKeep, m.styles.muted.Render(keepHint))
	return m.zones.Mark(zoneCancel, m.styles.warning.Render(
		ansi.Truncate(text, max(m.width-ansi.StringWidth(deleteHint+keepHint)-4, 8),
			m.deps.Theme.Glyphs.Ellipsis))) + yes + " " + no
}

// The sentences that name a key, spelt from the bindings rather than written out.
// The retry names the kernel's own refresh, which this pane registers nothing
// for.
var (
	retryHint  = kernel.DefaultGlobalKeys().Refresh.Help().Key + " tries the read again."
	showHint   = defaultKeys().Show.Help().Key
	openHint   = defaultKeys().Open.Help().Key + " opens it outside the terminal."
	attachHint = defaultKeys().Upload.Help().Key + " attaches one."
	deleteHint = defaultKeys().Confirm.Help().Key + " deletes it"
	keepHint   = defaultKeys().Keep.Help().Key + " keeps it"
)

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes: a filename is whatever anybody's machine allowed.
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

// padLeft is padTruncate for a column read right to left, which a byte count is.
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
