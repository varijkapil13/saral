package release

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// sortField is one order the list can draw its versions in. The site is
// always read in its own sequence, because that one list also feeds the flow's
// picker, so every other order is laid over it here.
type sortField struct {
	id    string
	label string
	// compare is nil for the project's own order, which is the order the
	// versions arrived in and so needs nothing but a direction.
	compare func(a, b sortable) int
	// dated fields put a version with no date last whichever way they run: an
	// undated version has not been planned, and reversing the list should not
	// bring the unplanned ones to the top.
	date func(*jira.Version) jira.Date
}

// sortable is what one version is ordered by: the version itself and the
// state its row was drawn in, which depends on the reader's own date.
type sortable struct {
	v     *jira.Version
	state string
	owner string
}

const sortProject = "project"

var sortFields = []sortField{
	{id: sortProject, label: "project order"},
	{id: "name", label: "name", compare: func(a, b sortable) int {
		return strings.Compare(strings.ToLower(a.v.Name), strings.ToLower(b.v.Name))
	}},
	{id: "release", label: "release date", date: func(v *jira.Version) jira.Date { return v.ReleaseDate }},
	{id: "start", label: "start date", date: func(v *jira.Version) jira.Date { return v.StartDate }},
	{id: "state", label: "state", compare: func(a, b sortable) int {
		return stateRank(a.state) - stateRank(b.state)
	}},
}

// stateRank puts what still needs doing first: an overdue version, then one
// still to ship, then the shipped ones and last whatever has been put away.
func stateRank(state string) int {
	switch state {
	case stateOverdue:
		return 0
	case stateUnreleased:
		return 1
	case stateReleased:
		return 2
	default:
		return 3
	}
}

func compareDates(a, b jira.Date) int {
	switch {
	case a.Before(b):
		return -1
	case b.Before(a):
		return 1
	default:
		return 0
	}
}

func fieldIndex(fields []sortField, id string) int {
	for i, f := range fields {
		if f.id == id {
			return i
		}
	}
	return 0
}

func lookupField(fields []sortField, id string) (sortField, bool) {
	for _, f := range fields {
		if f.id == id {
			return f, true
		}
	}
	return sortField{}, false
}

func (m *Model) fields() []sortField {
	if m.set != nil {
		return setSortFields
	}
	return sortFields
}

func (m *Model) ownerLabel(i int) string {
	if m.set == nil {
		return ""
	}
	return m.set.projects[i]
}

func (m *Model) viewID() string {
	if m.set != nil {
		return SetViewID
	}
	return ViewID
}

// sortChoice is the order the rows are drawn in. The zero value is the
// project's own order, ascending, which is what the site sends.
type sortChoice struct {
	field string
	desc  bool
}

func (c sortChoice) fieldID() string {
	if c.field == "" {
		return sortProject
	}
	return c.field
}

// chosen is whether anything but the site's own order is in force.
func (c sortChoice) chosen() bool { return c.fieldID() != sortProject || c.desc }

func (c sortChoice) plain(g kernel.Glyphs) string { return c.plainIn(sortFields, g) }

func (c sortChoice) plainIn(fields []sortField, g kernel.Glyphs) string {
	f, _ := lookupField(fields, c.fieldID())
	return f.label + " " + sortArrow(c.desc, g)
}

func (c sortChoice) label(g kernel.Glyphs) string { return "sort: " + c.plain(g) }

func sortArrow(desc bool, g kernel.Glyphs) string {
	switch {
	case g.IsASCII() && desc:
		return "v"
	case g.IsASCII():
		return "^"
	case desc:
		return "↓"
	default:
		return "↑"
	}
}

func (c sortChoice) toSpec() config.SortSpec {
	if !c.chosen() {
		return config.SortSpec{}
	}
	return config.SortSpec{Field: c.fieldID(), Desc: c.desc}
}

// loadSort is the order this machine last left the list in, or the project's
// own on a first run, an unwritable cache or a field this build does not know.
func loadSort(view string) sortChoice {
	spec, ok := config.LoadUIState().Sort(view)
	if !ok {
		return sortChoice{}
	}
	fields := sortFields
	if view == SetViewID {
		fields = setSortFields
	}
	if _, known := lookupField(fields, spec.Field); !known {
		return sortChoice{}
	}
	return sortChoice{field: spec.Field, desc: spec.Desc}
}

// reorder lays the chosen order and the state filter over the versions as the
// site sent them. m.versions is never reordered itself: it is what the cache
// keeps and what the flow is offered, both in the project's own sequence.
func (m *Model) reorder() {
	if len(m.cells) != len(m.versions) {
		m.rebuildCells()
		return
	}
	m.sorted = m.sorted[:0]
	for i := range m.versions {
		m.sorted = append(m.sorted, i)
	}
	f, _ := lookupField(m.fields(), m.sort.fieldID())
	desc := m.sort.desc
	switch {
	case f.compare == nil && f.date == nil:
		if desc {
			slices.Reverse(m.sorted)
		}
	default:
		slices.SortStableFunc(m.sorted, func(a, b int) int { return m.compareRows(f, desc, a, b) })
	}
	m.order = m.order[:0]
	if m.set != nil {
		m.arrangeSlots()
		return
	}
	for _, i := range m.sorted {
		if m.filter.keeps(m.cells[i].state) {
			m.order = append(m.order, slot{v: int32(i), g: -1})
		}
	}
}

