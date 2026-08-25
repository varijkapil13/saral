package form

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

const (
	marker     = 2
	gap        = 2
	minLabel   = 10
	maxLabel   = 26
	minValue   = 12
	requiredMK = " *"
)

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout.
type layout struct {
	width   int
	label   int
	value   int
	problem int
}

// planLayout gives the labels what they need up to a ceiling, and splits what
// is left between the value and the message about it. A field whose problem has
// nowhere to go is a field whose refusal is invisible.
func planLayout(width, widestLabel int) layout {
	lay := layout{width: max(width, minLabel+marker+minValue)}
	lay.label = min(max(widestLabel, minLabel), maxLabel)
	rest := lay.width - marker - lay.label - gap
	lay.value = max(rest, minValue)
	lay.problem = 0
	if rest >= minValue*2+gap {
		lay.value = rest/2 - gap
		lay.problem = rest - lay.value - gap
	}
	return lay
}

// styles are the form's own styles, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen      int
	glyphs   kernel.Glyphs
	title    lipgloss.Style
	value    lipgloss.Style
	empty    lipgloss.Style
	problem  lipgloss.Style
	selected lipgloss.Style
	muted    lipgloss.Style
	accent   lipgloss.Style
	banner   lipgloss.Style
	action   lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	return &styles{
		gen:      t.Gen,
		glyphs:   t.Glyphs,
		title:    t.Title,
		value:    t.Base,
		empty:    t.Muted,
		problem:  t.Danger,
		selected: t.Selected,
		muted:    t.Muted,
		accent:   t.Accent,
		banner:   t.Warning,
		action:   t.Success,
	}
}

// rowKey is what makes two renderings of a row the same rendering: the field's
// own revision, the column plan, whether it is the focused row and the theme
// generation, which is the tuple docs/PERFORMANCE.md asks for.
type rowKey struct {
	kind     rowKind
	at       int
	rev      int
	lay      layout
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

func (c *rowCache) put(k rowKey, s string) {
	if len(c.rows) >= c.limit {
		clear(c.rows)
	}
	c.rows[k] = s
}

func (c *rowCache) reset() { clear(c.rows) }

func labelWidth(f *field) int {
	n := ansi.StringWidth(f.meta.Name)
	if f.meta.Required {
		n += len(requiredMK)
	}
	return n
}

// View draws the visible window and nothing else, so that a screen with six
// fields and one with two hundred cost the same per frame.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if m.stage == stageTypes {
		return m.viewTypes()
	}
	return m.viewFields()
}

func (m *Model) viewTypes() string {
	lines := m.lines[:0]
	lines = append(lines, m.styles.title.Render(m.fit("New issue in "+m.project)))
	switch {
	case m.loading:
		lines = append(lines, m.styles.muted.Render("  Looking for the issue types in use"+m.styles.glyphs.Ellipsis))
	case m.note != "":
		lines = append(lines, m.styles.muted.Render("  "+m.fit(m.note)))
	default:
		lines = append(lines, m.styles.muted.Render("  Which kind of issue?"))
	}
	h := max(m.height-2, 1)
	end := min(m.typeTop+h, len(m.types))
	for i := m.typeTop; i < end; i++ {
		lines = append(lines, m.typeRow(i))
	}
	for i := end - m.typeTop; i < h; i++ {
		lines = append(lines, "")
	}
	m.lines = lines
	return strings.Join(lines, "\n")
}

func (m *Model) typeRow(i int) string {
	typ := m.types[i]
	label := typ.Name
	if typ.Subtask {
		label += "  " + m.styles.glyphs.Bullet + " subtask"
	}
	prefix := strings.Repeat(" ", marker)
	if i == m.typeCursor {
		prefix = m.styles.glyphs.Collapsed + strings.Repeat(" ", max(marker-ansi.StringWidth(m.styles.glyphs.Collapsed), 0))
	}
	line := padTruncate(prefix+label, m.width, m.styles.glyphs.Ellipsis)
	if i == m.typeCursor {
		line = m.styles.selected.Render(line)
	}
	return m.mark(m.typeZone(i), line)
}

func (m *Model) viewFields() string {
	lines := m.lines[:0]
	lines = append(lines, m.headingLine())
	for i, message := range m.banner {
		if i == 2 {
			break
		}
		lines = append(lines, m.styles.banner.Render(m.fit("  "+message)))
	}

	h := m.rowsHeight()
	end := min(m.top+h, len(m.index))
	for i := m.top; i < end; i++ {
		lines = append(lines, m.row(i))
	}
	for i := end - m.top; i < h; i++ {
		lines = append(lines, "")
	}
	m.warm(end)
	lines = append(lines, m.editorLines()...)
	m.lines = lines
	return strings.Join(lines, "\n")
}

