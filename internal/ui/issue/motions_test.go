package issue

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// The pager this pane used to scroll was the bubbles widget, and f, b, u and d
// worked because the widget bound them. The window is the pane's own now, so
// every stroke it advertises has to be one it answers itself — the footer and the
// ? overlay must not lie either way.
func TestMotions_EveryAdvertisedStrokeMovesSomething(t *testing.T) {
	t.Parallel()

	down := []string{"j", "down", "f", "pgdown", "space", "d", "ctrl+d", "G", "end"}
	up := []string{"k", "up", "b", "pgup", "u", "ctrl+u", "home"}

	for _, stroke := range down {
		dr := motionPane(t)
		before := dr.m.tops[regionDesc]
		dr.key(stroke)
		if dr.m.tops[regionDesc] <= before {
			t.Errorf("%q left the description at line %d", stroke, dr.m.tops[regionDesc])
		}
	}

	for _, stroke := range up {
		dr := motionPane(t)
		dr.key("G")
		before := dr.m.tops[regionDesc]
		if before == 0 {
			t.Fatal("G reached line 0, so nothing here can move up")
		}
		dr.key(stroke)
		if dr.m.tops[regionDesc] >= before {
			t.Errorf("%q left the description at line %d of %d", stroke, dr.m.tops[regionDesc], before)
		}
	}

	for _, stroke := range []string{"l", "right"} {
		dr := motionPane(t)
		dr.key(stroke)
		if dr.m.pans[regionDesc] == 0 {
			t.Errorf("%q panned nothing", stroke)
		}
		for _, back := range []string{"h", "left"} {
			at := dr.m.pans[regionDesc]
			dr.key(back)
			if dr.m.pans[regionDesc] >= at {
				t.Errorf("%q did not pan back from cell %d", back, at)
			}
			dr.key(stroke)
		}
	}
}

// motionPane has more lines than any box and a line wider than any box, so every
// motion has somewhere to go.
func motionPane(t *testing.T) *driver {
	t.Helper()

	f := newFake(8)
	full := readIssue(t, f, "PROJ-3")
	long := longDoc(80)
	block := adf.NewNode("codeBlock", adf.NewText(
		"func (c *Client) Export(ctx context.Context, tenant string) (Report, error) { return nil, nil }"))
	block.Attrs = adf.Attrs{"language": "go"}
	full.Description = adf.NewDoc(append([]adf.Node{block}, long.Content...)...)

	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-3"), 120, 24)
	dr.send(loadedMsg{gen: dr.m.gen, issue: full})
	return dr
}

// The thread pans itself, for the same reason the description does, so a pan
// aimed at it is handed over rather than swallowed.
func TestMotions_PanningReachesTheThreadWhenItHasTheKeyboard(t *testing.T) {
	t.Parallel()

	f := newFake(8)
	// A code block is what reaches past a box: the renderer wraps prose and
	// leaves code at its own width on purpose.
	block := adf.NewNode("codeBlock", adf.NewText(
		"func (c *Client) Export(ctx context.Context, tenant string) (Report, error) { return nil, nil }"))
	block.Attrs = adf.Attrs{"language": "go"}
	if _, err := f.AddComment(t.Context(), "PROJ-4", adf.NewDoc(
		adf.NewNode("paragraph", adf.NewText("The client is below.")), block)); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	dr := newDriver(t, testDeps(f), seedOf(t, f, "PROJ-4"), 120, 30)
	dr.key("tab", "tab")
	if dr.m.focus != regionComments {
		t.Fatalf("two tabs left the keyboard on region %d", dr.m.focus)
	}

	before := dr.m.thread.View()
	dr.key("l")
	if dr.m.thread.View() == before {
		t.Error("l with the thread focused panned nothing, so the stroke was swallowed")
	}
	dr.key("h")
	if dr.m.thread.View() != before {
		t.Error("h did not pan the thread back")
	}
	if dr.m.pans[regionComments] != 0 {
		t.Error("the pane kept a pan of its own for a region that pans itself")
	}
}
