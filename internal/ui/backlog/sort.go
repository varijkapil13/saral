package backlog

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// sortField is one field this view can order its own rows by, matching
// internal/ui/list's offer field for field (docs/FILTERS.md). It is this
// package's own copy rather than a shared one: jira.BoardQuery deliberately
// narrows nothing beyond a board's own filter and sub-query — see its doc
// comment in pkg/jira/port.go — so unlike a JQL search this cannot ask the
// site to read in another order. A chosen field reorders the issues a section
// already holds instead, which is what every section already does locally
// when the board names no rank field of its own.
type sortField struct {
	id      string
	label   string
	compare func(a, b *jira.Issue) int
	// desc is the direction a field opens in the first time it is chosen: the
	// three date fields read newest first, everything else alphabetically.
	desc bool
}

var sortFields = []sortField{
	{id: "key", label: "key", compare: func(a, b *jira.Issue) int { return compareIssueKeys(a.Key, b.Key) }},
	{id: "summary", label: "summary", compare: compareStrings(func(i *jira.Issue) string { return i.Summary })},
	{id: "status", label: "status", compare: compareStrings(func(i *jira.Issue) string { return i.Status.Name })},
	{id: "type", label: "type", compare: compareStrings(func(i *jira.Issue) string { return i.Type.Name })},
	{id: "priority", label: "priority", compare: compareStrings(priorityName)},
	{id: "assignee", label: "assignee", compare: compareStrings(assigneeName)},
	{
		id: "created", label: "created", desc: true,
		compare: compareTimes(func(i *jira.Issue) time.Time { return i.Created }),
	},
	{
		id: "updated", label: "updated", desc: true,
		compare: compareTimes(func(i *jira.Issue) time.Time { return i.Updated }),
	},
	{id: "due", label: "due", desc: true, compare: compareDue},
}

func compareStrings(key func(*jira.Issue) string) func(a, b *jira.Issue) int {
	return func(a, b *jira.Issue) int { return strings.Compare(key(a), key(b)) }
}

func compareTimes(key func(*jira.Issue) time.Time) func(a, b *jira.Issue) int {
	return func(a, b *jira.Issue) int {
		at, bt := key(a), key(b)
		switch {
		case at.Before(bt):
			return -1
		case bt.Before(at):
			return 1
		default:
			return 0
		}
	}
}

func compareDue(a, b *jira.Issue) int {
	switch {
	case a.Due.Before(b.Due):
		return -1
	case b.Due.Before(a.Due):
		return 1
	default:
		return 0
	}
}

