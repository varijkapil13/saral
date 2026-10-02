package search

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	marker      = 2
	typeCol     = 3
	gap         = 2
	minKeyWidth = 6
	maxKeyWidth = 14
	statusWidth = 14
	userWidth   = 14
	ageWidth    = 4
	showStatus  = 84
	showUser    = 100
	unassigned  = "unassigned"
)

type layout struct {
	width    int
	key      int
	summary  int
	status   int
	assignee int
}

func planLayout(width, keyWidth int) layout {
	lay := layout{width: width, key: min(max(keyWidth, minKeyWidth), maxKeyWidth)}
	if width >= showStatus {
		lay.status = statusWidth
	}
	if width >= showUser {
		lay.assignee = userWidth
	}
	used := marker + typeCol + lay.key + gap + gap + ageWidth
	for _, w := range [...]int{lay.status, lay.assignee} {
		if w > 0 {
			used += gap + w
		}
	}
	lay.summary = max(width-used, 1)
	return lay
}

type styles struct {
	gen        int
	muted      lipgloss.Style
	key        lipgloss.Style
	warn       lipgloss.Style
	accent     lipgloss.Style
	hlOpen     string
	hlClose    string
	selOpen    string
	selClose   string
	hlSelOpen  string
	hlSelClose string
	categories [4]lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	s := &styles{
		gen:    t.Gen,
		muted:  t.Muted,
		key:    t.Accent,
		warn:   t.Warning,
		accent: t.Accent,
	}
	s.categories = [4]lipgloss.Style{
		jira.CategoryUnknown:    t.Muted,
		jira.CategoryToDo:       t.Base,
		jira.CategoryInProgress: t.Accent,
		jira.CategoryDone:       t.Success,
	}
	s.hlOpen, s.hlClose = sequences(t.Accent.Bold(true))
	s.selOpen, s.selClose = sequences(t.Selected)
	s.hlSelOpen, s.hlSelClose = sequences(t.Selected.Bold(true).Underline(true))
	return s
}

// sequences splits a style once per theme: rendering each piece of a row through an
// underline style writes one sequence per character.
func sequences(st lipgloss.Style) (open, closing string) {
	const probe = "x"
	painted := st.Render(probe)
	at := strings.Index(painted, probe)
	if at < 0 {
		return "", ""
	}
	return painted[:at], painted[at+len(probe):]
}

func categoryIndex(c jira.StatusCategory) int {
	if c < jira.CategoryUnknown || c > jira.CategoryDone {
		return int(jira.CategoryUnknown)
	}
	return int(c)
}

type row struct {
	iss     jira.Issue
	summary string
	spans   []span
	pinned  bool
}

type rowKey struct {
	key      string
	updated  int64
	lay      layout
	gen      int32
	terms    int32
	selected bool
	pinned   bool
	mouse    bool
}

type piece struct {
	text string
	hl   bool
}

func rowZone(key string) string { return "row:" + key }

const (
	scopeZone = "scope"
	queryZone = "query"
	moreZone  = "more"
)

func (m *Model) row(i int, selected bool) string {
	if i >= len(m.rows) {
		return m.moreRow(selected)
	}
	r := m.rows[i]
	k := rowKey{
		key: r.iss.Key, updated: r.iss.Updated.UnixNano(), lay: m.lay, gen: int32(m.styles.gen),
		terms: int32(m.termsGen), selected: selected, pinned: r.pinned, mouse: m.zones.Enabled(),
	}
	if s, ok := m.memo.Get(k); ok {
		return s
	}
	s := m.zones.Mark(rowZone(r.iss.Key), m.renderRow(r, selected))
	m.memo.Put(k, s)
	return s
}

func (m *Model) moreRow(selected bool) string {
	k := rowKey{key: moreZone, lay: m.lay, gen: int32(m.styles.gen), selected: selected, mouse: m.zones.Enabled()}
	if s, ok := m.memo.Get(k); ok {
		return s
	}
	t := m.deps.Theme
	text := "more match " + dash(t.Glyphs) + " L shows every one in the issue list"
	line := widget.PadTruncate(strings.Repeat(" ", marker)+text, m.lay.width, t.Glyphs.Ellipsis)
	if selected {
		line = m.styles.selOpen + line + m.styles.selClose
	} else {
		line = m.styles.muted.Render(line)
	}
	s := m.zones.Mark(moreZone, line)
	m.memo.Put(k, s)
	return s
}

