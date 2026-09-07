package board

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// quickFiltersMemoryKey is where this view keeps which of a board's quick
// filters were toggled on, under its own ViewID.
const quickFiltersMemoryKey = "quickfilters"

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

func (m *Model) tookQuickFilters(msg quickFiltersMsg) tea.Cmd {
	if !m.current(msg.gen) {
		return nil
	}
	m.quickFilters = msg.filters
	m.qfOn = make(map[int64]bool, len(msg.filters))
	stored := m.storeBoard()
	// loadCards already ran once in tookConfig, before this board's own live
	// quick filters were known, so a recalled selection reaches the cards on
	// screen only by asking again now that there is something to turn on.
	if m.applyRecalledQuickFilters() {
		return tea.Batch(stored, m.loadCards())
	}
	return stored
}

// recallQuickFilterIDs is which of a board's quick filters the last session
// on this profile left toggled on, and whether it ever kept one at all. The
// ids are this program's own memory rather than anything read off the
// board's own config, so a filter this board no longer offers is simply
// never among them.
func (m *Model) recallQuickFilterIDs() ([]int64, bool) {
	enc, ok := kernel.Recall(m.deps, ViewID, quickFiltersMemoryKey)
	if !ok {
		return nil, false
	}
	var ids []int64
	if err := json.Unmarshal([]byte(enc), &ids); err != nil || len(ids) == 0 {
		return nil, false
	}
	return ids, true
}

// applyRecalledQuickFilters turns on whichever of this board's own quick
// filters were remembered, and reports whether it turned on any at all — an
// id this board's live list has never heard of, from a filter deleted since
// or from a different board entirely, is simply not among them rather than a
// filter this program invents a fifth one to represent.
func (m *Model) applyRecalledQuickFilters() bool {
	ids, ok := m.recallQuickFilterIDs()
	if !ok || len(m.quickFilters) == 0 {
		return false
	}
	changed := false
	for _, qf := range m.quickFilters {
		if !slices.Contains(ids, qf.ID) {
			continue
		}
		if m.qfOn == nil {
			m.qfOn = make(map[int64]bool, len(m.quickFilters))
		}
		m.qfOn[qf.ID] = true
		changed = true
	}
	return changed
}

// rememberQuickFilters keeps which quick filters are toggled on right now, in
// the board's own display order, so the next session opens with the same
// cards this one was narrowed to instead of the board's full column.
func (m *Model) rememberQuickFilters() {
	ids := make([]int64, 0, len(m.qfOn))
	for _, qf := range m.quickFilters {
		if m.qfOn[qf.ID] {
			ids = append(ids, qf.ID)
		}
	}
	if len(ids) == 0 {
		kernel.Keep(m.deps, ViewID, quickFiltersMemoryKey, "")
		return
	}
	enc, err := json.Marshal(ids)
	if err != nil {
		return
	}
	kernel.Keep(m.deps, ViewID, quickFiltersMemoryKey, string(enc))
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
	m.rememberQuickFilters()
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
