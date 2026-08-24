package adf_test

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// TestParseMarkdown_CannotUndoTheseRenderings pins the cases where ADF →
// markdown is not invertible, with the answer the parser actually gives. Every
// one of them is a place the renderer had to choose between showing a reader
// what the author typed and keeping enough to rebuild the node, and chose the
// reader — which is the right call for a viewer and the reason
// [adf.ParseMarkdownInto] exists.
//
// A case moving out of this table is a fix. A case appearing in it that nobody
// wrote down is a bug. The width case is next door, because it needs a table
// wide enough to be truncated.
func TestParseMarkdown_CannotUndoTheseRenderings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		in    string // ADF
		once  string // what the renderer writes
		twice string // what rendering the parse of that writes
	}{
		{
			name:  "prose that begins a line with a marker, because the renderer does not escape",
			in:    wrap(para(text("- not a list"))),
			once:  "- not a list",
			twice: "- not a list",
		},
		{
			name:  "a hard break followed by a line that reads as a block",
			in:    wrap(para(text("above") + `,{"type":"hardBreak"},` + text("- below"))),
			once:  "above\n- below",
			twice: "above\n\n- below",
		},
		{
			name:  "a hard break with nothing before it, which markdown spells as a blank line",
			in:    wrap(para(`{"type":"hardBreak"},` + text("after"))),
			once:  "\nafter",
			twice: "after",
		},
		{
			name:  "a hard break inside a heading, which markdown ends at the newline",
			in:    wrap(node("heading", `"attrs":{"level":2},"content":[`+text("one")+`,{"type":"hardBreak"},`+text("two")+`]`)),
			once:  "## one\ntwo",
			twice: "## one\n\ntwo",
		},
		{
			name:  "a heading with nothing in it, which the renderer erases",
			in:    wrap(node("heading", `"attrs":{"level":2}`) + "," + para(text("after"))),
			once:  "after",
			twice: "after",
		},
		{
			name: "adjacent emphasis whose marks overlap, which has no single reading",
			in: wrap(para(marked("one", "em") + "," +
				`{"type":"text","text":"two","marks":[{"type":"em"},{"type":"code"}]}`)),
			once:  "*one**" + bt + "two" + bt + "*",
			twice: "*one**" + bt + "two" + bt + "*",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := parse(t, tc.in)
			if got := adf.Markdown(d); got != tc.once {
				t.Fatalf("the renderer changed\n got %q\nwant %q", got, tc.once)
			}
			out, err := adf.ParseMarkdown(tc.once)
			if err != nil {
				t.Fatal(err)
			}
			if got := adf.Markdown(out); got != tc.twice {
				t.Errorf("\n got %q\nwant %q", got, tc.twice)
			}
		})
	}
}

// TestParseMarkdown_CannotWidenATableThatWasNarrowedToFit is the width case:
// a bounded render truncates a cell with an ellipsis, and the ellipsis is what
// comes back. Markdown handed to an editor is rendered with no TableWidth for
// exactly this reason.
func TestParseMarkdown_CannotWidenATableThatWasNarrowedToFit(t *testing.T) {
	t.Parallel()
	d := parse(t, wrap(headedTable))
	narrow := adf.MarkdownWith(d, adf.Options{TableWidth: 20})
	if !strings.Contains(narrow, "…") {
		t.Fatalf("the table was not truncated at all, so this proves nothing:\n%s", narrow)
	}

	out, err := adf.ParseMarkdownWith(narrow, adf.Options{TableWidth: 20})
	if err != nil {
		t.Fatal(err)
	}
	kept := ""
	out.Walk(func(n adf.Node) bool {
		kept += n.Text
		return true
	})
	if !strings.Contains(kept, "…") {
		t.Error("the ellipsis did not survive, so the truncation was silently repaired")
	}
	if strings.Contains(kept, "Environment") {
		t.Error("a truncated cell came back whole, which markdown cannot know")
	}
}

// TestParseMarkdownInto_RestoresEveryOneWayCase is the other half: none of the
// cases above cost anything when the document they came from is still to hand.
func TestParseMarkdownInto_RestoresEveryOneWayCase(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"prose that reads as a marker": wrap(para(text("- not a list"))),
		"a hard break before a marker": wrap(para(text("above") + `,{"type":"hardBreak"},` + text("- below"))),
		"a leading hard break":         wrap(para(`{"type":"hardBreak"},` + text("after"))),
		"a hard break in a heading":    wrap(node("heading", `"attrs":{"level":2},"content":[`+text("one")+`,{"type":"hardBreak"},`+text("two")+`]`)),
		"an empty heading":             wrap(node("heading", `"attrs":{"level":2}`) + "," + para(text("after"))),
		"overlapping emphasis": wrap(para(marked("one", "em") + "," +
			`{"type":"text","text":"two","marks":[{"type":"em"},{"type":"code"}]}`)),
		"a narrowed table": wrap(headedTable),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := parse(t, in)
			for _, opt := range renderOptions() {
				out, err := adf.ParseMarkdownInto(d, adf.MarkdownWith(d, opt), opt)
				if err != nil {
					t.Fatalf("%+v: %v", opt, err)
				}
				if got, want := encoded(t, out), encoded(t, d); got != want {
					t.Errorf("%+v\n got %s\nwant %s", opt, got, want)
				}
			}
		})
	}
}
