package attach

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// The pane registers its keys and its actions but not a view spec: it is reached
// by being pushed from the view that holds an issue, and a registry constructor
// has no issue to read the files of. A pane offering to open on no issue is a
// dead end nothing can then satisfy.
//
// The key and the palette entry that open it belong to that view too, for the
// same reason: it is the one that knows which issue is on screen.
func init() {
	kernel.RegisterKeys(ViewID, defaultKeys().keySet())
	keys := defaultKeys()
	for _, c := range []struct {
		id, title string
		action    kernel.Action
		requires  jira.CapabilityKey
		keys      []string
		msg       tea.Msg
	}{
		{
			id: "attachments.show", action: kernel.ActOpen, title: "Show the file you are on",
			keys: shown(keys.Show), msg: ShowMsg{},
		},
		{
			id: "attachments.open", action: kernel.ActBrowser, title: "Open the file you are on outside the terminal",
			keys: shown(keys.Open), msg: OpenOutsideMsg{},
		},
		{
			id: "attachments.upload", action: kernel.ActAdd, title: "Attach a file to this issue",
			requires: jira.CapAttachments, keys: shown(keys.Upload), msg: UploadMsg{},
		},
		{
			id: "attachments.delete", action: kernel.ActDelete, title: "Delete the file you are on",
			requires: jira.CapAttachments, keys: shown(keys.Delete), msg: DeleteMsg{},
		},
	} {
		kernel.RegisterCommand(kernel.Command{
			ID:       c.id,
			Title:    c.title,
			Action:   c.action,
			Group:    "Attachments",
			Requires: c.requires,
			Keys:     c.keys,
			Run:      func(kernel.Deps) tea.Cmd { return kernel.Broadcast(c.msg) },
		})
	}
}

// shown is the key a binding tells the user to press, which is the half of it a
// command carries: the footer shows "ctrl+s" while the binding also matches
// enter.
func shown(b kernel.Binding) []string { return []string{b.Help().Key} }
