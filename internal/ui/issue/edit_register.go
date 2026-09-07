package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// MoveIssueMsg asks whichever detail pane is open to put up the transition
// picker for the issue it is showing. It is how the command palette reaches
// the same gesture the key does rather than a second implementation of it: the
// palette knows which command was run and never which issue is on screen.
type MoveIssueMsg struct{}

// The transition picker registers its own keys under its own scope. It is not
// a footer slot: it is opened with an issue, and a registry constructor has no
// issue to open it with.
func init() {
	kernel.RegisterKeys(MoveViewID, defaultMoveKeys().keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.transition",
		Title: "Change this issue's status",
		Group: "Issue",
		Keys:  []string{moveBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(MoveIssueMsg{}) },
	})
}

// moveKey answers the key the detail pane hands over to open the status
// picker, and reports whether it took the keypress.
func (m *Model) moveKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if kernel.Matches(msg, m.keys.Move) {
		return m.openStatusPicker(), true
	}
	return nil, false
}

// moveMsg answers the palette's way into the same picker. MoveIssueMsg and
// MoveViewID are unchanged: internal/ui/board still pushes moveModel directly
// for a card whose drag needs a screen the board itself has no sidebar to
// draw inline, and this broadcast is the detail pane's own route to the same
// gesture drawn in place instead.
func (m *Model) moveMsg(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(MoveIssueMsg); ok {
		return m.openStatusPicker()
	}
	return nil
}
