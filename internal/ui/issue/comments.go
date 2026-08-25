package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// CommentsMsg asks whichever detail pane is open to put up the comment thread
// for the issue it is showing. It is how the command palette reaches the same
// gesture the key does rather than a second implementation of it: the palette
// knows which command was run and never which issue is on screen.
type CommentsMsg struct{}

// commentsBinding is the key the detail pane hangs the thread off. It lives here
// with the rest of reaching the thread, and is named in the pane's keymap so the
// footer and the help overlay advertise it.
//
// "comment" and not "comments": one column longer drops the whole hint line in
// an 80-column terminal, the smallest size docs/UX.md supports.
func commentsBinding() kernel.Binding {
	return kernel.Bind([]string{"c"}, "c", "comment")
}

func init() {
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.comments",
		Title: "Comment on this issue",
		Group: "Issue",
		Keys:  []string{commentsBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(CommentsMsg{}) },
	})
}

// openComments pushes the thread for the issue this pane is showing, so that esc
// comes back to it. The read-only thread drawn inside the pane is a different
// thing: that one is the first paint, this one can be written in.
func (m *Model) openComments() tea.Cmd {
	if m.issue.Key == "" {
		return nil
	}
	return comment.Push(m.deps, m.issue.Key)
}
