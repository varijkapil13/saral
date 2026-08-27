package release

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	// marker is the gutter the selected row's arrow sits in.
	marker = 2
	gap    = 1
	// headHeight is the summary line and the caption row under it.
	headHeight = 2
	// inputChrome is what a typed line costs beyond the text: its two-cell
	// prompt and the cell the cursor sits in past the last rune.
	inputChrome = 3
	// reasonLines is how many lines of a refusal the pane wraps before it stops.
	reasonLines = 4

	minName = 12
	maxName = 28
	// minDescription is the narrowest description worth a column. Under that it
	// is given up: three columns of a sentence is noise where the room could go
	// to the name.
	minDescription = 12
	stateWidth     = 10
	openWidth      = 5
	dateWidth      = 10
	// labelWidth is the gutter the editor's field names sit in.
	labelWidth = 14

	// rowCacheLimit is how many rendered rows are kept. A project has tens of
	// versions rather than thousands, so this is a window and its overscan in
	// both forms, several relayouts deep.
	rowCacheLimit = 256
	// overscan is how many rows outside the window are rendered into the memo,
	// so the next scroll step is a hit rather than a build.
	overscan = 4
)

// unknownOpen is what the open column says when nobody has counted. Drawing a
// zero there would say a version has nothing left open on it, which is the one
// thing a release decision turns on.
const unknownOpen = "?"

// The four states a version is in, as words. They are derived from the port's
// own booleans and dates and never from anything the site can rename.
const (
	stateReleased   = "released"
	stateArchived   = "archived"
	stateOverdue    = "overdue"
	stateUnreleased = "unreleased"
)

// styles are the list's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	selected lipgloss.Style
	name     lipgloss.Style
	muted    lipgloss.Style
	danger   lipgloss.Style
	warning  lipgloss.Style
	success  lipgloss.Style
	accent   lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		selected: t.Selected,
		name:     t.Base,
		muted:    t.Muted,
		danger:   t.Danger,
		warning:  t.Warning,
		success:  t.Success,
		accent:   t.Accent,
	}
}

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width       int
	name        int
	state       int
	open        int
	start       int
	release     int
	description int
}

// planLayout drops columns from the right until the name and the state have
// their room. Those two are what says which version this is and whether it has
// shipped; a description squeezed to nothing costs nothing that cannot be read
// in the editor.
func planLayout(width, widestName int) layout {
	lay := layout{
		width: max(width, marker+minName+gap+stateWidth),
		name:  min(max(widestName, minName), maxName),
		state: stateWidth, open: openWidth, start: dateWidth, release: dateWidth,
	}
	// The columns are given up from the right: the description first, since it
	// is the one thing the editor shows in full anyway, then the dates, then the
	// count.
	for _, drop := range []*int{&lay.release, &lay.start, &lay.open} {
		if lay.fixed() <= lay.width {
			break
		}
		*drop = 0
	}
	if rest := lay.width - lay.fixed(); rest >= gap+minDescription {
		lay.description = rest - gap
	}
	return lay
}

// fixed is what every column but the description takes, gaps included.
func (lay layout) fixed() int {
	n := marker + lay.name + gap + lay.state
	for _, w := range []int{lay.open, lay.start, lay.release} {
		if w > 0 {
			n += gap + w
		}
	}
	return n
}

func (m *Model) relayout() {
	lay := planLayout(m.width, m.widestName())
	if lay == m.lay && m.head != "" {
		return
	}
	m.lay = lay
	m.head = lay.caption(m.styles, m.deps.Theme.Glyphs.Ellipsis)
	m.rows.reset()
}

func (m *Model) widestName() int {
	widest := 0
	for i := range m.versions {
		widest = max(widest, ansi.StringWidth(m.versions[i].Name))
	}
	return widest
}

// caption is the row of column names. It is built on a relayout and on nothing
// else, because that is the only thing it depends on.
func (lay layout) caption(st *styles, ell string) string {
	var b strings.Builder
	b.Grow(lay.width + 16)
	b.WriteString(strings.Repeat(" ", marker))
	b.WriteString(padTruncate("version", lay.name, ell))
	for _, cell := range []struct {
		text  string
		width int
	}{
		{"state", lay.state}, {"open", lay.open}, {"starts", lay.start},
		{"releases", lay.release}, {"description", lay.description},
	} {
		if cell.width <= 0 {
			continue
		}
		b.WriteString(strings.Repeat(" ", gap))
		b.WriteString(padTruncate(cell.text, cell.width, ell))
	}
	return st.muted.Render(padTruncate(b.String(), lay.width, ell))
}

