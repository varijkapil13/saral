package release

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
)

const (
	setHeadHeight = 3
	maxNotes      = 3
	minSetRows    = 3
	headGap       = 3
)

var (
	excludedHint = defaultSetKeys().Excluded.Help().Key
	pickHint     = defaultSetKeys().Filter.Help().Key
	findHint     = defaultSetKeys().Find.Help().Key
)

type setSummary struct {
	arrange      arrangement
	pick         string
	showExcluded bool
	excludedN    int
}

func (m *Model) headHeight() int {
	if m.set != nil {
		return setHeadHeight
	}
	return headHeight
}

func (m *Model) notesHeight() int {
	if m.set == nil {
		return 0
	}
	n := min(len(m.set.src.Notes), maxNotes)
	if m.height < setHeadHeight+n+minSetRows {
		return 0
	}
	return n
}

func (m *Model) setSummaryKey() setSummary {
	s := m.set
	return setSummary{
		arrange: s.arrange, pick: s.pick,
		showExcluded: s.showExcluded, excludedN: s.excludedN,
	}
}

func (m *Model) titleLine() string {
	src := m.set.src
	ell := m.deps.Theme.Glyphs.Ellipsis
	line := m.styles.accent.Render("  " + widget.Sanitize(src.Title))
	if src.Explain != "" {
		line += m.styles.muted.Render("   " + widget.Sanitize(src.Explain))
	}
	return ansi.Truncate(line, max(m.width, 8), ell)
}

func (m *Model) setSummaryLine(key summaryKey) string {
	s := m.set
	glyphs := m.deps.Theme.Glyphs
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(arrangementLabels[key.set.arrange])
	sum := m.styles.muted.Render(b.String())
	if m.sort.chosen() {
		sum += m.styles.muted.Render(" · ") +
			m.zones.Mark(sortZone, m.styles.accent.Render("sort: "+m.sort.plainIn(setSortFields, glyphs)))
	}
	b.Reset()
	if key.filter != filterAll {
		b.WriteString(" · ")
		b.WriteString(key.filter.name())
	}
	if key.set.pick != "" {
		b.WriteString(" · project ")
		b.WriteString(s.pickLabel())
	}
	if key.needle != "" {
		b.WriteString(" · matching ")
		b.WriteString(strconv.Quote(key.needle))
	}
	b.WriteString(" · ")
	if key.shown != key.versions {
		b.WriteString(strconv.Itoa(key.shown))
		b.WriteString(" of ")
	}
	b.WriteString(plural(key.versions, "version", "versions"))
	if key.shown == key.versions && key.filter == filterAll && key.released > 0 {
		b.WriteString(" · ")
		b.WriteString(strconv.Itoa(key.released))
		b.WriteString(" released")
	}
	if n := key.set.excludedN; n > 0 {
		b.WriteString(" · ")
		b.WriteString(strconv.Itoa(n))
		if key.set.showExcluded {
			b.WriteString(" excluded by the plan, shown, ")
			b.WriteString(excludedHint)
			b.WriteString(" hides")
		} else {
			b.WriteString(" excluded by the plan, ")
			b.WriteString(excludedHint)
			b.WriteString(" shows")
		}
	}
	sum += m.styles.muted.Render(b.String())
	if key.stale {
		sum += " " + m.deps.Theme.StaleBadge.Render(staleLabel)
	}
	sum += m.styles.muted.Render(m.progress(key))
	m.sum = ansi.Truncate(sum, max(m.width, 8), glyphs.Ellipsis)
	m.sumAt = key
	return m.sum
}

func renderHeader(k setKey, st *styles, t *kernel.Theme) string {
	ell := t.Glyphs.Ellipsis
	glyph := t.Glyphs.Expanded
	if k.folded {
		glyph = t.Glyphs.Collapsed
	}
	name := widget.PadTruncate(k.cells.name, max(k.lay.name, ansi.StringWidth(k.cells.name)), ell)
	gap := strings.Repeat(" ", headGap)
	if k.selected {
		line := widget.PadTruncate(glyph, marker, ell) + name + gap + k.cells.description
		return st.selected.Render(widget.PadTruncate(line, k.lay.width, ell))
	}
	line := widget.PadTruncate(glyph, marker, ell) + st.accent.Render(name) + gap + st.muted.Render(k.cells.description)
	return widget.PadTruncate(line, k.lay.width, ell)
}

