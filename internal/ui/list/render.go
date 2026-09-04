package list

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	gap         = 2
	marker      = 2
	minSummary  = 28
	minKeyWidth = 6
	maxKeyWidth = 14
	typeWidth   = 9
	statusWidth = 12
	userWidth   = 16
	whenWidth   = 12
)

// layout is the column plan for one width. It is comparable so that a row
// memoized under it is invalidated by any relayout, not only by a resize.
type layout struct {
	width    int
	key      int
	summary  int
	typ      int
	status   int
	assignee int
	updated  int
}

// planLayout drops columns from the right until the summary has room. A summary
// squeezed to nothing is worse than no assignee column, because the summary is
// the only part of a row that says what the issue is.
func planLayout(width, keyWidth int) layout {
	keyWidth = min(max(keyWidth, minKeyWidth), maxKeyWidth)
	lay := layout{
		width: max(width, minKeyWidth+marker+minSummary),
		key:   keyWidth, typ: typeWidth, status: statusWidth,
		assignee: userWidth, updated: whenWidth,
	}
	drop := []*int{&lay.updated, &lay.assignee, &lay.typ, &lay.status}
	for {
		lay.summary = lay.width - marker - lay.key - gap - optionalWidth(lay)
		if lay.summary >= minSummary || len(drop) == 0 {
			break
		}
		*drop[0] = 0
		drop = drop[1:]
	}
	lay.summary = max(lay.summary, 1)
	return lay
}

func optionalWidth(lay layout) int {
	total := 0
	for _, w := range [...]int{lay.typ, lay.status, lay.assignee, lay.updated} {
		if w > 0 {
			total += gap + w
		}
	}
	return total
}

// header is the column caption row. It is rebuilt only when the layout is.
func (lay layout) header(t *kernel.Theme) string {
	var b strings.Builder
	b.Grow(lay.width)
	b.WriteString(strings.Repeat(" ", marker))
	writeCell(&b, "KEY", lay.key, t.Glyphs.Ellipsis)
	writeGap(&b)
	writeCell(&b, "SUMMARY", lay.summary, t.Glyphs.Ellipsis)
	for _, col := range [...]struct {
		label string
		width int
	}{{"TYPE", lay.typ}, {"STATUS", lay.status}, {"ASSIGNEE", lay.assignee}, {"UPDATED", lay.updated}} {
		if col.width == 0 {
			continue
		}
		writeGap(&b)
		writeCell(&b, col.label, col.width, t.Glyphs.Ellipsis)
	}
	return t.Muted.Render(b.String())
}

// styles are the list's own styles, built once per theme generation because
// constructing a lipgloss.Style is the expensive half of drawing a row.
type styles struct {
	gen        int
	selected   lipgloss.Style
	key        lipgloss.Style
	muted      lipgloss.Style
	title      lipgloss.Style
	count      lipgloss.Style
	prompt     lipgloss.Style
	danger     lipgloss.Style
	categories [4]lipgloss.Style
}

func newStyles(t *kernel.Theme) *styles {
	s := &styles{
		gen:      t.Gen,
		selected: t.Selected,
		key:      t.Accent,
		muted:    t.Muted,
		title:    t.Title,
		count:    t.Muted,
		prompt:   t.Accent,
		danger:   t.Danger,
	}
	s.categories = [4]lipgloss.Style{
		jira.CategoryUnknown:    t.Muted,
		jira.CategoryToDo:       t.Base,
		jira.CategoryInProgress: t.Accent,
		jira.CategoryDone:       t.Success,
	}
	return s
}

// rowKey is what makes two renderings of a row the same rendering. It is the
// tuple docs/PERFORMANCE.md asks for — updated, width, selected, theme
// generation — widened to the whole column plan and to the issue's identity,
// since one cache serves every row.
type rowKey struct {
	key      string
	updated  int64
	lay      layout
	selected bool
	gen      int
}

// rowCache is a bounded memo of rendered rows. It holds the visible window and
// its overscan several times over; past that it is emptied rather than evicted
// one at a time, because a scroll invalidates a whole screen at once anyway and
// clear keeps the map's capacity.
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

// renderRow draws one row to exactly lay.width columns.
//
// The three cells that name a facet carry a zone of their own, inside the row's,
// so that a click can mean "narrow to this status" rather than only "this row".
// They are marked here, inside what the memo holds, so that a marked cell costs
// its id once per issue and nothing per frame.
func renderRow(iss *jira.Issue, lay layout, sel bool, st *styles, t *kernel.Theme, loc *time.Location, now time.Time, z widget.Zoner) string {
	ell := t.Glyphs.Ellipsis
	var b strings.Builder
	b.Grow(lay.width + 32)

	if sel {
		b.WriteString(t.Glyphs.Collapsed)
		b.WriteString(strings.Repeat(" ", max(marker-ansi.StringWidth(t.Glyphs.Collapsed), 0)))
	} else {
		b.WriteString(strings.Repeat(" ", marker))
	}
	writeCell(&b, iss.Key, lay.key, ell)
	writeGap(&b)
	writeCell(&b, iss.Summary, lay.summary, ell)
	if lay.typ > 0 {
		writeGap(&b)
		b.WriteString(z.Mark(typeZone(iss.Key), iconOrName(iss.Type.Name, t.Glyphs.TypeGlyph(iss.Type), lay.typ, ell)))
	}
	if lay.status > 0 {
		writeGap(&b)
		cell := iconOrName(iss.Status.Name, t.Glyphs.CategoryGlyph(iss.Status.Category), lay.status, ell)
		if !sel {
			cell = st.categories[categoryIndex(iss.Status.Category)].Render(cell)
		}
		b.WriteString(z.Mark(statusZone(iss.Key), cell))
	}
	if lay.assignee > 0 {
		writeGap(&b)
		b.WriteString(z.Mark(whoZone(iss.Key), padTruncate(assigneeName(iss, unassigned), lay.assignee, ell)))
	}
	if lay.updated > 0 {
		writeGap(&b)
		writeCell(&b, formatWhen(iss.Updated, now, loc), lay.updated, ell)
	}

	if sel {
		return st.selected.Render(b.String())
	}
	return b.String()
}

// iconOrName drops to an icon only where the name would have been
// truncated anyway, never beside a name that already fits.
func iconOrName(name, icon string, width int, ellipsis string) string {
	if icon == "" || ansi.StringWidth(name) <= width {
		return padTruncate(name, width, ellipsis)
	}
	return padTruncate(icon, width, ellipsis)
}

func categoryIndex(c jira.StatusCategory) int {
	if c < jira.CategoryUnknown || c > jira.CategoryDone {
		return int(jira.CategoryUnknown)
	}
	return int(c)
}

func assigneeName(iss *jira.Issue, fallback string) string {
	if iss.Assignee == nil || strings.TrimSpace(iss.Assignee.DisplayName) == "" {
		return fallback
	}
	return iss.Assignee.DisplayName
}

func writeGap(b *strings.Builder) { b.WriteString("  ") }

func writeCell(b *strings.Builder, s string, width int, ellipsis string) {
	if width <= 0 {
		return
	}
	b.WriteString(padTruncate(s, width, ellipsis))
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

// formatWhen renders an instant in the Jira account's timezone, which is not
// the machine's. The year is shown only when it is not the current one, which
// is what buys the column back to twelve cells.
func formatWhen(t, now time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.UTC
	}
	in := t.In(loc)
	if in.Year() == now.In(loc).Year() {
		return in.Format("02 Jan 15:04")
	}
	return in.Format("02 Jan 2006")
}