// rowCells is one version as its row draws it. A version carries no updated
// stamp, so everything drawn from it is here — and it is built when the version
// arrives rather than per frame, because rendering a date and a count allocates
// and a frame asks for forty rows.
type rowCells struct {
	id          string
	name        string
	state       string
	open        string
	start       string
	release     string
	description string
}

// rowKey is what makes two renderings of a row the same rendering.
type rowKey struct {
	cells    rowCells
	lay      layout
	selected bool
	gen      int
}

// memo is a bounded cache of rendered rows. Past its limit it is emptied rather
// than evicted one at a time, because a scroll invalidates a screenful at once
// anyway and clearing keeps the map's capacity.
//
// It is generic because the list and the release flow both memoize rows, over
// keys of their own.
type memo[K comparable] struct {
	rows  map[K]string
	limit int
}

func newMemo[K comparable](limit int) *memo[K] {
	return &memo[K]{rows: make(map[K]string, limit), limit: limit}
}

func (c *memo[K]) get(k K) (string, bool) {
	s, ok := c.rows[k]
	return s, ok
}

func (c *memo[K]) put(k K, s string) {
	if len(c.rows) >= c.limit {
		clear(c.rows)
	}
	c.rows[k] = s
}

func (c *memo[K]) reset() { clear(c.rows) }

// rowZone is the click target one row is marked with, named by the version's id
// because that is stable for the life of the list and a row number is not.
func rowZone(id string) string { return "version:" + id }

// cellsOf is one version's row, drawn out once.
func cellsOf(v jira.Version, today jira.Date) rowCells {
	return rowCells{
		id: v.ID, name: v.Name, state: versionState(v, today), open: openLabel(v),
		start: v.StartDate.String(), release: v.ReleaseDate.String(), description: v.Description,
	}
}

// rebuildCells draws every version out again. It runs when the versions change,
// when one of them is written, and when the reader's own date has moved on —
// which is what turns a version overdue.
func (m *Model) rebuildCells() {
	m.day = m.today()
	m.cells = m.cells[:0]
	for i := range m.versions {
		m.cells = append(m.cells, cellsOf(m.versions[i], m.day))
	}
	m.rows.reset()
}

func (m *Model) rowKeyOf(at int, selected bool) rowKey {
	return rowKey{cells: m.cells[at], lay: m.lay, selected: selected, gen: m.styles.gen}
}

func (m *Model) row(at int, selected bool) string {
	k := m.rowKeyOf(at, selected)
	if s, ok := m.rows.get(k); ok {
		return s
	}
	s := m.zones.Mark(rowZone(k.cells.id), renderRow(k, m.styles, m.deps.Theme))
	m.rows.put(k, s)
	return s
}

// warm renders the overscan into the memo so that the next scroll step is a
// cache hit rather than a row build. It draws nothing.
func (m *Model) warm(end int) {
	for i := max(m.top-overscan, 0); i < min(end+overscan, len(m.versions)); i++ {
		if i < m.top || i >= end {
			m.row(i, false)
		}
	}
}

// today is the reader's own calendar date, which is what an overdue version is
// overdue against. The account's zone comes from the probe, so a version due
// today is not overdue for somebody the other side of a date line.
func (m *Model) today() jira.Date {
	now := m.now()
	if now.IsZero() {
		return jira.Date{}
	}
	return jira.DateOf(now.In(m.deps.Caps.Location()))
}

// versionState is which of the four states a version is in. Archived beats
// released because an archived version is out of the way whatever else is true
// of it, and overdue is a release date in the past on something unreleased.
func versionState(v jira.Version, today jira.Date) string {
	switch {
	case v.Archived:
		return stateArchived
	case v.Released:
		return stateReleased
	case !v.ReleaseDate.IsZero() && !today.IsZero() && v.ReleaseDate.Before(today):
		return stateOverdue
	default:
		return stateUnreleased
	}
}

