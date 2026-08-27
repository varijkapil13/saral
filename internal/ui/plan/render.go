package plan

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	marker = 2
	gap    = 2
	// headHeight is the head line and the rule under it.
	headHeight = 2
	minName    = 16
	// maxName keeps the columns beside the name rather than at the far edge of a
	// wide terminal.
	maxName    = 44
	statusWide = 10
	noteWidth  = 28
	// label is the width of the word that opens a detail line, so that the
	// sources, the search and the releases line up under the plan they belong to.
	label = 10
	// reasonLines is how many lines of a refusal the pane wraps before it stops.
	reasonLines = 4
	// rowMemoLimit holds a screenful and its overscan several relayouts deep. A
	// site holds a handful of plans; a profile can define as many as anybody
	// types, and past the limit the map is cleared rather than evicted one row
	// at a time.
	rowMemoLimit = 256
)

// retryHint is spelt from the kernel's own refresh binding rather than written
// out, since this view registers nothing for it.
var retryHint = kernel.DefaultGlobalKeys().Refresh.Help().Key + " asks again."

// styles are this view's own, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	selected lipgloss.Style
	name     lipgloss.Style
	note     lipgloss.Style
	muted    lipgloss.Style
	rule     lipgloss.Style
	status   lipgloss.Style
	warn     lipgloss.Style
	danger   lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		selected: t.Selected,
		name:     t.Base,
		note:     t.Muted,
		muted:    t.Muted,
		rule:     t.Muted,
		status:   t.Accent,
		warn:     t.Warning,
		danger:   t.Danger,
	}
}

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width  int
	name   int
	status int
	note   int
	pad    int
}

// planLayout drops the origin, then the status, before the name loses its room:
// the name is the only part of a row that says which plan it is.
func planLayout(width int) layout {
	lay := layout{width: max(width, marker+minName), status: statusWide, note: noteWidth}
	for {
		lay.name = lay.width - marker - optional(lay.status) - optional(lay.note)
		if lay.name >= minName {
			break
		}
		switch {
		case lay.note > 0:
			lay.note = 0
		case lay.status > 0:
			lay.status = 0
		default:
			lay.name = max(lay.name, 1)
			return lay
		}
	}
	if lay.name > maxName {
		lay.pad, lay.name = lay.name-maxName, maxName
	}
	return lay
}

func optional(w int) int {
	if w == 0 {
		return 0
	}
	return gap + w
}

// rowKind is what one line of the list is. A plan is the row a cursor acts on;
// everything else is the detail under an open one.
type rowKind uint8

const (
	rowPlan rowKind = iota
	rowDetail
	rowWarn
)

// viewRow is one line of the flattened list. The detail of an open plan is
// flattened into rows here rather than at render time, so that the window drawn
// per frame is a slice and the cursor and the wheel agree about what a line is.
type viewRow struct {
	plan int
	kind rowKind
	text string
}

// rowKey is what makes two renderings of a row the same rendering.
type rowKey struct {
	kind     rowKind
	id       string
	name     string
	status   string
	note     string
	text     string
	lay      layout
	selected bool
	problem  bool
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

// reflow rebuilds the flattened rows. It runs when a plan opens or closes, when
// the plans change and on a resize, and never per frame.
func (m *Model) reflow() {
	rows := m.rows[:0]
	for i := range m.plans {
		rows = append(rows, viewRow{plan: i, kind: rowPlan})
		if !m.open[m.plans[i].plan.ID] {
			continue
		}
		rows = m.appendDetail(rows, i)
	}
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = max(len(rows)-1, 0)
	}
	m.scrollToCursor()
}

// appendDetail is what an open plan says about itself: what it draws from, the
// search it renders to, where its dates come from, and its releases.
func (m *Model) appendDetail(rows []viewRow, at int) []viewRow {
	row := &m.plans[at]
	for _, s := range row.plan.Sources {
		rows = append(rows, viewRow{plan: at, kind: rowDetail, text: line("source", sourceWords(s, row.plan.Local))})
	}
	if len(row.plan.Sources) == 0 && row.jql == "" {
		rows = append(rows, viewRow{plan: at, kind: rowDetail, text: line("source", "nothing")})
	}
	if row.problem != "" {
		rows = append(rows, viewRow{plan: at, kind: rowWarn, text: line("problem", row.problem)})
	}
	if row.jql != "" {
		rows = append(rows, viewRow{plan: at, kind: rowDetail, text: line("search", row.jql)})
	}
	if row.dates != "" {
		rows = append(rows, viewRow{plan: at, kind: rowDetail, text: line("dates", row.dates)})
	}
	return m.appendReleases(rows, at)
}

