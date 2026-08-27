package palette

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// currentBadge marks the scope the session is already on.
const currentBadge = "  (current)"

// projectKeys is what the picker answers to. Every letter goes into the filter,
// so moving the selection is arrows and their control-key twins and nothing else.
type projectKeys struct {
	Up       kernel.Binding
	Down     kernel.Binding
	PageUp   kernel.Binding
	PageDown kernel.Binding
	Choose   kernel.Binding
	Close    kernel.Binding
}

func defaultProjectKeys() projectKeys {
	return projectKeys{
		Up:       kernel.Bind([]string{"up", "ctrl+p"}, "↑", "up"),
		Down:     kernel.Bind([]string{"down", "ctrl+n"}, "↓", "down"),
		PageUp:   kernel.Bind([]string{"pgup"}, "pgup", "page up"),
		PageDown: kernel.Bind([]string{"pgdown"}, "pgdn", "page down"),
		Choose:   kernel.Bind([]string{"enter"}, "enter", "switch to it"),
		Close:    kernel.Bind([]string{"esc"}, "esc", "cancel"),
	}
}

func (k projectKeys) table() map[string]action {
	entries := []struct {
		b kernel.Binding
		a action
	}{
		{k.Up, actUp}, {k.Down, actDown},
		{k.PageUp, actPageUp}, {k.PageDown, actPageDown},
		{k.Choose, actRun}, {k.Close, actClose},
	}
	out := make(map[string]action, len(entries)*2)
	for _, e := range entries {
		if !e.b.Enabled() {
			continue
		}
		for _, stroke := range e.b.Keys() {
			out[stroke] = e.a
		}
	}
	return out
}

// projectState is which of the picker's states the keys belong to, and doubles
// as the generation the footer repaints on.
type projectState int

const (
	projectPicking projectState = iota
	projectNothing
	projectStates
)

var projectSets = func() [projectStates]kernel.KeySet {
	k := defaultProjectKeys()
	var sets [projectStates]kernel.KeySet
	sets[projectPicking] = kernel.KeySet{
		Acts: []kernel.Binding{k.Choose, k.Close},
		Full: [][]kernel.Binding{
			{k.Down, k.Up, k.PageDown, k.PageUp},
			{k.Choose, k.Close},
		},
	}
	sets[projectNothing] = kernel.KeySet{
		Acts: []kernel.Binding{k.Close},
		Full: [][]kernel.Binding{{k.Close}},
	}
	return sets
}()

// LiveKeys reports the keys that work right now. A filter matching nothing has
// nothing to switch to, and advertising enter there names a key that is refused.
func (m *projectModel) LiveKeys() (set kernel.KeySet, gen int) {
	state := projectPicking
	if len(m.shown) == 0 {
		state = projectNothing
	}
	return projectSets[state], int(state)
}

// projectHeadKey is everything the rule line is built from, so that it is
// rebuilt when one of them moves and never once per frame.
type projectHeadKey struct {
	width    int
	gen      int
	shown    int
	total    int
	filtered bool
	looking  bool
}

func (m *projectModel) rule() string {
	key := projectHeadKey{
		width: m.width, gen: m.styles.gen,
		shown: len(m.shown), total: len(m.rows),
		filtered: m.query != "", looking: m.looking,
	}
	if m.head != "" && key == m.headAt {
		return m.head
	}
	count := projectCount(key)
	dashes := max(m.width-ansi.StringWidth(count)-1, 0)
	m.head = m.styles.rule.Render(strings.Repeat(m.deps.Theme.Glyphs.HLine, dashes)) +
		" " + m.styles.muted.Render(count)
	m.headAt = key
	return m.head
}

// projectCount says how many scopes are on offer, and that the site is still
// being asked while it is.
func projectCount(key projectHeadKey) string {
	count := strconv.Itoa(key.total) + " scopes"
	switch {
	case key.filtered:
		count = strconv.Itoa(key.shown) + " of " + strconv.Itoa(key.total)
	case key.total == 1:
		count = "1 scope"
	}
	if key.looking {
		return count + ", still looking"
	}
	return count
}

// renderProject draws one scope in the palette's own columns: what to call it,
// then what tells it apart where a command shows its group.
func renderProject(r *projectRow, lay layout, sel bool, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(lay.width + 32)

	writeMarker(&b, sel, t)
	label := r.label
	if r.current {
		label += currentBadge
	}
	text := padTruncate(label, lay.title, ell)
	if sel {
		b.WriteString(text)
	} else {
		b.WriteString(st.title.Render(text))
	}
	if lay.group > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		cell := padTruncate(r.note, lay.group, ell)
		if sel {
			b.WriteString(cell)
		} else {
			b.WriteString(st.group.Render(cell))
		}
	}
	if lay.slack > 0 {
		b.WriteString(strings.Repeat(" ", lay.slack))
	}
	// No key reaches one project, so the column stays blank rather than moving.
	if lay.keys > 0 {
		b.WriteString(strings.Repeat(" ", gap+lay.keys))
	}
	if sel {
		return st.selected.Render(b.String())
	}
	return b.String()
}

func (m *projectModel) row(at int) string {
	sel := at == m.cursor
	r := &m.rows[m.shown[at]]
	k := rowKey{id: r.key, text: r.label, age: r.note, lay: m.lay, selected: sel, gen: m.styles.gen}
	if s, ok := m.memo.get(k); ok {
		return s
	}
	s := renderProject(r, m.lay, sel, m.styles, m.deps.Theme)
	if m.deps.Zones != nil {
		s = m.deps.Zones.Mark(m.zone(m.shown[at]), s)
	}
	m.memo.put(k, s)
	return s
}

// View draws the filter, the rule and the window of scopes under it.
func (m *projectModel) View() string {
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

// appendEmpty says why there is nothing to switch to. The whole site is always
// on the list, so this is only ever a filter that matched none of it — and the
// reason the site named none is worth saying here too.
func (m *projectModel) appendEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	lines = append(lines, m.styles.muted.Render(
		ansi.Truncate("  Nothing matches "+strconv.Quote(m.query)+".", m.width, ell)))
	if m.problem != "" {
		lines = append(lines, "  "+m.styles.muted.Render(ansi.Truncate(m.problem, room, ell)))
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}