// openLabel is the count of what is still open, or the mark that says nobody has
// asked. Version.Unresolved is nil until something counts it, and nil is not
// zero.
func openLabel(v jira.Version) string {
	if v.Unresolved == nil {
		return unknownOpen
	}
	return strconv.Itoa(*v.Unresolved)
}

// renderRow draws one row to exactly lay.width columns.
func renderRow(k rowKey, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(k.lay.width + 32)

	if k.selected {
		b.WriteString(padTruncate(t.Glyphs.Collapsed, marker, ell))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	name := padTruncate(k.cells.name, k.lay.name, ell)
	if k.selected {
		b.WriteString(name)
	} else {
		b.WriteString(st.name.Render(name))
	}
	state := padTruncate(k.cells.state, k.lay.state, ell)
	b.WriteString(strings.Repeat(" ", gap))
	if k.selected {
		b.WriteString(state)
	} else {
		b.WriteString(stateStyle(k.cells.state, st).Render(state))
	}
	for _, cell := range []struct {
		text  string
		width int
	}{
		{k.cells.open, k.lay.open},
		{k.cells.start, k.lay.start},
		{k.cells.release, k.lay.release},
		{k.cells.description, k.lay.description},
	} {
		if cell.width <= 0 {
			continue
		}
		b.WriteString(strings.Repeat(" ", gap))
		text := padTruncate(cell.text, cell.width, ell)
		if k.selected {
			b.WriteString(text)
			continue
		}
		b.WriteString(st.muted.Render(text))
	}
	line := padTruncate(b.String(), k.lay.width, ell)
	if k.selected {
		return st.selected.Render(line)
	}
	return line
}

func stateStyle(state string, st *styles) lipgloss.Style {
	switch state {
	case stateReleased:
		return st.success
	case stateOverdue:
		return st.warning
	case stateArchived:
		return st.muted
	default:
		return st.name
	}
}

// summaryKey is everything the summary line is built from, so that the line is
// rebuilt when one of them moves and never once per frame.
type summaryKey struct {
	project    string
	width, gen int
	versions   int
	released   int
	loading    bool
	loaded     bool
	failed     bool
	counting   bool
	saving     bool
	editing    bool
	creating   bool
	checked    int64
}

func (m *Model) summaryKey() summaryKey {
	released := 0
	for i := range m.versions {
		if m.versions[i].Released {
			released++
		}
	}
	return summaryKey{
		project: m.deps.Project, width: m.width, gen: m.styles.gen,
		versions: len(m.versions), released: released,
		loading: m.loading, loaded: m.loaded, failed: m.failure != nil,
		counting: m.counting != "", saving: m.saving,
		editing: m.mode == editing, creating: m.mode == editing && m.form.id == "",
		checked: m.checked.UnixNano(),
	}
}

// summaryLine says what is on screen and what is being waited for. It keeps the
// time the versions last came from the site, because a status line goes away and
// a question about how old the screen is comes back.
func (m *Model) summaryLine() string {
	key := m.summaryKey()
	if m.sum != "" && key == m.sumAt {
		return m.sum
	}
	var b strings.Builder
	b.WriteString("  ")
	if key.project != "" {
		b.WriteString(key.project)
		b.WriteString(" ")
	}
	b.WriteString(plural(key.versions, "version", "versions"))
	if key.released > 0 {
		b.WriteString(" · ")
		b.WriteString(strconv.Itoa(key.released))
		b.WriteString(" released")
	}
	switch {
	case key.counting:
		b.WriteString(" · counting what is open")
		b.WriteString(m.deps.Theme.Glyphs.Ellipsis)
	case key.saving:
		b.WriteString(" · saving")
		b.WriteString(m.deps.Theme.Glyphs.Ellipsis)
	case key.loading:
		b.WriteString(" · reading")
		b.WriteString(m.deps.Theme.Glyphs.Ellipsis)
	case !m.checked.IsZero():
		b.WriteString(" · read ")
		b.WriteString(m.checked.In(m.deps.Caps.Location()).Format("15:04"))
	}
	// Counts are one request each, so the pane says out loud that the column is
	// unread rather than leaving a reader to wonder why it is full of marks.
	if key.versions > 0 && m.anyUncounted() {
		b.WriteString(" · open counts are read when a version is released")
	}
	m.sum = m.styles.muted.Render(ansi.Truncate(b.String(), max(m.width, 8), m.deps.Theme.Glyphs.Ellipsis))
	m.sumAt = key
	return m.sum
}

func (m *Model) anyUncounted() bool {
	for i := range m.versions {
		if m.versions[i].Unresolved == nil {
			return true
		}
	}
	return false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// appendEmpty says which kind of empty this is. There are five, and a reader
// cannot act on the difference unless the pane names it: no site in this
// session, no project to read versions of, a read in flight, a read that
// failed, and a project that genuinely has no versions.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	switch {
	case m.deps.Jira == nil:
		lines = append(lines, m.styles.muted.Render("  No Jira connection in this session yet."))
	case strings.TrimSpace(m.deps.Project) == "":
		lines = append(lines, m.styles.muted.Render(
			"  Versions belong to a project, and this session is not scoped to one."))
	case m.loading && !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Reading the versions"+ell))
	case m.failure != nil:
		lines = m.appendFailure(lines, room, h)
	case !m.loaded:
		lines = append(lines, m.styles.muted.Render("  Nothing has been asked of Jira yet."))
	default:
		lines = append(lines,
			m.styles.muted.Render(ansi.Truncate("  "+m.deps.Project+" has no versions yet.", room, ell)),
			m.styles.muted.Render("  "+newHint))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

// appendFailure is the refusal in the words the site used, wrapped rather than
// cut: a transport failure names a host and a port before it says what is wrong
// with them.
func (m *Model) appendFailure(lines []string, room, h int) []string {
	reason, _ := jira.Reason(m.failure)
	lines = append(lines, m.styles.danger.Render("  "+m.what))
	said := strings.Split(ansi.Wrap(reason, room, ""), "\n")
	for _, line := range said[:min(len(said), min(reasonLines, max(h-2, 1)))] {
		lines = append(lines, m.styles.muted.Render("  "+line))
	}
	return append(lines, "", m.styles.muted.Render("  "+retryHint))
}

// The two sentences that name a key, spelt from the bindings rather than written
// out. The retry names the kernel's own refresh, which this view registers
// nothing for.
var (
	retryHint = kernel.DefaultGlobalKeys().Refresh.Help().Key + " reads them again."
	newHint   = defaultKeys().New.Help().Key + " creates one."
)

// appendForm draws the editor under the rows: which version is being typed, a
// line per field, the line being typed into, and why the values are refused.
func (m *Model) appendForm(lines []string) []string {
	ell := m.deps.Theme.Glyphs.Ellipsis
	title := "  a new version"
	if m.form.id != "" {
		title = "  editing " + m.form.name
	}
	if m.saving {
		title += " · saving" + ell
	}
	clip := func(line string) string { return ansi.Truncate(line, max(m.width, 1), ell) }
	lines = append(lines, m.styles.accent.Render(clip(title)))
	for f := field(0); f < fieldCount; f++ {
		label := m.styles.muted.Render(padTruncate("  "+fieldLabels[f], labelWidth, ell))
		if f == m.form.at {
			lines = append(lines, clip(label+m.form.input.View()))
			continue
		}
		lines = append(lines, clip(label+m.form.values[f]))
	}
	if m.form.problem != "" {
		lines = append(lines, m.styles.danger.Render(clip("  "+m.form.problem)))
	}
	return lines
}

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes: a version name is whatever anybody typed and a
// description is whatever anybody pasted.
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

// View draws the summary, the caption and the window of rows under them. Only
// the visible rows are built, so a project with four hundred versions costs what
// one with four costs.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	// The cells mirror the versions, so a write that appended one and a day that
	// has rolled over both mean they are drawn again.
	if m.day != m.today() || len(m.cells) != len(m.versions) {
		m.rebuildCells()
	}
	lines := m.lines[:0]
	lines = append(lines, m.summaryLine(), m.head)
	h := m.rowsHeight()
	if len(m.versions) == 0 {
		lines = m.appendEmpty(lines, h)
	} else {
		end := min(m.top+h, len(m.versions))
		for i := m.top; i < end; i++ {
			lines = append(lines, m.row(i, i == m.cursor))
		}
		for i := end - m.top; i < h; i++ {
			lines = append(lines, "")
		}
		m.warm(end)
	}
	if m.mode == editing {
		lines = m.appendForm(lines)
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}