type setKey struct {
	rowKey
	project  string
	excluded bool
	header   bool
	folded   bool
}

func (m *Model) setRowKey(at int, selected bool) setKey {
	s := m.set
	sl := m.order[at]
	if sl.v < 0 {
		h := &s.heads[sl.g]
		return setKey{
			rowKey: rowKey{
				cells: rowCells{id: h.zone, name: h.name, description: h.text},
				lay:   m.lay, selected: selected, gen: m.styles.gen,
			},
			header: true, folded: s.folded[h.key],
		}
	}
	k := setKey{
		rowKey:   rowKey{cells: m.cells[sl.v], lay: m.lay, selected: selected, gen: m.styles.gen},
		project:  s.projects[sl.v],
		excluded: s.excluded[sl.v],
	}
	if k.excluded {
		k.cells.description = excludedCell
	}
	return k
}

func (m *Model) setRow(at int, selected bool) string {
	k := m.setRowKey(at, selected)
	if s, ok := m.set.rows.Get(k); ok {
		return s
	}
	var s string
	if k.header {
		s = m.zones.Mark(k.cells.id, renderHeader(k, m.styles, m.deps.Theme))
	} else {
		s = m.zones.Mark(rowZone(k.cells.id), drawRow(k.rowKey, k.project, k.excluded, m.styles, m.deps.Theme))
	}
	m.set.rows.Put(k, s)
	return s
}

func (m *Model) appendNotes(lines []string) []string {
	n := m.notesHeight()
	ell := m.deps.Theme.Glyphs.Ellipsis
	for _, note := range m.set.src.Notes[:n] {
		lines = append(lines, m.styles.muted.Render(ansi.Truncate("  "+widget.Sanitize(note), max(m.width, 1), ell)))
	}
	return lines
}

func (m *Model) findPrompt() string {
	return ansi.Truncate(m.find.input.View(), max(m.width, 1), m.deps.Theme.Glyphs.Ellipsis)
}

// appendNarrowed says that the filters in force leave nothing, and names each one with the key that undoes it.
func (m *Model) appendNarrowed(lines []string, room int, more []string) []string {
	var parts []string
	if m.filter != filterAll {
		parts = append(parts, "state "+m.filter.name()+" ("+filterHint+")")
	}
	if m.find.rawNeedle != "" {
		parts = append(parts, "text "+strconv.Quote(m.find.rawNeedle)+" ("+findHint+")")
	}
	parts = append(parts, more...)
	ell := m.deps.Theme.Glyphs.Ellipsis
	return append(lines,
		m.styles.muted.Render("  No version matches what is narrowing the list."),
		m.styles.muted.Render(ansi.Truncate("  Narrowed by "+strings.Join(parts, ", ")+".", room, ell)))
}

func (m *Model) appendSetEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	s := m.set
	switch {
	case m.failure != nil:
		lines = m.appendFailure(lines, room, h)
	case len(m.versions) == 0:
		lines = append(lines, m.styles.muted.Render("  This set holds no versions."))
	default:
		var parts []string
		if s.pick != "" {
			parts = append(parts, "project "+s.pickLabel()+" ("+pickHint+")")
		}
		if n := s.excludedN; n > 0 && !s.showExcluded {
			parts = append(parts, strconv.Itoa(n)+" excluded by the plan ("+excludedHint+")")
		}
		lines = m.appendNarrowed(lines, room, parts)
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	return lines[:at+h]
}

var setSortFields = func() []sortField {
	out := make([]sortField, 0, len(sortFields)+1)
	for i, f := range sortFields {
		if i == 0 {
			f.label = "plan order"
		}
		out = append(out, f)
		if i == 0 {
			out = append(out, sortField{id: "owner", label: "project", compare: func(a, b sortable) int {
				return strings.Compare(strings.ToLower(a.owner), strings.ToLower(b.owner))
			}})
		}
	}
	return out
}()