func (m *Model) appendReleases(rows []viewRow, at int) []viewRow {
	row := &m.plans[at]
	if len(projectKeys(row)) == 0 {
		return append(rows, viewRow{plan: at, kind: rowWarn, text: line("releases", noReleases(row))})
	}
	held := m.rel[row.plan.ID]
	switch {
	case held.loading:
		return append(rows, viewRow{plan: at, kind: rowDetail,
			text: line("releases", "asking the site"+m.deps.Theme.Glyphs.Ellipsis)})
	case held.err != nil:
		reason, _ := jira.Reason(held.err)
		return append(rows, viewRow{plan: at, kind: rowWarn, text: line("releases", reason)})
	case !held.read:
		return append(rows, viewRow{plan: at, kind: rowDetail, text: line("releases", "not read yet")})
	case len(held.versions) == 0:
		return append(rows, viewRow{plan: at, kind: rowDetail,
			text: line("releases", "none on "+strings.Join(projectKeys(row), ", "))})
	}
	for i := range held.versions {
		rows = append(rows, viewRow{plan: at, kind: rowDetail, text: line(labelOf(i), versionWords(&held.versions[i]))})
	}
	return rows
}

func labelOf(i int) string {
	if i == 0 {
		return "releases"
	}
	return ""
}

// noReleases says why a plan's releases are not on screen. A plan the site
// answered with names each project by a numeric id, and no port method turns
// one into the key a version read takes — so the honest answer is that this
// cannot be read here, rather than a number drawn as if it were a project.
func noReleases(row *planRow) string {
	if !row.plan.Local {
		return "not readable for a plan the site defines: its projects arrive as ids, " +
			"and nothing here turns a project id into a key"
	}
	return "this plan names no project, so there is none to read releases from"
}

// sourceWords is one issue source in words. A local plan names a project by its
// key, which is what a version read and a JQL clause both take; the site names
// it by an id that is neither, and the row says so instead of printing the
// number as though it were a project.
func sourceWords(s jira.PlanSource, local bool) string {
	value := strings.TrimSpace(s.Value)
	if value == "" {
		value = "unnamed"
	}
	switch {
	case s.Type == jira.PlanSourceProject && local:
		return "project " + value
	case s.Type == jira.PlanSourceProject:
		return "project id " + value + " (this port cannot resolve an id to a project key)"
	case s.Type == jira.PlanSourceFilter:
		return "filter " + value
	case s.Type == jira.PlanSourceBoard:
		return "board " + value
	default:
		return string(s.Type) + " " + value
	}
}

func versionWords(v *jira.Version) string {
	var b strings.Builder
	b.WriteString(v.Name)
	b.WriteString("  ")
	switch {
	case v.Archived:
		b.WriteString("archived")
	case v.Released:
		b.WriteString("released")
	default:
		b.WriteString("unreleased")
	}
	if d := v.ReleaseDate.String(); d != "" {
		b.WriteString("  ")
		b.WriteString(d)
	}
	if v.Unresolved != nil {
		b.WriteString("  ")
		b.WriteString(strconv.Itoa(*v.Unresolved))
		b.WriteString(" unresolved")
	}
	return b.String()
}

// line indents a detail row under the plan it belongs to and lines its label up
// with the others.
func line(word, text string) string {
	pad := max(label-len(word), 1)
	return "    " + word + strings.Repeat(" ", pad) + text
}

// zoneOf is the click target one row is marked with. Only a plan row has one:
// the lines under it are prose, and nothing happens when they are pointed at.
func (m *Model) zoneOf(at int) string {
	if at < 0 || at >= len(m.rows) || m.rows[at].kind != rowPlan {
		return ""
	}
	return "plan:" + m.plans[m.rows[at].plan].plan.ID
}

func (m *Model) row(at int) string {
	r := m.rows[at]
	sel := at == m.cursor
	if r.kind != rowPlan {
		k := rowKey{kind: r.kind, text: r.text, lay: m.lay, selected: sel,
			problem: r.kind == rowWarn, gen: m.styles.gen}
		if s, ok := m.memo.get(k); ok {
			return s
		}
		s := renderDetail(k, m.styles, m.deps.Theme)
		m.memo.put(k, s)
		return s
	}
	row := &m.plans[r.plan]
	k := rowKey{
		kind: rowPlan, id: row.plan.ID, name: row.plan.Name,
		status: statusWords(row), note: row.origin, lay: m.lay,
		selected: sel, problem: row.problem != "", gen: m.styles.gen,
	}
	if s, ok := m.memo.get(k); ok {
		return s
	}
	s := m.zones.Mark(m.zoneOf(at), renderPlan(k, m.styles, m.deps.Theme))
	m.memo.put(k, s)
	return s
}

// statusWords is the site's own word for a plan's state, and "profile" for one
// the site never answered for. The words are not matched on anywhere: they are
// carried through as they arrived.
func statusWords(row *planRow) string {
	if row.plan.Local {
		return "profile"
	}
	return row.plan.Status
}

