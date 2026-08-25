package list

import (
	tea "charm.land/bubbletea/v2"

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
		New:         New,
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.open",
		Title: "Issues",
		Group: "Go to",
		Keys:  []string{kernel.SlotGesture(slot)},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.edit-query",
		Title: "Edit this search",
		Group: "Search",
		Keys:  []string{keys.Edit.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(EditQueryMsg{}))
		},
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.save-query",
		Title: "Save this query to a number key",
		Group: "Search",
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
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(ClearFilterMsg{}))
		},
	})
	// The cells a click narrows the rows by, reachable without a pointer. There
	// is no key for them: every letter left is one somebody types into a filter.
	for _, f := range []struct {
		id, title string
		kind      Facet
	}{
		{"issues.only-status", "Show only rows with this row's status", FacetStatus},
		{"issues.only-type", "Show only rows with this row's type", FacetType},
		{"issues.only-assignee", "Show only rows with this row's assignee", FacetAssignee},
		{"issues.show-all", "Show every row again", FacetNone},
	} {
		kind := f.kind
		kernel.RegisterCommand(kernel.Command{
			ID:    f.id,
			Title: f.title,
			Group: "Search",
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