// headingKey is everything the caption is built from, so that it is rebuilt
// when one of them moves and never otherwise.
type headingKey struct {
	width, gen    int
	name, project string
	note          string
	busy, loading bool
}

func (m *Model) headingLine() string {
	key := headingKey{
		width: m.width, gen: m.styles.gen, name: m.chosen.Name,
		project: m.project, note: m.note, busy: m.busy, loading: m.loading,
	}
	if m.head != "" && key == m.headKey {
		return m.head
	}
	m.head, m.headKey = m.styles.title.Render(m.fit(m.heading())), key
	return m.head
}

func (m *Model) heading() string {
	head := "New " + m.chosen.Name + " in " + m.project
	switch {
	case m.busy:
		return head + "  " + m.styles.glyphs.Bullet + " creating"
	case m.loading:
		return head + "  " + m.styles.glyphs.Bullet + " reading the create screen"
	case m.note != "":
		return head + "  " + m.styles.glyphs.Bullet + " " + m.note
	default:
		return head
	}
}

// warm renders the overscan into the memo so that the next scroll step is a
// cache hit rather than a row build. It draws nothing.
func (m *Model) warm(end int) {
	const overscan = 4
	for i := max(m.top-overscan, 0); i < min(end+overscan, len(m.index)); i++ {
		if i < m.top || i >= end {
			m.row(i)
		}
	}
}

func (m *Model) row(i int) string {
	at := m.index[i]
	selected := i == m.cursor
	k := rowKey{kind: at.kind, at: at.at, lay: m.lay, selected: selected, gen: m.styles.gen}
	if at.kind == rowField {
		k.rev = m.fields[at.at].rev
	}
	if at.kind == rowNotes && m.shown {
		k.rev = 1
	}
	if s, ok := m.rows.get(k); ok {
		return s
	}
	s := m.mark(m.rowZone(i), m.buildRow(at, selected))
	m.rows.put(k, s)
	return s
}

func (m *Model) buildRow(at row, selected bool) string {
	switch at.kind {
	case rowField:
		return renderField(m.fields[at.at], m.lay, selected, m.styles)
	case rowNotes:
		glyph := m.styles.glyphs.Collapsed
		if m.shown {
			glyph = m.styles.glyphs.Expanded
		}
		return m.plain(glyph+" "+plural(len(m.hidden), "field")+" not on this form", selected, m.styles.muted)
	case rowHidden:
		h := m.hidden[at.at]
		text := "    " + h.name + " " + m.styles.glyphs.Bullet + " " + h.reason
		if h.required {
			text = "    " + h.name + " (required) " + m.styles.glyphs.Bullet + " " + h.reason
		}
		return m.plain(text, selected, m.styles.muted)
	default:
		return m.plain(m.styles.glyphs.Arrow+" Create the issue", selected, m.styles.action)
	}
}

func (m *Model) plain(text string, selected bool, style lipgloss.Style) string {
	line := padTruncate(strings.Repeat(" ", marker)+text, m.lay.width, m.styles.glyphs.Ellipsis)
	if selected {
		return m.styles.selected.Render(line)
	}
	return style.Render(line)
}

// renderField draws one field to exactly lay.width columns: what it is called,
// what is in it, and what is wrong with it.
func renderField(f *field, lay layout, selected bool, st *styles) string {
	var b strings.Builder
	b.Grow(lay.width + 48)

	if selected {
		b.WriteString(st.glyphs.Collapsed)
		b.WriteString(strings.Repeat(" ", max(marker-ansi.StringWidth(st.glyphs.Collapsed), 0)))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}

	label := f.meta.Name
	if f.meta.Required {
		label += requiredMK
	}
	b.WriteString(padTruncate(label, lay.label, st.glyphs.Ellipsis))
	b.WriteString(strings.Repeat(" ", gap))

	value, style := f.display(), st.value
	if value == "" {
		value, style = placeholder(f, st.glyphs.Bullet), st.empty
	}
	cell := padTruncate(value, lay.value, st.glyphs.Ellipsis)
	if selected {
		b.WriteString(cell)
	} else {
		b.WriteString(style.Render(cell))
	}

	if lay.problem > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		problem := ""
		if f.problem != "" {
			problem = st.glyphs.Cross + " " + f.problem
		}
		cell := padTruncate(problem, lay.problem, st.glyphs.Ellipsis)
		if selected {
			b.WriteString(cell)
		} else {
			b.WriteString(st.problem.Render(cell))
		}
	}

	line := padTruncate(b.String(), lay.width, st.glyphs.Ellipsis)
	if selected {
		return st.selected.Render(line)
	}
	return line
}

