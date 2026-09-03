package sprint

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// The view takes the footer slot docs/UX.md allocates to the sprints, and
// declares the capability it cannot work without: a session whose token cannot
// reach the board API has no sprints to show, so the view is hidden with the
// site's own reason rather than opening onto a refusal.
//
// Every action is registered as a command as well as a key, and each command
// reaches this view before it broadcasts: the palette knows which command was
// run and never which sprint is on screen.
func init() {
	keys := defaultKeys()
	kernel.RegisterView(kernel.ViewSpec{
		ID:       ViewID,
		Title:    "Sprints",
		Slot:     slot,
		Requires: jira.CapBoards,
		New:      New,
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:       "sprints.open",
		Title:    "Sprints",
		Group:    "Go to",
		Kind:     kernel.KindGoTo,
		Requires: jira.CapBoards,
		Keys:     []string{kernel.SlotGesture(slot)},
		Run:      func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	for _, c := range []struct {
		id, title string
		key       kernel.Binding
		msg       tea.Msg
	}{
		{id: "sprints.new", title: "Plan a new sprint", key: keys.New, msg: NewMsg{}},
		{id: "sprints.edit", title: "Edit the sprint you are on", key: keys.Edit, msg: EditMsg{}},
		{id: "sprints.start", title: "Start the sprint you are on", key: keys.Start, msg: StartMsg{}},
		{id: "sprints.complete", title: "Complete the sprint you are on", key: keys.Complete, msg: CompleteMsg{}},
		{id: "sprints.closed", title: "Show or hide the closed sprints", key: keys.Closed, msg: ClosedMsg{}},
	} {
		kernel.RegisterCommand(kernel.Command{
			ID:       c.id,
			Title:    c.title,
			Group:    "Sprints",
			Requires: jira.CapBoards,
			Keys:     shown(c.key),
			Run: func(kernel.Deps) tea.Cmd {
				return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(c.msg))
			},
		})
	}
}

// shown is the key a binding tells the user to press, which is the half of it a
// command carries: the footer shows "e" while the binding also matches enter.
func shown(b kernel.Binding) []string { return []string{b.Help().Key} }
