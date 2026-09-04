package board

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// quickFiltersMsg carries a board's own quick filters. An error reading them
// answers the same shape with none: the board still draws without them, the
// way it draws without an estimation field, so a site the token cannot ask a
// second endpoint of does not lose the first one's cards over it.
type quickFiltersMsg struct {
	gen     int
	filters []jira.QuickFilter
}

func quickFiltersCmd(ctx context.Context, reader jira.BoardReader, boardID int64, gen int) tea.Cmd {
	return func() tea.Msg {
		found, err := reader.QuickFilters(ctx, boardID)
		if err != nil {
			return quickFiltersMsg{gen: gen}
		}
		return quickFiltersMsg{gen: gen, filters: found}
	}
}

func (m *Model) tookQuickFilters(msg quickFiltersMsg) {
	if !m.current(msg.gen) {
		return
	}
	m.quickFilters = msg.filters
	m.qfOn = make(map[int64]bool, len(msg.filters))
}

// toggleQuickFilter flips the nth quick filter this board offers, 1-indexed the
// way a footer digit is, and reports whether there was one to flip.
func (m *Model) toggleQuickFilter(n int) bool {
	if n < 1 || n > len(m.quickFilters) {
		return false
	}
	id := m.quickFilters[n-1].ID
	if m.qfOn == nil {
		m.qfOn = make(map[int64]bool, len(m.quickFilters))
	}
	m.qfOn[id] = !m.qfOn[id]
	return true
}

// activeQuickFilterJQL is the JQL of every toggled-on quick filter, in the
// board's own display order rather than map order, so a request built from it
// is reproducible from one frame to the next.
func (m *Model) activeQuickFilterJQL() []string {
	if len(m.qfOn) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.qfOn))
	for _, qf := range m.quickFilters {
		if m.qfOn[qf.ID] {
			out = append(out, qf.JQL)
		}
	}
	return out
}

// quickFilterLine names whichever quick filters are toggled on, in the board's
// own display order, and is empty when none are.
func (m *Model) quickFilterLine() string {
	if len(m.qfOn) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.qfOn))
	for _, qf := range m.quickFilters {
		if m.qfOn[qf.ID] {
			names = append(names, qf.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "filters: " + strings.Join(names, ", ")
}