func dash(g kernel.Glyphs) string {
	if g.IsASCII() {
		return "-"
	}
	return "—"
}

func (m *Model) renderRow(r *row, sel bool) string {
	t, st, lay := m.deps.Theme, m.styles, m.lay
	ell := t.Glyphs.Ellipsis
	iss := &r.iss
	pieces := make([]piece, 0, 12)
	plain := func(s string) { pieces = append(pieces, piece{text: s}) }

	if sel {
		plain(t.Glyphs.Collapsed + strings.Repeat(" ", max(marker-ansi.StringWidth(t.Glyphs.Collapsed), 0)))
	} else {
		plain(strings.Repeat(" ", marker))
	}
	plain(widget.PadTruncate(t.Glyphs.TypeGlyph(iss.Type), typeCol, ""))
	keyCell := widget.PadTruncate(widget.Sanitize(iss.Key), lay.key, ell)
	if !sel {
		keyCell = st.key.Render(keyCell)
	}
	plain(keyCell)
	plain(strings.Repeat(" ", gap))
	pieces = m.summaryPieces(pieces, r, lay.summary)
	if lay.status > 0 {
		plain(strings.Repeat(" ", gap))
		cell := widget.PadTruncate(widget.Sanitize(iss.Status.Name), lay.status-2, ell)
		cell = widget.PadTruncate(t.Glyphs.CategoryGlyph(iss.Status.Category), 2, "") + cell
		if !sel {
			cell = st.categories[categoryIndex(iss.Status.Category)].Render(cell)
		}
		plain(cell)
	}
	if lay.assignee > 0 {
		plain(strings.Repeat(" ", gap))
		who := unassigned
		if iss.Assignee != nil && strings.TrimSpace(iss.Assignee.DisplayName) != "" {
			who = iss.Assignee.DisplayName
		}
		plain(widget.PadTruncate(widget.Sanitize(who), lay.assignee, ell))
	}
	plain(strings.Repeat(" ", gap))
	when := age(m.now(), iss.Updated)
	if r.pinned {
		when = "key"
	}
	cell := widget.PadLeft(when, ageWidth, ell)
	if !sel {
		cell = st.muted.Render(cell)
	}
	plain(cell)
	return m.paint(pieces, sel)
}

func (m *Model) summaryPieces(dst []piece, r *row, width int) []piece {
	ell := m.deps.Theme.Glyphs.Ellipsis
	text := ansi.Truncate(r.summary, width, ell)
	cut := len(text)
	if text != r.summary {
		cut = len(text) - len(ell)
	}
	at := 0
	for _, sp := range r.spans {
		if sp.from >= cut {
			break
		}
		to := min(sp.to, cut)
		if sp.from > at {
			dst = append(dst, piece{text: text[at:sp.from]})
		}
		dst = append(dst, piece{text: text[sp.from:to], hl: true})
		at = to
	}
	if at < len(text) {
		dst = append(dst, piece{text: text[at:]})
	}
	if pad := width - ansi.StringWidth(text); pad > 0 {
		dst = append(dst, piece{text: strings.Repeat(" ", pad)})
	}
	return dst
}

func (m *Model) paint(pieces []piece, sel bool) string {
	st := m.styles
	var b strings.Builder
	b.Grow(m.lay.width + 48)
	var run strings.Builder
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if sel {
			b.WriteString(st.selOpen)
			b.WriteString(run.String())
			b.WriteString(st.selClose)
		} else {
			b.WriteString(run.String())
		}
		run.Reset()
	}
	for _, p := range pieces {
		if !p.hl {
			run.WriteString(p.text)
			continue
		}
		flush()
		switch {
		case sel:
			b.WriteString(st.hlSelOpen)
			b.WriteString(p.text)
			b.WriteString(st.hlSelClose)
		default:
			b.WriteString(st.hlOpen)
			b.WriteString(p.text)
			b.WriteString(st.hlClose)
		}
	}
	flush()
	return b.String()
}

