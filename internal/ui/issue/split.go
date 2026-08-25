package issue

import (
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// dividerZone is the column between the description and the sidebar, marked so
// that a press on it resolves the way every other press here does.
const dividerZone = "divider"

// splitStep is how far one stroke moves the divider. Four crosses the whole
// range a 120-column terminal allows in eight strokes and still lands on a
// column somebody meant.
const splitStep = 4

// split is the sidebar's share of the pane, out of config.SplitScale. Zero is a
// reader who has not chosen one, and the width decides.
//
// A share rather than a column count, because the same terminal is not always
// the same width: a choice made in a full-screen window has to mean something in
// half of one.
type split int

// cells is the sidebar width this share asks for at this pane width, before the
// floors are applied.
func (s split) cells(w int) int {
	return (int(s)*w + config.SplitScale/2) / config.SplitScale
}

// shareOf is the split a sidebar of this width is, at this pane width. It is the
// inverse of cells over every width a terminal reaches, which is what lets a
// chosen split be stored as a share and come back as the same column.
func shareOf(w, sideW int) split {
	if w <= 0 {
		return 0
	}
	return split((sideW*config.SplitScale + w/2) / w)
}

// WidenSidebarMsg asks whichever detail pane is open to move the divider left,
// giving the fields and the thread the room. It is how the command palette
// reaches the same gesture the key does rather than a second implementation of
// it: the palette knows which command was run and never which pane is on screen.
type WidenSidebarMsg struct{}

// WidenDescriptionMsg asks the open detail pane to move the divider right.
type WidenDescriptionMsg struct{}

// ResetSplitMsg asks the open detail pane to put the divider back where its
// width alone would have put it.
type ResetSplitMsg struct{}

func init() {
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.split.sidebar",
		Title: "Widen the fields and the thread",
		Group: "Issue",
		Keys:  []string{sidebarBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(WidenSidebarMsg{}) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.split.description",
		Title: "Widen the description",
		Group: "Issue",
		Keys:  []string{descriptionBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(WidenDescriptionMsg{}) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "issue.split.reset",
		Title: "Put the split back where the width chooses",
		Group: "Issue",
		Keys:  []string{resetBinding().Help().Key},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Broadcast(ResetSplitMsg{}) },
	})
}

// splitMsg answers the palette's way to the three gestures the keys reach.
func (m *Model) splitMsg(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case WidenSidebarMsg:
		return m.moveDivider(-splitStep)
	case WidenDescriptionMsg:
		return m.moveDivider(splitStep)
	case ResetSplitMsg:
		return m.resetSplit()
	}
	return nil
}

// canSplit reports whether this frame has a boundary on it at all. It reads the
// width rather than the last layout, so a gesture that arrives before the first
// frame is answered by the size the pane actually has.
func (m *Model) canSplit() bool { return m.width >= wideAt }

// moveDivider takes the boundary by cells, positively to the right, which is the
// direction that gives the description the room. It answers with what has to be
// said: a refusal where there is no split to move or no room left to move it in,
// and otherwise the write that remembers where it now is.
func (m *Model) moveDivider(by int) tea.Cmd {
	if !m.canSplit() {
		return m.noSplitHere()
	}
	m.cancelDrag()
	was := sideWidth(m.width, m.split)
	if m.setSide(was-by) == was {
		return kernel.Warn(atFloor(by))
	}
	return m.keepSplit()
}

// resetSplit gives the divider back to the width, which is where it starts and
// where a terminal that changes size wants it.
func (m *Model) resetSplit() tea.Cmd {
	if !m.canSplit() {
		return m.noSplitHere()
	}
	m.cancelDrag()
	if m.split == 0 {
		return kernel.Warn("the split is already the one this width chooses")
	}
	m.split = 0
	return m.keepSplit()
}

// noSplitHere is what the three gestures say below the breakpoint, where the
// regions take the screen in turn and there is no boundary on it to move.
func (m *Model) noSplitHere() tea.Cmd {
	return kernel.Warn("the regions take the screen in turn below " + strconv.Itoa(wideAt) +
		" columns, so there is no split to move; tab brings up the next one")
}

// atFloor names the floor a move ran into, in what the floor is for rather than
// in cells: the numbers are measurements of what content needs and mean nothing
// to somebody holding a key down.
func atFloor(toward int) string {
	if toward > 0 {
		return "the fields are as narrow as a label and its value fit in"
	}
	return "the description is as narrow as a paragraph reads in"
}

// setSide puts the sidebar at a width, clamped to the floors, and answers with
// the width it settled on so that a caller can tell a move from a refusal.
func (m *Model) setSide(sideW int) int {
	m.split = shareOf(m.width, clampSide(m.width, sideW))
	return sideWidth(m.width, m.split)
}

// grabDivider starts a drag when a press landed on the boundary. The press is
// what decides what is being dragged; nothing after it can change that, which is
// why a release outside the column still lands on this divider.
func (m *Model) grabDivider(msg tea.MouseClickMsg) bool {
	if !m.canSplit() || msg.Button != tea.MouseLeft || !m.zones.Hit(dividerZone, msg) {
		return false
	}
	m.dragFrom, m.dragSide = m.split, sideWidth(m.width, m.split)
	return m.drag.Start(dividerZone, msg)
}

// dragDivider follows the pointer while the boundary is held.
func (m *Model) dragDivider(msg tea.MouseMsg) {
	if dx, _, ok := m.drag.Move(msg); ok {
		m.dragTo(dx)
	}
}

// dropDivider ends the gesture wherever the pointer is and remembers the split
// it left behind. A press and release that never moved is a click on a
// one-column target rather than a choice, and is not written down.
func (m *Model) dropDivider(msg tea.MouseMsg) tea.Cmd {
	dx, _, ok := m.drag.Release(msg)
	if !ok {
		return nil
	}
	m.dragTo(dx)
	if dx == 0 {
		return nil
	}
	return m.keepSplit()
}

// dragTo puts the boundary where a delta from the press asks for it. A delta of
// zero puts the split back rather than recomputing it, because recomputing turns
// whatever the width was choosing into a share pinned at that width — the same
// frame today and a different one in a window of another size.
func (m *Model) dragTo(dx int) {
	if dx == 0 {
		m.split = m.dragFrom
		return
	}
	m.setSide(m.dragSide - dx)
}

// cancelDrag drops a gesture in progress and puts the split back where the press
// found it, which is what a resize, a key or a view switch does to one.
func (m *Model) cancelDrag() {
	if !m.drag.Active() {
		return
	}
	m.drag.Cancel()
	m.split = m.dragFrom
}

// keepSplit writes the share to the cache directory, off the event loop. The
// split already works when this runs, so a failure is reported rather than
// undone — and said once, because a warning on every stroke would bury whatever
// came before it.
func (m *Model) keepSplit() tea.Cmd {
	if m.splitFailed {
		return nil
	}
	share := int(m.split)
	return func() tea.Msg {
		if err := config.SaveSplit(ViewID, share); err != nil {
			return splitFailedMsg{err: err}
		}
		return nil
	}
}

// splitFailedMsg reports that the split on screen is not the one the next
// session will open with.
type splitFailedMsg struct{ err error }