// placeholder says what an empty field would take, which is the only hint a
// user gets about a widget they have not opened yet.
func placeholder(f *field, bullet string) string {
	head := "empty " + bullet + " "
	switch f.kind {
	case kindDate:
		return head + "2026-03-27"
	case kindDateTime:
		return head + "2026-03-27 09:30"
	case kindIssueKey:
		return head + "PROJ-142"
	case kindLabels:
		return head + "labels, separated by spaces"
	case kindOther:
		return head + f.meta.Field.Schema.Type + ", kept as typed"
	default:
		return head + f.kind.String()
	}
}

// --- the editor pane --------------------------------------------------------

func (m *Model) editorLines() []string {
	switch m.edit {
	case editText:
		return m.exactly([]string{
			m.styles.accent.Render(m.fit(m.editing2Label() + m.hint())),
			m.input.View(),
		}, m.editorHeight())
	case editDoc:
		out := []string{m.styles.accent.Render(m.fit(m.editing2Label() + "  esc or ctrl+d when done"))}
		if warning := m.oneWayLine(); warning != "" {
			out = append(out, m.styles.muted.Render(m.fit("  "+warning)))
		}
		out = append(out, strings.Split(m.area.View(), "\n")...)
		return m.exactly(out, m.editorHeight())
	case editChoose:
		return m.exactly(m.chooserLines(), m.editorHeight())
	default:
		return nil
	}
}

func (m *Model) editing2Label() string {
	f := m.fields[m.editing]
	label := f.meta.Name
	if f.meta.Required {
		label += requiredMK
	}
	return label
}

func (m *Model) hint() string {
	switch m.fields[m.editing].kind {
	case kindNumber:
		return "  a number"
	case kindDate:
		return "  2026-03-27"
	case kindDateTime:
		return "  2026-03-27 09:30"
	case kindIssueKey:
		return "  an issue key, PROJ-142"
	case kindLabels:
		return "  labels, separated by spaces"
	case kindOther:
		return "  kept as typed: " + m.fields[m.editing].meta.Field.Schema.Type
	default:
		return ""
	}
}

// oneWayLine warns about what an edit costs before it is made rather than
// after. It names only the constructs the document in hand actually has.
func (m *Model) oneWayLine() string {
	lost := m.fields[m.editing].oneWay()
	if len(lost) == 0 {
		return ""
	}
	names := make([]string, 0, len(lost))
	for _, entry := range lost {
		node, _, _ := strings.Cut(entry, ":")
		names = append(names, node)
	}
	return "editing a block that holds " + strings.Join(names, ", ") +
		" rewrites what markdown cannot carry; blocks you leave alone are kept as they arrived"
}

func (m *Model) chooserLines() []string {
	f := m.fields[m.editing]
	head := m.editing2Label()
	if f.kind.multiple() {
		head += "  tab picks, enter is done"
	} else {
		head += "  enter takes one, esc is done"
	}
	out := []string{m.styles.accent.Render(m.fit(head)), m.filter.View()}

	visible := m.visibleChoices()
	h := m.chooserHeight()
	if len(visible) == 0 {
		return append(out, m.styles.muted.Render("  nothing here matches"))
	}
	end := min(m.pickTop+h, len(visible))
	for i := m.pickTop; i < end; i++ {
		out = append(out, m.choiceRow(i, visible[i], f.kind.multiple()))
	}
	return out
}

func (m *Model) choiceRow(i, at int, multiple bool) string {
	c := m.choices[at]
	box := "  "
	switch {
	case c.on && multiple:
		box = m.styles.glyphs.Check + " "
	case c.on:
		box = m.styles.glyphs.Diamond + " "
	}
	line := padTruncate("  "+box+c.label, m.width, m.styles.glyphs.Ellipsis)
	if i == m.pick {
		line = m.styles.selected.Render(line)
	}
	return m.mark(m.choiceZone(i), line)
}

// exactly pads or clips a pane to the height it was given, so that the frame is
// the size the kernel handed the view whatever the editor put in it.
func (m *Model) exactly(lines []string, h int) []string {
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lines[:h]
}

func (m *Model) fit(s string) string {
	return padTruncate(s, m.width, m.styles.glyphs.Ellipsis)
}

// --- zones ------------------------------------------------------------------

func (m *Model) mark(id, line string) string { return m.zones.Mark(id, line) }

// The zones are named after the row on screen rather than the field in it, so
// the ids a session mints are bounded by the height of the terminal.
func (m *Model) rowZone(i int) string    { return "row:" + strconv.Itoa(i) }
func (m *Model) typeZone(i int) string   { return "type:" + strconv.Itoa(i) }
func (m *Model) choiceZone(i int) string { return "choice:" + strconv.Itoa(i) }

// padTruncate makes a string exactly width columns wide, counting grapheme
// clusters rather than bytes so that an emoji or a CJK field name does not
// shift every column to its right.
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
