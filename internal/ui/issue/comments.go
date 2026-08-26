package issue

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// CommentsMsg asks whichever detail pane is open to put the comment thread for
// the issue it is showing on the whole screen. It is how the command palette
// reaches the same gesture the key does rather than a second implementation of
// it: the palette knows which command was run and never which issue is on
// screen.
type CommentsMsg struct{}

// commentsBinding is the key that gives the thread the whole screen. The sidebar
// already shows it, so this is not "open the comments" — it is the size at which
// one can be written, and the capital says it is the bigger version of what is
// already there.
//
// "comment" and not "comments": one column longer drops the whole hint line in
// an 80-column terminal, the smallest size docs/UX.md supports.
func commentsBinding() kernel.Binding {
	return kernel.Bind([]string{"C"}, "C", "comment")
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

// openComments gives the thread the whole screen, lending the very instance the
// sidebar holds. Pushing a fresh one would read the thread again and land on the
// first comment with an empty editor, so esc would not come back to where the
// reader was; this way the kernel resizes the same model and popping restores
// the box it had.
//
// Lent and not pushed, because this pane keeps it: a kernel that closed it on
// esc would cancel the read the sidebar is still waiting for, and the sidebar
// would then show an empty thread for as long as the issue is open.
func (m *Model) openComments() tea.Cmd {
	if m.issue.Key == "" || m.thread == nil || m.pushed {
		return nil
	}
	m.pushed = true
	return kernel.Lend(comment.ViewID, m.issue.Key, m.thread)
}

// commentAction answers the palette's write, edit and delete commands. They are
// broadcasts, so they reach this pane whenever it is the one on top — and the
// sidebar is no place for an editor, so the thread goes full screen first and is
// handed the message there.
func (m *Model) commentAction(msg tea.Msg) tea.Cmd {
	if m.pushed || m.issue.Key == "" || m.thread == nil {
		return nil
	}
	told := m.tell(msg)
	return tea.Batch(told, m.openComments())
}
