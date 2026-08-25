package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// EditIssueMsg asks whichever detail pane is open to put up the editor for the
// issue it is showing. It is how the command palette reaches the same gesture
// the key does rather than a second implementation of it: the palette knows
// which command was run and never which issue is on screen.
type EditIssueMsg struct{}

// MoveIssueMsg asks whichever detail pane is open to put up the transition
// picker for the issue it is showing.
type MoveIssueMsg struct{}

// The two panes register their own keys under their own scopes. Neither takes a
// footer slot: both are opened with an issue, and a registry constructor has no
// issue to open them with.
func init() {
	kernel.RegisterKeys(EditViewID, defaultEditKeys().keySet())
	kernel.RegisterKeys(MoveViewID, defaultMoveKeys().keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.edit",
		Title: "Edit this issue",
		Group: "Issue",
		Keys:  []string{editBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(EditIssueMsg{}) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.transition",
		Title: "Change this issue's status",
		Group: "Issue",
		Keys:  []string{moveBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(MoveIssueMsg{}) },
	})
}

// editKey answers the two keys the detail pane hands over, and reports whether
// it took the keypress.
func (m *Model) editKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	switch {
	case kernel.Matches(msg, m.keys.Edit):
		return m.openEdit(), true
	case kernel.Matches(msg, m.keys.Move):
		return m.openMove(), true
	}
	return nil, false
}

// editMsg answers the palette's way into the same two panes.
func (m *Model) editMsg(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case EditIssueMsg:
		return m.openEdit()
	case MoveIssueMsg:
		return m.openMove()
	}
	return nil
}

// openEdit pushes the editor with the issue as the detail pane has it. What it
// can change is decided by what that issue was read with, not by what this pane
// happens to be showing.
func (m *Model) openEdit() tea.Cmd {
	if m.issue.Key == "" {
		return nil
	}
	return kernel.Push(EditViewID, m.issue.Key, NewEdit(m.deps, m.issue))
}

func (m *Model) openMove() tea.Cmd {
	if m.issue.Key == "" {
		return nil
	}
	return kernel.Push(MoveViewID, m.issue.Key, NewMove(m.deps, m.issue))
}
