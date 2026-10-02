package search

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func init() {
	kernel.RegisterView(kernel.ViewSpec{ID: ViewID, Title: "Search", New: NewView})
	kernel.RegisterKeys(ViewID, defaultKeys().keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "search.open",
		Title: "Search issues on the site",
		Group: "Search",
		Kind:  kernel.KindSearch,
		Keys:  []string{kernel.DefaultGlobalKeys().Search.Help().Key},
		Run:   func(d kernel.Deps) tea.Cmd { return Open(d, "") },
	})
}
