package widget

import (
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

func numbered(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, "line "+strconv.Itoa(i))
	}
	return out
}

func TestWindow_ShowsTheLinesThatFitAndKeepsTheOneThatMatters(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		lines       int
		top, height int
		keep        int
		wantFirst   string
		wantTop     int
		wantLines   int
	}{
		"everything fits, so there is nothing to scroll": {
			lines: 4, height: 10, keep: 3, wantFirst: "line 0", wantTop: 0, wantLines: 4,
		},
		"an offset inside the range is kept": {
			lines: 40, top: 12, height: 6, keep: -1, wantFirst: "line 12", wantTop: 12, wantLines: 6,
		},
		"an offset past the end is pulled back to the last screen": {
			lines: 40, top: 400, height: 6, keep: -1, wantFirst: "line 34", wantTop: 34, wantLines: 6,
		},
		"a negative offset is pulled back to the top": {
			lines: 40, top: -8, height: 6, keep: -1, wantFirst: "line 0", wantTop: 0, wantLines: 6,
		},
		"a line above the window pulls the window up to it": {
			lines: 40, top: 20, height: 6, keep: 3, wantFirst: "line 3", wantTop: 3, wantLines: 6,
		},
		"a line below the window pulls the window down to it": {
			lines: 40, top: 0, height: 6, keep: 9, wantFirst: "line 4", wantTop: 4, wantLines: 6,
		},
		"a line already on screen moves nothing": {
			lines: 40, top: 10, height: 6, keep: 12, wantFirst: "line 10", wantTop: 10, wantLines: 6,
		},
		"a line that does not exist is ignored": {
			lines: 40, top: 10, height: 6, keep: 400, wantFirst: "line 10", wantTop: 10, wantLines: 6,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, at := Window(numbered(tc.lines), tc.top, tc.height, tc.keep)
			if at != tc.wantTop {
				t.Errorf("the window settled at %d, want %d", at, tc.wantTop)
			}
			if len(got) != tc.wantLines {
				t.Fatalf("the window is %d lines, want %d", len(got), tc.wantLines)
			}
			if got[0] != tc.wantFirst {
				t.Errorf("the window opens on %q, want %q", got[0], tc.wantFirst)
			}
		})
	}
}

func TestWindow_ANoHeightPaneDrawsNothing(t *testing.T) {
	t.Parallel()

	got, at := Window(numbered(10), 4, 0, 2)
	if len(got) != 0 || at != 0 {
		t.Errorf("a pane with no height drew %d lines at %d, want none", len(got), at)
	}
}

// The window is a slice of what it was handed, not a copy of it: a pane redrawn
// on every frame must not allocate one.
func TestWindow_SlicesRatherThanCopies(t *testing.T) {
	t.Parallel()

	lines := numbered(40)
	got, _ := Window(lines, 10, 6, -1)
	if len(got) == 0 {
		t.Fatal("the window is empty")
	}
	if unsafe.SliceData(got) != unsafe.SliceData(lines[10:]) {
		t.Error("the window copied the lines it was handed")
	}
	if strings.Join(got, "\n") == "" {
		t.Error("the window holds nothing")
	}
}