func (m *Model) compareRows(f sortField, desc bool, a, b int) int {
	var c int
	if f.date != nil {
		da, db := f.date(&m.versions[a]), f.date(&m.versions[b])
		switch {
		case da.IsZero() && db.IsZero():
			return 0
		case da.IsZero():
			return 1
		case db.IsZero():
			return -1
		}
		c = compareDates(da, db)
	} else {
		c = f.compare(
			sortable{&m.versions[a], m.cells[a].state, m.ownerLabel(a)},
			sortable{&m.versions[b], m.cells[b].state, m.ownerLabel(b)})
	}
	if desc {
		return -c
	}
	return c
}

// --- the picker -------------------------------------------------------------

func (m *Model) startSort() tea.Cmd {
	if m.saving || m.mode != browsing {
		return nil
	}
	m.mode = sorting
	m.sortCursor = fieldIndex(m.fields(), m.sort.fieldID())
	m.sum = ""
	m.clampScroll()
	return nil
}

func (m *Model) cancelSort() tea.Cmd {
	m.mode = browsing
	m.sum = ""
	m.scrollToCursor()
	return nil
}

func (m *Model) sortKey(stroke string) tea.Cmd {
	switch m.inSort[stroke] {
	case actSortPrev:
		m.sortCursor = (m.sortCursor - 1 + len(m.fields())) % len(m.fields())
	case actSortNext:
		m.sortCursor = (m.sortCursor + 1) % len(m.fields())
	case actSortChoose:
		return m.chooseSort()
	case actSortCancel:
		return m.cancelSort()
	default:
	}
	return nil
}

// chooseSort applies the field under the cursor. Choosing the field already in
// force turns it round instead, so one gesture reaches both directions.
func (m *Model) chooseSort() tea.Cmd {
	f := m.fields()[m.sortCursor]
	next := sortChoice{field: f.id}
	if m.sort.fieldID() == f.id {
		next.desc = !m.sort.desc
	}
	if next.field == sortProject {
		next.field = ""
	}
	m.mode = browsing
	m.sum = ""
	if next == m.sort {
		m.scrollToCursor()
		return nil
	}
	under := m.selectedID()
	m.sort = next
	m.reorder()
	m.moveOnto(under)
	return m.keepSort()
}

// sortSaveFailedMsg reports that the order on screen is not the one the next
// session will open with.
type sortSaveFailedMsg struct{ err error }

// keepSort writes the choice off the event loop. The order already works when
// this runs, so a failure is reported rather than undone, and only once.
func (m *Model) keepSort() tea.Cmd {
	if m.sortSaveFailed {
		return nil
	}
	spec, view := m.sort.toSpec(), m.viewID()
	return kernel.Reply(func() tea.Msg {
		if err := config.SaveSort(view, spec); err != nil {
			return sortSaveFailedMsg{err: err}
		}
		return nil
	}, m.addr)
}

func (m *Model) reportSortSaveFailed(msg sortSaveFailedMsg) tea.Cmd {
	if m.sortSaveFailed {
		return nil
	}
	m.sortSaveFailed = true
	return kernel.Warn("the sort order on screen will not survive a restart: " + msg.err.Error())
}

// --- rendering --------------------------------------------------------------

// sortZone names the summary line's sort label, so a click on it reopens the
// picker.
const sortZone = "sort"

// sortPrompt is the line the picker puts under the rows. It is built as plain
// text and styled whole, so narrowing it never reopens a style mid-line.
func (m *Model) sortPrompt() string {
	hint := "  enter chooses, ←/→ move, esc cancels"
	if m.deps.Theme.Glyphs.IsASCII() {
		hint = "  enter chooses, left/right move, esc cancels"
	}
	label := "  sort by:  " + m.sortFieldsLine()
	// The fields are what the line is for; the hint repeats the footer and
	// is the first thing given up.
	if ansi.StringWidth(label)+ansi.StringWidth(hint) > m.width {
		hint = ""
	}
	ell := m.deps.Theme.Glyphs.Ellipsis
	return m.styles.accent.Render(ansi.Truncate(label, max(m.width, 1), ell)) + m.styles.muted.Render(hint)
}

func (m *Model) sortFieldsLine() string {
	var b strings.Builder
	for i, f := range m.fields() {
		if i > 0 {
			b.WriteString("  ")
		}
		name := f.label
		if f.id == m.sort.fieldID() {
			name += " " + sortArrow(m.sort.desc, m.deps.Theme.Glyphs)
		}
		if i == m.sortCursor {
			name = "[" + name + "]"
		}
		b.WriteString(name)
	}
	return b.String()
}
