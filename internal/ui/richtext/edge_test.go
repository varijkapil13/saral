package richtext

import (
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// TestRender_IsDeterministic is what a pane memoizing a render depends on: the
// same document at the same width is the same lines, whatever order a map was
// walked in.
func TestRender_IsDeterministic(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"kitchen.json", "edges.json"} {
		d := load(t, name)
		opt := options(72)
		opt.Open = map[int]bool{0: true, 1: true, 2: true}
		first := Render(d, opt)
		for range 3 {
			again := Render(d, opt)
			if !slices.Equal(first.Lines, again.Lines) || !slices.Equal(first.Widths, again.Widths) {
				t.Fatalf("%s rendered differently the second time", name)
			}
			if !slices.Equal(first.Folds, again.Folds) {
				t.Fatalf("%s reported different folds the second time", name)
			}
		}
	}
}

// TestRender_TableWithNoHeaderRow gets no divider under its first row, which is
// the only honest thing to draw when nothing said it was a heading.
func TestRender_TableWithNoHeaderRow(t *testing.T) {
	t.Parallel()
	cell := func(text string) adf.Node { return adf.NewNode("tableCell", para(text)) }
	d := doc(adf.NewNode("table",
		adf.NewNode("tableRow", cell("a"), cell("b")),
		adf.NewNode("tableRow", cell("c"), cell("d"))))
	got := stripped(Render(d, options(40)))
	if strings.Contains(got, UnicodeMarkers().HLine) {
		t.Errorf("a table with no header row was given a divider:\n%s", got)
	}
	for _, want := range []string{"│ a │ b │", "│ c │ d │"} {
		if !strings.Contains(got, want) {
			t.Errorf("wanted the row %q:\n%s", want, got)
		}
	}
}

// TestRender_ListHoldingSomethingElse covers a list holding what ADF's content
// model does not allow: it is rendered rather than dropped.
func TestRender_ListHoldingSomethingElse(t *testing.T) {
	t.Parallel()
	d := doc(adf.NewNode("bulletList",
		adf.NewNode("listItem", para("an item")),
		para("a paragraph where an item should be")))
	got := stripped(Render(d, options(40)))
	for _, want := range []string{"• an item", "a paragraph where an item"} {
		if !strings.Contains(got, want) {
			t.Errorf("wanted %q:\n%s", want, got)
		}
	}
}
