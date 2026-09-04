package plan

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// SourcesMsg opens or closes the plan under the cursor. It is a broadcast
// because the palette knows which command was run and never which plan is on
// screen.
type SourcesMsg struct{}

// The view declares no Requires. CapPlans being absent is what this view is
// for: it draws the plans the profile defines and says why they are not the
// site's, and a view hidden on the capability would take that answer away from
// exactly the sessions that need it.
func init() {
	const slot = 7
	keys := defaultKeys()
	kernel.RegisterView(kernel.ViewSpec{
		ID:    ViewID,
		Title: "Plans",
		Slot:  slot,
		New:   func(d kernel.Deps) kernel.View { return New(d) },
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "plans.open",
		Title: "Plans",
		Group: "Go to",
		Kind:  kernel.KindGoTo,
		Keys:  []string{kernel.SlotGesture(slot)},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "plans.sources",
		Title: "Show what a plan is made of",
		Group: "Plans",
		Keys:  []string{keys.Open.Help().Key},
		Run: func(kernel.Deps) tea.Cmd {
			return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(SourcesMsg{}))
		},
	})
}
