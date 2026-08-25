package richtext

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/adf"
)

// section is the run of blocks under one of the kitchen sink's headings, so
// that a golden is per construct and still driven from what a site stored
// rather than from nodes a test made up.
func section(t *testing.T, d adf.Doc, title string) adf.Doc {
	t.Helper()
	var out []adf.Node
	on := false
	for i := range d.Content {
		n := d.Content[i]
		if n.Type == "heading" {
			if on {
				break
			}
			on = strings.Contains(Summary(adf.NewDoc(n), 200), title)
			continue
		}
		if on {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		t.Fatalf("the stored document has no section under a heading holding %q", title)
	}
	return adf.NewDoc(out...)
}

func TestRender_WholeDocument(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"kitchen", "edges"} {
		d := load(t, name+".json")
		for _, width := range []int{80, 40} {
			golden(t, fmt.Sprintf("%s_%d.golden", name, width), stripped(Render(d, options(width))))
		}
	}
}

func TestRender_PerConstruct(t *testing.T) {
	t.Parallel()
	kitchen := load(t, "kitchen.json")
	for _, tc := range []struct {
		name    string
		heading string
		widths  []int
	}{
		{"lists", "Lists", []int{60, 28}},
		{"tasks", "Task and decision", []int{60, 28}},
		{"code", "Code", []int{60, 28}},
		{"quote", "Quote and rule", []int{60, 28}},
		{"panels", "Panels", []int{60, 24}},
		{"table", "Table with a header", []int{80, 40, 24}},
		{"cards", "Expand, layout and cards", []int{60, 28}},
		{"inline", "Inline nodes", []int{60, 30}},
		{"nonascii", "Non-ASCII", []int{60, 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := section(t, kitchen, tc.heading)
			for _, width := range tc.widths {
				golden(t, fmt.Sprintf("%s_%d.golden", tc.name, width), stripped(Render(d, options(width))))
			}
		})
	}
}

// TestRender_ASCIIMarkers covers the terminals docs/UX.md says never to assume
// anything about: the glyph set changes and nothing else does.
func TestRender_ASCIIMarkers(t *testing.T) {
	t.Parallel()
	kitchen := load(t, "kitchen.json")
	for _, heading := range []string{"Panels", "Task and decision", "Lists"} {
		d := section(t, kitchen, heading)
		opt := options(60)
		opt.Markers = ASCIIMarkers()
		name := strings.ToLower(strings.Fields(heading)[0]) + "_ascii_60.golden"
		golden(t, name, stripped(Render(d, opt)))
	}
}

// TestRender_OpenFold covers the other half of an expand: the same document
// rendered with the fold the reader has opened.
func TestRender_OpenFold(t *testing.T) {
	t.Parallel()
	d := section(t, load(t, "kitchen.json"), "Expand, layout and cards")
	opt := options(60)
	opt.Open = map[int]bool{0: true}
	golden(t, "cards_open_60.golden", stripped(Render(d, opt)))
}

func TestRender_ZeroOptionsStillRenders(t *testing.T) {
	t.Parallel()
	d := load(t, "kitchen.json")
	r := Render(d, Options{})
	if len(r.Lines) == 0 {
		t.Fatal("a zero Options rendered nothing")
	}
	if r.Width() > defaultWidth {
		// Code and tables are allowed past the width; nothing else is.
		for i, line := range r.Lines {
			if r.Widths[i] > defaultWidth && !strings.Contains(line, "select") {
				continue
			}
		}
	}
	if !strings.Contains(stripped(r), UnicodeMarkers().Bullet) {
		t.Error("a zero Options lost the markers; the Unicode set is the default")
	}
}

func TestRender_EmptyDocument(t *testing.T) {
	t.Parallel()
	for _, d := range []adf.Doc{{}, adf.NewDoc()} {
		r := Render(d, options(80))
		if len(r.Lines) != 0 || len(r.Widths) != 0 || len(r.Folds) != 0 {
			t.Errorf("an empty document rendered %d lines", len(r.Lines))
		}
	}
}