func age(now, t time.Time) string {
	if now.IsZero() || t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d < 14*24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d < 60*24*time.Hour:
		return strconv.Itoa(int(d/(7*24*time.Hour))) + "w"
	case d < 365*24*time.Hour:
		return strconv.Itoa(int(d/(30*24*time.Hour))) + "mo"
	default:
		return strconv.Itoa(int(d/(365*24*time.Hour))) + "y"
	}
}

type headKey struct {
	width, gen int
	typing     bool
	focused    bool
	scope      Scope
	scopeable  bool
	project    string
	valueGen   int
	pos        int
	mode       mode
	count      int
	more       bool
	run        int
}

type mode uint8

const (
	modeIdle mode = iota
	modeShort
	modeLoading
	modeResults
	modeEmpty
	modeFailed
)

func (m *Model) mode() mode {
	switch {
	case m.failure != nil:
		return modeFailed
	case m.loading:
		return modeLoading
	case len(m.rows) > 0:
		return modeResults
	case m.jql != "":
		return modeEmpty
	case m.short:
		return modeShort
	default:
		return modeIdle
	}
}

func (m *Model) headKey() headKey {
	return headKey{
		width: m.width, gen: m.styles.gen, typing: !m.browsing, focused: m.focused,
		scope: m.scope, scopeable: m.canScope(), project: m.deps.Project,
		valueGen: m.valueGen, pos: m.input.Position(), mode: m.mode(),
		count: len(m.rows), more: m.hasMore(), run: m.gen,
	}
}

func (m *Model) chip() string {
	if m.scope == ScopeProject && m.deps.Project != "" {
		return "[" + widget.Sanitize(m.deps.Project) + "]"
	}
	return "[all projects]"
}

func (m *Model) buildHead(key headKey) {
	t := m.deps.Theme
	chip := m.chip()
	chipW := ansi.StringWidth(chip)
	m.input.SetWidth(max(m.width-ansi.StringWidth(m.input.Prompt)-chipW-3, 4))
	inputLine := widget.PadTruncate(m.input.View(), max(m.width-chipW-1, 1), t.Glyphs.Ellipsis)
	chipStyled := m.styles.muted.Render(chip)
	if m.scope == ScopeProject {
		chipStyled = m.styles.accent.Render(chip)
	}
	if key.scopeable {
		chipStyled = m.zones.Mark(scopeZone, chipStyled)
	}
	line1 := m.zones.Mark(queryZone, inputLine) + " " + chipStyled

	state, isWarn := m.stateLine()
	style := m.styles.muted
	if isWarn {
		style = m.styles.warn
	}
	line2 := style.Render(widget.PadTruncate(widget.Sanitize(state), m.width, t.Glyphs.Ellipsis))
	rule := m.styles.muted.Render(strings.Repeat(t.Glyphs.HLine, m.width))

	m.head = [3]string{line1, line2, rule}
	m.headAt = key
	m.body = m.body[:0]
	if len(m.rows) == 0 {
		for _, line := range m.bodyLines() {
			m.body = append(m.body, widget.PadTruncate(widget.Sanitize(line), m.width, t.Glyphs.Ellipsis))
		}
	}
}

func (m *Model) rowsHeight() int { return max(m.height-3, 1) }

// View draws the visible window and nothing else.
func (m *Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	if key := m.headKey(); m.headAt != key || m.head[0] == "" {
		m.buildHead(key)
	}
	h := m.rowsHeight()
	lines := m.lines[:0]
	lines = append(lines, m.head[0], m.head[1], m.head[2])
	at := len(lines)
	if len(m.rows) == 0 {
		lines = append(lines, m.body...)
	} else {
		end := min(m.top+h, m.count())
		for i := m.top; i < end; i++ {
			lines = append(lines, m.row(i, m.browsing && i == m.cursor))
		}
		m.warm(end)
	}
	for len(lines)-at < h {
		lines = append(lines, "")
	}
	lines = lines[:at+h]
	m.lines = lines
	return strings.Join(lines, "\n")
}

func (m *Model) warm(end int) {
	const overscan = 4
	for i := max(m.top-overscan, 0); i < min(end+overscan, m.count()); i++ {
		if i < m.top || i >= end {
			m.row(i, false)
		}
	}
}
