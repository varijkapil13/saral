package issue

// region is one of the three boxes this pane draws: the description, the fields
// beside it and the thread under those. Which one holds the keyboard is what a
// motion moves, and below ninety columns there is room for one at a time.
type region int

// The regions, in the order tab walks them.
const (
	regionDesc region = iota
	regionDetails
	regionComments
	regionCount
)

const (
	// wideAt is the width at which the sidebar sits beside the description
	// rather than taking its turn in front of it.
	wideAt = 90

	// sideMin and sideMax bound the sidebar the width alone chooses. Thirty-five
	// is labelWidth plus a value somebody can read; past forty-five a column of
	// short values is spending width the description wants. A reader who asks for
	// more than forty-five gets it: sideMax is where the arithmetic stops, not
	// where the sidebar does.
	sideMin = 35
	sideMax = 45

	// descMin is the description's floor, gutter included: fifty-three cells of
	// prose. Below that the same paragraph loses about two words a line, which is
	// what layout_test measures rather than assumes.
	descMin = 54

	// gutter is every region's leftmost column: the focus rail and the
	// scrollbar in one, which costs a column instead of a title bar's whole row.
	gutter = 1

	// divider is the blank column between the description and the sidebar.
	divider = 1

	// minThread is the smallest box the thread is worth drawing in: its own
	// identity line, the rule under it, and a line of somebody's words.
	minThread = 5

	// panStep is how far one stroke moves a region sideways. Eight is a Go
	// indent, which is what a reader panning a code block is chasing.
	panStep = 8
)

// step is how far a motion moves the region it is aimed at.
type step int

// The motions every region answers to.
const (
	stepUp step = iota
	stepDown
	stepPageUp
	stepPageDown
	stepHalfUp
	stepHalfDown
	stepTop
	stepBottom
	stepCount
)

// next is the region tab reaches from this one.
func (r region) next(by int) region {
	return region((int(r) + by + int(regionCount)) % int(regionCount))
}

// box is one region's rectangle, gutter included.
type box struct{ x, y, w, h int }

// content is how many cells are left once the gutter has its column.
func (b box) content() int { return max(b.w-gutter, 1) }

// drawn reports whether the box has room to be drawn at all.
func (b box) drawn() bool { return b.w > gutter && b.h > 0 }

// layout is where this frame's regions go, and which of them the frame shows.
type layout struct {
	wide  bool
	paneH int
	focus region
	boxes [regionCount]box
}

// sideWidth is the sidebar's width in the wide mode: the share the reader chose
// where there is one, and a third of the pane where there is not, held between
// the two floors either way. It is a function of the pane's width and that share
// alone, so that the fields can be rendered before the layout that places them.
func sideWidth(w int, s split) int {
	if s <= 0 {
		return clampSide(w, min(max(w/3, sideMin), sideMax))
	}
	return clampSide(w, s.cells(w))
}

// clampSide keeps the sidebar wide enough for a label and a value, and the
// description wide enough to read a paragraph in. At ninety columns the two
// floors meet, so the split there has exactly one legal value.
func clampSide(w, sideW int) int {
	return min(max(sideW, sideMin), max(w-divider-descMin, sideMin))
}

// newLayout places the regions.
//
// detailsNeed is how many lines the fields want, and it decides how much of the
// sidebar is left for the thread: the fields take what they need, and the thread
// keeps a third of the sidebar or minThread, whichever is larger, so that a long
// field list scrolls rather than squeezing the conversation off the screen.
//
// In the narrow mode every region gets the same full-width box even though only
// the focused one is drawn. Sizing the thread to a box that appears and vanishes
// with the focus would reflow its whole content on every tab.
func newLayout(w, h, detailsNeed int, focus region, s split) layout {
	l := layout{wide: w >= wideAt, paneH: max(h-headerHeight, 0), focus: focus}
	if !l.wide {
		full := box{y: headerHeight, w: max(w, gutter+1), h: l.paneH}
		for r := range regionCount {
			l.boxes[r] = full
		}
		return l
	}
	sideW := sideWidth(w, s)
	descW := max(w-sideW-divider, gutter+1)
	floor := max(l.paneH/3, minThread)
	detailsH := min(max(detailsNeed, 1), max(l.paneH-floor, 1))
	l.boxes[regionDesc] = box{y: headerHeight, w: descW, h: l.paneH}
	l.boxes[regionDetails] = box{x: descW + divider, y: headerHeight, w: sideW, h: min(detailsH, l.paneH)}
	l.boxes[regionComments] = box{
		x: descW + divider, y: headerHeight + detailsH,
		w: sideW, h: max(l.paneH-detailsH, 0),
	}
	return l
}

// shows reports whether this frame draws a region. Every region is drawn in the
// wide mode; in the narrow one only the focused region is on screen, which is
// what tab moves.
func (l layout) shows(r region) bool { return l.wide || r == l.focus }

// scroll moves one region's first visible line, clamped to what there is to
// see. It answers with the offset it settled on so that a wheel and a key
// cannot disagree about where a region is.
func scroll(at step, top, total, h int) int {
	page := max(h, 1)
	switch at {
	case stepUp:
		top--
	case stepDown:
		top++
	case stepPageUp:
		top -= page
	case stepPageDown:
		top += page
	case stepHalfUp:
		top -= max(page/2, 1)
	case stepHalfDown:
		top += max(page/2, 1)
	case stepTop:
		top = 0
	case stepBottom:
		top = total
	case stepCount:
	}
	return min(max(top, 0), max(total-h, 0))
}
