package list

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// sortLabel is what the header names the choice as, docs/FILTERS.md's own
// example being "sort: updated ↓". The arrow is drawn from Glyphs.IsASCII
// rather than from a field of its own on kernel.Glyphs, which this packet does
// not own.
func sortLabel(c appsearch.SortChoice, g kernel.Glyphs) string {
	f, ok := appsearch.SortFieldByID(c.Field)
	if !ok {
		return ""
	}
	return "sort: " + f.Label + " " + sortArrow(c.Desc, g)
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

func sortSpec(c appsearch.SortChoice) config.SortSpec {
	return config.SortSpec{Field: c.Field, Desc: c.Desc}
}

func sortChoiceFromSpec(spec config.SortSpec) appsearch.SortChoice {
	if _, ok := appsearch.SortFieldByID(spec.Field); !ok {
		return appsearch.SortChoice{}
	}
	return appsearch.SortChoice{Field: spec.Field, Desc: spec.Desc}
}

// loadSort is what this view opens its sort on: whatever this machine last
// left it as, or no choice at all on a first run or an unwritable cache.
func loadSort(view string) appsearch.SortChoice {
	spec, ok := config.LoadUIState().Sort(view)
	if !ok {
		return appsearch.SortChoice{}
	}
	return sortChoiceFromSpec(spec)
}

// --- the picker ---------------------------------------------------------

// startSort opens the picker, the cursor on the field already chosen so that
// pressing enter again is what toggles its direction.
func (m *Model) startSort() tea.Cmd {
	m.sorting = true
	m.sortCursor = appsearch.SortFieldIndex(m.sort.Field)
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
		m.sortCursor = (m.sortCursor - 1 + len(appsearch.SortFields)) % len(appsearch.SortFields)
	case actSortNext:
		m.sortCursor = (m.sortCursor + 1) % len(appsearch.SortFields)
	case actSortChoose:
		return m.chooseSort()
	case actSortCancel:
		return m.cancelSort()
	default:
	}
	return nil
}

// chooseSort applies the field under the cursor.
func (m *Model) chooseSort() tea.Cmd {
	next := m.sort.Next(appsearch.SortFields[m.sortCursor].ID)
	m.sorting = false
	m.clampScroll()
	return m.applySortChoice(next)
}

// applySortChoice puts a new order in force and re-runs the search on screen
// under it, keeping the terms and everything else the search was already
// narrowed by — a sort is a search's order, not the rest of it.
func (m *Model) applySortChoice(next appsearch.SortChoice) tea.Cmd {
	if next == m.sort {
		return nil
	}
	m.sort = next
	terms := m.terms
	cmd := m.setQuery(m.jql, m.title, m.defaulted)
	m.terms, m.termsGen = terms, m.termsGen+1
	m.rememberTerms()
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
	spec := sortSpec(m.sort)
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
	for i, f := range appsearch.SortFields {
		if i > 0 {
			b.WriteString("  ")
		}
		name := f.Label
		if f.ID == m.sort.Field {
			name += " " + sortArrow(m.sort.Desc, m.deps.Theme.Glyphs)
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