// TestRender_WidthsAreWhatTheLinesMeasure is the contract a pane clamps panning
// against: it must not have to measure a line itself.
func TestRender_WidthsAreWhatTheLinesMeasure(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"kitchen.json", "edges.json"} {
		d := load(t, name)
		for _, width := range []int{120, 80, 40, 24, 12} {
			for _, palette := range []Palette{plainPalette(), colourPalette()} {
				opt := options(width)
				opt.Styles = NewStyles(palette)
				r := Render(d, opt)
				if len(r.Lines) != len(r.Widths) {
					t.Fatalf("%s at %d: %d lines against %d widths", name, width, len(r.Lines), len(r.Widths))
				}
				for i, line := range r.Lines {
					if got := ansi.StringWidth(line); got != r.Widths[i] {
						t.Errorf("%s at %d, line %d: measured %d, reported %d\n%q",
							name, width, i, got, r.Widths[i], line)
					}
				}
			}
		}
	}
}

// TestRender_ProseFitsTheWidth holds the wrapping to its promise, over the
// sections that hold no code and no grid: everything else wraps, however narrow
// the pane.
func TestRender_ProseFitsTheWidth(t *testing.T) {
	t.Parallel()
	kitchen := load(t, "kitchen.json")
	for _, heading := range []string{"Task and decision", "Quote and rule", "Panels", "Inline nodes"} {
		d := section(t, kitchen, heading)
		// Nothing narrower than 16 is asserted: a gutter deep enough to squeeze
		// the content below minAvail is allowed to push a line past the width,
		// which is the floor doing its job rather than the wrapping failing.
		for _, width := range []int{80, 40, 24, 16} {
			r := Render(d, options(width))
			for i, line := range r.Lines {
				if r.Widths[i] > width {
					t.Errorf("%q at width %d, line %d is %d wide: %q",
						heading, width, i, r.Widths[i], ansi.Strip(line))
				}
			}
		}
	}
}

// TestRender_OnlyCodeAndGridsOverflow is the other half of that promise: the
// two constructs that are allowed past the width are the only ones that go
// past it, and both are marked as such by the bar they carry.
func TestRender_OnlyCodeAndGridsOverflow(t *testing.T) {
	t.Parallel()
	bar := UnicodeMarkers().VLine
	for _, name := range []string{"kitchen.json", "edges.json"} {
		d := load(t, name)
		for _, width := range []int{80, 40, 24} {
			r := Render(d, options(width))
			for i, line := range r.Lines {
				plain := ansi.Strip(line)
				if r.Widths[i] <= max(width, minAvail) || strings.HasPrefix(plain, bar) {
					continue
				}
				t.Errorf("%s at width %d, line %d is %d wide and is neither code nor a grid: %q",
					name, width, i, r.Widths[i], plain)
			}
		}
	}
}

// TestRender_CodeIsNeitherWrappedNorTruncated is the decision written down: a
// line of code arrives whole or not at all, and it arrives whole.
func TestRender_CodeIsNeitherWrappedNorTruncated(t *testing.T) {
	t.Parallel()
	const sql = "select key, summary, status from issue where project = 'EX' " +
		"and status in ('open', 'in review') order by updated desc;"
	d := load(t, "edges.json")
	for _, width := range []int{80, 40, 12} {
		r := Render(d, options(width))
		found := false
		for _, line := range r.Lines {
			if strings.Contains(ansi.Strip(line), sql) {
				found = true
			}
		}
		if !found {
			t.Errorf("at width %d the code line came back wrapped or cut:\n%s", width, stripped(r))
		}
	}
}

