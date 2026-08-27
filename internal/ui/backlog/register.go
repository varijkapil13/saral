package backlog

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// The backlog takes the third footer slot, which docs/UX.md allocates to it, and
// declares the boards capability: a project with no board has no backlog, and a
// view that is hidden with the probe's own reason beats one that opens onto an
// explanation.
//
// Changing which board is on screen has no key of its own. It is the pointer and
// the palette: a project usually has one board, and a key that does nothing on
// those is worse than no key at all.
func init() {
	const slot = 3
	keys := defaultKeys()
	kernel.RegisterView(kernel.ViewSpec{
		ID:       ViewID,
		Title:    "Backlog",
		Slot:     slot,
		Requires: jira.CapBoards,
		New:      New,
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "backlog.open",
		Title: "Backlog",
		Group: "Go to",
		Keys:  []string{kernel.SlotGesture(slot)},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:       "backlog.move",
		Title:    "Move the picked issues to a sprint or the backlog",
		Group:    "Backlog",
		Requires: jira.CapBoards,
		Keys:     []string{keys.Move.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(MoveMsg{}))
		},
	})
	kernel.RegisterCommand(kernel.Command{
		ID:       "backlog.next-board",
		Title:    "Show the next board on this project",
		Group:    "Backlog",
		Requires: jira.CapBoards,
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(NextBoardMsg{}))
		},
	})
}
