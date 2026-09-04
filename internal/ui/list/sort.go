package list

import (
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// sortField is one field JQL can order by that this client can name without
// guessing: a keyword of the query language itself, never a customfield id or
// anything read off a site. jqlName is the language's own spelling, which is
// not always the label this program draws (issuetype, not type).
type sortField struct {
	id      string
	label   string
	jqlName string
	// desc is the direction a field opens in the first time it is chosen: the
	// three date fields read newest first, everything else alphabetically.
	desc bool
}

// sortFields is the picker's whole offer, in the order docs/FILTERS.md lists
// them.
var sortFields = []sortField{
	{id: "key", label: "key", jqlName: "key"},
	{id: "summary", label: "summary", jqlName: "summary"},
	{id: "status", label: "status", jqlName: "status"},
	{id: "type", label: "type", jqlName: "issuetype"},
	{id: "priority", label: "priority", jqlName: "priority"},
	{id: "assignee", label: "assignee", jqlName: "assignee"},
	{id: "created", label: "created", jqlName: "created", desc: true},
	{id: "updated", label: "updated", jqlName: "updated", desc: true},
	{id: "due", label: "due", jqlName: "duedate", desc: true},
}

func sortFieldByID(id string) (sortField, bool) {
	for _, f := range sortFields {
		if f.id == id {
			return f, true
		}
	}
	return sortField{}, false
}

func sortFieldIndex(id string) int {
	for i, f := range sortFields {
		if f.id == id {
			return i
		}
	}
	return 0
}

// sortChoice is the order this view's search is asked to run in, over and
// above whatever it would otherwise carry. A zero value is no choice at all,
// which leaves a search reading in the order it always named for itself.
type sortChoice struct {
	field string
	desc  bool
}

func (c sortChoice) chosen() bool { return c.field != "" }

// clause is the ORDER BY this choice asks for, and false where it names a
// field a hand-edited file or an older build put there and this one does not.
func (c sortChoice) clause() (string, bool) {
	f, ok := sortFieldByID(c.field)
	if !ok {
		return "", false
	}
	dir := "ASC"
	if c.desc {
		dir = "DESC"
	}
	return "ORDER BY " + f.jqlName + " " + dir, true
}

// label is what the header names the choice as, docs/FILTERS.md's own example
// being "sort: updated ↓". The arrow is drawn from Glyphs.IsASCII rather than
// from a field of its own on kernel.Glyphs, which this packet does not own.
func (c sortChoice) label(g kernel.Glyphs) string {
	f, ok := sortFieldByID(c.field)
	if !ok {
		return ""
	}
	return "sort: " + f.label + " " + sortArrow(c.desc, g)
}

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

func (c sortChoice) toSpec() config.SortSpec { return config.SortSpec{Field: c.field, Desc: c.desc} }

func sortChoiceFromSpec(spec config.SortSpec) sortChoice {
	if _, ok := sortFieldByID(spec.Field); !ok {
		return sortChoice{}
	}
	return sortChoice{field: spec.Field, desc: spec.Desc}
}

// loadSort is what this view opens its sort on: whatever this machine last
// left it as, or no choice at all on a first run or an unwritable cache.
func loadSort(view string) sortChoice {
	spec, ok := config.LoadUIState().Sort(view)
	if !ok {
		return sortChoice{}
	}
	return sortChoiceFromSpec(spec)
}

// orderByPattern finds the ORDER BY a composed query ends in. JQL puts
// exactly one at the end of a query, never inside a clause's own text, so
// matching to the end of the string is enough to take the whole thing off.
var orderByPattern = regexp.MustCompile(`(?i)\s+order\s+by\s+.*$`)

// applySort replaces whatever order a query already carries with the one
// chosen for this view, leaving everything before it untouched. A jql with no
// choice made for it is returned as it arrived, which is what leaves a
// search's own ORDER BY the answer until something asks for another one.
func applySort(jql string, c sortChoice) string {
	clause, ok := c.clause()
	if !ok {
		return jql
	}
	base := strings.TrimSpace(orderByPattern.ReplaceAllString(jql, ""))
	if base == "" {
		return clause
	}
	return base + " " + clause
}

// --- the picker ---------------------------------------------------------

// startSort opens the picker, the cursor on the field already chosen so that
// pressing enter again is what toggles its direction.
func (m *Model) startSort() tea.Cmd {
	m.sorting = true
	m.sortCursor = sortFieldIndex(m.sort.field)
	m.clampScroll()
	return nil
}

func (m *Model) cancelSort() tea.Cmd {
	m.sorting = false
	m.clampScroll()
	return nil
}

// sortKey takes the picker's own keys: left and right move the cursor, enter
// chooses the field under it and esc leaves the order as it was.
func (m *Model) sortKey(stroke string) tea.Cmd {
	switch m.inSort[stroke] {
	case actSortPrev:
		m.sortCursor = (m.sortCursor - 1 + len(sortFields)) % len(sortFields)
	case actSortNext:
		m.sortCursor = (m.sortCursor + 1) % len(sortFields)
	case actSortChoose:
		return m.chooseSort()
	case actSortCancel:
		return m.cancelSort()
	default:
	}
	return nil
}

// chooseSort applies the field under the cursor. Choosing the field already in
// force is what toggles its direction, which is how one gesture reaches both
// halves of docs/FILTERS.md's "each toggling ascending and descending".
func (m *Model) chooseSort() tea.Cmd {
	f := sortFields[m.sortCursor]
	next := sortChoice{field: f.id, desc: f.desc}
	if m.sort.field == f.id {
		next.desc = !m.sort.desc
	}
	m.sorting = false
	m.clampScroll()
	return m.applySortChoice(next)
}

// applySortChoice puts a new order in force and re-runs the search on screen
// under it, keeping the terms and everything else the search was already
// narrowed by — a sort is a search's order, not the rest of it.
func (m *Model) applySortChoice(next sortChoice) tea.Cmd {
	if next == m.sort {
		return nil
	}
	m.sort = next
	terms := m.terms
	cmd := m.setQuery(m.jql, m.title, m.defaulted)
	m.terms, m.termsGen = terms, m.termsGen+1
	return tea.Batch(cmd, m.keepSort())
}

// sortSaveFailedMsg reports that the order on screen is not the one the next
// session will open with.
type sortSaveFailedMsg struct{ err error }

// keepSort writes the choice to the cache directory, off the event loop. The
// order already works when this runs, so a failure is reported rather than
// undone — and said once, the way issue.Model's keepSplit already does for its
// own split, because a warning on every stroke would bury whatever came
// before it.
func (m *Model) keepSort() tea.Cmd {
	if m.sortSaveFailed {
		return nil
	}
	spec := m.sort.toSpec()
	return kernel.Reply(func() tea.Msg {
		if err := config.SaveSort(ViewID, spec); err != nil {
			return sortSaveFailedMsg{err: err}
		}
		return nil
	}, m.addr)
}

func (m *Model) reportSortSaveFailed(msg sortSaveFailedMsg) tea.Cmd {
	m.sortSaveFailed = true
	return kernel.Warn("the sort order on screen will not survive a restart: " + msg.err.Error())
}

// sortPrompt is the line the gesture puts under the rows: every field this
// client can order by, the cursor on one of them, and the one already chosen
// naming its own direction. The line is built as plain text and styled once
// as a whole, the same order askPrompt and bindPrompt already keep, so that
// truncating it for a narrow terminal never has to reopen a style mid-line.
func (m *Model) sortPrompt() string {
	hint := "  enter chooses, ←/→ move, esc cancels"
	label := "sort by:  " + m.sortFieldsLine()
	room := max(m.width-ansi.StringWidth(hint), 8)
	return m.styles.prompt.Render(ansi.Truncate(label, room, m.deps.Theme.Glyphs.Ellipsis)) +
		m.styles.muted.Render(hint)
}

func (m *Model) sortFieldsLine() string {
	var b strings.Builder
	for i, f := range sortFields {
		if i > 0 {
			b.WriteString("  ")
		}
		name := f.label
		if f.id == m.sort.field {
			name += " " + sortArrow(m.sort.desc, m.deps.Theme.Glyphs)
		}
		if i == m.sortCursor {
			name = "[" + name + "]"
		}
		b.WriteString(name)
	}
	return b.String()
}

// sortZone names the header's own sort label, so a click on it reopens the
// picker the same way a click on the title reopens the search prompt.
const sortZone = "sort"