func renderPlan(k rowKey, st *styles, t *kernel.Theme) string {
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
	if k.lay.status > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		cell := padTruncate(k.status, k.lay.status, ell)
		if k.selected {
			b.WriteString(cell)
		} else {
			b.WriteString(st.status.Render(cell))
		}
	}
	if k.lay.note > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		cell := padTruncate(k.note, k.lay.note, ell)
		switch {
		case k.selected:
			b.WriteString(cell)
		case k.problem:
			b.WriteString(st.danger.Render(cell))
		default:
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

func renderDetail(k rowKey, st *styles, t *kernel.Theme) string {
	text := padTruncate(k.text, k.lay.width, t.Glyphs.Ellipsis)
	switch {
	case k.selected:
		return st.selected.Render(text)
	case k.problem:
		return st.danger.Render(text)
	default:
		return st.muted.Render(text)
	}
}

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes: a plan name is whatever anybody typed.
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

// headKey is everything the two lines above the rows are built from, so that
// they are rebuilt when one of them moves and never once per frame.
type headKey struct {
	profile bool
	width   int
	gen     int
	plans   int
	rows    int
	loading bool
	loaded  bool
	failed  bool
}

func (m *Model) headKey() headKey {
	return headKey{
		profile: m.source == fromProfile, width: m.width, gen: m.styles.gen,
		plans: len(m.plans), rows: len(m.rows),
		loading: m.loading, loaded: m.loaded, failed: m.failure != nil,
	}
}

// chrome is the two lines above the rows: which plans these are, and the rule
// with the count at its right end. Both are memoized on everything they are
// built from, because a frame whose rows are all memo hits is otherwise these
// two strings and nothing else.
func (m *Model) chrome() (head, rule string) {
	key := m.headKey()
	if m.head != "" && key == m.headAt {
		return m.headText, m.head
	}
	ell := m.deps.Theme.Glyphs.Ellipsis
	text := "  the site's plans"
	if key.profile {
		text = "  plans defined in this profile"
	}
	m.headText = m.styles.muted.Render(ansi.Truncate(text, max(m.width, 8), ell))

	count := countLabel(key)
	dashes := max(m.width-ansi.StringWidth(count)-1, 0)
	m.head = m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) +
		" " + m.styles.muted.Render(count)
	m.headAt = key
	return m.headText, m.head
}

func countLabel(key headKey) string {
	switch {
	case key.loading && key.plans == 0:
		return "asking the site"
	case key.failed && key.plans == 0:
		return "no answer"
	case key.plans == 1:
		return "1 plan"
	default:
		return strconv.Itoa(key.plans) + " plans"
	}
}

// View draws the head, the rule, the window of rows under it, and the reason
// the plans are the profile's where they are. Only the visible rows are built,
// so a profile defining two thousand plans costs what one defining two costs.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	lines := m.lines[:0]
	head, rule := m.chrome()
	lines = append(lines, head, rule)
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
	if m.reasonShown() {
		lines = append(lines, m.reasonLine())
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

// reasonLine keeps the refusal under the rows it explains. The status line that
// said it first is gone by the next keypress, and a screen of plans the site
// never answered for has to keep saying that it is not the site's.
//
// It is memoized like the chrome: it is one of the two or three strings a frame
// whose rows are all memo hits would otherwise be.
func (m *Model) reasonLine() string {
	key := reasonKey{reason: m.reason, width: m.width, gen: m.styles.gen}
	if m.reasonAt == key {
		return m.reasonText
	}
	m.reasonText = m.styles.warn.Render(
		ansi.Truncate("  "+m.reason, max(m.width, 8), m.deps.Theme.Glyphs.Ellipsis))
	m.reasonAt = key
	return m.reasonText
}

// reasonKey is everything the reason line is built from.
type reasonKey struct {
	reason string
	width  int
	gen    int
}

// appendEmpty says which kind of empty this is. All of these drew one sentence
// once, and a token without the permission, a profile with nothing in it, a
// read in flight and a dead host are four different things to do next.
func (m *Model) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	say := func(text string) {
		lines = append(lines, m.styles.muted.Render(ansi.Truncate("  "+text, room+marker, ell)))
	}
	switch {
	case m.deps.Jira == nil:
		say("There is no Jira connection in this session, and this profile defines no plans.")
	case m.loading && !m.loaded:
		say("Asking the site for its plans" + ell)
	case m.failure != nil:
		lines = m.appendFailure(lines, room)
	case !m.loaded:
		say("Nothing has been asked of Jira yet.")
	case m.source == fromProfile:
		lines = append(lines, m.styles.danger.Render("  The site's plans cannot be read here."))
		lines = m.appendWrapped(lines, m.reason, room)
		say("This profile defines no plans of its own either.")
	default:
		say("This site has no plans.")
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
	lines = append(lines, m.styles.danger.Render("  The plans could not be read."))
	lines = m.appendWrapped(lines, reason, room)
	return append(lines, "", m.styles.muted.Render("  "+retryHint))
}

func (m *Model) appendWrapped(lines []string, text string, room int) []string {
	said := strings.Split(ansi.Wrap(text, room, ""), "\n")
	for _, said := range said[:min(len(said), reasonLines)] {
		lines = append(lines, m.styles.muted.Render("  "+said))
	}
	return lines
}
