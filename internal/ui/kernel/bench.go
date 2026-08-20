package kernel

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// FirstPaint builds the root model at a given size and renders one frame,
// reporting how long that took and what was drawn.
//
// It exists because the two start-up budgets in docs/PERFORMANCE.md are
// otherwise unmeasurable: a human with a stopwatch cannot see 60 ms, and
// hyperfine cannot time something that never exits. Nothing here touches the
// network — the frame is whatever the views can draw from what they already
// have, which is exactly the thing being budgeted.
func FirstPaint(d Deps, w, h int, opts ...Option) (time.Duration, string, error) {
	start := time.Now()
	m, err := New(d, append([]Option{WithSize(w, h), WithMouse(false)}, opts...)...)
	if err != nil {
		return 0, "", err
	}
	model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	frame := model.(Model).Frame()
	return time.Since(start), frame, nil
}
