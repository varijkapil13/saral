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
	pickHint     = defaultSetKeys().Pick.Help().Key
	findHint     = defaultSetKeys().Find.Help().Key
)

type setSummary struct {
	arrange      arrangement
	pick         string
	needle       string
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
		arrange: s.arrange, pick: s.pick, needle: s.rawNeedle,
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
	if key.set.needle != "" {
		b.WriteString(" · matching ")
		b.WriteString(strconv.Quote(key.set.needle))
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

func renderHeader(k rowKey, st *styles, t *kernel.Theme) string {
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

func (m *Model) appendNotes(lines []string) []string {
	n := m.notesHeight()
	ell := m.deps.Theme.Glyphs.Ellipsis
	for _, note := range m.set.src.Notes[:n] {
		lines = append(lines, m.styles.muted.Render(ansi.Truncate("  "+widget.Sanitize(note), max(m.width, 1), ell)))
	}
	return lines
}

func (m *Model) findPrompt() string {
	return ansi.Truncate(m.set.find.View(), max(m.width, 1), m.deps.Theme.Glyphs.Ellipsis)
}

func (m *Model) appendSetEmpty(lines []string, h int) []string {
	at := len(lines)
	room := max(m.width-marker, 8)
	ell := m.deps.Theme.Glyphs.Ellipsis
	s := m.set
	switch {
	case m.failure != nil:
		lines = m.appendFailure(lines, room, h)
	case len(m.versions) == 0:
		lines = append(lines, m.styles.muted.Render("  This set holds no versions."))
	default:
		lines = append(lines, m.styles.muted.Render("  No version matches what is narrowing the list."))
		var parts []string
		if m.filter != filterAll {
			parts = append(parts, "state "+m.filter.name()+" ("+filterHint+")")
		}
		if s.pick != "" {
			parts = append(parts, "project "+s.pickLabel()+" ("+pickHint+")")
		}
		if s.rawNeedle != "" {
			parts = append(parts, "text "+strconv.Quote(s.rawNeedle)+" ("+findHint+")")
		}
		if n := s.excludedN; n > 0 && !s.showExcluded {
			parts = append(parts, strconv.Itoa(n)+" excluded by the plan ("+excludedHint+")")
		}
		lines = append(lines, m.styles.muted.Render(ansi.Truncate("  Narrowed by "+strings.Join(parts, ", ")+".", room, ell)))
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
