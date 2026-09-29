package list

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	gap         = 2
	marker      = 2
	minSummary  = 28
	minKeyWidth = 6
	maxKeyWidth = 14
	typeWidth   = 10
	statusWidth = 13
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
	// leads are the icons a type or status cell opens with, each already joined
	// to the space that keeps it off the name. Ten glyphs exist; building the
	// pair per row put the join on every fresh row, which has a budget.
	leads map[string]string
}

func (s *styles) lead(glyph string) string {
	if glyph == "" {
		return ""
	}
	if l, ok := s.leads[glyph]; ok {
		return l
	}
	return glyph + " "
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
	g := t.Glyphs
	s.leads = make(map[string]string, 10)
	for _, glyph := range []string{
		g.TypeEpic, g.TypeStory, g.TypeTask, g.TypeBug, g.TypeSubtask, g.TypeOther,
		g.CategoryToDo, g.CategoryInProgress, g.CategoryDone, g.CategoryUnknown,
	} {
		s.leads[glyph] = glyph + " "
	}
	return s
}

// rowKey is what makes two renderings of a row the same rendering. It is the
// tuple docs/PERFORMANCE.md asks for — updated, width, selected, theme
// generation — widened to the whole column plan and to the issue's identity,
// since one cache serves every row.
//
// The narrow fields sit together at the end so that the key packs into fewer
// words: the memo is sized for rowCacheLimit entries up front, which a view
// pays on its first paint.
type rowKey struct {
	key        string
	updated    int64
	lay        layout
	width, gen int32
	look       card.Look
	selected   bool
	mouse      bool
}

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
		b.WriteString(z.Mark(typeZone(iss.Key), iconAndName(iss.Type.Name, st.lead(t.Glyphs.TypeGlyph(iss.Type)), lay.typ, ell)))
	}
	if lay.status > 0 {
		writeGap(&b)
		cell := iconAndName(iss.Status.Name, st.lead(t.Glyphs.CategoryGlyph(iss.Status.Category)), lay.status, ell)
		if !sel {
			cell = st.categories[categoryIndex(iss.Status.Category)].Render(cell)
		}
		b.WriteString(z.Mark(statusZone(iss.Key), cell))
	}
	if lay.assignee > 0 {
		writeGap(&b)
		who := widget.PadTruncate(widget.Sanitize(assigneeName(iss, unassigned)), lay.assignee, ell)
		b.WriteString(z.Mark(whoZone(iss.Key), who))
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

// iconAndName draws a cell as the icon and then the name — the name where it
// fits behind the icon, the icon alone where it does not. It used to be the
// name where it fit and the icon only where it did not, so a row at any
// ordinary width showed the word and never the shape; the shape is the part a
// reader takes in without reading, and it goes first, everywhere.
func iconAndName(name, lead string, width int, ellipsis string) string {
	name = widget.Sanitize(name)
	if lead == "" {
		return widget.PadTruncate(name, width, ellipsis)
	}
	if rest := width - ansi.StringWidth(lead); rest >= 1 {
		return lead + widget.PadTruncate(name, rest, ellipsis)
	}
	return widget.PadTruncate(strings.TrimSuffix(lead, " "), width, ellipsis)
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
	b.WriteString(widget.PadTruncate(widget.Sanitize(s), width, ellipsis))
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

// facts is what a card shows of an issue. The facet cells are named only while
// the mouse is on, since a zone nobody can click is an allocation for nothing.
func (m *Model) facts(iss *jira.Issue) card.Facts {
	g := m.deps.Theme.Glyphs
	loc, now := m.deps.Caps.Location(), m.now()
	f := card.Facts{
		Key: iss.Key, Summary: iss.Summary,
		TypeGlyph: g.TypeGlyph(iss.Type), TypeName: iss.Type.Name,
		StatusGlyph: g.CategoryGlyph(iss.Status.Category), StatusName: iss.Status.Name,
		Category: categoryIndex(iss.Status.Category),
		Assignee: assigneeName(iss, ""),
		Updated:  formatWhen(iss.Updated, now, loc),
		Subtasks: subtaskCount(iss.Subtasks),
		Labels:   iss.Labels,
	}
	if iss.Priority != nil {
		f.Priority = iss.Priority.Name
	}
	f.Due, f.Overdue = formatDue(iss.Due, now, loc)
	if len(iss.FixVersions) > 0 {
		f.FixVersions = make([]string, len(iss.FixVersions))
		for i := range iss.FixVersions {
			f.FixVersions[i] = iss.FixVersions[i].Name
		}
	}
	if m.zones.Enabled() {
		f.TypeZone, f.StatusZone, f.WhoZone = typeZone(iss.Key), statusZone(iss.Key), whoZone(iss.Key)
	}
	return f
}

// formatDue spells a due date the way formatWhen spells a day, the year shown
// only when it is not the current one, and says whether it has passed. Both are
// judged in the account's timezone: a due date is a calendar day, and today is
// the account's today rather than the machine's.
func formatDue(d jira.Date, now time.Time, loc *time.Location) (string, bool) {
	if d.IsZero() {
		return "", false
	}
	if loc == nil {
		loc = time.UTC
	}
	today := jira.DateOf(now.In(loc))
	layout := "due 02 Jan"
	if d.Year != today.Year {
		layout = "due 02 Jan 2006"
	}
	return d.In(loc).Format(layout), !now.IsZero() && d.Before(today)
}

// subtaskCount is "done/total", and nothing for an issue with no subtasks.
func subtaskCount(subs []jira.IssueRef) string {
	if len(subs) == 0 {
		return ""
	}
	done := 0
	for i := range subs {
		if subs[i].Status.Category == jira.CategoryDone {
			done++
		}
	}
	return strconv.Itoa(done) + "/" + strconv.Itoa(len(subs))
}
