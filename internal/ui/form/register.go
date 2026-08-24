package form

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// The form claims no footer slot: docs/UX.md leaves the nine digits to the
// views a user navigates to daily, and a create screen is something pushed over
// whatever they were looking at.
func init() {
	kernel.RegisterView(kernel.ViewSpec{
		ID:    ViewID,
		Title: "New issue",
		New:   New,
	})
	kernel.RegisterKeys(ViewID, defaultKeys().keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.create",
		Title: "Create an issue",
		Group: "Issues",
		Run: func(d kernel.Deps) tea.Cmd {
			return kernel.Push(ViewID, "New issue", New(d))
		},
	})
}
