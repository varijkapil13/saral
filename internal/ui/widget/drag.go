package widget

import tea "charm.land/bubbletea/v2"

// Drag is the press, move, release gesture: what was grabbed, and how far the
// pointer has come from where it grabbed it.
//
// Nothing binds it yet. No view in this build has two panes, so there is no
// divider to drag and no ratio to persist; the view that grows a second pane
// binds this rather than writing the state machine again. It is here because the
// machine is the part that is easy to get wrong — a release that lands outside
// the element still ends the drag it started, and a move that arrives without a
// press is not one.
type Drag struct {
	id     string
	fromX  int
	fromY  int
	atX    int
	atY    int
	active bool
}

// Start grabs the element name at the press position. An empty name is a press
// on nothing draggable and starts nothing, which is what a view passes when its
// hit test missed.
func (d *Drag) Start(name string, msg tea.MouseMsg) bool {
	if name == "" {
		return false
	}
	at := msg.Mouse()
	d.id, d.active = name, true
	d.fromX, d.fromY = at.X, at.Y
	d.atX, d.atY = at.X, at.Y
	return true
}

// Move follows the pointer and reports how far it has come from the press. It
// reports false while nothing is being dragged, which is most motion messages:
// cell-motion reporting sends them with no button held too.
func (d *Drag) Move(msg tea.MouseMsg) (dx, dy int, ok bool) {
	if !d.active {
		return 0, 0, false
	}
	at := msg.Mouse()
	d.atX, d.atY = at.X, at.Y
	return d.atX - d.fromX, d.atY - d.fromY, true
}

// Release ends the drag wherever the pointer is, inside the element it grabbed
// or far outside it. A divider dragged past the edge of its own zone is still
// the divider being dragged, so the bounds are not checked again here: the
// press decided what is being dragged and nothing since can change it.
func (d *Drag) Release(msg tea.MouseMsg) (dx, dy int, ok bool) {
	if !d.active {
		return 0, 0, false
	}
	dx, dy, _ = d.Move(msg)
	d.Cancel()
	return dx, dy, true
}

// Cancel drops the gesture without applying it, which is what a view does when
// something else takes over — a resize, a view switch, a key.
func (d *Drag) Cancel() {
	d.id, d.active = "", false
	d.fromX, d.fromY, d.atX, d.atY = 0, 0, 0, 0
}

// Active reports whether a drag is under way.
func (d *Drag) Active() bool { return d.active }

// ID is the element the press grabbed, empty when nothing is being dragged.
func (d *Drag) ID() string { return d.id }

// At is where the pointer is now, and is meaningful only while Active.
func (d *Drag) At() (x, y int) { return d.atX, d.atY }
