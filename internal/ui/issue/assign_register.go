package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// AssignMsg is the palette's way into @: opening the assignee picker.
type AssignMsg struct{}

// AssignSelfMsg is the palette's "Assign to me".
type AssignSelfMsg struct{}

// UnassignMsg is the palette's "Unassign". All three are broadcasts, because
// the palette never knows which issue is on screen.
type UnassignMsg struct{}

func init() {
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.assign",
		Title: "Assign to…",
		Group: "Issue",
		Keys:  []string{assignBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(AssignMsg{}) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.assignSelf",
		Title: "Assign to me",
		Group: "Issue",
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(AssignSelfMsg{}) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.unassign",
		Title: "Unassign",
		Group: "Issue",
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(UnassignMsg{}) },
	})
}

// assignMsg answers the three assignee broadcasts.
func (m *Model) assignMsg(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case AssignMsg:
		return m.openAssigneePicker()
	case AssignSelfMsg:
		return m.assignToMe()
	case UnassignMsg:
		return m.unassign()
	}
	return nil
}
