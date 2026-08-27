package release

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// The list takes the footer slot docs/UX.md allocates to releases; the flow
// takes none and registers no view spec at all. It is reached by being pushed
// with a version and the count of what is open on it, and a registry constructor
// has neither — a release screen over no version is one nothing can then answer.
//
// No Requires: there is no capability for versions. A token that cannot read
// them is refused by the site, and the pane says so in the site's own words
// rather than hiding a view over a guess.
func init() {
	const slot = 5
	keys := defaultKeys()
	kernel.RegisterView(kernel.ViewSpec{
		ID:    ViewID,
		Title: "Releases",
		Slot:  slot,
		New:   New,
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterKeys(FlowViewID, defaultFlowKeys().keySet())

	kernel.RegisterCommand(kernel.Command{
		ID:    "releases.open",
		Title: "Releases",
		Group: "Go to",
		Keys:  []string{kernel.SlotGesture(slot)},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	for _, c := range []struct {
		id, title string
		key       kernel.Binding
		msg       tea.Msg
	}{
		{id: "releases.new", title: "Create a version", key: keys.New, msg: NewVersionMsg{}},
		{id: "releases.edit", title: "Edit the version you are on", key: keys.Edit, msg: EditVersionMsg{}},
		{
			id: "releases.archive", title: "Archive or unarchive the version you are on",
			key: keys.Archive, msg: ArchiveMsg{},
		},
		{
			id: "releases.release", title: "Release the version you are on",
			key: keys.Release, msg: ReleaseMsg{},
		},
	} {
		kernel.RegisterCommand(kernel.Command{
			ID:    c.id,
			Title: c.title,
			Group: "Releases",
			Keys:  []string{c.key.Help().Key},
			Run: func(kernel.Deps) tea.Cmd {
				return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(c.msg))
			},
		})
	}
}
