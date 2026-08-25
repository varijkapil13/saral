package list

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func init() {
	const slot = 1
	kernel.RegisterView(kernel.ViewSpec{
		ID:          ViewID,
		Title:       "Issues",
		Slot:        slot,
		RunsQueries: true,
		New:         New,
	})
	kernel.RegisterKeys(ViewID, defaultKeys().keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.open",
		Title: "Issues",
		Group: "Go to",
		Keys:  []string{kernel.SlotGesture(slot)},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issues.save-query",
		Title: "Save this query to a number key",
		Group: "Search",
		Keys:  []string{defaultKeys().Save.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(SaveQueryMsg{}))
		},
	})
	for _, q := range []struct{ id, title, jql string }{
		{"issues.mine", "My issues", "assignee = currentUser() ORDER BY updated DESC"},
		{"issues.reported", "Issues I reported", "reporter = currentUser() ORDER BY created DESC"},
		{"issues.unassigned", "Unassigned issues", "assignee IS EMPTY ORDER BY created DESC"},
	} {
		kernel.RegisterCommand(kernel.Command{
			ID:    q.id,
			Title: q.title,
			Group: "Search",
			Run: func(d kernel.Deps) tea.Cmd {
				return tea.Sequence(
					kernel.Open(ViewID),
					kernel.Broadcast(QueryMsg{JQL: scoped(d.Project, q.jql), Title: q.title}),
				)
			},
		})
	}
}

// scoped narrows a query to the session's project when there is one. The key is
// whatever the session was opened against; nothing about it is written down.
func scoped(project, jql string) string {
	if strings.TrimSpace(project) == "" {
		return jql
	}
	return "project = " + quote(project) + " AND " + jql
}