// compareIssueKeys orders PROJ-2 before PROJ-10. A plain string compare would
// not — '1' sorts before '2' — which is not the order a person means by "by
// key" and not the order the real ORDER BY key internal/ui/list sends produces
// either.
func compareIssueKeys(a, b string) int {
	ap, an, aok := splitKeySuffix(a)
	bp, bn, bok := splitKeySuffix(b)
	if aok && bok && ap == bp {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(a, b)
}

// splitKeySuffix reads a key's project prefix and its numeric tail. A key this
// program did not mint itself — one with no dash, or a non-numeric tail — falls
// back to a plain compare rather than guessing at a shape it cannot name.
func splitKeySuffix(key string) (prefix string, n int, ok bool) {
	i := strings.LastIndexByte(key, '-')
	if i < 0 || i == len(key)-1 {
		return "", 0, false
	}
	num, err := strconv.Atoi(key[i+1:])
	if err != nil {
		return "", 0, false
	}
	return key[:i], num, true
}

// priorityName is a priority's own name where the issue has one at all.
// pkg/jira.Issue carries it as *Priority, unset wherever a site's screen
// scheme leaves the field off.
func priorityName(iss *jira.Issue) string {
	if iss.Priority == nil {
		return ""
	}
	return iss.Priority.Name
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

// sortChoice is the order this view reads its sections in, over and above a
// board's own rank. A zero value is no choice made.
type sortChoice struct {
	field string
	desc  bool
}

func (c sortChoice) chosen() bool { return c.field != "" }

// plain is the field and its direction with nothing in front of it; label is
// the same thing with the word docs/FILTERS.md's own header example uses.
func (c sortChoice) plain(g kernel.Glyphs) string {
	f, ok := sortFieldByID(c.field)
	if !ok {
		return ""
	}
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

func (c sortChoice) toSpec() config.SortSpec { return config.SortSpec{Field: c.field, Desc: c.desc} }

func sortChoiceFromSpec(spec config.SortSpec) sortChoice {
	if _, ok := sortFieldByID(spec.Field); !ok {
		return sortChoice{}
	}
	return sortChoice{field: spec.Field, desc: spec.Desc}
}

// loadSort is what this view opens its order on: whatever this machine last
// left it as, or no choice at all on a first run or an unwritable cache.
func loadSort(view string) sortChoice {
	spec, ok := config.LoadUIState().Sort(view)
	if !ok {
		return sortChoice{}
	}
	return sortChoiceFromSpec(spec)
}

// orderIssues puts each section's own issues in order: by the field chosen
// here when there is one, and by the board's own rank (or arrival order on a
// board with no rank field) otherwise. The two never combine, since choosing a
// field is choosing to stop reading the board's own order.
func (m *Model) orderIssues() {
	f, ok := sortFieldByID(m.sort.field)
	if !ok {
		m.rank()
		return
	}
	desc := m.sort.desc
	for g := range m.groups {
		issues := m.groups[g].issues
		slices.SortStableFunc(issues, func(a, b int) int {
			c := f.compare(&m.issues[a], &m.issues[b])
			if desc {
				return -c
			}
			return c
		})
	}
}

// --- the picker ---------------------------------------------------------

func (m *Model) startSort() tea.Cmd {
	if m.busy() {
		return nil
	}
	m.mode = sorting
	m.sortCursor = sortFieldIndex(m.sort.field)
	m.keepVisible()
	return nil
}

func (m *Model) cancelSort() tea.Cmd {
	m.mode = browsing
	m.keepVisible()
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
// force toggles its direction instead, which is how one gesture reaches both
// halves of docs/FILTERS.md's "each toggling ascending and descending".
func (m *Model) chooseSort() tea.Cmd {
	f := sortFields[m.sortCursor]
	next := sortChoice{field: f.id, desc: f.desc}
	if m.sort.field == f.id {
		next.desc = !m.sort.desc
	}
	m.mode = browsing
	return m.applySortChoice(next)
}

// sortNeedsTheRest is what the status line says while the pages a chosen order
// has not seen yet are being read.
const sortNeedsTheRest = "reading the rest of the backlog, because an order over part of it is an order over the wrong issues"

// applySortChoice puts a new order in force.
//
// It cannot ask the site to read in another order — see sortField's own doc
// comment — so it orders the issues it holds. That is only the same thing as
// ordering the backlog once it holds all of them: this view reads fifty at a
// time and pages as the cursor nears the end, so ordering a part would put a
// later page's issue above the row somebody is reading the moment that page
// lands, which is both a wrong answer and the one thing docs/UX.md asks a
// background read never to do. So the rest is read first and the order changes
// once, rather than settling over several pages.
func (m *Model) applySortChoice(next sortChoice) tea.Cmd {
	if next == m.sort {
		m.keepVisible()
		return nil
	}
	if _, orders := sortFieldByID(next.field); orders && m.page.HasMore() {
		m.pendingSort, m.reading = next, true
		return tea.Batch(kernel.Status(sortNeedsTheRest), m.readRest())
	}
	return m.setSort(next)
}

// setSort is the order taking effect over everything in hand.
func (m *Model) setSort(next sortChoice) tea.Cmd {
	m.sort = next
	under := m.under()
	m.orderIssues()
	m.rebuildRows()
	m.restore(under)
	return m.keepSort()
}

// sortSaveFailedMsg reports that the order on screen is not the one the next
// session will open with.
type sortSaveFailedMsg struct{ err error }

// keepSort writes the choice to the cache directory, off the event loop. The
// order already works when this runs, so a failure is reported rather than
// undone, and said once so a warning on every stroke does not bury whatever
// came before it.
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

// --- rendering ------------------------------------------------------------

// sortZone names the header's own sort label, so a click on it reopens the
// picker.
const sortZone = "sort"

// sortPrompt is the line the gesture puts under the rows: every field this
// view can order by, the cursor on one of them, and the one already chosen
// naming its own direction. Built as plain text and styled once as a whole,
// so truncating it for a narrow terminal never has to reopen a style mid-line.
func (m *Model) sortPrompt() string {
	hint := "  enter chooses, ←/→ move, esc cancels"
	label := "sort by:  " + m.sortFieldsLine()
	room := max(m.width-ansi.StringWidth(hint), 8)
	return m.styles.accent.Render(ansi.Truncate(label, room, m.deps.Theme.Glyphs.Ellipsis)) +
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
