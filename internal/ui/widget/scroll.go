package widget

// Window is the part of a rendered pane that fits: height lines starting at
// top, moved as little as it takes to keep the line at keep on screen. It
// returns the offset it settled on so the caller can store it back, which is
// what makes a wheel and a cursor agree about where the pane is.
//
// It slices rather than copies, so drawing a windowed pane costs nothing per
// frame. A keep of -1 means no line has to stay visible.
func Window(lines []string, top, height, keep int) (visible []string, at int) {
	if height <= 0 {
		return nil, 0
	}
	if len(lines) <= height {
		return lines, 0
	}
	top = min(max(top, 0), len(lines)-height)
	if keep >= 0 && keep < len(lines) {
		switch {
		case keep < top:
			top = keep
		case keep >= top+height:
			top = keep - height + 1
		}
	}
	return lines[top : top+height], top
}

// WheelStep is how many lines one notch of the wheel moves. Three is what a
// terminal emulator sends a pager, and what every other pane in this program
// already scrolls by.
const WheelStep = 3
