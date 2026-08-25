package comment

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// The thread claims no footer slot: docs/UX.md keeps the digits for the views a
// session lives in, and this one is about whichever issue is being read. It is
// reached by being pushed with an issue, by name, and from the palette.
func init() {
	kernel.RegisterView(kernel.ViewSpec{
		ID:    ViewID,
		Title: "Comments",
		New:   New,
	})
	kernel.RegisterKeys(ViewID, defaultKeys().keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "comments.open",
		Title: "Comments",
		Group: "Go to",
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	keys := defaultKeys()
	for _, c := range []struct {
		id, title string
		keys      []string
		msg       tea.Msg
	}{
		{id: "comments.write", title: "Write a comment", keys: shown(keys.Write), msg: WriteMsg{}},
		{id: "comments.edit", title: "Edit the comment you are on", keys: shown(keys.Edit), msg: EditMsg{}},
		{id: "comments.delete", title: "Delete the comment you are on", keys: shown(keys.Delete), msg: DeleteMsg{}},
	} {
		kernel.RegisterCommand(kernel.Command{
			ID:    c.id,
			Title: c.title,
			Group: "Comments",
			Keys:  c.keys,
			Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(c.msg) },
		})
	}
}

// shown is the key a binding tells the user to press, which is the half of it a
// command carries: the footer shows "a" while the binding also matches "c".
func shown(b kernel.Binding) []string { return []string{b.Help().Key} }
