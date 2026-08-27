package board

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// MoveIssueMsg asks the board to take the card under its cursor off the board,
// which is where both the key and the pointer drag start. It is exported so the
// palette reaches the gesture the key does rather than a second implementation
// of it: the palette knows which command was run and never which issue is on
// screen.
type MoveIssueMsg struct{}

// NextBoardMsg asks the board to draw the next of the boards this project has.
type NextBoardMsg struct{}

// The board takes the footer slot docs/UX.md allocates it, and declares the
// capability it cannot exist without: a token that may not read boards gets the
// probe's own sentence instead of a view that fails on every read.
func init() {
	const slot = 2
	keys := defaultKeys()
	kernel.RegisterView(kernel.ViewSpec{
		ID:       ViewID,
		Title:    "Board",
		Slot:     slot,
		Requires: jira.CapBoards,
		New:      New,
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "board.open",
		Title: "Board",
		Group: "Go to",
		Keys:  []string{kernel.SlotGesture(slot)},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:       "board.move-issue",
		Title:    "Move this issue to another column",
		Group:    "Board",
		Requires: jira.CapBoards,
		Keys:     []string{keys.Pick.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(MoveIssueMsg{}))
		},
	})
	kernel.RegisterCommand(kernel.Command{
		ID:       "board.next",
		Title:    "Show another board of this project",
		Group:    "Board",
		Requires: jira.CapBoards,
		Keys:     []string{keys.Board.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(NextBoardMsg{}))
		},
	})
}
