package adf_test

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
)

// The parse path runs once per save, not once per frame, so what is guarded
// here is the shape of the cost rather than a frame budget: parsing has to stay
// linear in the text, and reconciling an untouched document has to cost about
// what one render costs rather than a re-encode of the whole tree.

var docSink adf.Doc

// known drops the node the fixture carries to prove an unknown type survives.
// A parse with no original to restore it from refuses that marker on purpose,
// which is a case for a test rather than for a benchmark.
func known(d adf.Doc) adf.Doc {
	content := make([]adf.Node, 0, len(d.Content))
	for i := range d.Content {
		if n := d.Content[i]; !strings.HasPrefix(n.Type, "future") {
			content = append(content, n)
		}
	}
	return adf.NewDoc(content...)
}

func BenchmarkParseMarkdown_RichFixture(b *testing.B) {
	md := adf.Markdown(known(fixtureDescription(b)))
	b.SetBytes(int64(len(md)))
	b.ReportAllocs()
	for b.Loop() {
		d, err := adf.ParseMarkdown(md)
		if err != nil {
			b.Fatal(err)
		}
		docSink = d
	}
}

func BenchmarkParseMarkdown_LargeDocument(b *testing.B) {
	md := adf.Markdown(known(largeDoc(b, 200)))
	b.SetBytes(int64(len(md)))
	b.ReportAllocs()
	for b.Loop() {
		d, err := adf.ParseMarkdown(md)
		if err != nil {
			b.Fatal(err)
		}
		docSink = d
	}
}

// BenchmarkParseMarkdown_Prose is the common case: a comment body, all text and
// no markup, which must not pay for the reading-check the ambiguous lines do.
func BenchmarkParseMarkdown_Prose(b *testing.B) {
	md := strings.TrimSpace(strings.Repeat("The basket total is recalculated before the line items are read.\n\n", 200))
	b.SetBytes(int64(len(md)))
	b.ReportAllocs()
	for b.Loop() {
		d, err := adf.ParseMarkdown(md)
		if err != nil {
			b.Fatal(err)
		}
		docSink = d
	}
}

func BenchmarkParseMarkdownInto_Unchanged(b *testing.B) {
	d := largeDoc(b, 200)
	md := adf.Markdown(d)
	b.SetBytes(int64(len(md)))
	b.ReportAllocs()
	for b.Loop() {
		out, err := adf.ParseMarkdownInto(d, md, adf.Options{})
		if err != nil {
			b.Fatal(err)
		}
		docSink = out
	}
}

func BenchmarkParseMarkdownInto_OneBlockEdited(b *testing.B) {
	d := largeDoc(b, 200)
	md := strings.Replace(adf.Markdown(d), "The basket total is", "The basket sum is", 1)
	b.SetBytes(int64(len(md)))
	b.ReportAllocs()
	for b.Loop() {
		out, err := adf.ParseMarkdownInto(d, md, adf.Options{})
		if err != nil {
			b.Fatal(err)
		}
		docSink = out
	}
}

// BenchmarkParseMarkdown_ScalesWithTheText is the assertion the numbers above
// cannot make on their own: ten times the text costs about ten times as much,
// not a hundred. A parser that rescans a line per delimiter passes every other
// test in this file and then falls over on a description somebody pasted a log
// into.
func BenchmarkParseMarkdown_ScalesWithTheText(b *testing.B) {
	for _, size := range []int{1, 10, 100} {
		md := strings.TrimSpace(strings.Repeat(
			"A **bold** claim about `basketTotal()` and [the runbook](https://example.com/x).\n\n", size*20))
		b.Run(itoa(size), func(b *testing.B) {
			b.SetBytes(int64(len(md)))
			b.ReportAllocs()
			for b.Loop() {
				d, err := adf.ParseMarkdown(md)
				if err != nil {
					b.Fatal(err)
				}
				docSink = d
			}
		})
	}
}