// TestRender_TableCellWrapsRatherThanBeingCut is the same decision for a grid:
// the column is squeezed and the words go onto another line of the row.
func TestRender_TableCellWrapsRatherThanBeingCut(t *testing.T) {
	t.Parallel()
	d := load(t, "edges.json")
	r := Render(d, options(80))
	got := stripped(r)
	for _, word := range strings.Fields("a sentence long enough to wrap inside its own column rather than be cut off") {
		if !strings.Contains(got, word) {
			t.Errorf("the squeezed cell lost %q:\n%s", word, got)
		}
	}
	if strings.Contains(got, UnicodeMarkers().Ellipsis) {
		t.Error("a cell was truncated; a squeezed column wraps")
	}
}

// TestRender_NothingIsDropped is the property behind every construct decision:
// whatever the document says, in order, is somewhere in the lines. Wrapping may
// put a break inside a word, so the comparison ignores whitespace.
func TestRender_NothingIsDropped(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"kitchen.json", "edges.json"} {
		d := load(t, name)
		want := packed(documentText(d))
		for _, width := range []int{120, 80, 40, 16} {
			got := packed(withoutGrids(stripped(Render(d, options(width)))))
			if missing, ok := subsequence(want, got); !ok {
				t.Errorf("%s at width %d dropped text from %q onwards", name, width, missing)
			}
		}
	}
}

// documentText is every character the document asks to be shown, in order.
//
// A table is left out because a grid is laid out in columns: a row two lines
// tall interleaves its cells, so document order is not line order, and what a
// grid keeps is asserted on its own. A closed expand is left out because not
// showing what is inside it is the whole point of a fold.
func documentText(d adf.Doc) string {
	var b strings.Builder
	d.Walk(func(n adf.Node) bool {
		switch n.Type {
		case "table", "expand", "nestedExpand":
			return false
		case "text":
			b.WriteString(n.Text)
		case "mention", "emoji", "status", "placeholder":
			text, _ := attrString(n.Attrs, "text")
			b.WriteString(text)
		}
		return true
	})
	return b.String()
}

// withoutGrids drops the lines a grid drew, which are the ones whose order is
// the layout's rather than the document's.
func withoutGrids(s string) string {
	bar := UnicodeMarkers().VLine
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, bar) && strings.HasSuffix(line, bar) {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

func packed(s string) string {
	var b strings.Builder
	for _, r := range sanitize(s) {
		if r == ' ' || r == '\n' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// subsequence reports whether want appears in got in order, and where it stops
// if it does not.
func subsequence(want, got string) (string, bool) {
	at := 0
	for i, r := range want {
		next := strings.IndexRune(got[at:], r)
		if next < 0 {
			end := min(i+24, len(want))
			return want[i:end], false
		}
		at += next + len(string(r))
	}
	return "", true
}

func TestSummary(t *testing.T) {
	t.Parallel()
	d := load(t, "kitchen.json")
	got := Summary(d, 60)
	if w := ansi.StringWidth(got); w > 60 {
		t.Errorf("a summary of 60 came back %d wide: %q", w, got)
	}
	if strings.ContainsAny(got, "\n\x1b") {
		t.Errorf("a summary is one plain line: %q", got)
	}
	if !strings.HasPrefix(got, "Kitchen sink") {
		t.Errorf("a summary leads with the document: %q", got)
	}
	if !strings.HasSuffix(got, UnicodeMarkers().Ellipsis) {
		t.Errorf("a summary that ran out of room says so: %q", got)
	}
	if Summary(d, 0) != "" {
		t.Error("a summary with no room is empty")
	}
	if Summary(adf.Doc{}, 40) != "" {
		t.Error("a summary of nothing is empty")
	}
}

// TestSummary_CountsCellsNotBytes measures a cut in cells rather than bytes,
// which is the way a truncation helper is usually got wrong.
func TestSummary_CountsCellsNotBytes(t *testing.T) {
	t.Parallel()
	d := doc(para("日本語のテキスト、中文字符 🚀 Größe"))
	for _, width := range []int{4, 5, 8, 11, 20} {
		if w := ansi.StringWidth(Summary(d, width)); w > width {
			t.Errorf("a summary of %d came back %d wide: %q", width, w, Summary(d, width))
		}
	}
}
