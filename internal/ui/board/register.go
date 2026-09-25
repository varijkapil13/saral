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

// NextSprintMsg asks the board to draw the next of the sprints it is running.
type NextSprintMsg struct{}

// RankMsg ranks the card under the cursor within its column, ShiftMsg lands it
// in the column beside it, MineMsg toggles only-my-issues and FindMsg opens the
// search: each is exported so the palette reaches the gesture its key does.
type RankMsg struct{ Where rankWhere }

// ShiftMsg is described with RankMsg.
type ShiftMsg struct{ By int }

// MineMsg is described with RankMsg.
type MineMsg struct{}

// FindMsg is described with RankMsg.
type FindMsg struct{}

// ClearFilterMsg drops every term the filter picker put in force. It is
// exported so the palette reaches the gesture ctrl+g does rather than a second
// implementation of it.
type ClearFilterMsg struct{}

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
		Filters:  true,
		New:      New,
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "board.open",
		Title: "Board",
		Group: "Go to",
		Kind:  kernel.KindGoTo,
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
			return kernel.OpenThen(ViewID, MoveIssueMsg{})
		},
	})
	kernel.RegisterCommand(kernel.Command{
		ID:       "board.next",
		Title:    "Show another board of this project",
		Group:    "Board",
		Requires: jira.CapBoards,
		Keys:     []string{keys.Board.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return kernel.OpenThen(ViewID, NextBoardMsg{})
		},
	})
	kernel.RegisterCommand(kernel.Command{
		ID:       "board.next-sprint",
		Title:    "Show another sprint running on this board",
		Group:    "Board",
		Requires: jira.CapBoards,
		Keys:     []string{keys.Sprint.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return kernel.OpenThen(ViewID, NextSprintMsg{})
		},
	})
	for _, c := range []struct {
		id, title string
		key       kernel.Binding
		msg       tea.Msg
	}{
		{"board.rank-up", "Rank this card up", keys.RankUp, RankMsg{Where: rankUp}},
		{"board.rank-down", "Rank this card down", keys.RankDown, RankMsg{Where: rankDown}},
		{"board.rank-top", "Rank this card first in its column", keys.RankTop, RankMsg{Where: rankTop}},
		{"board.rank-bottom", "Rank this card last in its column", keys.RankBottom, RankMsg{Where: rankBottom}},
		{"board.shift-left", "Move this card to the previous column", keys.ShiftLeft, ShiftMsg{By: -1}},
		{"board.shift-right", "Move this card to the next column", keys.ShiftRight, ShiftMsg{By: 1}},
	} {
		kernel.RegisterCommand(kernel.Command{
			ID:       c.id,
			Title:    c.title,
			Group:    "Board",
			Requires: jira.CapBoards,
			Keys:     []string{c.key.Help().Key},
			Run:      func(kernel.Deps) tea.Cmd { return kernel.OpenThen(ViewID, c.msg) },
		})
	}
	kernel.RegisterCommand(kernel.Command{
		ID:       "board.mine",
		Title:    "Show only my issues on the board",
		Group:    "Search",
		Kind:     kernel.KindSearch,
		Requires: jira.CapBoards,
		Keys:     []string{keys.Mine.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return kernel.OpenThen(ViewID, MineMsg{})
		},
	})
	kernel.RegisterCommand(kernel.Command{
		ID:       "board.find",
		Title:    "Find a card on the board",
		Group:    "Search",
		Kind:     kernel.KindSearch,
		Requires: jira.CapBoards,
		Keys:     []string{keys.Find.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return kernel.OpenThen(ViewID, FindMsg{})
		},
	})
	// No Keys: kernel.KeysFor holds a view's resting keys, and the stroke that
	// clears a filter is shown only by the state that has one to clear.
	kernel.RegisterCommand(kernel.Command{
		ID:       "board.clear-filter",
		Title:    "Clear the filter on this board",
		Group:    "Search",
		Kind:     kernel.KindSearch,
		Requires: jira.CapBoards,
		Run: func(kernel.Deps) tea.Cmd {
			return kernel.OpenThen(ViewID, ClearFilterMsg{})
		},
	})
}
