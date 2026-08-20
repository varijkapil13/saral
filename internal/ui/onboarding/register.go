package onboarding

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// The view claims no footer slot: it is where the program starts when there is
// no profile, and after that it is something reached by name or by the palette,
// not a place anyone navigates to daily.
func init() {
	kernel.RegisterView(kernel.ViewSpec{
		ID:    ViewID,
		Title: "Setup",
		New:   New,
	})

	kernel.RegisterKeys(ViewID, kernel.KeySet{
		Short: []kernel.Binding{
			kernel.Bind([]string{"enter"}, "enter", "continue"),
			kernel.Bind([]string{"shift+tab"}, "shift+tab", "back a step"),
		},
		Full: [][]kernel.Binding{{
			kernel.Bind([]string{"enter", "tab"}, "enter", "continue"),
			kernel.Bind([]string{"shift+tab"}, "shift+tab", "back a step"),
			kernel.Bind([]string{"up", "down"}, "up/down", "choose"),
			kernel.Bind([]string{"ctrl+r"}, "ctrl+r", "try that again"),
		}},
	})

	kernel.RegisterCommand(kernel.Command{
		ID:    "onboarding.run",
		Title: "Set up a Jira profile",
		Group: "Profile",
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
}
