package list

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func init() {
	const slot = 1
	keys := defaultKeys()
	kernel.RegisterView(kernel.ViewSpec{
		ID:          ViewID,
		Title:       "Issues",
		Slot:        slot,
		RunsQueries: true,
		Filters:     true,
		New:         New,
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.open",
		Title: "Issues",
		Group: "Go to",
		Kind:  kernel.KindGoTo,
		Keys:  []string{kernel.SlotGesture(slot)},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.filter-by",
		Title: "Filter these issues by a person, a status or a label",
		Group: "Search",
		Kind:  kernel.KindSearch,
		Keys:  []string{keys.FilterBy.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(OpenFilterMsg{}))
		},
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.edit-query",
		Title: "Edit this search",
		Group: "Search",
		Kind:  kernel.KindSearch,
		Keys:  []string{keys.Edit.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(EditQueryMsg{}))
		},
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.save-query",
		Title: "Save this query to a number key",
		Group: "Search",
		Kind:  kernel.KindSearch,
		Keys:  []string{keys.Save.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(SaveQueryMsg{}))
		},
	})
	// No Keys: kernel.KeysFor holds a view's resting keys, and the stroke that
	// clears a filter is shown only by the state that has one to clear.
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.clear-filter",
		Title: "Clear the filter on these rows",
		Group: "Search",
		Kind:  kernel.KindSearch,
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(ClearFilterMsg{}))
		},
	})
	// The cells a click narrows by, reachable without a pointer. There is no key
	// for them: f opens the picker, which reaches every value rather than only
	// the ones a loaded row happens to carry.
	for _, f := range []struct {
		id, title string
		kind      filter.Facet
	}{
		{"issues.only-status", "Filter by this row's status", filter.FacetStatus},
		{"issues.only-type", "Filter by this row's type", filter.FacetType},
		{"issues.only-assignee", "Filter by this row's assignee", filter.FacetAssignee},
		{"issues.show-all", "Drop every filter on these issues", filter.FacetNone},
	} {
		kind := f.kind
		kernel.RegisterCommand(kernel.Command{
			ID:    f.id,
			Title: f.title,
			Group: "Search",
			Kind:  kernel.KindSearch,
			Run: func(kernel.Deps) tea.Cmd {
				return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(FacetMsg{Kind: kind}))
			},
		})
	}
	// The searches themselves. Each is composed against the project the session
	// is on at the moment it runs, and titled after what it is about to show.
	bound := map[string][]string{everyIssue.id: {keys.All.Help().Key}}
	for _, s := range searches {
		kernel.RegisterCommand(kernel.Command{
			ID:    s.id,
			Title: s.palette(),
			Group: "Search",
			Kind:  kernel.KindSearch,
			Keys:  bound[s.id],
			Run: func(d kernel.Deps) tea.Cmd {
				jql, title := s.at(d.Project)
				return tea.Sequence(
					kernel.Open(ViewID),
					kernel.Broadcast(QueryMsg{JQL: jql, Title: title}),
				)
			},
		})
	}
}
